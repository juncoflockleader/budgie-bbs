package natsconn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	nats "github.com/nats-io/nats.go"
)

const (
	DefaultJetStreamEventLogStream          = "BUDGIE_EVENT_LOG"
	defaultJetStreamEventLogStream          = DefaultJetStreamEventLogStream
	defaultJetStreamEventLogWait            = 5 * time.Second
	defaultJetStreamEventLogDuplicateWindow = 24 * time.Hour
	jetStreamEventLogAppendRetries          = 64
)

type JetStreamEventLogOptions struct {
	Stream          string
	Replicas        int
	Wait            time.Duration
	DuplicateWindow time.Duration
	ReadOnly        bool
}

func NewJetStreamEventStore(ctx context.Context, conn *Conn, options JetStreamEventLogOptions) (*core.BrokerEventStore, error) {
	client, err := NewJetStreamEventLogClient(ctx, conn, options)
	if err != nil {
		return nil, err
	}
	return core.NewBrokerEventStore(client), nil
}

type JetStreamEventLogClient struct {
	js         nats.JetStreamContext
	stream     string
	replicas   int
	wait       time.Duration
	dupeWindow time.Duration

	mu     sync.Mutex
	seeded map[core.LogPartition]int64
	known  map[core.LogPartition]jetStreamPartitionTail
}

func NewJetStreamEventLogClient(ctx context.Context, conn *Conn, options JetStreamEventLogOptions) (*JetStreamEventLogClient, error) {
	if conn == nil || conn.nc == nil {
		return nil, fmt.Errorf("nats event log: nil connection")
	}
	stream := JetStreamName(options.Stream, defaultJetStreamEventLogStream)
	wait := jetStreamWait(options.Wait)
	replicas := jetStreamReplicas(options.Replicas)
	dupeWindow := jetStreamDuration(options.DuplicateWindow, defaultJetStreamEventLogDuplicateWindow)
	js, err := conn.nc.JetStream(nats.MaxWait(wait))
	if err != nil {
		return nil, err
	}
	client := &JetStreamEventLogClient{
		js:         js,
		stream:     stream,
		replicas:   replicas,
		wait:       wait,
		dupeWindow: dupeWindow,
		seeded:     map[core.LogPartition]int64{},
		known:      map[core.LogPartition]jetStreamPartitionTail{},
	}
	if options.ReadOnly {
		err = client.validateStream(ctx)
	} else {
		err = client.ensureStream(ctx)
	}
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (c *JetStreamEventLogClient) AppendEvent(ctx context.Context, partition core.LogPartition, record logmodel.BrokerEventRecord) (logmodel.BrokerEventLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return logmodel.BrokerEventLogMessage{}, err
	}
	if c == nil || c.js == nil {
		return logmodel.BrokerEventLogMessage{}, fmt.Errorf("nats event log: nil client")
	}
	partition = partition.Normalize()
	return c.appendEvent(ctx, partition, record, nil)
}

func (c *JetStreamEventLogClient) AppendEvents(ctx context.Context, records []logmodel.BrokerEventRecord) ([]logmodel.BrokerEventLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.js == nil {
		return nil, fmt.Errorf("nats event log: nil client")
	}
	tails := map[core.LogPartition]jetStreamPartitionTail{}
	messages := make([]logmodel.BrokerEventLogMessage, 0, len(records))
	for _, record := range records {
		partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
		msg, err := c.appendEvent(ctx, partition, record, tails)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func (c *JetStreamEventLogClient) appendEvent(ctx context.Context, partition core.LogPartition, record logmodel.BrokerEventRecord, tails map[core.LogPartition]jetStreamPartitionTail) (logmodel.BrokerEventLogMessage, error) {
	subject := core.BrokerEventSubject(partition)
	requestedOffset := record.PartitionOffset

	var lastErr error
	for attempt := 0; attempt < jetStreamEventLogAppendRetries; attempt++ {
		tail, cached := tails[partition]
		if !cached {
			var err error
			tail, err = c.partitionTail(ctx, partition, subject)
			if err != nil {
				return logmodel.BrokerEventLogMessage{}, err
			}
		}
		record.PartitionKind = partition.Kind
		record.PartitionKey = partition.Key
		nextOffset := tail.logicalOffset + 1
		if requestedOffset > 0 {
			record.PartitionOffset = requestedOffset
			if record.ID != "" && requestedOffset <= tail.logicalOffset {
				return c.findEventByID(ctx, partition, subject, record.ID, record)
			}
			if requestedOffset != nextOffset {
				return logmodel.BrokerEventLogMessage{}, fmt.Errorf("nats event log: partition offset %d for %s/%s must follow current tail %d",
					requestedOffset, partition.Kind, partition.Key, tail.logicalOffset)
			}
		} else {
			record.PartitionOffset = nextOffset
		}
		data, err := logmodel.EncodeBrokerEventRecord(record)
		if err != nil {
			return logmodel.BrokerEventLogMessage{}, err
		}
		baseOpts := []nats.PubOpt{
			nats.Context(ctx),
			nats.ExpectStream(c.stream),
			nats.ExpectLastSequencePerSubject(tail.streamSeq),
		}
		opts := append([]nats.PubOpt(nil), baseOpts...)
		if record.ID != "" {
			opts = append(opts, nats.MsgId(record.ID))
		}
		ack, err := c.js.PublishMsg(&nats.Msg{Subject: subject, Data: data}, opts...)
		if err != nil {
			if isWrongLastSequence(err) {
				lastErr = err
				delete(tails, partition)
				if err := waitJetStreamCASRetry(ctx, attempt, jetStreamEventLogAppendRetries); err != nil {
					return logmodel.BrokerEventLogMessage{}, err
				}
				continue
			}
			return logmodel.BrokerEventLogMessage{}, err
		}
		if ack.Duplicate && record.ID != "" {
			existing, err := c.findEventByID(ctx, partition, subject, record.ID, record)
			if err == nil {
				return existing, nil
			}
			if !isJetStreamDuplicateEventNotFound(err) {
				return logmodel.BrokerEventLogMessage{}, err
			}
			ack, err = c.js.PublishMsg(&nats.Msg{Subject: subject, Data: data}, baseOpts...)
			if err != nil {
				if isWrongLastSequence(err) {
					lastErr = err
					delete(tails, partition)
					if err := waitJetStreamCASRetry(ctx, attempt, jetStreamEventLogAppendRetries); err != nil {
						return logmodel.BrokerEventLogMessage{}, err
					}
					continue
				}
				return logmodel.BrokerEventLogMessage{}, err
			}
			if ack.Duplicate {
				return logmodel.BrokerEventLogMessage{}, fmt.Errorf("nats event log: duplicate event id %q was not recoverable", record.ID)
			}
		}
		c.rememberEventPosition(partition, record.PartitionOffset, ack.Sequence)
		if tails != nil {
			tails[partition] = jetStreamPartitionTail{
				streamSeq:     ack.Sequence,
				logicalOffset: record.PartitionOffset,
			}
		}
		return logmodel.BrokerEventLogMessage{
			Partition: partition,
			Offset:    record.PartitionOffset,
			StreamSeq: int64(ack.Sequence),
			Data:      data,
		}, nil
	}
	return logmodel.BrokerEventLogMessage{}, fmt.Errorf("nats event log: append CAS failed after %d attempts: %w", jetStreamEventLogAppendRetries, lastErr)
}

func (c *JetStreamEventLogClient) FetchEvents(ctx context.Context, partition core.LogPartition, afterOffset int64, limit int) ([]logmodel.BrokerEventLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.js == nil {
		return nil, fmt.Errorf("nats event log: nil client")
	}
	if limit <= 0 {
		limit = 100
	}
	partition = partition.Normalize()
	subject := core.BrokerEventSubject(partition)
	tail, err := c.partitionTail(ctx, partition, subject)
	if err != nil {
		return nil, err
	}
	if tail.logicalOffset <= afterOffset {
		return nil, nil
	}
	want := int(tail.logicalOffset - afterOffset)
	if want > limit {
		want = limit
	}
	out := make([]logmodel.BrokerEventLogMessage, 0, limit)
	seq := c.knownStreamSequenceAfterOffset(partition, afterOffset)
	for len(out) < want {
		raw, err := c.js.GetMsg(c.stream, seq, nats.Context(ctx), nats.DirectGetNext(subject))
		if errors.Is(err, nats.ErrMsgNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		seq = raw.Sequence + 1
		record, err := logmodel.DecodeBrokerEventRecord(raw.Data)
		if err != nil {
			return nil, err
		}
		c.rememberEventPosition(partition, record.PartitionOffset, raw.Sequence)
		if record.PartitionOffset <= afterOffset {
			continue
		}
		out = append(out, logmodel.BrokerEventLogMessage{
			Partition: partition,
			Offset:    record.PartitionOffset,
			StreamSeq: int64(raw.Sequence),
			Data:      append([]byte(nil), raw.Data...),
		})
	}
	return out, nil
}

func (c *JetStreamEventLogClient) Head(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if c == nil || c.js == nil {
		return 0, fmt.Errorf("nats event log: nil client")
	}
	info, err := c.js.StreamInfo(c.stream, nats.Context(ctx))
	if err != nil {
		return 0, err
	}
	return int64(info.State.LastSeq), nil
}

func (c *JetStreamEventLogClient) SeedEventPartitionOffset(ctx context.Context, partition core.LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("nats event log: nil client")
	}
	if offset < 0 {
		offset = 0
	}
	c.rememberOffset(partition.Normalize(), offset)
	return nil
}

func (c *JetStreamEventLogClient) ListEventPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.js == nil {
		return nil, fmt.Errorf("nats event log: nil client")
	}
	info, err := c.js.StreamInfo(c.stream, &nats.StreamInfoRequest{SubjectsFilter: core.BrokerEventSubjectWildcard()}, nats.Context(ctx))
	if err != nil {
		return nil, err
	}
	offsets := make([]core.EventPartitionOffset, 0, len(info.State.Subjects))
	seen := map[core.LogPartition]bool{}
	for subject, count := range info.State.Subjects {
		partition, ok := core.ParseBrokerEventSubject(subject)
		if !ok {
			continue
		}
		partition = partition.Normalize()
		seen[partition] = true
		offsets = append(offsets, core.EventPartitionOffset{
			Partition:  partition,
			LastOffset: int64(count),
		}.Normalize())
	}
	for partition, offset := range c.seededOffsets() {
		if offset <= 0 {
			continue
		}
		partition = partition.Normalize()
		if !seen[partition] {
			offsets = append(offsets, core.EventPartitionOffset{
				Partition:  partition,
				LastOffset: offset,
			}.Normalize())
		}
	}
	return logmodel.EventPartitionsByLastOffset(offsets, limit), nil
}

func (c *JetStreamEventLogClient) ListEventPartitionOffsets(ctx context.Context, limit int) ([]core.EventPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.js == nil {
		return nil, fmt.Errorf("nats event log: nil client")
	}
	partitions, err := c.ListEventPartitions(ctx, 0)
	if err != nil {
		return nil, err
	}
	offsets := make([]core.EventPartitionOffset, 0, len(partitions))
	for _, partition := range partitions {
		partition = partition.Normalize()
		tail, err := c.partitionTail(ctx, partition, core.BrokerEventSubject(partition))
		if err != nil {
			return nil, err
		}
		offsets = append(offsets, core.EventPartitionOffset{
			Partition:  partition,
			LastOffset: tail.logicalOffset,
		}.Normalize())
	}
	logmodel.SortEventPartitionOffsetsByLastOffset(offsets)
	if limit > 0 && len(offsets) > limit {
		offsets = offsets[:limit]
	}
	return offsets, nil
}

func (c *JetStreamEventLogClient) ensureStream(ctx context.Context) error {
	cfg := JetStreamEventLogStreamConfig(JetStreamEventLogOptions{
		Stream:          c.stream,
		Replicas:        c.replicas,
		DuplicateWindow: c.dupeWindow,
	})
	return ensureJetStreamStream(ctx, c.js, cfg)
}

func (c *JetStreamEventLogClient) validateStream(ctx context.Context) error {
	return validateJetStreamStream(ctx, c.js, c.stream, "nats event log", []string{core.BrokerEventSubjectWildcard()})
}

func (c *JetStreamEventLogClient) findEventByID(ctx context.Context, partition core.LogPartition, subject, id string, requested logmodel.BrokerEventRecord) (logmodel.BrokerEventLogMessage, error) {
	var seq uint64
	for {
		raw, err := c.js.GetMsg(c.stream, seq, nats.Context(ctx), nats.DirectGetNext(subject))
		if errors.Is(err, nats.ErrMsgNotFound) {
			break
		}
		if err != nil {
			return logmodel.BrokerEventLogMessage{}, err
		}
		seq = raw.Sequence + 1
		record, err := logmodel.DecodeBrokerEventRecord(raw.Data)
		if err != nil {
			return logmodel.BrokerEventLogMessage{}, err
		}
		if record.ID != id {
			continue
		}
		if !logmodel.SameBrokerEventRecordIdentity(record, requested) {
			return logmodel.BrokerEventLogMessage{}, fmt.Errorf("nats event log: duplicate event id %q has different content", id)
		}
		c.rememberEventPosition(partition, record.PartitionOffset, raw.Sequence)
		return logmodel.BrokerEventLogMessage{
			Partition: partition.Normalize(),
			Offset:    record.PartitionOffset,
			StreamSeq: int64(raw.Sequence),
			Data:      append([]byte(nil), raw.Data...),
		}, nil
	}
	return logmodel.BrokerEventLogMessage{}, fmt.Errorf("nats event log: duplicate event id %q was not found in partition %s/%s", id, partition.Kind, partition.Key)
}

func isJetStreamDuplicateEventNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate event id") && strings.Contains(err.Error(), "was not found")
}

func (c *JetStreamEventLogClient) partitionTail(ctx context.Context, partition core.LogPartition, subject string) (jetStreamPartitionTail, error) {
	raw, err := c.js.GetLastMsg(c.stream, subject, nats.Context(ctx), nats.DirectGet())
	tail := jetStreamPartitionTail{}
	if errors.Is(err, nats.ErrMsgNotFound) {
		tail.logicalOffset = c.seededOffset(partition)
		return tail, nil
	}
	if err != nil {
		return jetStreamPartitionTail{}, err
	}
	record, err := logmodel.DecodeBrokerEventRecord(raw.Data)
	if err != nil {
		return jetStreamPartitionTail{}, err
	}
	tail.streamSeq = raw.Sequence
	tail.logicalOffset = record.PartitionOffset
	if seed := c.seededOffset(partition); seed > tail.logicalOffset {
		tail.logicalOffset = seed
	}
	return tail, nil
}

func (c *JetStreamEventLogClient) seededOffset(partition core.LogPartition) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seeded[partition.Normalize()]
}

func (c *JetStreamEventLogClient) seededOffsets() map[core.LogPartition]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[core.LogPartition]int64, len(c.seeded))
	for partition, offset := range c.seeded {
		out[partition.Normalize()] = offset
	}
	return out
}

func (c *JetStreamEventLogClient) rememberOffset(partition core.LogPartition, offset int64) {
	c.mu.Lock()
	partition = partition.Normalize()
	if offset > c.seeded[partition] {
		c.seeded[partition] = offset
	}
	c.mu.Unlock()
}

func (c *JetStreamEventLogClient) rememberEventPosition(partition core.LogPartition, offset int64, streamSeq uint64) {
	c.mu.Lock()
	partition = partition.Normalize()
	if c.seeded == nil {
		c.seeded = map[core.LogPartition]int64{}
	}
	if offset > c.seeded[partition] {
		c.seeded[partition] = offset
	}
	if streamSeq > 0 {
		if c.known == nil {
			c.known = map[core.LogPartition]jetStreamPartitionTail{}
		}
		if known := c.known[partition]; offset >= known.logicalOffset {
			c.known[partition] = jetStreamPartitionTail{streamSeq: streamSeq, logicalOffset: offset}
		}
	}
	c.mu.Unlock()
}

func (c *JetStreamEventLogClient) knownStreamSequenceAfterOffset(partition core.LogPartition, offset int64) uint64 {
	if offset <= 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	known := c.known[partition.Normalize()]
	if known.logicalOffset != offset || known.streamSeq == 0 {
		return 0
	}
	return known.streamSeq + 1
}

type jetStreamPartitionTail struct {
	streamSeq     uint64
	logicalOffset int64
}

func isWrongLastSequence(err error) bool {
	var apiErr *nats.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode == nats.JSErrCodeStreamWrongLastSequence
}
