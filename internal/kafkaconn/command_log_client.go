package kafkaconn

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

type FranzCommandLogClientOptions struct {
	PollTimeout     time.Duration
	PollRecordLimit int
	// ReadyTimeout bounds how long the very first fetch may block while the
	// consumer group completes its initial join/sync. A Kafka group join (member
	// id round trip + cooperative-sticky rebalance) takes far longer than the
	// steady-state PollTimeout, so without this grace the first few empty polls
	// look like a stalled drain. After the first successful poll the client
	// reverts to the fast PollTimeout. Zero falls back to a built-in default.
	ReadyTimeout time.Duration
}

func FastDrainFranzCommandLogClientOptions() FranzCommandLogClientOptions {
	return FranzCommandLogClientOptions{
		PollTimeout:     25 * time.Millisecond,
		PollRecordLimit: 4096,
		ReadyTimeout:    10 * time.Second,
	}
}

type franzCommandLogRuntime interface {
	ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults
	PollRecords(ctx context.Context, maxPollRecords int) kgo.Fetches
	CommitOffsetsSync(ctx context.Context, offsets map[string]map[int32]kgo.EpochOffset, onDone func(*kgo.Client, *kmsg.OffsetCommitRequest, *kmsg.OffsetCommitResponse, error))
	CommittedOffsets() map[string]map[int32]kgo.EpochOffset
	SetOffsets(map[string]map[int32]kgo.EpochOffset)
	AllowRebalance()
}

// FranzCommandLogClient adapts a franz-go group consumer/producer to the
// narrower command-log client used by CommandLog. It buffers records for other
// Budgie logical partitions so polling one physical Kafka partition does not
// drop work that the core worker will ask for later by logical partition.
type FranzCommandLogClient struct {
	client  franzCommandLogRuntime
	options FranzCommandLogClientOptions

	mu        sync.Mutex
	buffered  map[commandPhysicalPartition][]*kgo.Record
	committed map[commandPhysicalPartition]int64
	warmedUp  bool // first poll completed; consumer group has joined/synced
}

var _ CommandLogClient = (*FranzCommandLogClient)(nil)

func NewFranzCommandLogClient(client *kgo.Client, options FranzCommandLogClientOptions) *FranzCommandLogClient {
	return newFranzCommandLogClient(client, options)
}

func newFranzCommandLogClient(client franzCommandLogRuntime, options FranzCommandLogClientOptions) *FranzCommandLogClient {
	return &FranzCommandLogClient{
		client:    client,
		options:   normalizeFranzCommandLogClientOptions(options),
		buffered:  map[commandPhysicalPartition][]*kgo.Record{},
		committed: map[commandPhysicalPartition]int64{},
	}
}

func (c *FranzCommandLogClient) AppendCommandRecord(ctx context.Context, record *kgo.Record) (*kgo.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("kafka command log client: nil client")
	}
	if record == nil {
		return nil, fmt.Errorf("kafka command log client: nil append record")
	}
	results := c.client.ProduceSync(ctx, cloneKafkaRecord(record))
	if len(results) == 0 {
		return nil, fmt.Errorf("kafka command log client: produce returned no result")
	}
	produced, err := results.First()
	if err != nil {
		return nil, err
	}
	if produced == nil {
		return nil, fmt.Errorf("kafka command log client: produce returned nil record")
	}
	return cloneKafkaRecord(produced), nil
}

func (c *FranzCommandLogClient) FetchCommandRecords(ctx context.Context, request CommandFetchRequest) ([]*kgo.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("kafka command log client: nil client")
	}
	request.Partition = request.Partition.Normalize()
	if request.Topic == "" {
		return nil, fmt.Errorf("kafka command log client: fetch topic is required")
	}
	if request.PhysicalPartition < 0 {
		return nil, fmt.Errorf("kafka command log client: fetch physical partition %d is negative", request.PhysicalPartition)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	out, blocked, err := c.takeBufferedLocked(request)
	if err != nil || len(out) > 0 || blocked {
		return out, err
	}

	// The first fetch absorbs the consumer-group join latency (see ReadyTimeout);
	// thereafter polls stay fast so steady-state drain throughput is unaffected.
	pollBudget := c.options.PollTimeout
	if !c.warmedUp && c.options.ReadyTimeout > pollBudget {
		pollBudget = c.options.ReadyTimeout
	}
	deadline := time.Now().Add(pollBudget)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			out, _, err := c.takeBufferedLocked(request)
			return out, err
		}
		c.client.SetOffsets(map[string]map[int32]kgo.EpochOffset{
			request.Topic: {
				request.PhysicalPartition: {Epoch: -1, Offset: request.AfterLogicalOffset},
			},
		})
		pollCtx, cancel := context.WithTimeout(ctx, remaining)
		fetches := c.client.PollRecords(pollCtx, c.pollRecordLimit(request))
		pollTimedOut := pollCtx.Err() != nil
		cancel()
		if err := fetches.Err(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if pollTimedOut && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
				out, _, err := c.takeBufferedLocked(request)
				return out, err
			}
			return nil, err
		}
		// A poll returned without error: the group has joined/synced, so revert
		// to fast steady-state polling for subsequent fetches.
		c.warmedUp = true
		for _, record := range fetches.Records() {
			if record == nil || record.Topic != request.Topic {
				continue
			}
			if err := c.bufferRecordLocked(record); err != nil {
				return nil, err
			}
		}
		out, blocked, err = c.takeBufferedLocked(request)
		if err != nil || len(out) > 0 || blocked {
			return out, err
		}
		if fetches.NumRecords() == 0 {
			remaining = time.Until(deadline)
			if remaining <= 0 {
				return out, err
			}
			if remaining > time.Millisecond {
				remaining = time.Millisecond
			}
			timer := time.NewTimer(remaining)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
}

func (c *FranzCommandLogClient) CommitCommandOffset(ctx context.Context, commit CommandOffsetCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.client == nil {
		return fmt.Errorf("kafka command log client: nil client")
	}
	if commit.Topic == "" {
		return fmt.Errorf("kafka command log client: commit topic is required")
	}
	if commit.PhysicalPartition < 0 {
		return fmt.Errorf("kafka command log client: commit physical partition %d is negative", commit.PhysicalPartition)
	}
	if commit.Offset < 0 {
		return fmt.Errorf("kafka command log client: commit offset %d is negative", commit.Offset)
	}
	var commitErr error
	called := false
	c.client.CommitOffsetsSync(ctx, map[string]map[int32]kgo.EpochOffset{
		commit.Topic: {
			commit.PhysicalPartition: {Offset: commit.Offset},
		},
	}, func(_ *kgo.Client, _ *kmsg.OffsetCommitRequest, _ *kmsg.OffsetCommitResponse, err error) {
		called = true
		commitErr = err
	})
	if !called {
		return fmt.Errorf("kafka command log client: commit returned no result")
	}
	if commitErr != nil {
		return commitErr
	}
	c.recordCommittedOffset(commandPhysicalPartition{
		topic:     commit.Topic,
		partition: commit.PhysicalPartition,
	}, commit.Offset)
	return nil
}

func (c *FranzCommandLogClient) RecordCommandOffsetCommit(ctx context.Context, commit CommandOffsetCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.client == nil {
		return fmt.Errorf("kafka command log client: nil client")
	}
	if commit.Topic == "" {
		return fmt.Errorf("kafka command log client: commit topic is required")
	}
	if commit.PhysicalPartition < 0 {
		return fmt.Errorf("kafka command log client: commit physical partition %d is negative", commit.PhysicalPartition)
	}
	if commit.Offset < 0 {
		return fmt.Errorf("kafka command log client: commit offset %d is negative", commit.Offset)
	}
	c.recordCommittedOffset(commandPhysicalPartition{
		topic:     commit.Topic,
		partition: commit.PhysicalPartition,
	}, commit.Offset)
	return nil
}

func (c *FranzCommandLogClient) AllowRebalance() {
	if c == nil || c.client == nil {
		return
	}
	c.client.AllowRebalance()
}

func (c *FranzCommandLogClient) CommittedCommandOffset(ctx context.Context, partition CommandTopicPartition) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if c == nil || c.client == nil {
		return 0, fmt.Errorf("kafka command log client: nil client")
	}
	if partition.Topic == "" {
		return 0, fmt.Errorf("kafka command log client: committed topic is required")
	}
	if partition.PhysicalPartition < 0 {
		return 0, fmt.Errorf("kafka command log client: committed physical partition %d is negative", partition.PhysicalPartition)
	}
	offsets := c.client.CommittedOffsets()
	topicOffsets := offsets[partition.Topic]
	if topicOffsets == nil {
		return 0, nil
	}
	offset := topicOffsets[partition.PhysicalPartition]
	if offset.Offset < 0 {
		return 0, nil
	}
	return offset.Offset, nil
}

func (c *FranzCommandLogClient) takeBufferedLocked(request CommandFetchRequest) ([]*kgo.Record, bool, error) {
	key := commandPhysicalPartition{
		topic:     request.Topic,
		partition: request.PhysicalPartition,
	}
	records := c.buffered[key]
	if len(records) == 0 {
		return nil, false, nil
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Offset < records[j].Offset
	})
	out := make([]*kgo.Record, 0, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		if record.Offset+1 <= request.AfterLogicalOffset {
			continue
		}
		command, _, err := DecodeKafkaCommandLogRecord(record)
		if err != nil {
			return nil, false, err
		}
		if command.Partition != request.Partition {
			if committed := c.committed[key]; committed > 0 && record.Offset < committed {
				continue
			}
			return out, true, nil
		}
		if request.Limit <= 0 || len(out) < request.Limit {
			out = append(out, cloneKafkaRecord(record))
			continue
		}
		return out, false, nil
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Offset < out[j].Offset
	})
	return out, false, nil
}

func (c *FranzCommandLogClient) bufferRecordLocked(record *kgo.Record) error {
	command, _, err := DecodeKafkaCommandLogRecord(record)
	if err != nil {
		return err
	}
	key := commandPhysicalPartition{
		topic:     record.Topic,
		partition: command.SourcePosition.PhysicalPartition,
	}
	records := c.buffered[key]
	for _, existing := range records {
		if existing != nil && existing.Offset == record.Offset {
			return nil
		}
	}
	c.buffered[key] = append(records, cloneKafkaRecord(record))
	return nil
}

func (c *FranzCommandLogClient) recordCommittedOffset(key commandPhysicalPartition, commitOffset int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if commitOffset > c.committed[key] {
		c.committed[key] = commitOffset
	}
	c.pruneCommittedLocked(key, commitOffset)
}

func (c *FranzCommandLogClient) pruneCommittedLocked(key commandPhysicalPartition, commitOffset int64) {
	records := c.buffered[key]
	if len(records) == 0 {
		return
	}
	kept := records[:0]
	for _, record := range records {
		if record == nil || record.Offset < commitOffset {
			continue
		}
		kept = append(kept, record)
	}
	if len(kept) == 0 {
		delete(c.buffered, key)
		return
	}
	c.buffered[key] = kept
}

func (c *FranzCommandLogClient) pollRecordLimit(request CommandFetchRequest) int {
	if c.options.PollRecordLimit > 0 {
		return c.options.PollRecordLimit
	}
	if request.Limit > 0 {
		return request.Limit
	}
	return 0
}

func normalizeFranzCommandLogClientOptions(options FranzCommandLogClientOptions) FranzCommandLogClientOptions {
	if options.PollTimeout <= 0 {
		options.PollTimeout = 2 * time.Second
	}
	if options.ReadyTimeout < options.PollTimeout {
		options.ReadyTimeout = options.PollTimeout
	}
	return options
}

func cloneKafkaRecord(record *kgo.Record) *kgo.Record {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.Key = append([]byte(nil), record.Key...)
	cloned.Value = append([]byte(nil), record.Value...)
	cloned.Headers = append([]kgo.RecordHeader(nil), record.Headers...)
	return &cloned
}

type commandPhysicalPartition struct {
	topic     string
	partition int32
}
