package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
)

const (
	brokerCommandRecordVersion = logmodel.BrokerCommandRecordVersion
	brokerCommandSubjectPrefix = "budgie.commandlog"
	brokerCommandCommitPrefix  = "budgie.commandcommit"
)

type BrokerCommandLogClient interface {
	AppendCommand(ctx context.Context, partition LogPartition, record logmodel.BrokerCommandRecord) (logmodel.BrokerCommandLogMessage, error)
	FetchCommands(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]logmodel.BrokerCommandLogMessage, error)
	CommitPartition(ctx context.Context, partition LogPartition, offset int64) error
	CommittedOffset(ctx context.Context, partition LogPartition) (int64, error)
}

// BrokerCommandLog adapts a partitioned broker command log to CommandLog.
type BrokerCommandLog struct {
	client BrokerCommandLogClient
}

func NewBrokerCommandLog(client BrokerCommandLogClient) *BrokerCommandLog {
	return &BrokerCommandLog{client: client}
}

func (l *BrokerCommandLog) Produce(ctx context.Context, record CommandLogRecord) (CommandLogRecord, error) {
	if err := ctx.Err(); err != nil {
		return CommandLogRecord{}, err
	}
	if l == nil || l.client == nil {
		return CommandLogRecord{}, fmt.Errorf("broker command log: nil client")
	}
	partition := record.Partition.Normalize()
	payload := append([]byte(nil), record.Payload...)
	if !json.Valid(payload) {
		return CommandLogRecord{}, fmt.Errorf("broker command log: payload is not valid JSON")
	}
	if strings.TrimSpace(record.CID) != "" && record.EnqueuedAt <= 0 {
		return CommandLogRecord{}, fmt.Errorf("broker command log: enqueue time is required when command receipt is set")
	}
	enqueuedAt := record.EnqueuedAt
	if enqueuedAt <= 0 {
		enqueuedAt = nowMS()
	}
	msg, err := l.client.AppendCommand(ctx, partition, logmodel.BrokerCommandRecord{
		Version:       brokerCommandRecordVersion,
		ActorID:       record.ActorID,
		CID:           record.CID,
		Command:       record.Command,
		Payload:       payload,
		EnqueuedAt:    enqueuedAt,
		PartitionKind: partition.Kind,
		PartitionKey:  partition.Key,
	})
	if err != nil {
		return CommandLogRecord{}, err
	}
	return logmodel.DecodeBrokerCommandMessage(msg)
}

func (l *BrokerCommandLog) FetchPartition(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]CommandLogRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("broker command log: nil client")
	}
	partition = partition.Normalize()
	messages, err := l.client.FetchCommands(ctx, partition, afterOffset, limit)
	if err != nil {
		return nil, err
	}
	records := make([]CommandLogRecord, 0, len(messages))
	for _, msg := range messages {
		record, err := logmodel.DecodeBrokerCommandMessage(msg)
		if err != nil {
			return nil, err
		}
		if record.Partition != partition {
			return nil, fmt.Errorf("broker command log: fetch returned wrong partition %s/%s for %s/%s",
				record.Partition.Kind, record.Partition.Key, partition.Kind, partition.Key)
		}
		if record.Offset <= afterOffset {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

func (l *BrokerCommandLog) CommitPartition(ctx context.Context, partition LogPartition, offset int64) error {
	if l == nil || l.client == nil {
		return fmt.Errorf("broker command log: nil client")
	}
	return l.client.CommitPartition(ctx, partition.Normalize(), offset)
}

func (l *BrokerCommandLog) CommittedOffset(ctx context.Context, partition LogPartition) (int64, error) {
	if l == nil || l.client == nil {
		return 0, fmt.Errorf("broker command log: nil client")
	}
	return l.client.CommittedOffset(ctx, partition.Normalize())
}

func (l *BrokerCommandLog) ListCommandPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	lister, ok := l.client.(interface {
		ListCommandPartitions(context.Context, int) ([]LogPartition, error)
	})
	if !ok {
		return nil, fmt.Errorf("broker command log: partition listing is not supported")
	}
	return lister.ListCommandPartitions(ctx, limit)
}

func (l *BrokerCommandLog) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]logmodel.CommandPartitionOffset, error) {
	lister, ok := l.client.(interface {
		ListCommandPartitionOffsets(context.Context, int) ([]logmodel.CommandPartitionOffset, error)
	})
	if !ok {
		return nil, fmt.Errorf("broker command log: partition offset listing is not supported")
	}
	return lister.ListCommandPartitionOffsets(ctx, limit)
}

func BrokerCommandSubject(partition LogPartition) string {
	return logmodel.BrokerSubject(brokerCommandSubjectPrefix, partition)
}

func BrokerCommandSubjectWildcard() string {
	return logmodel.BrokerSubjectWildcard(brokerCommandSubjectPrefix)
}

func ParseBrokerCommandSubject(subject string) (LogPartition, bool) {
	return logmodel.ParseBrokerSubject(brokerCommandSubjectPrefix, subject)
}

func BrokerCommandCommitSubject(partition LogPartition) string {
	return logmodel.BrokerSubject(brokerCommandCommitPrefix, partition)
}

func BrokerCommandCommitSubjectWildcard() string {
	return logmodel.BrokerSubjectWildcard(brokerCommandCommitPrefix)
}

func ParseBrokerCommandCommitSubject(subject string) (LogPartition, bool) {
	return logmodel.ParseBrokerSubject(brokerCommandCommitPrefix, subject)
}

// MemoryBrokerCommandLogClient is a broker-shaped command log for tests and
// local fixtures. It makes the future broker writer contract executable without
// requiring a running broker.
type MemoryBrokerCommandLogClient struct {
	mu        sync.Mutex
	messages  map[LogPartition][]logmodel.BrokerCommandLogMessage
	tails     map[LogPartition]int64
	byReceipt map[logmodel.CommandReceiptKey]logmodel.BrokerCommandLogMessage
	committed map[LogPartition]int64
	head      int64
}

func NewMemoryBrokerCommandLogClient() *MemoryBrokerCommandLogClient {
	return &MemoryBrokerCommandLogClient{
		messages:  map[LogPartition][]logmodel.BrokerCommandLogMessage{},
		tails:     map[LogPartition]int64{},
		byReceipt: map[logmodel.CommandReceiptKey]logmodel.BrokerCommandLogMessage{},
		committed: map[LogPartition]int64{},
	}
}

func (c *MemoryBrokerCommandLogClient) AppendCommand(ctx context.Context, partition LogPartition, record logmodel.BrokerCommandRecord) (logmodel.BrokerCommandLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return logmodel.BrokerCommandLogMessage{}, err
	}
	if c == nil {
		return logmodel.BrokerCommandLogMessage{}, fmt.Errorf("memory broker command log: nil receiver")
	}
	partition = partition.Normalize()
	if strings.TrimSpace(record.CID) != "" && record.EnqueuedAt <= 0 {
		return logmodel.BrokerCommandLogMessage{}, fmt.Errorf("memory broker command log: enqueue time is required when command receipt is set")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	if key, ok := logmodel.NewCommandReceiptKey(partition, record.ActorID, record.CID); ok {
		if existing, ok := c.byReceipt[key]; ok {
			existingRecord, err := logmodel.DecodeBrokerCommandRecord(existing.Data)
			if err != nil {
				return logmodel.BrokerCommandLogMessage{}, err
			}
			if !logmodel.SameBrokerCommandRecordIdentity(existingRecord, record) {
				return logmodel.BrokerCommandLogMessage{}, fmt.Errorf("memory broker command log: duplicate command receipt %q has different content", record.CID)
			}
			return logmodel.CloneBrokerCommandLogMessage(existing), nil
		}
	}
	offset := c.tails[partition] + 1
	record.Offset = offset
	if record.CID == "" {
		record.CID = logmodel.SyntheticCommandLogCID(partition, offset)
	}
	if record.EnqueuedAt <= 0 {
		record.EnqueuedAt = nowMS()
	}
	data, err := logmodel.EncodeBrokerCommandRecord(record)
	if err != nil {
		return logmodel.BrokerCommandLogMessage{}, err
	}
	c.head++
	msg := logmodel.BrokerCommandLogMessage{
		Partition: partition,
		Offset:    offset,
		StreamSeq: c.head,
		Data:      data,
	}
	c.tails[partition] = offset
	c.messages[partition] = append(c.messages[partition], logmodel.CloneBrokerCommandLogMessage(msg))
	if key, ok := logmodel.NewCommandReceiptKey(partition, record.ActorID, record.CID); ok {
		c.byReceipt[key] = logmodel.CloneBrokerCommandLogMessage(msg)
	}
	return logmodel.CloneBrokerCommandLogMessage(msg), nil
}

func (c *MemoryBrokerCommandLogClient) FetchCommands(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]logmodel.BrokerCommandLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("memory broker command log: nil receiver")
	}
	partition = partition.Normalize()

	c.mu.Lock()
	defer c.mu.Unlock()
	source := c.messages[partition]
	out := make([]logmodel.BrokerCommandLogMessage, 0, len(source))
	for _, msg := range source {
		if msg.Offset <= afterOffset {
			continue
		}
		out = append(out, logmodel.CloneBrokerCommandLogMessage(msg))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (c *MemoryBrokerCommandLogClient) CommitPartition(ctx context.Context, partition LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("memory broker command log: nil receiver")
	}
	if offset < 0 {
		offset = 0
	}
	partition = partition.Normalize()
	c.mu.Lock()
	if offset > c.committed[partition] {
		c.committed[partition] = offset
	}
	c.mu.Unlock()
	return nil
}

func (c *MemoryBrokerCommandLogClient) CommittedOffset(ctx context.Context, partition LogPartition) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if c == nil {
		return 0, fmt.Errorf("memory broker command log: nil receiver")
	}
	partition = partition.Normalize()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.committed[partition], nil
}

func (c *MemoryBrokerCommandLogClient) ListCommandPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	offsets, err := c.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		return nil, err
	}
	return logmodel.CommandPartitionsByTailOffset(offsets, limit), nil
}

func (c *MemoryBrokerCommandLogClient) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]logmodel.CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("memory broker command log: nil receiver")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	offsets := make([]logmodel.CommandPartitionOffset, 0, len(c.tails))
	for partition, tail := range c.tails {
		partition = partition.Normalize()
		offsets = append(offsets, logmodel.CommandPartitionOffset{
			Partition:       partition,
			TailOffset:      tail,
			CommittedOffset: c.committed[partition],
		})
	}
	logmodel.SortCommandPartitionOffsetsByLag(offsets)
	if limit > 0 && len(offsets) > limit {
		offsets = offsets[:limit]
	}
	return offsets, nil
}

func BrokerCommandMessageID(partition LogPartition, actorID, cid string) string {
	return logmodel.CommandReceiptMessageID(brokerCommandSubjectPrefix, partition, actorID, cid)
}
