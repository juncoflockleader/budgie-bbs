package loadtest

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
)

func TestOpenCommandLogMemoryModes(t *testing.T) {
	log := requireOpenCommandLog(t, CommandLogOpenConfig{
		Backend: "memory",
	})
	if log != nil {
		t.Fatalf("direct memory command log = %T, want nil so runner uses its default fixture", log)
	}

	log = requireOpenCommandLog(t, CommandLogOpenConfig{
		Backend:       "memory",
		Authoritative: true,
	})
	if log == nil {
		t.Fatalf("authoritative memory command log = nil, want command log")
	}
}

func TestOpenCommandLogRejectsUnsupportedOrMissingNATS(t *testing.T) {
	requireOpenCommandLogError(t, CommandLogOpenConfig{
		Backend: "redis",
	}, "unsupported command log backend")
	requireOpenCommandLogError(t, CommandLogOpenConfig{
		Backend:    "nats",
		NATSStream: "BUDGIE_COMMAND_LOG_TEST",
	}, "requires -nats")
}

func TestOpenCommandLogLoadIndexWrapsRedisIndex(t *testing.T) {
	log, cleanup, err := OpenCommandLogLoadIndex(
		context.Background(),
		core.NewBrokerCommandLog(core.NewMemoryBrokerCommandLogClient()),
		CommandLogIndexOpenConfig{
			Backend:  "redis",
			RedisURL: "redis://:secret@redis.internal:6379/3",
			Prefix:   "test:load:index",
		},
	)
	defer cleanup()
	if err != nil {
		t.Fatalf("open redis command-log index: %v", err)
	}
	if _, ok := log.(*core.IndexedCommandLog); !ok {
		t.Fatalf("indexed log = %T, want *core.IndexedCommandLog", log)
	}
	_, cleanup, err = OpenCommandLogLoadIndex(context.Background(), nil, CommandLogIndexOpenConfig{
		Backend:  "redis",
		RedisURL: "redis://redis.internal:6379",
		Prefix:   "test",
	})
	defer cleanup()
	requireErrorContains(t, err, "requires an explicit command log backend")
}

func TestOpenCommandLogOpensKafkaBackend(t *testing.T) {
	kafkaConfig := kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "", "", "")
	log := requireOpenCommandLog(t, CommandLogOpenConfig{
		Backend:         "redpanda",
		Kafka:           kafkaConfig,
		KafkaPartitions: 32,
		ClientID:        "budgie-commandlog-loadgen-test",
	})
	if log == nil {
		t.Fatalf("open redpanda command log returned nil")
	}
	requireOpenCommandLogError(t, CommandLogOpenConfig{
		Backend:  "redpanda",
		Kafka:    kafkaConfig,
		ClientID: "budgie-commandlog-loadgen-test",
	}, "requires -kafka-command-partitions")
	stores, err := OpenNativeCommandEventStores(context.Background(), NativeCommandEventStoreConfig{
		Backend:                "kafka",
		Kafka:                  kafkaConfig,
		KafkaCommandPartitions: 32,
		KafkaEventPartitions:   32,
		ClientID:               "budgie-commandlog-loadgen-test",
	})
	defer stores.Cleanup()
	if err != nil {
		t.Fatalf("open native kafka backend: %v", err)
	}
	if stores.CommandLog == nil || stores.Transactions != nil || stores.EventStore != nil || stores.Binder == nil {
		t.Fatalf("native kafka open = log:%T transactions:%T eventStore:%T binder:%v, want log plus post-core binder",
			stores.CommandLog, stores.Transactions, stores.EventStore, stores.Binder != nil)
	}
	requireNativeCommandEventStoresError(t, NativeCommandEventStoreConfig{
		Backend:                "kafka",
		Kafka:                  kafkaConfig,
		KafkaCommandPartitions: 32,
		ClientID:               "budgie-commandlog-loadgen-test",
	}, "requires -kafka-event-partitions")
	if got := NormalizeCommandLogLoadBackend("Redpanda"); got != "kafka" {
		t.Fatalf("normalize redpanda backend = %q, want kafka", got)
	}
}

func TestOpenCommandLogValidatesKafkaRuntimeConfigBeforePendingAdapter(t *testing.T) {
	requireOpenCommandLogError(t, CommandLogOpenConfig{
		Backend:         "kafka",
		KafkaPartitions: 32,
		ClientID:        "budgie-commandlog-loadgen-test",
	}, "broker list is required")
	kafkaConfig := kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.log", "budgie.log", "")
	requireNativeCommandEventStoresError(t, NativeCommandEventStoreConfig{
		Backend:                "kafka",
		Kafka:                  kafkaConfig,
		KafkaCommandPartitions: 32,
		KafkaEventPartitions:   32,
		ClientID:               "budgie-commandlog-loadgen-test",
	}, "command and event topics must be distinct")
}

func requireOpenCommandLog(t *testing.T, config CommandLogOpenConfig) core.CommandLog {
	t.Helper()
	log, cleanup, err := OpenCommandLog(context.Background(), config)
	t.Cleanup(cleanup)
	if err != nil {
		t.Fatalf("OpenCommandLog: %v", err)
	}
	return log
}

func requireOpenCommandLogError(t *testing.T, config CommandLogOpenConfig, want string) {
	t.Helper()
	_, cleanup, err := OpenCommandLog(context.Background(), config)
	defer cleanup()
	requireErrorContains(t, err, want)
}

func requireNativeCommandEventStoresError(t *testing.T, config NativeCommandEventStoreConfig, want string) {
	t.Helper()
	stores, err := OpenNativeCommandEventStores(context.Background(), config)
	if stores.Cleanup != nil {
		defer stores.Cleanup()
	}
	requireErrorContains(t, err, want)
}
