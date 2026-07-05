package kafkaconn

import (
	"context"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/twmb/franz-go/pkg/kgo"
)

type franzEventLogProducerRuntime interface {
	ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults
}

// FranzEventLogShadowClient mirrors already-positioned SQL events into a
// Kafka/Redpanda event topic while reusing EventLog for replay. Native Kafka
// writer appends still go through the command/event transaction boundary.
type FranzEventLogShadowClient struct {
	producer franzEventLogProducerRuntime
	replay   *EventLog
	options  EventLogOptions
}

var _ core.BrokerEventLogClient = (*FranzEventLogShadowClient)(nil)
var _ core.EventPartitionLister = (*FranzEventLogShadowClient)(nil)

func NewFranzEventLogShadowClient(client *kgo.Client, options EventLogOptions, replayOptions FranzEventLogClientOptions) *FranzEventLogShadowClient {
	return newFranzEventLogShadowClient(client, NewFranzEventLogClient(client, replayOptions), options)
}

func newFranzEventLogShadowClient(producer franzEventLogProducerRuntime, replayClient EventLogClient, options EventLogOptions) *FranzEventLogShadowClient {
	options = normalizeEventLogOptions(options)
	return &FranzEventLogShadowClient{
		producer: producer,
		replay:   NewEventLog(replayClient, options),
		options:  options,
	}
}

func NewEventLogShadowStore(client *kgo.Client, options EventLogOptions, replayOptions FranzEventLogClientOptions) *core.BrokerEventStore {
	return core.NewBrokerEventStore(NewFranzEventLogShadowClient(client, options, replayOptions))
}

func (c *FranzEventLogShadowClient) AppendEvent(ctx context.Context, partition core.LogPartition, record logmodel.BrokerEventRecord) (logmodel.BrokerEventLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return logmodel.BrokerEventLogMessage{}, err
	}
	if c == nil || c.producer == nil {
		return logmodel.BrokerEventLogMessage{}, fmt.Errorf("kafka event log shadow: nil producer")
	}
	partition = partition.Normalize()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	if record.ID == "" {
		return logmodel.BrokerEventLogMessage{}, fmt.Errorf("kafka event log shadow: event id is required")
	}
	if record.TS <= 0 {
		return logmodel.BrokerEventLogMessage{}, fmt.Errorf("kafka event log shadow: event timestamp is required")
	}
	if record.CompatibilitySeq <= 0 {
		return logmodel.BrokerEventLogMessage{}, fmt.Errorf("kafka event log shadow: compatibility seq is required")
	}
	if record.PartitionOffset <= 0 {
		return logmodel.BrokerEventLogMessage{}, fmt.Errorf("kafka event log shadow: partition offset is required")
	}
	topic := c.options.EventTopic
	if topic == "" {
		topic = DefaultEventTopic
	}
	key := LogicalPartitionKey(partition)
	appendRecord, err := NewKafkaEventRecord(topic, record)
	if err != nil {
		return logmodel.BrokerEventLogMessage{}, err
	}
	results := c.producer.ProduceSync(ctx, cloneKafkaRecord(appendRecord))
	if len(results) == 0 {
		return logmodel.BrokerEventLogMessage{}, fmt.Errorf("kafka event log shadow: produce returned no result")
	}
	produced, err := results.First()
	if err != nil {
		return logmodel.BrokerEventLogMessage{}, err
	}
	if produced == nil {
		return logmodel.BrokerEventLogMessage{}, fmt.Errorf("kafka event log shadow: produce returned nil record")
	}
	result, err := kafkaEventAppendResultFromRecord(cloneKafkaRecord(produced))
	if err != nil {
		return logmodel.BrokerEventLogMessage{}, err
	}
	if err := validateEventAppendResult(topic, key, partition, record, result); err != nil {
		return logmodel.BrokerEventLogMessage{}, fmt.Errorf("kafka event log shadow: %w", err)
	}
	return result.Message, nil
}

func (c *FranzEventLogShadowClient) FetchEvents(ctx context.Context, partition core.LogPartition, afterOffset int64, limit int) ([]logmodel.BrokerEventLogMessage, error) {
	if c == nil || c.replay == nil {
		return nil, fmt.Errorf("kafka event log shadow: nil replay client")
	}
	return c.replay.FetchEvents(ctx, partition, afterOffset, limit)
}

func (c *FranzEventLogShadowClient) Head(ctx context.Context) (int64, error) {
	if c == nil || c.replay == nil {
		return 0, fmt.Errorf("kafka event log shadow: nil replay client")
	}
	return c.replay.Head(ctx)
}

func (c *FranzEventLogShadowClient) ListEventPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	if c == nil || c.replay == nil {
		return nil, fmt.Errorf("kafka event log shadow: nil replay client")
	}
	return c.replay.ListEventPartitions(ctx, limit)
}
