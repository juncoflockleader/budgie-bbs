package kafkaconn

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

type EventPositionAllocator interface {
	AllocateEventPositions(ctx context.Context, records []logmodel.BrokerEventRecord) ([]EventPositionAllocation, error)
}

type franzCommandEventTransactionRuntime interface {
	Begin() error
	ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults
	End(ctx context.Context, end kgo.TransactionEndTry, commandCommit *CommandOffsetCommit) (bool, error)
	SetOffsets(offsets map[string]map[int32]kgo.EpochOffset)
}

// FranzCommandEventTransactionBeginner adapts a franz-go group transaction
// session to the command/event transaction boundary.
type FranzCommandEventTransactionBeginner struct {
	runtime   franzCommandEventTransactionRuntime
	allocator EventPositionAllocator
	mu        sync.Mutex
}

var _ TransactionBeginner = (*FranzCommandEventTransactionBeginner)(nil)

func NewFranzCommandEventTransactionBeginner(session *kgo.GroupTransactSession, allocator EventPositionAllocator) *FranzCommandEventTransactionBeginner {
	return newFranzCommandEventTransactionBeginner(franzCommandEventGroupSessionRuntime{session: session}, allocator)
}

func newFranzCommandEventTransactionBeginner(runtime franzCommandEventTransactionRuntime, allocator EventPositionAllocator) *FranzCommandEventTransactionBeginner {
	return &FranzCommandEventTransactionBeginner{
		runtime:   runtime,
		allocator: allocator,
	}
}

func (b *FranzCommandEventTransactionBeginner) BeginCommandEventTransaction(ctx context.Context) (Transaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || b.runtime == nil {
		return nil, fmt.Errorf("franz command/event transaction: nil runtime")
	}
	if b.allocator == nil {
		return nil, fmt.Errorf("franz command/event transaction: nil event position allocator")
	}
	b.mu.Lock()
	if err := b.runtime.Begin(); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	return &franzCommandEventTransaction{
		runtime:   b.runtime,
		allocator: b.allocator,
		release:   b.mu.Unlock,
	}, nil
}

type franzCommandEventTransaction struct {
	runtime       franzCommandEventTransactionRuntime
	allocator     EventPositionAllocator
	commandCommit *CommandOffsetCommit
	release       func()
	releaseOnce   sync.Once
}

var _ Transaction = (*franzCommandEventTransaction)(nil)

func (tx *franzCommandEventTransaction) AllocateEventPositions(ctx context.Context, records []logmodel.BrokerEventRecord) ([]EventPositionAllocation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tx == nil || tx.allocator == nil {
		return nil, fmt.Errorf("franz command/event transaction: nil event position allocator")
	}
	allocations, err := tx.allocator.AllocateEventPositions(ctx, records)
	if err != nil {
		return nil, err
	}
	return append([]EventPositionAllocation(nil), allocations...), nil
}

func (tx *franzCommandEventTransaction) AppendEvent(ctx context.Context, topic, key string, record logmodel.BrokerEventRecord) (EventAppendResult, error) {
	if err := ctx.Err(); err != nil {
		return EventAppendResult{}, err
	}
	if tx == nil || tx.runtime == nil {
		return EventAppendResult{}, fmt.Errorf("franz command/event transaction: nil runtime")
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return EventAppendResult{}, fmt.Errorf("franz command/event transaction: event topic is required")
	}
	if key == "" {
		return EventAppendResult{}, fmt.Errorf("franz command/event transaction: event key is required")
	}
	data, err := logmodel.EncodeBrokerEventRecord(record)
	if err != nil {
		return EventAppendResult{}, err
	}
	results := tx.runtime.ProduceSync(ctx, &kgo.Record{
		Topic: topic,
		Key:   []byte(key),
		Value: data,
	})
	if len(results) == 0 {
		return EventAppendResult{}, fmt.Errorf("franz command/event transaction: event produce returned no result")
	}
	produced, err := results.First()
	if err != nil {
		return EventAppendResult{}, err
	}
	if produced == nil {
		return EventAppendResult{}, fmt.Errorf("franz command/event transaction: event produce returned nil record")
	}
	produced = cloneKafkaRecord(produced)
	return kafkaEventAppendResultFromRecord(produced)
}

func (tx *franzCommandEventTransaction) CommitCommandOffset(ctx context.Context, commit CommandOffsetCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx == nil {
		return fmt.Errorf("franz command/event transaction: nil transaction")
	}
	if tx.commandCommit != nil {
		return fmt.Errorf("franz command/event transaction: command offset already staged")
	}
	if err := validateFranzCommandOffsetCommit(commit); err != nil {
		return err
	}
	staged := commit
	tx.commandCommit = &staged
	return nil
}

func (tx *franzCommandEventTransaction) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx == nil || tx.runtime == nil {
		return fmt.Errorf("franz command/event transaction: nil runtime")
	}
	if tx.commandCommit == nil {
		err := fmt.Errorf("franz command/event transaction: command offset was not staged")
		if abortErr := tx.Abort(ctx); abortErr != nil {
			return fmt.Errorf("%w; abort failed: %v", err, abortErr)
		}
		return err
	}
	committed, err := tx.runtime.End(ctx, kgo.TryCommit, tx.commandCommit)
	tx.releaseLock()
	if err != nil {
		return err
	}
	if !committed {
		return fmt.Errorf("franz command/event transaction: transaction aborted before commit")
	}
	tx.runtime.SetOffsets(franzCommandOffsetMap(*tx.commandCommit))
	return nil
}

func (tx *franzCommandEventTransaction) Abort(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx == nil || tx.runtime == nil {
		return fmt.Errorf("franz command/event transaction: nil runtime")
	}
	committed, err := tx.runtime.End(ctx, kgo.TryAbort, nil)
	tx.releaseLock()
	if err != nil {
		return err
	}
	if committed {
		return fmt.Errorf("franz command/event transaction: abort unexpectedly committed")
	}
	return nil
}

func (tx *franzCommandEventTransaction) releaseLock() {
	if tx == nil || tx.release == nil {
		return
	}
	tx.releaseOnce.Do(tx.release)
}

type franzCommandEventGroupSessionRuntime struct {
	session *kgo.GroupTransactSession
}

func (r franzCommandEventGroupSessionRuntime) Begin() error {
	if r.session == nil {
		return fmt.Errorf("franz command/event transaction: nil group transaction session")
	}
	return r.session.Begin()
}

func (r franzCommandEventGroupSessionRuntime) ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults {
	return r.session.ProduceSync(ctx, records...)
}

func (r franzCommandEventGroupSessionRuntime) End(ctx context.Context, end kgo.TransactionEndTry, commandCommit *CommandOffsetCommit) (bool, error) {
	if r.session == nil {
		return false, fmt.Errorf("franz command/event transaction: nil group transaction session")
	}
	if end == kgo.TryCommit {
		if commandCommit == nil {
			return false, fmt.Errorf("franz command/event transaction: command offset was not staged")
		}
		commit := *commandCommit
		ctx = kgo.PreTxnCommitFnContext(ctx, func(req *kmsg.TxnOffsetCommitRequest) error {
			return rewriteTxnOffsetCommitRequest(req, commit)
		})
	}
	return r.session.End(ctx, end)
}

func (r franzCommandEventGroupSessionRuntime) SetOffsets(offsets map[string]map[int32]kgo.EpochOffset) {
	if r.session == nil {
		return
	}
	client := r.session.Client()
	if client == nil {
		return
	}
	client.SetOffsets(offsets)
}

func kafkaEventAppendResultFromRecord(record *kgo.Record) (EventAppendResult, error) {
	if record == nil {
		return EventAppendResult{}, fmt.Errorf("franz command/event transaction: event produce returned nil record")
	}
	decoded, err := logmodel.DecodeBrokerEventRecord(record.Value)
	if err != nil {
		return EventAppendResult{}, err
	}
	partition := core.LogPartition{Kind: decoded.PartitionKind, Key: decoded.PartitionKey}.Normalize()
	return EventAppendResult{
		Topic: record.Topic,
		Key:   string(record.Key),
		Message: logmodel.BrokerEventLogMessage{
			Partition: partition,
			Offset:    decoded.PartitionOffset,
			StreamSeq: decoded.CompatibilitySeq,
			Data:      append([]byte(nil), record.Value...),
		},
	}, nil
}

func validateFranzCommandOffsetCommit(commit CommandOffsetCommit) error {
	if strings.TrimSpace(commit.Topic) == "" {
		return fmt.Errorf("franz command/event transaction: command commit topic is required")
	}
	if commit.PhysicalPartition < 0 {
		return fmt.Errorf("franz command/event transaction: command commit physical partition %d is negative", commit.PhysicalPartition)
	}
	if commit.Offset < 0 {
		return fmt.Errorf("franz command/event transaction: command commit offset %d is negative", commit.Offset)
	}
	return nil
}

func franzCommandOffsetMap(commit CommandOffsetCommit) map[string]map[int32]kgo.EpochOffset {
	return map[string]map[int32]kgo.EpochOffset{
		commit.Topic: {
			commit.PhysicalPartition: {Epoch: -1, Offset: commit.Offset},
		},
	}
}

func rewriteTxnOffsetCommitRequest(req *kmsg.TxnOffsetCommitRequest, commit CommandOffsetCommit) error {
	if req == nil {
		return fmt.Errorf("franz command/event transaction: nil transaction offset commit request")
	}
	if err := validateFranzCommandOffsetCommit(commit); err != nil {
		return err
	}
	if commit.ConsumerGroup != "" && req.Group != "" && req.Group != commit.ConsumerGroup {
		return fmt.Errorf("franz command/event transaction: transaction group %q does not match command commit group %q", req.Group, commit.ConsumerGroup)
	}
	metadata := req.MemberID
	partition := kmsg.NewTxnOffsetCommitRequestTopicPartition()
	partition.Partition = commit.PhysicalPartition
	partition.Offset = commit.Offset
	partition.LeaderEpoch = -1
	partition.Metadata = &metadata
	topic := kmsg.NewTxnOffsetCommitRequestTopic()
	topic.Topic = commit.Topic
	topic.Partitions = []kmsg.TxnOffsetCommitRequestTopicPartition{partition}
	req.Topics = []kmsg.TxnOffsetCommitRequestTopic{topic}
	return nil
}
