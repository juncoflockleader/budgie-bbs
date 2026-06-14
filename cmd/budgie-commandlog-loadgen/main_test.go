package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
)

func TestOpenCommandLogMemoryModes(t *testing.T) {
	log, cleanup, err := openCommandLog(context.Background(), "memory", "", "", 1, false, kafkaconn.RuntimeConfig{}, 0)
	defer cleanup()
	if err != nil {
		t.Fatalf("open memory direct command log: %v", err)
	}
	if log != nil {
		t.Fatalf("direct memory command log = %T, want nil so runner uses its default fixture", log)
	}

	log, cleanup, err = openCommandLog(context.Background(), "memory", "", "", 1, true, kafkaconn.RuntimeConfig{}, 0)
	defer cleanup()
	if err != nil {
		t.Fatalf("open memory authoritative command log: %v", err)
	}
	if log == nil {
		t.Fatalf("authoritative memory command log = nil, want command log")
	}
}

func TestOpenCommandLogRejectsUnsupportedOrMissingNATS(t *testing.T) {
	if _, cleanup, err := openCommandLog(context.Background(), "redis", "", "", 1, false, kafkaconn.RuntimeConfig{}, 0); err == nil {
		defer cleanup()
		t.Fatalf("open unsupported backend succeeded, want error")
	}
	if _, cleanup, err := openCommandLog(context.Background(), "nats", "", "BUDGIE_COMMAND_LOG_TEST", 1, false, kafkaconn.RuntimeConfig{}, 0); err == nil || !strings.Contains(err.Error(), "requires -nats") {
		defer cleanup()
		t.Fatalf("open nats without URL err = %v, want missing URL error", err)
	}
}

func TestOpenCommandLogLoadIndexWrapsRedisIndex(t *testing.T) {
	log, cleanup, err := openCommandLogLoadIndex(
		context.Background(),
		core.NewBrokerCommandLog(core.NewMemoryBrokerCommandLogClient()),
		"redis",
		"redis://:secret@redis.internal:6379/3",
		"test:load:index",
	)
	defer cleanup()
	if err != nil {
		t.Fatalf("open redis command-log index: %v", err)
	}
	if _, ok := log.(*core.IndexedCommandLog); !ok {
		t.Fatalf("indexed log = %T, want *core.IndexedCommandLog", log)
	}
	if _, cleanup, err := openCommandLogLoadIndex(context.Background(), nil, "redis", "redis://redis.internal:6379", "test"); err == nil || !strings.Contains(err.Error(), "requires an explicit command log backend") {
		defer cleanup()
		t.Fatalf("open redis index without command log err = %v, want explicit command-log error", err)
	}
}

func TestOpenCommandLogOpensKafkaBackend(t *testing.T) {
	kafkaConfig := kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "", "", "")
	log, cleanup, err := openCommandLog(context.Background(), "redpanda", "", "", 1, false, kafkaConfig, 32)
	defer cleanup()
	if err != nil {
		t.Fatalf("open redpanda command log: %v", err)
	}
	if log == nil {
		t.Fatalf("open redpanda command log returned nil")
	}
	if _, cleanup, err := openCommandLog(context.Background(), "redpanda", "", "", 1, false, kafkaConfig, 0); err == nil || !strings.Contains(err.Error(), "requires -kafka-command-partitions") {
		defer cleanup()
		t.Fatalf("open redpanda backend without partitions err = %v, want partition-count error", err)
	}
	nativeLog, transactions, eventStore, binder, cleanup, err := openNativeCommandEventStores(context.Background(), "kafka", "", "", 1, "", 1, kafkaConfig, 32, 32, "")
	defer cleanup()
	if err != nil {
		t.Fatalf("open native kafka backend: %v", err)
	}
	if nativeLog == nil || transactions != nil || eventStore != nil || binder == nil {
		t.Fatalf("native kafka open = log:%T transactions:%T eventStore:%T binder:%v, want log plus post-core binder", nativeLog, transactions, eventStore, binder != nil)
	}
	if _, _, _, _, cleanup, err := openNativeCommandEventStores(context.Background(), "kafka", "", "", 1, "", 1, kafkaConfig, 32, 0, ""); err == nil || !strings.Contains(err.Error(), "requires -kafka-event-partitions") {
		defer cleanup()
		t.Fatalf("open native kafka backend without event partitions err = %v, want event partition error", err)
	}
	if got := normalizeCommandLogLoadBackend("Redpanda"); got != "kafka" {
		t.Fatalf("normalize redpanda backend = %q, want kafka", got)
	}
}

func TestOpenCommandLogValidatesKafkaRuntimeConfigBeforePendingAdapter(t *testing.T) {
	if _, cleanup, err := openCommandLog(context.Background(), "kafka", "", "", 1, false, kafkaconn.RuntimeConfig{}, 32); err == nil || !strings.Contains(err.Error(), "broker list is required") {
		defer cleanup()
		t.Fatalf("open kafka without brokers err = %v, want broker-list error", err)
	}
	kafkaConfig := kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.log", "budgie.log", "")
	if _, _, _, _, cleanup, err := openNativeCommandEventStores(context.Background(), "kafka", "", "", 1, "", 1, kafkaConfig, 32, 32, ""); err == nil || !strings.Contains(err.Error(), "command and event topics must be distinct") {
		defer cleanup()
		t.Fatalf("open native kafka with shared topic err = %v, want distinct topic error", err)
	}
}

func TestCommandLogLoadRuntimeReportsScalarCompatibilityAllocator(t *testing.T) {
	natsRuntime := commandLogLoadRuntimeReport(commandLogLoadRuntimeConfig{
		Backend:      "nats",
		ExecutorMode: core.CommandLogDrainExecutorNative,
		PostgresDSN:  "postgres://postgres.internal:5432/budgie",
	})
	if natsRuntime.ScalarCompatibilityAllocator != core.CommandLogDrainScalarAllocatorBrokerStreamSequence {
		t.Fatalf("nats scalar allocator = %q, want broker stream sequence", natsRuntime.ScalarCompatibilityAllocator)
	}

	kafkaRuntime := commandLogLoadRuntimeReport(commandLogLoadRuntimeConfig{
		Backend:              "redpanda",
		ExecutorMode:         core.CommandLogDrainExecutorNative,
		PostgresDSN:          "postgres://postgres.internal:5432/budgie",
		Kafka:                kafkaconn.RuntimeConfigFromFlags("redpanda.internal:9092", "budgie.commands", "budgie.events", ""),
		KafkaPartitions:      32,
		KafkaEventPartitions: 32,
	})
	if kafkaRuntime.ScalarCompatibilityAllocator != core.CommandLogDrainScalarAllocatorSQLEventOffsets {
		t.Fatalf("kafka scalar allocator = %q, want SQL scalar offsets", kafkaRuntime.ScalarCompatibilityAllocator)
	}

	kafkaPartitionRuntime := commandLogLoadRuntimeReport(commandLogLoadRuntimeConfig{
		Backend:              "redpanda",
		ExecutorMode:         core.CommandLogDrainExecutorNative,
		PostgresDSN:          "postgres://postgres.internal:5432/budgie",
		ScalarAllocator:      "partition-only",
		Kafka:                kafkaconn.RuntimeConfigFromFlags("redpanda.internal:9092", "budgie.commands", "budgie.events", ""),
		KafkaPartitions:      32,
		KafkaEventPartitions: 32,
	})
	if kafkaPartitionRuntime.ScalarCompatibilityAllocator != core.CommandLogDrainScalarAllocatorSQLEventPartitions {
		t.Fatalf("partition-only kafka scalar allocator = %q, want SQL partition offsets", kafkaPartitionRuntime.ScalarCompatibilityAllocator)
	}

	sqlRuntime := commandLogLoadRuntimeReport(commandLogLoadRuntimeConfig{
		Backend:      "memory",
		ExecutorMode: core.CommandLogDrainExecutorSQL,
	})
	if sqlRuntime.ScalarCompatibilityAllocator != core.CommandLogDrainScalarAllocatorPostgresEventSeq {
		t.Fatalf("sql scalar allocator = %q, want Postgres event seq", sqlRuntime.ScalarCompatibilityAllocator)
	}
}

func TestValidateCommandLogLoadRuntimeConfigRequiresKafkaPartitions(t *testing.T) {
	err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:            "kafka",
		ExecutorMode:       "sql",
		Kafka:              kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "", "", ""),
		KafkaTopicReplicas: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "kafka command-log load requires -kafka-command-partitions") {
		t.Fatalf("validate kafka without partitions err = %v, want partition-count error", err)
	}
	if err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:            "kafka",
		ExecutorMode:       "sql",
		Kafka:              kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "", "", ""),
		KafkaPartitions:    32,
		KafkaTopicReplicas: 1,
	}); err != nil {
		t.Fatalf("validate kafka with partitions: %v", err)
	}
	err = validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:            "kafka",
		ExecutorMode:       "native",
		Kafka:              kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", ""),
		KafkaPartitions:    32,
		KafkaTopicReplicas: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "kafka-event-partitions") {
		t.Fatalf("validate native kafka without event partitions err = %v, want event partition error", err)
	}
	if err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:              "kafka",
		ExecutorMode:         "native",
		Kafka:                kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", ""),
		KafkaPartitions:      32,
		KafkaEventPartitions: 32,
		ScalarAllocator:      "sql-event-partition-offsets",
		KafkaTopicReplicas:   1,
	}); err != nil {
		t.Fatalf("validate native kafka with partitions: %v", err)
	}
	err = validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:              "kafka",
		ExecutorMode:         "native",
		Kafka:                kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", ""),
		KafkaPartitions:      32,
		KafkaEventPartitions: 32,
		ScalarAllocator:      "global-sequence-service",
		KafkaTopicReplicas:   1,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported -kafka-scalar-allocator") {
		t.Fatalf("validate native kafka with unsupported scalar allocator err = %v, want allocator error", err)
	}
	err = validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:              "kafka",
		ExecutorMode:         "native",
		Kafka:                kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", ""),
		KafkaPartitions:      32,
		KafkaEventPartitions: 32,
	})
	if err == nil || !strings.Contains(err.Error(), "kafka-topic-replicas") {
		t.Fatalf("validate native kafka without positive replicas err = %v, want replica error", err)
	}
}

func TestValidateCommandLogLoadRuntimeConfigRequiresRedisURLForIndex(t *testing.T) {
	err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:         "nats",
		ExecutorMode:    "sql",
		CommandLogIndex: "redis",
	})
	if err == nil || !strings.Contains(err.Error(), "requires -redis") {
		t.Fatalf("validate redis index without URL err = %v, want redis URL error", err)
	}
	if err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:         "nats",
		ExecutorMode:    "sql",
		CommandLogIndex: "redis",
		RedisURL:        "redis://redis.internal:6379",
	}); err != nil {
		t.Fatalf("validate redis index with URL: %v", err)
	}
	err = validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:         "nats",
		ExecutorMode:    "sql",
		CommandLogIndex: "memcached",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported -command-log-index") {
		t.Fatalf("validate unsupported command-log index err = %v, want unsupported-index error", err)
	}
}

func TestCommandLogLoadKafkaTopicSpecs(t *testing.T) {
	specs, err := commandLogLoadKafkaTopicSpecs(commandLogLoadRuntimeConfig{
		Backend:              "kafka",
		ExecutorMode:         "native",
		Kafka:                kafkaconn.RuntimeConfigFromFlags("redpanda:9092", " budgie.commands.load.1 ", " budgie.events.load.1 ", "writers"),
		KafkaPartitions:      32,
		KafkaEventPartitions: 48,
		KafkaTopicReplicas:   2,
	})
	if err != nil {
		t.Fatalf("kafka topic specs: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("topic specs = %d, want command and event topics", len(specs))
	}
	if specs[0].Topic != "budgie.commands.load.1" || specs[0].Partitions != 32 || specs[0].ReplicationFactor != 2 {
		t.Fatalf("command spec = %+v, want command topic partitions and replicas", specs[0])
	}
	if specs[1].Topic != "budgie.events.load.1" || specs[1].Partitions != 48 || specs[1].ReplicationFactor != 2 {
		t.Fatalf("event spec = %+v, want event topic partitions and replicas", specs[1])
	}

	specs, err = commandLogLoadKafkaTopicSpecs(commandLogLoadRuntimeConfig{
		Backend:            "kafka",
		ExecutorMode:       "sql",
		Kafka:              kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands.load.1", "budgie.events.load.1", "writers"),
		KafkaPartitions:    32,
		KafkaTopicReplicas: 1,
	})
	if err != nil {
		t.Fatalf("sql kafka topic specs: %v", err)
	}
	if len(specs) != 1 || specs[0].Topic != "budgie.commands.load.1" {
		t.Fatalf("sql topic specs = %+v, want command topic only", specs)
	}
}

func TestDefaultCommandLogLoadNATSStream(t *testing.T) {
	if got := defaultCommandLogLoadNATSStream(); !strings.HasPrefix(got, "BUDGIE_COMMAND_LOG_LOAD_") {
		t.Fatalf("default stream = %q, want load prefix", got)
	}
}

func TestValidateCommandLogLoadRuntimeConfigRequiresPostgres(t *testing.T) {
	err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		RequirePostgres: true,
		Backend:         "memory",
		ExecutorMode:    "sql",
	})
	if err == nil || !strings.Contains(err.Error(), "requires -postgres-dsn") {
		t.Fatalf("validate require postgres err = %v, want missing DSN error", err)
	}
	if err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		PostgresDSN:     "postgres://example/budgie",
		RequirePostgres: true,
		Backend:         "memory",
		ExecutorMode:    "sql",
	}); err != nil {
		t.Fatalf("validate with postgres DSN: %v", err)
	}
}

func TestValidateCommandLogLoadRuntimeConfigRejectsSharedNativeNATSStreams(t *testing.T) {
	err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		PostgresDSN:       "postgres://example/budgie",
		Backend:           "nats",
		ExecutorMode:      "native",
		CommandNATSStream: "BUDGIE_LOAD",
		EventNATSStream:   "BUDGIE_LOAD",
	})
	if err == nil || !strings.Contains(err.Error(), "distinct command and event streams") {
		t.Fatalf("validate shared native NATS stream err = %v, want distinct-stream error", err)
	}
	if err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		PostgresDSN:       "postgres://example/budgie",
		Backend:           "nats",
		ExecutorMode:      "native",
		CommandNATSStream: "BUDGIE_COMMAND_LOAD",
		EventNATSStream:   "BUDGIE_EVENT_LOAD",
	}); err != nil {
		t.Fatalf("validate distinct native NATS streams: %v", err)
	}
	if err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:           "memory",
		ExecutorMode:      "native",
		CommandNATSStream: "BUDGIE_LOAD",
		EventNATSStream:   "BUDGIE_LOAD",
	}); err != nil {
		t.Fatalf("validate shared stream names for memory backend: %v", err)
	}
}

func TestValidateCommandLogLoadRuntimeConfigRequiresPostgresForNativeNATS(t *testing.T) {
	err := validateCommandLogLoadRuntimeConfig(commandLogLoadRuntimeConfig{
		Backend:           "nats",
		ExecutorMode:      "native",
		CommandNATSStream: "BUDGIE_COMMAND_LOAD",
		EventNATSStream:   "BUDGIE_EVENT_LOAD",
	})
	if err == nil || !strings.Contains(err.Error(), "native NATS command-log load requires -postgres-dsn") {
		t.Fatalf("validate native nats without postgres err = %v, want postgres required error", err)
	}
}

func TestCommandLogLoadRuntimeReportIdentifiesNativeNATSDurableStaging(t *testing.T) {
	report := commandLogLoadRuntimeReport(commandLogLoadRuntimeConfig{
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
	if report.CommandLogBackend != "nats" || report.EventLogBackend != "nats" || report.MaterializationStore != "postgres" {
		t.Fatalf("runtime report = %+v, want nats/nats/postgres", report)
	}
	if !report.RequirePostgres || !report.DurableStaging {
		t.Fatalf("runtime report = %+v, want requirePostgres and durableStaging", report)
	}
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
	report := commandLogLoadRuntimeReport(commandLogLoadRuntimeConfig{
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
	if report.CommandLogBackend != "kafka" || report.EventLogBackend != "kafka" || report.MaterializationStore != "postgres" {
		t.Fatalf("runtime report = %+v, want kafka/kafka/postgres", report)
	}
	if !report.RequirePostgres || !report.DurableStaging {
		t.Fatalf("runtime report = %+v, want requirePostgres and durableStaging", report)
	}
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
	c, cleanup, err := openCore(ctx, "", "", "", false, nil)
	if err != nil {
		t.Fatalf("open core: %v", err)
	}
	defer cleanup()
	defer c.DB.Close()

	report := core.CommandLogDrainLoadReport{
		Runtime: core.CommandLogDrainLoadRuntime{
			MaterializationStore: "sqlite",
		},
	}
	if err := attachCommandLogLoadScalarCompatibilityAudit(ctx, c.DB, &report); err != nil {
		t.Fatalf("attach skipped sqlite audit: %v", err)
	}
	if report.ScalarCompatibilityAudit.Enabled {
		t.Fatalf("sqlite materialization audit = %+v, want skipped", report.ScalarCompatibilityAudit)
	}

	if _, err := c.DB.Exec(`UPDATE event_scalar_offsets SET last_seq=7 WHERE id='broker_event_log'`); err != nil {
		t.Fatalf("seed scalar offset: %v", err)
	}
	report.Runtime.MaterializationStore = "postgres"
	if err := attachCommandLogLoadScalarCompatibilityAudit(ctx, c.DB, &report); err != nil {
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
	report := commandLogLoadRuntimeReport(commandLogLoadRuntimeConfig{
		Backend:           "memory",
		ExecutorMode:      "native",
		CommandNATSStream: "BUDGIE_COMMAND_LOAD",
		EventNATSStream:   "BUDGIE_EVENT_LOAD",
	})
	if report.CommandLogBackend != "memory" || report.EventLogBackend != "memory" || report.MaterializationStore != "sqlite" {
		t.Fatalf("runtime report = %+v, want memory/memory/sqlite", report)
	}
	if report.DurableStaging || report.CommandNATSStream != "" || report.EventNATSStream != "" {
		t.Fatalf("runtime report = %+v, want non-durable memory fixture without NATS streams", report)
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

func TestSanitizedCommandLogLoadEndpointRedactsKeywordDSN(t *testing.T) {
	got := sanitizedCommandLogLoadEndpoint("host=postgres.internal port=5432 dbname=budgie user=budgie password=secret", "postgres")
	if got != "postgres://postgres.internal:5432/budgie" {
		t.Fatalf("sanitized keyword dsn = %q, want host/database endpoint", got)
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "user") {
		t.Fatalf("sanitized keyword dsn leaked secret material: %q", got)
	}
}

func TestCommandLogLoadPostgresSchemaGeneratesDisposableName(t *testing.T) {
	if got := commandLogLoadPostgresSchema("", "ignored"); got != "" {
		t.Fatalf("schema without postgres dsn = %q, want empty", got)
	}
	if got := commandLogLoadPostgresSchema("postgres://example/budgie", " custom_schema "); got != "custom_schema" {
		t.Fatalf("custom schema = %q, want trimmed custom name", got)
	}
	got := commandLogLoadPostgresSchema("postgres://example/budgie", "")
	if !strings.HasPrefix(got, "budgie_cmdlog_load_") {
		t.Fatalf("generated schema = %q, want load prefix", got)
	}
}

func TestCommandLogLoadEvidenceRecordsToolAndBudget(t *testing.T) {
	budgetData := []byte(`{"commandLogDrain":{"requireReportEvidence":true}}`)
	budgetPath := filepath.Join(t.TempDir(), "budget.json")
	if err := os.WriteFile(budgetPath, budgetData, 0o600); err != nil {
		t.Fatalf("write budget: %v", err)
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256(budgetData))

	evidence := commandLogLoadEvidence(" " + budgetPath + " ")
	if evidence.Tool != "budgie-commandlog-loadgen" {
		t.Fatalf("evidence tool = %q, want loadgen", evidence.Tool)
	}
	if evidence.BudgetFile != budgetPath {
		t.Fatalf("evidence budget = %q, want trimmed budget path", evidence.BudgetFile)
	}
	if evidence.BudgetSHA256 != wantHash {
		t.Fatalf("evidence budget hash = %q, want %q", evidence.BudgetSHA256, wantHash)
	}
}
