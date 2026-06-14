package kafkaconn

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/twmb/franz-go/pkg/kgo"
)

type CommandLogOptions struct {
	CommandTopic   string
	ConsumerGroup  string
	PartitionCount int32
	Candidates     core.CommandPartitionLister
}

type CommandLogClient interface {
	AppendCommandRecord(ctx context.Context, record *kgo.Record) (*kgo.Record, error)
	FetchCommandRecords(ctx context.Context, request CommandFetchRequest) ([]*kgo.Record, error)
	CommitCommandOffset(ctx context.Context, commit CommandOffsetCommit) error
	CommittedCommandOffset(ctx context.Context, partition CommandTopicPartition) (int64, error)
}

type CommandFetchRequest struct {
	Topic              string
	Key                string
	Partition          core.LogPartition
	PhysicalPartition  int32
	AfterLogicalOffset int64
	Limit              int
}

type CommandTopicPartition struct {
	Topic             string
	Key               string
	Partition         core.LogPartition
	PhysicalPartition int32
}

// CommandLog adapts Kafka/Redpanda command records directly to core.CommandLog.
// It intentionally bypasses core.BrokerCommandLog because Kafka source-position
// evidence is required to commit physical offsets safely after event writes.
type CommandLog struct {
	client  CommandLogClient
	options CommandLogOptions
}

var _ core.CommandLog = (*CommandLog)(nil)
var _ core.CommandPartitionLister = (*CommandLog)(nil)
var _ core.CommandLogRebalanceAllower = (*CommandLog)(nil)
var _ core.CommandLogCommitRecorder = (*CommandLog)(nil)

func NewCommandLog(client CommandLogClient, options CommandLogOptions) *CommandLog {
	return &CommandLog{client: client, options: options}
}

func (l *CommandLog) Produce(ctx context.Context, record core.CommandLogRecord) (core.CommandLogRecord, error) {
	if err := ctx.Err(); err != nil {
		return core.CommandLogRecord{}, err
	}
	if l == nil || l.client == nil {
		return core.CommandLogRecord{}, fmt.Errorf("kafka command log: nil client")
	}
	options := normalizeCommandLogOptions(l.options)
	partition := record.Partition.Normalize()
	payload := append([]byte(nil), record.Payload...)
	if len(payload) == 0 || !json.Valid(payload) {
		return core.CommandLogRecord{}, fmt.Errorf("kafka command log: payload is not valid JSON")
	}
	if strings.TrimSpace(record.CID) != "" && record.EnqueuedAt <= 0 {
		return core.CommandLogRecord{}, fmt.Errorf("kafka command log: enqueue time is required when command receipt is set")
	}
	produce, err := NewKafkaCommandRecord(options.CommandTopic, core.BrokerCommandRecord{
		Version:       1,
		ActorID:       record.ActorID,
		CID:           record.CID,
		Command:       record.Command,
		Payload:       payload,
		EnqueuedAt:    record.EnqueuedAt,
		PartitionKind: partition.Kind,
		PartitionKey:  partition.Key,
	})
	if err != nil {
		return core.CommandLogRecord{}, err
	}
	assigned, err := l.client.AppendCommandRecord(ctx, produce)
	if err != nil {
		return core.CommandLogRecord{}, err
	}
	command, _, err := DecodeKafkaCommandLogRecord(assigned)
	if err != nil {
		return core.CommandLogRecord{}, err
	}
	if command.Partition != partition {
		return core.CommandLogRecord{}, fmt.Errorf("kafka command log: append returned partition %s/%s for %s/%s",
			command.Partition.Kind, command.Partition.Key, partition.Kind, partition.Key)
	}
	if err := validateKafkaCommandSource(options, command); err != nil {
		return core.CommandLogRecord{}, err
	}
	return command, nil
}

func (l *CommandLog) FetchPartition(ctx context.Context, partition core.LogPartition, afterOffset int64, limit int) ([]core.CommandLogRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("kafka command log: nil client")
	}
	options := normalizeCommandLogOptions(l.options)
	partition = partition.Normalize()
	physical, err := kafkaCommandPhysicalPartition(options, partition)
	if err != nil {
		return nil, err
	}
	request := CommandFetchRequest{
		Topic:              options.CommandTopic,
		Key:                LogicalPartitionKey(partition),
		Partition:          partition,
		PhysicalPartition:  physical,
		AfterLogicalOffset: afterOffset,
		Limit:              limit,
	}
	records, err := l.client.FetchCommandRecords(ctx, request)
	if err != nil {
		return nil, err
	}
	out := make([]core.CommandLogRecord, 0, len(records))
	for _, record := range records {
		command, _, err := DecodeKafkaCommandLogRecord(record)
		if err != nil {
			return nil, err
		}
		if command.Partition != partition {
			continue
		}
		if command.Offset <= afterOffset {
			continue
		}
		if err := validateKafkaCommandSource(options, command); err != nil {
			return nil, err
		}
		if command.SourcePosition.PhysicalPartition != physical {
			return nil, fmt.Errorf("kafka command log: fetched physical partition %d for %s/%s, want %d",
				command.SourcePosition.PhysicalPartition, partition.Kind, partition.Key, physical)
		}
		out = append(out, command)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Offset < out[j].Offset
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (l *CommandLog) CommitPartition(ctx context.Context, partition core.LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil || l.client == nil {
		return fmt.Errorf("kafka command log: nil client")
	}
	if offset < 0 {
		offset = 0
	}
	options := normalizeCommandLogOptions(l.options)
	partition = partition.Normalize()
	physical, err := kafkaCommandPhysicalPartition(options, partition)
	if err != nil {
		return err
	}
	return l.client.CommitCommandOffset(ctx, CommandOffsetCommit{
		ConsumerGroup:     options.ConsumerGroup,
		Topic:             options.CommandTopic,
		PhysicalPartition: physical,
		Key:               LogicalPartitionKey(partition),
		Offset:            offset,
		LogicalPartition:  partition,
		LogicalOffset:     offset,
	})
}

func (l *CommandLog) RecordCommandLogCommit(ctx context.Context, partition core.LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil || l.client == nil {
		return fmt.Errorf("kafka command log: nil client")
	}
	if offset < 0 {
		offset = 0
	}
	options := normalizeCommandLogOptions(l.options)
	partition = partition.Normalize()
	physical, err := kafkaCommandPhysicalPartition(options, partition)
	if err != nil {
		return err
	}
	recorder, ok := l.client.(interface {
		RecordCommandOffsetCommit(context.Context, CommandOffsetCommit) error
	})
	if !ok {
		return nil
	}
	return recorder.RecordCommandOffsetCommit(ctx, CommandOffsetCommit{
		ConsumerGroup:     options.ConsumerGroup,
		Topic:             options.CommandTopic,
		PhysicalPartition: physical,
		Key:               LogicalPartitionKey(partition),
		Offset:            offset,
		LogicalPartition:  partition,
		LogicalOffset:     offset,
	})
}

func (l *CommandLog) CommittedOffset(ctx context.Context, partition core.LogPartition) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if l == nil || l.client == nil {
		return 0, fmt.Errorf("kafka command log: nil client")
	}
	options := normalizeCommandLogOptions(l.options)
	partition = partition.Normalize()
	physical, err := kafkaCommandPhysicalPartition(options, partition)
	if err != nil {
		return 0, err
	}
	offset, err := l.client.CommittedCommandOffset(ctx, CommandTopicPartition{
		Topic:             options.CommandTopic,
		Key:               LogicalPartitionKey(partition),
		Partition:         partition,
		PhysicalPartition: physical,
	})
	if err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, nil
	}
	return offset, nil
}

func (l *CommandLog) AllowCommandLogRebalance() {
	if l == nil || l.client == nil {
		return
	}
	allower, ok := l.client.(interface{ AllowRebalance() })
	if !ok {
		return
	}
	allower.AllowRebalance()
}

func (l *CommandLog) ListCommandPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil || l.options.Candidates == nil {
		return nil, fmt.Errorf("kafka command log: partition listing requires candidates")
	}
	return l.options.Candidates.ListCommandPartitions(ctx, limit)
}

func normalizeCommandLogOptions(options CommandLogOptions) CommandLogOptions {
	options.CommandTopic = strings.TrimSpace(options.CommandTopic)
	if options.CommandTopic == "" {
		options.CommandTopic = DefaultCommandTopic
	}
	options.ConsumerGroup = strings.TrimSpace(options.ConsumerGroup)
	if options.ConsumerGroup == "" {
		options.ConsumerGroup = DefaultWriterConsumerGroup
	}
	return options
}

func kafkaCommandPhysicalPartition(options CommandLogOptions, partition core.LogPartition) (int32, error) {
	if options.PartitionCount <= 0 {
		return 0, fmt.Errorf("kafka command log: partition count is required")
	}
	return KafkaPartitionForLogicalPartition(partition, options.PartitionCount)
}

func validateKafkaCommandSource(options CommandLogOptions, record core.CommandLogRecord) error {
	source := record.SourcePosition.Normalize()
	if err := source.ValidateForRecord(record); err != nil {
		return err
	}
	if !strings.EqualFold(source.Backend, "kafka") && !strings.EqualFold(source.Backend, "redpanda") {
		return fmt.Errorf("kafka command log: command source backend %q is not Kafka-compatible", source.Backend)
	}
	if source.Topic != options.CommandTopic {
		return fmt.Errorf("kafka command log: command source topic %q does not match configured command topic %q", source.Topic, options.CommandTopic)
	}
	if options.PartitionCount > 0 {
		physical, err := KafkaPartitionForLogicalPartition(record.Partition, options.PartitionCount)
		if err != nil {
			return err
		}
		if source.PhysicalPartition != physical {
			return fmt.Errorf("kafka command log: command source physical partition %d for %s/%s, want %d",
				source.PhysicalPartition, record.Partition.Kind, record.Partition.Key, physical)
		}
	}
	return nil
}
