package kafkaconn

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/twmb/franz-go/pkg/kgo"
)

type EventLogOptions struct {
	EventTopic                  string
	PartitionCount              int32
	Partitions                  core.EventPartitionLister
	Head                        EventLogHeadReader
	DisableKafkaOffsetStreamSeq bool
}

type EventLogHeadReader interface {
	Head(ctx context.Context) (int64, error)
}

type EventLogClient interface {
	FetchEventRecords(ctx context.Context, request EventFetchRequest) ([]*kgo.Record, error)
}

type EventFetchRequest struct {
	Topic              string
	Key                string
	Partition          core.LogPartition
	PhysicalPartition  int32
	AfterLogicalOffset int64
	Limit              int
}

type EventTopicPartition struct {
	Topic             string
	Key               string
	Partition         core.LogPartition
	PhysicalPartition int32
}

// EventLog adapts a Kafka/Redpanda event topic to core.BrokerEventLogClient for
// projection replay. Direct appends intentionally fail closed: Kafka event
// writes must happen through the command/event transaction boundary so the
// consumed command offset advances atomically with produced events.
type EventLog struct {
	client  EventLogClient
	options EventLogOptions
}

var _ core.BrokerEventLogClient = (*EventLog)(nil)
var _ core.EventPartitionLister = (*EventLog)(nil)

func NewEventLog(client EventLogClient, options EventLogOptions) *EventLog {
	return &EventLog{client: client, options: options}
}

func NewEventStore(client EventLogClient, options EventLogOptions) *core.BrokerEventStore {
	return core.NewBrokerEventStore(NewEventLog(client, options))
}

func (l *EventLog) RequiresEventStoreProjectionWatermarkSeed() bool {
	return true
}

func (l *EventLog) AppendEvent(ctx context.Context, partition core.LogPartition, record core.BrokerEventRecord) (core.BrokerEventLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return core.BrokerEventLogMessage{}, err
	}
	return core.BrokerEventLogMessage{}, fmt.Errorf("kafka event log: append requires command/event transaction")
}

func (l *EventLog) FetchEvents(ctx context.Context, partition core.LogPartition, afterOffset int64, limit int) ([]core.BrokerEventLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("kafka event log: nil client")
	}
	options := normalizeEventLogOptions(l.options)
	partition = partition.Normalize()
	physical, err := kafkaEventPhysicalPartition(options, partition)
	if err != nil {
		return nil, err
	}
	request := EventFetchRequest{
		Topic:              options.EventTopic,
		Key:                LogicalPartitionKey(partition),
		Partition:          partition,
		PhysicalPartition:  physical,
		AfterLogicalOffset: afterOffset,
		Limit:              limit,
	}
	records, err := l.client.FetchEventRecords(ctx, request)
	if err != nil {
		return nil, err
	}
	out := make([]core.BrokerEventLogMessage, 0, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		if record.Topic != options.EventTopic {
			return nil, fmt.Errorf("kafka event log: fetched event topic %q, want %q", record.Topic, options.EventTopic)
		}
		if record.Partition != physical {
			return nil, fmt.Errorf("kafka event log: fetched physical partition %d for %s/%s, want %d",
				record.Partition, partition.Kind, partition.Key, physical)
		}
		msg, err := DecodeKafkaEventRecordWithOptions(record, KafkaEventRecordDecodeOptions{
			DisableKafkaOffsetStreamSeq: options.DisableKafkaOffsetStreamSeq,
		})
		if err != nil {
			return nil, err
		}
		if msg.Partition != partition {
			continue
		}
		if msg.Offset <= afterOffset {
			continue
		}
		out = append(out, msg)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Offset < out[j].Offset
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (l *EventLog) Head(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if l == nil {
		return 0, fmt.Errorf("kafka event log: nil receiver")
	}
	options := normalizeEventLogOptions(l.options)
	reader := options.Head
	if reader == nil {
		if clientReader, ok := l.client.(EventLogHeadReader); ok {
			reader = clientReader
		}
	}
	if reader == nil {
		return 0, fmt.Errorf("kafka event log: head requires event position reader")
	}
	return reader.Head(ctx)
}

func (l *EventLog) ListEventPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, fmt.Errorf("kafka event log: nil receiver")
	}
	options := normalizeEventLogOptions(l.options)
	lister := options.Partitions
	if lister == nil {
		if clientLister, ok := l.client.(core.EventPartitionLister); ok {
			lister = clientLister
		}
	}
	if lister == nil {
		return nil, fmt.Errorf("kafka event log: partition listing requires event position reader")
	}
	return lister.ListEventPartitions(ctx, limit)
}

func (l *EventLog) ListEventPartitionOffsets(ctx context.Context, limit int) ([]core.EventPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, fmt.Errorf("kafka event log: nil receiver")
	}
	options := normalizeEventLogOptions(l.options)
	lister, _ := options.Partitions.(core.EventPartitionOffsetLister)
	if lister == nil {
		if clientLister, ok := l.client.(core.EventPartitionOffsetLister); ok {
			lister = clientLister
		}
	}
	if lister == nil {
		return nil, fmt.Errorf("kafka event log: partition offset listing requires event position reader")
	}
	return lister.ListEventPartitionOffsets(ctx, limit)
}

func normalizeEventLogOptions(options EventLogOptions) EventLogOptions {
	options.EventTopic = strings.TrimSpace(options.EventTopic)
	if options.EventTopic == "" {
		options.EventTopic = DefaultEventTopic
	}
	return options
}

func kafkaEventPhysicalPartition(options EventLogOptions, partition core.LogPartition) (int32, error) {
	if options.PartitionCount <= 0 {
		return 0, fmt.Errorf("kafka event log: partition count is required")
	}
	return KafkaPartitionForLogicalPartition(partition, options.PartitionCount)
}
