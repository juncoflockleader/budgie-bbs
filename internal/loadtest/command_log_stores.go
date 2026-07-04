package loadtest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/natsconn"
	"github.com/juncoflockleader/budgie-bbs/internal/redisconn"
)

type CommandLogOpenConfig struct {
	Backend         string
	NATSURL         string
	NATSStream      string
	NATSReplicas    int
	Authoritative   bool
	Kafka           kafkaconn.RuntimeConfig
	KafkaPartitions int32
	ClientID        string
}

type CommandLogIndexOpenConfig struct {
	Backend  string
	RedisURL string
	Prefix   string
	Logf     func(string, ...any)
}

type NativeCommandEventStoreBinder func(*sql.DB) (core.CommandEventTransactionStore, core.EventStore, error)

type NativeCommandEventStoreConfig struct {
	Backend                string
	NATSURL                string
	CommandNATSStream      string
	CommandNATSReplicas    int
	EventNATSStream        string
	EventNATSReplicas      int
	Kafka                  kafkaconn.RuntimeConfig
	KafkaCommandPartitions int32
	KafkaEventPartitions   int32
	ScalarAllocator        string
	ClientID               string
}

type NativeCommandEventStores struct {
	CommandLog   core.CommandLog
	Transactions core.CommandEventTransactionStore
	EventStore   core.EventStore
	Binder       NativeCommandEventStoreBinder
	Cleanup      func()
}

func OpenCommandLog(ctx context.Context, config CommandLogOpenConfig) (core.CommandLog, func(), error) {
	backend := NormalizeCommandLogLoadBackend(config.Backend)
	switch backend {
	case "memory":
		if config.Authoritative {
			return core.NewBrokerCommandLog(core.NewMemoryBrokerCommandLogClient()), func() {}, nil
		}
		return nil, func() {}, nil
	case "nats":
		natsURL := strings.TrimSpace(config.NATSURL)
		if natsURL == "" {
			return nil, func() {}, fmt.Errorf("-command-log-backend nats requires -nats or BUDGIE_NATS_URL")
		}
		log, cleanup, err := natsconn.OpenJetStreamCommandLog(ctx, natsURL, natsconn.JetStreamCommandLogOptions{
			Stream:   config.NATSStream,
			Replicas: config.NATSReplicas,
		})
		if err != nil {
			return nil, func() {}, err
		}
		return log, cleanup, nil
	case "kafka":
		if err := config.Kafka.ValidateCommandLogRuntime(config.KafkaPartitions); err != nil {
			return nil, func() {}, fmt.Errorf("command log backend %q requires %w", backend, err)
		}
		log, cleanup, err := kafkaconn.OpenRuntimeCommandLog(ctx, config.Kafka, strings.TrimSpace(config.ClientID), kafkaconn.CommandLogOptions{
			PartitionCount: config.KafkaPartitions,
		}, kafkaconn.FranzCommandLogClientOptions{})
		if err != nil {
			return nil, func() {}, err
		}
		return newIndexedCommandLog(log), cleanup, nil
	default:
		return nil, func() {}, fmt.Errorf("unsupported command log backend %q; supported: memory,nats,kafka", backend)
	}
}

func OpenCommandLogLoadIndex(ctx context.Context, commandLog core.CommandLog, config CommandLogIndexOpenConfig) (core.CommandLog, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}
	backend := NormalizeCommandLogLoadIndexBackend(config.Backend)
	switch backend {
	case "":
		return commandLog, func() {}, nil
	case "redis":
		if commandLog == nil {
			return nil, func() {}, fmt.Errorf("-command-log-index redis requires an explicit command log backend; memory direct mode has no external command log")
		}
		client, err := redisconn.NewClient(config.RedisURL)
		if err != nil {
			return nil, func() {}, err
		}
		cleanup := func() {
			if err := client.Close(); err != nil && config.Logf != nil {
				config.Logf("close Redis command-log index: %v", err)
			}
		}
		index := redisconn.NewCommandLogPartitionIndex(client, redisconn.CommandLogPartitionIndexOptions{
			Prefix: config.Prefix,
		})
		return core.NewIndexedCommandLog(commandLog, index), cleanup, nil
	default:
		return nil, func() {}, fmt.Errorf("unsupported command-log index backend %q; supported: redis", backend)
	}
}

func OpenNativeCommandEventStores(ctx context.Context, config NativeCommandEventStoreConfig) (NativeCommandEventStores, error) {
	backend := NormalizeCommandLogLoadBackend(config.Backend)
	switch backend {
	case "memory":
		commandClient := core.NewMemoryBrokerCommandLogClient()
		eventClient := core.NewMemoryBrokerEventLogClient()
		return NativeCommandEventStores{
			CommandLog:   core.NewBrokerCommandLog(commandClient),
			Transactions: core.NewBrokerCommandEventTransactionStore(core.NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient)),
			EventStore:   core.NewBrokerEventStore(eventClient),
			Cleanup:      func() {},
		}, nil
	case "nats":
		natsURL := strings.TrimSpace(config.NATSURL)
		if natsURL == "" {
			return NativeCommandEventStores{Cleanup: func() {}}, fmt.Errorf("-command-log-backend nats requires -nats or BUDGIE_NATS_URL")
		}
		commandLog, transactions, eventStore, cleanup, err := natsconn.OpenJetStreamCommandEventStores(ctx, natsURL, natsconn.JetStreamCommandLogOptions{
			Stream:   config.CommandNATSStream,
			Replicas: config.CommandNATSReplicas,
		}, natsconn.JetStreamEventLogOptions{
			Stream:   config.EventNATSStream,
			Replicas: config.EventNATSReplicas,
		})
		if err != nil {
			return NativeCommandEventStores{Cleanup: func() {}}, err
		}
		return NativeCommandEventStores{
			CommandLog:   commandLog,
			Transactions: transactions,
			EventStore:   eventStore,
			Cleanup:      cleanup,
		}, nil
	case "kafka":
		return openNativeKafkaCommandEventStores(ctx, config)
	default:
		return NativeCommandEventStores{Cleanup: func() {}}, fmt.Errorf("unsupported command log backend %q; supported: memory,nats,kafka", backend)
	}
}

func openNativeKafkaCommandEventStores(ctx context.Context, config NativeCommandEventStoreConfig) (NativeCommandEventStores, error) {
	if err := config.Kafka.ValidateCommandEventRuntime(config.KafkaCommandPartitions, config.KafkaEventPartitions); err != nil {
		return NativeCommandEventStores{Cleanup: func() {}}, fmt.Errorf("native Kafka command/event load requires %w", err)
	}
	runtime := config.Kafka.Normalize()
	clientID := strings.TrimSpace(config.ClientID)
	commandProducerLog, commandProducerCleanup, err := kafkaconn.OpenRuntimeCommandProducerLog(ctx, config.Kafka, clientID+"-command-producer", kafkaconn.CommandLogOptions{
		PartitionCount: config.KafkaCommandPartitions,
	}, kafkaconn.FranzCommandLogClientOptions{})
	if err != nil {
		return NativeCommandEventStores{Cleanup: func() {}}, err
	}
	var eventClientCleanup func()
	var transactionSessionCleanup func()
	cleanup := func() {
		if eventClientCleanup != nil {
			eventClientCleanup()
		}
		if transactionSessionCleanup != nil {
			transactionSessionCleanup()
		}
		commandProducerCleanup()
	}
	commandLog := core.NewSwitchableCommandLog(commandProducerLog)
	binder := func(db *sql.DB) (core.CommandEventTransactionStore, core.EventStore, error) {
		if db == nil {
			return nil, nil, fmt.Errorf("native Kafka command/event load requires a materialization database")
		}
		partitionOnly := commandLogLoadScalarCompatibilityAllocator("kafka", loadmodel.CommandLogDrainExecutorNative, config.ScalarAllocator) == loadmodel.CommandLogDrainScalarAllocatorSQLEventPartitions
		eventLogOptions, allocator, err := kafkaconn.SQLEventPositionedEventLogOptions(db, kafkaconn.SQLEventPositionedEventStoreOptions{
			PartitionCount:       config.KafkaEventPartitions,
			PartitionOnlyOffsets: partitionOnly,
		})
		if err != nil {
			return nil, nil, err
		}
		transactionSession, err := kafkaconn.NewCommandWriterTransactionSession(ctx, kafkaconn.CommandWriterClientOptions{
			Runtime:         runtime,
			ClientID:        clientID + "-writer",
			TransactionalID: clientID + "-tx",
		})
		if err != nil {
			return nil, nil, err
		}
		transactionSessionCleanup = transactionSession.CloseAllowingRebalance
		commandLog.SetDrainLog(kafkaconn.NewCommandLog(
			kafkaconn.NewFranzCommandLogClient(transactionSession.Client(), kafkaconn.FastDrainFranzCommandLogClientOptions()),
			kafkaconn.CommandLogOptions{
				CommandTopic:   runtime.CommandTopic,
				ConsumerGroup:  runtime.ConsumerGroup,
				PartitionCount: config.KafkaCommandPartitions,
			},
		))
		transactions := core.NewBrokerCommandEventTransactionStoreWithOptions(
			kafkaconn.NewCommandEventTransactionClient(
				kafkaconn.NewFranzCommandEventTransactionBeginner(transactionSession, allocator),
				kafkaconn.CommandEventTransactionOptions{
					CommandTopic:             runtime.CommandTopic,
					EventTopic:               runtime.EventTopic,
					ConsumerGroup:            runtime.ConsumerGroup,
					AllowPartitionOnlyEvents: partitionOnly,
				},
			),
			core.BrokerCommandEventTransactionStoreOptions{AllowPartitionOnlyEvents: partitionOnly},
		)
		eventStore, cleanup, err := kafkaconn.OpenRuntimeEventStore(ctx, config.Kafka, clientID+"-events", eventLogOptions, kafkaconn.FranzEventLogClientOptions{})
		if err != nil {
			return nil, nil, err
		}
		eventClientCleanup = cleanup
		return transactions, eventStore, nil
	}
	return NativeCommandEventStores{
		CommandLog: newIndexedCommandLog(commandLog),
		Binder:     binder,
		Cleanup:    cleanup,
	}, nil
}
