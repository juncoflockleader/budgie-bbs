package kafkaconn

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	"github.com/twmb/franz-go/pkg/kgo"
)

const kafkaCommandRecordVersion = 1

type CommandSourcePosition = core.CommandLogSourcePosition

type kafkaCommandRecord struct {
	Version       int               `json:"v"`
	ActorID       string            `json:"actorId,omitempty"`
	CID           string            `json:"cid,omitempty"`
	Command       proto.CommandName `json:"command"`
	Payload       json.RawMessage   `json:"payload"`
	EnqueuedAt    int64             `json:"enqueuedAt"`
	PartitionKind string            `json:"partitionKind"`
	PartitionKey  string            `json:"partitionKey"`
}

func NewKafkaCommandRecord(topic string, record core.BrokerCommandRecord) (*kgo.Record, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		topic = DefaultCommandTopic
	}
	partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	if record.Command == "" {
		return nil, fmt.Errorf("kafka command log: missing command")
	}
	if len(record.Payload) == 0 || !json.Valid(record.Payload) {
		return nil, fmt.Errorf("kafka command log: invalid payload")
	}
	if record.EnqueuedAt <= 0 {
		record.EnqueuedAt = time.Now().UnixMilli()
	}
	data, err := json.Marshal(kafkaCommandRecord{
		Version:       kafkaCommandRecordVersion,
		ActorID:       record.ActorID,
		CID:           record.CID,
		Command:       record.Command,
		Payload:       append([]byte(nil), record.Payload...),
		EnqueuedAt:    record.EnqueuedAt,
		PartitionKind: partition.Kind,
		PartitionKey:  partition.Key,
	})
	if err != nil {
		return nil, err
	}
	return &kgo.Record{
		Topic: topic,
		Key:   []byte(LogicalPartitionKey(partition)),
		Value: data,
	}, nil
}

func DecodeKafkaCommandRecord(record *kgo.Record) (core.BrokerCommandLogMessage, CommandSourcePosition, error) {
	if record == nil {
		return core.BrokerCommandLogMessage{}, CommandSourcePosition{}, fmt.Errorf("kafka command log: nil record")
	}
	if record.Offset < 0 {
		return core.BrokerCommandLogMessage{}, CommandSourcePosition{}, fmt.Errorf("kafka command log: negative record offset %d", record.Offset)
	}
	var wire kafkaCommandRecord
	if err := json.Unmarshal(record.Value, &wire); err != nil {
		return core.BrokerCommandLogMessage{}, CommandSourcePosition{}, err
	}
	if wire.Version != kafkaCommandRecordVersion {
		return core.BrokerCommandLogMessage{}, CommandSourcePosition{}, fmt.Errorf("kafka command record: unsupported version %d", wire.Version)
	}
	if wire.Command == "" {
		return core.BrokerCommandLogMessage{}, CommandSourcePosition{}, fmt.Errorf("kafka command record: missing command")
	}
	if len(wire.Payload) == 0 || !json.Valid(wire.Payload) {
		return core.BrokerCommandLogMessage{}, CommandSourcePosition{}, fmt.Errorf("kafka command record: invalid payload")
	}
	if wire.EnqueuedAt <= 0 {
		return core.BrokerCommandLogMessage{}, CommandSourcePosition{}, fmt.Errorf("kafka command record: missing enqueue time")
	}
	partition := core.LogPartition{Kind: wire.PartitionKind, Key: wire.PartitionKey}.Normalize()
	if len(record.Key) > 0 && string(record.Key) != LogicalPartitionKey(partition) {
		return core.BrokerCommandLogMessage{}, CommandSourcePosition{}, fmt.Errorf("kafka command log: record key %q does not match partition %s/%s",
			string(record.Key), partition.Kind, partition.Key)
	}
	logicalOffset := record.Offset + 1
	brokerRecord := core.BrokerCommandRecord{
		Version:       1,
		ActorID:       wire.ActorID,
		CID:           wire.CID,
		Command:       wire.Command,
		Payload:       append([]byte(nil), wire.Payload...),
		EnqueuedAt:    wire.EnqueuedAt,
		PartitionKind: partition.Kind,
		PartitionKey:  partition.Key,
		Offset:        logicalOffset,
	}
	data, err := core.EncodeBrokerCommandRecord(brokerRecord)
	if err != nil {
		return core.BrokerCommandLogMessage{}, CommandSourcePosition{}, err
	}
	message := core.BrokerCommandLogMessage{
		Partition: partition,
		Offset:    logicalOffset,
		StreamSeq: logicalOffset,
		Data:      data,
	}
	position := core.CommandLogSourcePosition{
		Backend:           "kafka",
		Topic:             strings.TrimSpace(record.Topic),
		PhysicalPartition: record.Partition,
		PhysicalOffset:    record.Offset,
		CommitOffset:      record.Offset + 1,
		LogicalPartition:  partition,
		LogicalOffset:     logicalOffset,
	}
	return message, position, nil
}

func DecodeKafkaCommandLogRecord(record *kgo.Record) (core.CommandLogRecord, CommandSourcePosition, error) {
	message, position, err := DecodeKafkaCommandRecord(record)
	if err != nil {
		return core.CommandLogRecord{}, CommandSourcePosition{}, err
	}
	command, err := core.DecodeBrokerCommandMessage(message)
	if err != nil {
		return core.CommandLogRecord{}, CommandSourcePosition{}, err
	}
	command.SourcePosition = position
	if err := command.SourcePosition.ValidateForRecord(command); err != nil {
		return core.CommandLogRecord{}, CommandSourcePosition{}, err
	}
	return command, position, nil
}
