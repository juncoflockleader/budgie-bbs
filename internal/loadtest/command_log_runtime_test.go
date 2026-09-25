package loadtest

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
)

func TestCommandLogLoadRuntimeReportsScalarCompatibilityAllocator(t *testing.T) {
	requireScalarCompatibilityAllocator(t, CommandLogLoadRuntimeConfig{
		Backend:      "nats",
		ExecutorMode: loadmodel.CommandLogDrainExecutorNative,
		PostgresDSN:  "postgres://postgres.internal:5432/budgie",
	}, loadmodel.CommandLogDrainScalarAllocatorBrokerStreamSequence)

	requireScalarCompatibilityAllocator(t, nativeKafkaRuntimeConfig(), loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets)

	kafkaPartitionConfig := nativeKafkaRuntimeConfig()
	kafkaPartitionConfig.ScalarAllocator = "partition-only"
	requireScalarCompatibilityAllocator(t, kafkaPartitionConfig, loadmodel.CommandLogDrainScalarAllocatorSQLEventPartitions)

	requireScalarCompatibilityAllocator(t, CommandLogLoadRuntimeConfig{
		Backend:      "memory",
		ExecutorMode: loadmodel.CommandLogDrainExecutorSQL,
	}, loadmodel.CommandLogDrainScalarAllocatorPostgresEventSeq)
}

func TestValidateCommandLogLoadRuntimeConfigRequiresKafkaPartitions(t *testing.T) {
	config := nativeKafkaRuntimeConfig()
	config.ExecutorMode = "sql"
	config.KafkaPartitions = 0
	requireRuntimeConfigError(t, config, "kafka command-log load requires -kafka-command-partitions")
	config.KafkaPartitions = 32
	requireValidRuntimeConfig(t, config, "kafka with partitions")

	config = nativeKafkaRuntimeConfig()
	config.KafkaEventPartitions = 0
	requireRuntimeConfigError(t, config, "kafka-event-partitions")
	config.KafkaEventPartitions = 32
	config.ScalarAllocator = "sql-event-partition-offsets"
	requireValidRuntimeConfig(t, config, "native kafka with partitions")
	config.ScalarAllocator = "global-sequence-service"
	requireRuntimeConfigError(t, config, "unsupported -kafka-scalar-allocator")
	config.ScalarAllocator = ""
	config.KafkaTopicReplicas = 0
	requireRuntimeConfigError(t, config, "kafka-topic-replicas")
}

func TestValidateCommandLogLoadRuntimeConfigRequiresRedisURLForIndex(t *testing.T) {
	requireRuntimeConfigError(t, CommandLogLoadRuntimeConfig{
		Backend:         "nats",
		ExecutorMode:    "sql",
		CommandLogIndex: "redis",
	}, "requires -redis")
	requireValidRuntimeConfig(t, CommandLogLoadRuntimeConfig{
		Backend:         "nats",
		ExecutorMode:    "sql",
		CommandLogIndex: "redis",
		RedisURL:        "redis://redis.internal:6379",
	}, "redis index with URL")
	requireRuntimeConfigError(t, CommandLogLoadRuntimeConfig{
		Backend:         "nats",
		ExecutorMode:    "sql",
		CommandLogIndex: "memcached",
	}, "unsupported -command-log-index")
}

func TestValidateCommandLogLoadRuntimeConfigRequiresPostgres(t *testing.T) {
	requireRuntimeConfigError(t, CommandLogLoadRuntimeConfig{
		RequirePostgres: true,
		Backend:         "memory",
		ExecutorMode:    "sql",
	}, "requires -postgres-dsn")
	requireValidRuntimeConfig(t, CommandLogLoadRuntimeConfig{
		PostgresDSN:     "postgres://example/budgie",
		RequirePostgres: true,
		Backend:         "memory",
		ExecutorMode:    "sql",
	}, "with postgres DSN")
}

func TestValidateCommandLogLoadRuntimeConfigRejectsSharedNativeNATSStreams(t *testing.T) {
	requireRuntimeConfigError(t, CommandLogLoadRuntimeConfig{
		PostgresDSN:       "postgres://example/budgie",
		Backend:           "nats",
		ExecutorMode:      "native",
		CommandNATSStream: "BUDGIE_LOAD",
		EventNATSStream:   "BUDGIE_LOAD",
	}, "distinct command and event streams")
	requireValidRuntimeConfig(t, CommandLogLoadRuntimeConfig{
		PostgresDSN:       "postgres://example/budgie",
		Backend:           "nats",
		ExecutorMode:      "native",
		CommandNATSStream: "BUDGIE_COMMAND_LOAD",
		EventNATSStream:   "BUDGIE_EVENT_LOAD",
	}, "distinct native NATS streams")
	requireValidRuntimeConfig(t, CommandLogLoadRuntimeConfig{
		Backend:           "memory",
		ExecutorMode:      "native",
		CommandNATSStream: "BUDGIE_LOAD",
		EventNATSStream:   "BUDGIE_LOAD",
	}, "shared stream names for memory backend")
}

func TestValidateCommandLogLoadRuntimeConfigRequiresPostgresForNativeNATS(t *testing.T) {
	requireRuntimeConfigError(t, CommandLogLoadRuntimeConfig{
		Backend:           "nats",
		ExecutorMode:      "native",
		CommandNATSStream: "BUDGIE_COMMAND_LOAD",
		EventNATSStream:   "BUDGIE_EVENT_LOAD",
	}, "native NATS command-log load requires -postgres-dsn")
}

func TestCommandLogLoadRuntimeReportIdentifiesNativeNATSDurableStaging(t *testing.T) {
	report := CommandLogLoadRuntimeReport(CommandLogLoadRuntimeConfig{
		PostgresDSN:           "postgres://budgie:secret@postgres.internal:5432/budgie?sslmode=require",
		RequirePostgres:       true,
		PostgresSchema:        "budgie_cmdlog_load_123",
		KeepPostgresSchema:    false,
		NATSURL:               "nats://user:secret@nats.internal:4222?token=secret",
		Backend:               "jetstream",
		ExecutorMode:          "native",
		CommandNATSStream:     "BUDGIE_COMMAND_LOAD",
		CommandNATSReplicas:   3,
		EventNATSStream:       "BUDGIE_EVENT_LOAD",
		EventNATSReplicas:     2,
		CommandLogIndex:       "redis",
		CommandLogIndexPrefix: "budgie:commandlog-load:test",
		RedisURL:              "redis://default:secret@redis.internal:6379/2?password=secret",
	})
	requireRuntimeBackendReport(t, report, "nats", "nats", "postgres", true, true)
	if report.CommandNATSStream != "BUDGIE_COMMAND_LOAD" || report.EventNATSStream != "BUDGIE_EVENT_LOAD" {
		t.Fatalf("runtime streams = %q/%q, want command/event stream names", report.CommandNATSStream, report.EventNATSStream)
	}
	if report.CommandNATSReplicas != 3 || report.EventNATSReplicas != 2 {
		t.Fatalf("runtime replicas = %d/%d, want command/event replica counts", report.CommandNATSReplicas, report.EventNATSReplicas)
	}
	if report.NATSEndpoint != "nats://nats.internal:4222" || report.PostgresEndpoint != "postgres://postgres.internal:5432/budgie" {
		t.Fatalf("runtime endpoints = %q/%q, want redacted endpoints", report.NATSEndpoint, report.PostgresEndpoint)
	}
	if report.CommandLogIndexBackend != "redis" || report.CommandLogIndexPrefix != "budgie:commandlog-load:test" || report.RedisEndpoint != "redis://redis.internal:6379/2" {
		t.Fatalf("runtime redis index metadata = backend:%q prefix:%q endpoint:%q, want redacted redis index evidence", report.CommandLogIndexBackend, report.CommandLogIndexPrefix, report.RedisEndpoint)
	}
	if strings.Contains(fmt.Sprintf("%+v", report), "secret") || strings.Contains(fmt.Sprintf("%+v", report), "default@") {
		t.Fatalf("runtime report leaked secret material: %+v", report)
	}
	if report.PostgresSchema != "budgie_cmdlog_load_123" || report.KeepPostgresSchema {
		t.Fatalf("runtime postgres schema = %q keep=%v, want disposable schema evidence", report.PostgresSchema, report.KeepPostgresSchema)
	}
}

func TestCommandLogLoadRuntimeReportIdentifiesNativeKafkaDurableStaging(t *testing.T) {
	report := CommandLogLoadRuntimeReport(CommandLogLoadRuntimeConfig{
		PostgresDSN:     "postgres://budgie:secret@postgres.internal:5432/budgie?sslmode=require",
		RequirePostgres: true,
		PostgresSchema:  "budgie_cmdlog_load_123",
		Backend:         "redpanda",
		ExecutorMode:    "native",
		Kafka: kafkaconn.RuntimeConfigFromOptions("user:secret@redpanda-a.internal:9092?token=secret,redpanda-b.internal:9092", "budgie.commands.load.20260613", "budgie.events.load.20260613", "budgie-writers-load", kafkaconn.RuntimeSecurityConfig{
			TLS:           true,
			SASLMechanism: "scram-sha-512",
			SASLUser:      "budgie",
			SASLPassword:  "secret",
		}),
		KafkaPartitions:      32,
		KafkaEventPartitions: 48,
	})
	requireRuntimeBackendReport(t, report, "kafka", "kafka", "postgres", true, true)
	if len(report.KafkaBrokers) != 2 ||
		report.KafkaBrokers[0] != "kafka://redpanda-a.internal:9092" ||
		report.KafkaBrokers[1] != "kafka://redpanda-b.internal:9092" {
		t.Fatalf("runtime Kafka brokers = %+v, want sanitized broker endpoints", report.KafkaBrokers)
	}
	if report.KafkaCommandTopic != "budgie.commands.load.20260613" ||
		report.KafkaEventTopic != "budgie.events.load.20260613" ||
		report.KafkaConsumerGroup != "budgie-writers-load" {
		t.Fatalf("runtime Kafka topics/group = %+v, want command/event topics and group", report)
	}
	if !report.KafkaTLS || report.KafkaSASLMechanism != "scram-sha-512" {
		t.Fatalf("runtime Kafka security = tls:%v sasl:%q, want non-secret TLS/SASL evidence", report.KafkaTLS, report.KafkaSASLMechanism)
	}
	if strings.Contains(fmt.Sprintf("%+v", report), "secret") || strings.Contains(fmt.Sprintf("%+v", report), "budgie@") {
		t.Fatalf("runtime Kafka report leaked secret material: %+v", report)
	}
	if report.KafkaCommandPartitions != 32 || report.KafkaEventPartitions != 48 {
		t.Fatalf("runtime Kafka partitions = %d/%d, want command/event partition counts", report.KafkaCommandPartitions, report.KafkaEventPartitions)
	}
	if report.NATSEndpoint != "" || report.CommandNATSStream != "" || report.EventNATSStream != "" {
		t.Fatalf("runtime report = %+v, want no NATS metadata for Kafka run", report)
	}
}

func TestAttachCommandLogLoadScalarCompatibilityAudit(t *testing.T) {
	ctx := context.Background()
	c, err := core.New(filepath.Join(t.TempDir(), "scalar-compatibility-audit.db"))
	if err != nil {
		t.Fatalf("open core: %v", err)
	}
	defer c.DB.Close()

	report := loadmodel.CommandLogDrainLoadReport{
		Runtime: loadmodel.CommandLogDrainLoadRuntime{
			MaterializationStore: "sqlite",
		},
	}
	if err := AttachCommandLogLoadScalarCompatibilityAudit(ctx, c.DB, &report); err != nil {
		t.Fatalf("attach skipped sqlite audit: %v", err)
	}
	if report.ScalarCompatibilityAudit.Enabled {
		t.Fatalf("sqlite materialization audit = %+v, want skipped", report.ScalarCompatibilityAudit)
	}

	if _, err := c.DB.Exec(`UPDATE event_scalar_offsets SET last_seq=7 WHERE id='broker_event_log'`); err != nil {
		t.Fatalf("seed scalar offset: %v", err)
	}
	report.Runtime.MaterializationStore = "postgres"
	if err := AttachCommandLogLoadScalarCompatibilityAudit(ctx, c.DB, &report); err != nil {
		t.Fatalf("attach scalar audit: %v", err)
	}
	if !report.ScalarCompatibilityAudit.Enabled ||
		report.ScalarCompatibilityAudit.Store != "event_scalar_offsets" ||
		report.ScalarCompatibilityAudit.OffsetID != "broker_event_log" ||
		report.ScalarCompatibilityAudit.LegacySQLScalarOffsetAfter != 7 {
		t.Fatalf("scalar audit = %+v, want captured broker_event_log offset", report.ScalarCompatibilityAudit)
	}
}

func TestCommandLogLoadRuntimeReportLeavesMemoryFixtureNonDurable(t *testing.T) {
	report := CommandLogLoadRuntimeReport(CommandLogLoadRuntimeConfig{
		Backend:           "memory",
		ExecutorMode:      "native",
		CommandNATSStream: "BUDGIE_COMMAND_LOAD",
		EventNATSStream:   "BUDGIE_EVENT_LOAD",
	})
	requireRuntimeBackendReport(t, report, "memory", "memory", "sqlite", false, false)
	if report.CommandNATSStream != "" || report.EventNATSStream != "" {
		t.Fatalf("runtime report = %+v, want memory fixture without NATS streams", report)
	}
	if report.CommandNATSReplicas != 0 || report.EventNATSReplicas != 0 {
		t.Fatalf("runtime report = %+v, want non-durable memory fixture without NATS replicas", report)
	}
	if report.PostgresSchema != "" || report.KeepPostgresSchema {
		t.Fatalf("runtime report = %+v, want no postgres schema metadata for memory fixture", report)
	}
	if report.NATSEndpoint != "" || report.PostgresEndpoint != "" {
		t.Fatalf("runtime report = %+v, want no endpoint metadata for memory fixture", report)
	}
}

func requireScalarCompatibilityAllocator(t *testing.T, config CommandLogLoadRuntimeConfig, want string) {
	t.Helper()
	runtime := CommandLogLoadRuntimeReport(config)
	if runtime.ScalarCompatibilityAllocator != want {
		t.Fatalf("scalar allocator = %q, want %q", runtime.ScalarCompatibilityAllocator, want)
	}
}

func requireRuntimeBackendReport(t *testing.T, report loadmodel.CommandLogDrainLoadRuntime, commandBackend, eventBackend, materialization string, requirePostgres, durable bool) {
	t.Helper()
	if report.CommandLogBackend != commandBackend || report.EventLogBackend != eventBackend || report.MaterializationStore != materialization ||
		report.RequirePostgres != requirePostgres || report.DurableStaging != durable {
		t.Fatalf("runtime report = %+v, want backend=%s event=%s store=%s requirePostgres=%v durable=%v",
			report, commandBackend, eventBackend, materialization, requirePostgres, durable)
	}
}

func nativeKafkaRuntimeConfig() CommandLogLoadRuntimeConfig {
	return CommandLogLoadRuntimeConfig{
		Backend:              "redpanda",
		ExecutorMode:         loadmodel.CommandLogDrainExecutorNative,
		PostgresDSN:          "postgres://postgres.internal:5432/budgie",
		Kafka:                kafkaconn.RuntimeConfigFromFlags("redpanda.internal:9092", "budgie.commands", "budgie.events", ""),
		KafkaPartitions:      32,
		KafkaEventPartitions: 32,
		KafkaTopicReplicas:   1,
	}
}

func requireRuntimeConfigError(t *testing.T, config CommandLogLoadRuntimeConfig, want string) {
	t.Helper()
	err := ValidateCommandLogLoadRuntimeConfig(config)
	requireErrorContains(t, err, want)
}

func requireValidRuntimeConfig(t *testing.T, config CommandLogLoadRuntimeConfig, name string) {
	t.Helper()
	if err := ValidateCommandLogLoadRuntimeConfig(config); err != nil {
		t.Fatalf("ValidateCommandLogLoadRuntimeConfig(%s): %v", name, err)
	}
}
