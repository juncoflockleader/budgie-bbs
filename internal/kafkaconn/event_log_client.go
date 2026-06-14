package kafkaconn

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/twmb/franz-go/pkg/kgo"
)

type FranzEventLogClientOptions struct {
	PollTimeout     time.Duration
	PollRecordLimit int
}

type franzEventLogRuntime interface {
	PollRecords(ctx context.Context, maxPollRecords int) kgo.Fetches
}

// FranzEventLogClient adapts a franz-go read-committed consumer to event-log
// replay. Kafka physical partitions may contain many Budgie logical event
// partitions, so records for neighboring logical keys are buffered until their
// projection watermark asks for them.
type FranzEventLogClient struct {
	client  franzEventLogRuntime
	options FranzEventLogClientOptions

	mu       sync.Mutex
	buffered map[eventPhysicalPartition][]*kgo.Record
}

var _ EventLogClient = (*FranzEventLogClient)(nil)

func NewFranzEventLogClient(client *kgo.Client, options FranzEventLogClientOptions) *FranzEventLogClient {
	return newFranzEventLogClient(client, options)
}

func newFranzEventLogClient(client franzEventLogRuntime, options FranzEventLogClientOptions) *FranzEventLogClient {
	return &FranzEventLogClient{
		client:   client,
		options:  normalizeFranzEventLogClientOptions(options),
		buffered: map[eventPhysicalPartition][]*kgo.Record{},
	}
}

func (c *FranzEventLogClient) FetchEventRecords(ctx context.Context, request EventFetchRequest) ([]*kgo.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("kafka event log client: nil client")
	}
	request.Partition = request.Partition.Normalize()
	if request.Topic == "" {
		return nil, fmt.Errorf("kafka event log client: fetch topic is required")
	}
	if request.PhysicalPartition < 0 {
		return nil, fmt.Errorf("kafka event log client: fetch physical partition %d is negative", request.PhysicalPartition)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	out, err := c.takeBufferedLocked(request)
	if err != nil || len(out) > 0 {
		return out, err
	}

	for {
		pollCtx, cancel := context.WithTimeout(ctx, c.options.PollTimeout)
		fetches := c.client.PollRecords(pollCtx, c.pollRecordLimit(request))
		pollTimedOut := pollCtx.Err() != nil
		cancel()
		if err := fetches.Err(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if pollTimedOut && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
				return c.takeBufferedLocked(request)
			}
			return nil, err
		}
		for _, record := range fetches.Records() {
			if record == nil || record.Topic != request.Topic {
				continue
			}
			if err := c.bufferRecordLocked(record); err != nil {
				return nil, err
			}
		}
		out, err = c.takeBufferedLocked(request)
		if err != nil || len(out) > 0 || fetches.NumRecords() == 0 {
			return out, err
		}
	}
}

func (c *FranzEventLogClient) takeBufferedLocked(request EventFetchRequest) ([]*kgo.Record, error) {
	key := eventPhysicalPartition{
		topic:     request.Topic,
		partition: request.PhysicalPartition,
	}
	records := c.buffered[key]
	if len(records) == 0 {
		return nil, nil
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Offset < records[j].Offset
	})
	type bufferedEventRecord struct {
		record *kgo.Record
		msg    core.BrokerEventLogMessage
	}
	candidates := make([]bufferedEventRecord, 0, len(records))
	kept := records[:0]
	for _, record := range records {
		if record == nil {
			continue
		}
		msg, err := DecodeKafkaEventRecord(record)
		if err != nil {
			return nil, err
		}
		if msg.Partition == request.Partition && msg.Offset <= request.AfterLogicalOffset {
			continue
		}
		kept = append(kept, record)
		if msg.Partition != request.Partition {
			continue
		}
		candidates = append(candidates, bufferedEventRecord{record: record, msg: msg})
	}
	if len(kept) == 0 {
		delete(c.buffered, key)
	} else {
		c.buffered[key] = kept
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].msg.Offset == candidates[j].msg.Offset {
			return candidates[i].record.Offset < candidates[j].record.Offset
		}
		return candidates[i].msg.Offset < candidates[j].msg.Offset
	})
	out := make([]*kgo.Record, 0, len(candidates))
	nextOffset := request.AfterLogicalOffset + 1
	for _, candidate := range candidates {
		if candidate.msg.Offset < nextOffset {
			continue
		}
		if candidate.msg.Offset > nextOffset {
			break
		}
		if request.Limit > 0 && len(out) >= request.Limit {
			break
		}
		out = append(out, cloneKafkaRecord(candidate.record))
		nextOffset++
	}
	return out, nil
}

func (c *FranzEventLogClient) bufferRecordLocked(record *kgo.Record) error {
	if _, err := DecodeKafkaEventRecord(record); err != nil {
		return err
	}
	key := eventPhysicalPartition{
		topic:     record.Topic,
		partition: record.Partition,
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

func (c *FranzEventLogClient) pollRecordLimit(request EventFetchRequest) int {
	if c.options.PollRecordLimit > 0 {
		return c.options.PollRecordLimit
	}
	if request.Limit > 0 {
		return request.Limit
	}
	return 0
}

func normalizeFranzEventLogClientOptions(options FranzEventLogClientOptions) FranzEventLogClientOptions {
	if options.PollTimeout <= 0 {
		options.PollTimeout = 250 * time.Millisecond
	}
	return options
}

type eventPhysicalPartition struct {
	topic     string
	partition int32
}
