package kafkaconn

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/twmb/franz-go/pkg/kgo"
)

// OpenRuntimeCommandLog opens a consumer/producer command-log runtime client
// and adapts it to CommandLog. The returned cleanup allows a rebalance so
// drains do not pin partitions when the caller exits.
func OpenRuntimeCommandLog(ctx context.Context, config RuntimeConfig, clientID string, options CommandLogOptions, franzOptions FranzCommandLogClientOptions) (*CommandLog, func(), error) {
	if err := config.ValidateCommandLog(); err != nil {
		return nil, func() {}, err
	}
	runtime := config.Normalize()
	client, err := NewCommandLogRuntimeClient(ctx, CommandLogRuntimeClientOptions{
		Runtime:  runtime,
		ClientID: clientID,
	})
	if err != nil {
		return nil, func() {}, err
	}
	return newRuntimeCommandLog(client, runtime, options, franzOptions), client.CloseAllowingRebalance, nil
}

// OpenRuntimeCommandProducerLog opens a producer-only command-log runtime
// client for enqueue paths that do not consume from the command topic.
func OpenRuntimeCommandProducerLog(ctx context.Context, config RuntimeConfig, clientID string, options CommandLogOptions, franzOptions FranzCommandLogClientOptions) (*CommandLog, func(), error) {
	if err := config.ValidateCommandLog(); err != nil {
		return nil, func() {}, err
	}
	runtime := config.Normalize()
	client, err := NewCommandLogProducerRuntimeClient(ctx, CommandLogProducerRuntimeClientOptions{
		Runtime:  runtime,
		ClientID: clientID,
	})
	if err != nil {
		return nil, func() {}, err
	}
	return newRuntimeCommandLog(client, runtime, options, franzOptions), client.Close, nil
}

// OpenRuntimeEventStore opens an event-log runtime client and adapts it to the
// broker event-store interface used by projections and promotion checks.
func OpenRuntimeEventStore(ctx context.Context, config RuntimeConfig, clientID string, options EventLogOptions, franzOptions FranzEventLogClientOptions) (*core.BrokerEventStore, func(), error) {
	client, runtime, cleanup, err := openRuntimeEventLogClient(ctx, config, clientID)
	if err != nil {
		return nil, func() {}, err
	}
	options.EventTopic = runtime.EventTopic
	return NewEventStore(NewFranzEventLogClient(client, franzOptions), options), cleanup, nil
}

type SQLEventPositionedEventStoreOptions struct {
	PartitionCount       int32
	PartitionOnlyOffsets bool
}

func SQLEventPositionedEventLogOptions(db *sql.DB, options SQLEventPositionedEventStoreOptions) (EventLogOptions, *SQLEventPositionAllocator, error) {
	if db == nil {
		return EventLogOptions{}, nil, fmt.Errorf("sql-positioned kafka event store requires a materialization database")
	}
	allocator := NewSQLEventPositionAllocator(db, SQLEventPositionAllocatorOptions{
		DisableCompatibilitySeq: options.PartitionOnlyOffsets,
	})
	eventOptions := EventLogOptions{
		PartitionCount:              options.PartitionCount,
		Partitions:                  allocator,
		DisableKafkaOffsetStreamSeq: options.PartitionOnlyOffsets,
	}
	if !options.PartitionOnlyOffsets {
		eventOptions.Head = allocator
	}
	return eventOptions, allocator, nil
}

func OpenSQLPositionedRuntimeEventStore(ctx context.Context, config RuntimeConfig, clientID string, db *sql.DB, options SQLEventPositionedEventStoreOptions, franzOptions FranzEventLogClientOptions) (*core.BrokerEventStore, func(), error) {
	eventOptions, _, err := SQLEventPositionedEventLogOptions(db, options)
	if err != nil {
		return nil, func() {}, err
	}
	return OpenRuntimeEventStore(ctx, config, clientID, eventOptions, franzOptions)
}

// OpenRuntimeEventShadowStore opens the Kafka event shadow writer/replay store.
func OpenRuntimeEventShadowStore(ctx context.Context, config RuntimeConfig, clientID string, options EventLogOptions, franzOptions FranzEventLogClientOptions) (*core.BrokerEventStore, func(), error) {
	client, runtime, cleanup, err := openRuntimeEventLogClient(ctx, config, clientID)
	if err != nil {
		return nil, func() {}, err
	}
	options.EventTopic = runtime.EventTopic
	return NewEventLogShadowStore(client, options, franzOptions), cleanup, nil
}

func openRuntimeEventLogClient(ctx context.Context, config RuntimeConfig, clientID string) (*kgo.Client, RuntimeConfig, func(), error) {
	if err := config.ValidateEventLog(); err != nil {
		return nil, RuntimeConfig{}, func() {}, err
	}
	runtime := config.Normalize()
	client, err := NewEventLogRuntimeClient(ctx, EventLogRuntimeClientOptions{
		Runtime:  runtime,
		ClientID: clientID,
	})
	if err != nil {
		return nil, RuntimeConfig{}, func() {}, err
	}
	return client, runtime, client.Close, nil
}

func newRuntimeCommandLog(client *kgo.Client, runtime RuntimeConfig, options CommandLogOptions, franzOptions FranzCommandLogClientOptions) *CommandLog {
	options.CommandTopic = runtime.CommandTopic
	options.ConsumerGroup = runtime.ConsumerGroup
	return NewCommandLog(NewFranzCommandLogClient(client, franzOptions), options)
}
