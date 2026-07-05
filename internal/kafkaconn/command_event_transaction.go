package kafkaconn

import (
	"context"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
)

const (
	DefaultCommandTopic        = "budgie.commandlog"
	DefaultEventTopic          = "budgie.eventlog"
	DefaultWriterConsumerGroup = "budgie-command-writers"
)

type CommandEventTransactionOptions struct {
	CommandTopic             string
	EventTopic               string
	ConsumerGroup            string
	AllowPartitionOnlyEvents bool
}

type TransactionBeginner interface {
	BeginCommandEventTransaction(ctx context.Context) (Transaction, error)
}

type Transaction interface {
	AllocateEventPositions(ctx context.Context, records []core.BrokerEventRecord) ([]EventPositionAllocation, error)
	AppendEvent(ctx context.Context, topic, key string, record core.BrokerEventRecord) (EventAppendResult, error)
	CommitCommandOffset(ctx context.Context, commit CommandOffsetCommit) error
	Commit(ctx context.Context) error
	Abort(ctx context.Context) error
}

type EventPositionAllocation struct {
	Partition        core.LogPartition
	PartitionOffset  int64
	CompatibilitySeq int64
}

type EventAppendResult struct {
	Topic   string
	Key     string
	Message core.BrokerEventLogMessage
}

type CommandOffsetCommit struct {
	ConsumerGroup     string
	Topic             string
	PhysicalPartition int32
	Key               string
	Offset            int64
	LogicalPartition  core.LogPartition
	LogicalOffset     int64
}

// CommandEventTransactionClient adapts Redpanda/Kafka transactional producer
// semantics to Budgie's broker-native command/event transaction boundary.
type CommandEventTransactionClient struct {
	transactions TransactionBeginner
	options      CommandEventTransactionOptions
}

var _ core.BrokerCommandEventTransactionClient = (*CommandEventTransactionClient)(nil)

func NewCommandEventTransactionClient(transactions TransactionBeginner, options CommandEventTransactionOptions) *CommandEventTransactionClient {
	return &CommandEventTransactionClient{
		transactions: transactions,
		options:      options,
	}
}

func (c *CommandEventTransactionClient) AppendEventsAndCommitCommand(ctx context.Context, command core.CommandLogCommitPosition, records []core.BrokerEventRecord) (core.BrokerCommandEventTransactionResult, error) {
	if err := ctx.Err(); err != nil {
		return core.BrokerCommandEventTransactionResult{}, err
	}
	if c == nil || c.transactions == nil {
		return core.BrokerCommandEventTransactionResult{}, fmt.Errorf("kafka command/event transaction: nil client")
	}
	command = command.Normalize()
	if err := command.Validate(); err != nil {
		return core.BrokerCommandEventTransactionResult{}, fmt.Errorf("kafka command/event transaction: %w", err)
	}
	options := normalizeCommandEventTransactionOptions(c.options)
	commandCommit, err := commandOffsetCommit(options, command)
	if err != nil {
		return core.BrokerCommandEventTransactionResult{}, err
	}
	records, err = logmodel.NormalizeBrokerEventTransactionRecords(records, "one transaction")
	if err != nil {
		return core.BrokerCommandEventTransactionResult{}, fmt.Errorf("kafka command/event transaction: %w", err)
	}

	tx, err := c.transactions.BeginCommandEventTransaction(ctx)
	if err != nil {
		return core.BrokerCommandEventTransactionResult{}, err
	}
	if tx == nil {
		return core.BrokerCommandEventTransactionResult{}, fmt.Errorf("kafka command/event transaction: nil transaction")
	}
	commitAttempted := false
	committed := false
	defer func() {
		if !committed && !commitAttempted {
			_ = tx.Abort(ctx)
		}
	}()

	if len(records) > 0 {
		allocations, err := tx.AllocateEventPositions(ctx, records)
		if err != nil {
			return core.BrokerCommandEventTransactionResult{}, err
		}
		records, err = assignTransactionEventPositions(records, allocations, options)
		if err != nil {
			return core.BrokerCommandEventTransactionResult{}, err
		}
	}

	messages := make([]core.BrokerEventLogMessage, 0, len(records))
	for _, record := range records {
		partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
		key := LogicalPartitionKey(partition)
		result, err := tx.AppendEvent(ctx, options.EventTopic, key, record)
		if err != nil {
			return core.BrokerCommandEventTransactionResult{}, err
		}
		if err := validateEventAppendResult(options.EventTopic, key, partition, record, result); err != nil {
			return core.BrokerCommandEventTransactionResult{}, err
		}
		messages = append(messages, result.Message)
	}
	if err := tx.CommitCommandOffset(ctx, commandCommit); err != nil {
		return core.BrokerCommandEventTransactionResult{}, err
	}
	commitAttempted = true
	if err := tx.Commit(ctx); err != nil {
		return core.BrokerCommandEventTransactionResult{}, err
	}
	committed = true
	return core.BrokerCommandEventTransactionResult{
		Messages:           messages,
		CommittedPartition: command.Partition,
		CommittedOffset:    command.Offset,
	}, nil
}

func commandOffsetCommit(options CommandEventTransactionOptions, command core.CommandLogCommitPosition) (CommandOffsetCommit, error) {
	source := command.SourcePosition.Normalize()
	if source.IsZero() {
		return CommandOffsetCommit{}, fmt.Errorf("kafka command/event transaction: command source position is required")
	}
	if !strings.EqualFold(source.Backend, "kafka") && !strings.EqualFold(source.Backend, "redpanda") {
		return CommandOffsetCommit{}, fmt.Errorf("kafka command/event transaction: command source backend %q is not Kafka-compatible", source.Backend)
	}
	if source.Topic != options.CommandTopic {
		return CommandOffsetCommit{}, fmt.Errorf("kafka command/event transaction: command source topic %q does not match configured command topic %q", source.Topic, options.CommandTopic)
	}
	if source.PhysicalPartition < 0 {
		return CommandOffsetCommit{}, fmt.Errorf("kafka command/event transaction: command source physical partition %d is negative", source.PhysicalPartition)
	}
	return CommandOffsetCommit{
		ConsumerGroup:     options.ConsumerGroup,
		Topic:             source.Topic,
		PhysicalPartition: source.PhysicalPartition,
		Key:               LogicalPartitionKey(command.Partition),
		Offset:            source.CommitOffset,
		LogicalPartition:  command.Partition,
		LogicalOffset:     command.Offset,
	}, nil
}

func assignTransactionEventPositions(records []core.BrokerEventRecord, allocations []EventPositionAllocation, options CommandEventTransactionOptions) ([]core.BrokerEventRecord, error) {
	if len(allocations) != len(records) {
		return nil, fmt.Errorf("kafka command/event transaction: allocated %d event positions for %d events", len(allocations), len(records))
	}
	assigned := make([]core.BrokerEventRecord, 0, len(records))
	var lastSeq int64
	partitionOffsets := map[core.LogPartition]int64{}
	for i, record := range records {
		partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
		allocation := allocations[i]
		allocatedPartition := allocation.Partition.Normalize()
		if allocatedPartition != partition {
			return nil, fmt.Errorf("kafka command/event transaction: allocated event %d for partition %s/%s, want %s/%s",
				i, allocatedPartition.Kind, allocatedPartition.Key, partition.Kind, partition.Key)
		}
		if allocation.PartitionOffset <= 0 {
			return nil, fmt.Errorf("kafka command/event transaction: allocated event %d without logical partition offset", i)
		}
		if allocation.CompatibilitySeq <= 0 && !options.AllowPartitionOnlyEvents {
			return nil, fmt.Errorf("kafka command/event transaction: allocated event %d without scalar sequence evidence", i)
		}
		if record.CompatibilitySeq > 0 && allocation.CompatibilitySeq <= 0 {
			return nil, fmt.Errorf("kafka command/event transaction: allocated event %d without scalar sequence for requested compatibility sequence %d",
				i, record.CompatibilitySeq)
		}
		if record.CompatibilitySeq > 0 && record.CompatibilitySeq != allocation.CompatibilitySeq {
			return nil, fmt.Errorf("kafka command/event transaction: allocated event %d scalar sequence %d for requested compatibility sequence %d",
				i, allocation.CompatibilitySeq, record.CompatibilitySeq)
		}
		if allocation.CompatibilitySeq > 0 && lastSeq > 0 && allocation.CompatibilitySeq <= lastSeq {
			return nil, fmt.Errorf("kafka command/event transaction: allocated event %d with non-increasing scalar sequence %d after %d",
				i, allocation.CompatibilitySeq, lastSeq)
		}
		if lastOffset := partitionOffsets[partition]; lastOffset > 0 && allocation.PartitionOffset <= lastOffset {
			return nil, fmt.Errorf("kafka command/event transaction: allocated event %d for partition %s/%s with non-increasing partition offset %d after %d",
				i, partition.Kind, partition.Key, allocation.PartitionOffset, lastOffset)
		}
		record.PartitionKind = partition.Kind
		record.PartitionKey = partition.Key
		record.PartitionOffset = allocation.PartitionOffset
		record.CompatibilitySeq = allocation.CompatibilitySeq
		if allocation.CompatibilitySeq > 0 {
			lastSeq = allocation.CompatibilitySeq
		}
		partitionOffsets[partition] = allocation.PartitionOffset
		assigned = append(assigned, record)
	}
	return assigned, nil
}

func validateEventAppendResult(topic, key string, partition core.LogPartition, expected core.BrokerEventRecord, result EventAppendResult) error {
	if strings.TrimSpace(result.Topic) != topic {
		return fmt.Errorf("kafka command/event transaction: appended event returned topic %q for requested topic %q", result.Topic, topic)
	}
	if result.Key != key {
		return fmt.Errorf("kafka command/event transaction: appended event returned key %q for requested key %q", result.Key, key)
	}
	event, err := core.DecodeBrokerEventMessage(result.Message)
	if err != nil {
		return err
	}
	partition = partition.Normalize()
	if event.PartitionKind != partition.Kind || event.PartitionKey != partition.Key {
		return fmt.Errorf("kafka command/event transaction: appended event returned partition %s/%s for requested partition %s/%s",
			event.PartitionKind, event.PartitionKey, partition.Kind, partition.Key)
	}
	if event.PartitionOffset != expected.PartitionOffset {
		return fmt.Errorf("kafka command/event transaction: appended event returned logical partition offset %d for allocated offset %d",
			event.PartitionOffset, expected.PartitionOffset)
	}
	record, err := core.DecodeBrokerEventRecord(result.Message.Data)
	if err != nil {
		return err
	}
	if record.CompatibilitySeq != expected.CompatibilitySeq {
		return fmt.Errorf("kafka command/event transaction: appended event returned scalar sequence %d for allocated scalar sequence %d",
			record.CompatibilitySeq, expected.CompatibilitySeq)
	}
	return nil
}

func normalizeCommandEventTransactionOptions(options CommandEventTransactionOptions) CommandEventTransactionOptions {
	options.CommandTopic = strings.TrimSpace(options.CommandTopic)
	if options.CommandTopic == "" {
		options.CommandTopic = DefaultCommandTopic
	}
	options.EventTopic = strings.TrimSpace(options.EventTopic)
	if options.EventTopic == "" {
		options.EventTopic = DefaultEventTopic
	}
	options.ConsumerGroup = strings.TrimSpace(options.ConsumerGroup)
	if options.ConsumerGroup == "" {
		options.ConsumerGroup = DefaultWriterConsumerGroup
	}
	return options
}

func LogicalPartitionKey(partition core.LogPartition) string {
	return logmodel.PartitionKey(partition)
}

func ParseLogicalPartitionKey(key string) (core.LogPartition, bool) {
	partition, ok := logmodel.ParsePartitionKey(key)
	return core.LogPartition(partition), ok
}
