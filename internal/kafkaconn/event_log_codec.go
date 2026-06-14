package kafkaconn

import (
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/twmb/franz-go/pkg/kgo"
)

func NewKafkaEventRecord(topic string, record core.BrokerEventRecord) (*kgo.Record, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		topic = DefaultEventTopic
	}
	partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	data, err := core.EncodeBrokerEventRecord(record)
	if err != nil {
		return nil, err
	}
	return &kgo.Record{
		Topic: topic,
		Key:   []byte(LogicalPartitionKey(partition)),
		Value: data,
	}, nil
}

type KafkaEventRecordDecodeOptions struct {
	DisableKafkaOffsetStreamSeq bool
}

func DecodeKafkaEventRecord(record *kgo.Record) (core.BrokerEventLogMessage, error) {
	return DecodeKafkaEventRecordWithOptions(record, KafkaEventRecordDecodeOptions{})
}

func DecodeKafkaEventRecordWithOptions(record *kgo.Record, options KafkaEventRecordDecodeOptions) (core.BrokerEventLogMessage, error) {
	if record == nil {
		return core.BrokerEventLogMessage{}, fmt.Errorf("kafka event log: nil record")
	}
	if record.Offset < 0 {
		return core.BrokerEventLogMessage{}, fmt.Errorf("kafka event log: negative record offset %d", record.Offset)
	}
	decoded, err := core.DecodeBrokerEventRecord(record.Value)
	if err != nil {
		return core.BrokerEventLogMessage{}, err
	}
	partition := core.LogPartition{Kind: decoded.PartitionKind, Key: decoded.PartitionKey}.Normalize()
	if len(record.Key) > 0 && string(record.Key) != LogicalPartitionKey(partition) {
		return core.BrokerEventLogMessage{}, fmt.Errorf("kafka event log: record key %q does not match partition %s/%s",
			string(record.Key), partition.Kind, partition.Key)
	}
	streamSeq := decoded.CompatibilitySeq
	if streamSeq <= 0 && !options.DisableKafkaOffsetStreamSeq {
		streamSeq = record.Offset + 1
	}
	return core.BrokerEventLogMessage{
		Partition: partition,
		Offset:    decoded.PartitionOffset,
		StreamSeq: streamSeq,
		Data:      append([]byte(nil), record.Value...),
	}, nil
}
