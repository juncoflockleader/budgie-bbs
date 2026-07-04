package loadtest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
)

type CommandLogLoadRuntimeConfig struct {
	PostgresDSN           string
	RequirePostgres       bool
	PostgresSchema        string
	KeepPostgresSchema    bool
	NATSURL               string
	Backend               string
	ExecutorMode          string
	CommandNATSStream     string
	CommandNATSReplicas   int
	EventNATSStream       string
	EventNATSReplicas     int
	CommandLogIndex       string
	CommandLogIndexPrefix string
	RedisURL              string
	ScalarAllocator       string
	Kafka                 kafkaconn.RuntimeConfig
	KafkaPartitions       int32
	KafkaEventPartitions  int32
	KafkaTopicReplicas    int
}

func ValidateCommandLogLoadRuntimeConfig(config CommandLogLoadRuntimeConfig) error {
	if config.RequirePostgres && strings.TrimSpace(config.PostgresDSN) == "" {
		return fmt.Errorf("-require-postgres requires -postgres-dsn or BUDGIE_POSTGRES_DSN")
	}
	backend := NormalizeCommandLogLoadBackend(config.Backend)
	switch NormalizeCommandLogLoadIndexBackend(config.CommandLogIndex) {
	case "":
	case "redis":
		if strings.TrimSpace(config.RedisURL) == "" {
			return fmt.Errorf("-command-log-index redis requires -redis or BUDGIE_REDIS_URL")
		}
	default:
		return fmt.Errorf("unsupported -command-log-index %q; supported: redis", config.CommandLogIndex)
	}
	if config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative && backend == "nats" {
		if strings.TrimSpace(config.PostgresDSN) == "" {
			return fmt.Errorf("native NATS command-log load requires -postgres-dsn or BUDGIE_POSTGRES_DSN; temporary SQLite materialization is not a durable staging gate")
		}
		commandStream := strings.TrimSpace(config.CommandNATSStream)
		eventStream := strings.TrimSpace(config.EventNATSStream)
		if commandStream != "" && eventStream != "" && commandStream == eventStream {
			return fmt.Errorf("native NATS command-log load requires distinct command and event streams, got %q", commandStream)
		}
	}
	if backend == "kafka" {
		if _, err := kafkaconn.TopicReplicationFactor(config.KafkaTopicReplicas); err != nil {
			return fmt.Errorf("kafka command-log load requires -kafka-topic-replicas: %w", err)
		}
		if config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative {
			if err := config.Kafka.ValidateCommandEventRuntime(config.KafkaPartitions, config.KafkaEventPartitions); err != nil {
				return fmt.Errorf("native Kafka command-log load requires %w", err)
			}
			scalarAllocator := commandLogLoadScalarCompatibilityAllocator(backend, config.ExecutorMode, config.ScalarAllocator)
			switch scalarAllocator {
			case loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets, loadmodel.CommandLogDrainScalarAllocatorSQLEventPartitions:
			default:
				return fmt.Errorf("native Kafka command-log load has unsupported -kafka-scalar-allocator %q; supported: %s,%s",
					config.ScalarAllocator,
					loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets,
					loadmodel.CommandLogDrainScalarAllocatorSQLEventPartitions)
			}
		} else if err := config.Kafka.ValidateCommandLogRuntime(config.KafkaPartitions); err != nil {
			return fmt.Errorf("kafka command-log load requires %w", err)
		}
	}
	return nil
}

func CommandLogLoadRuntimeReport(config CommandLogLoadRuntimeConfig) loadmodel.CommandLogDrainLoadRuntime {
	backend := NormalizeCommandLogLoadBackend(config.Backend)
	materialization := "sqlite"
	if strings.TrimSpace(config.PostgresDSN) != "" {
		materialization = "postgres"
	}
	eventBackend := ""
	if config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative {
		eventBackend = backend
	}
	indexBackend := NormalizeCommandLogLoadIndexBackend(config.CommandLogIndex)
	runtime := loadmodel.CommandLogDrainLoadRuntime{
		CommandLogBackend:            backend,
		EventLogBackend:              eventBackend,
		MaterializationStore:         materialization,
		ScalarCompatibilityAllocator: commandLogLoadScalarCompatibilityAllocator(backend, config.ExecutorMode, config.ScalarAllocator),
		PostgresEndpoint:             runevidence.SanitizeEndpoint(config.PostgresDSN, "postgres"),
		RequirePostgres:              config.RequirePostgres,
		PostgresSchema:               strings.TrimSpace(config.PostgresSchema),
		KeepPostgresSchema:           config.KeepPostgresSchema,
	}
	if indexBackend != "" {
		runtime.CommandLogIndexBackend = indexBackend
		runtime.CommandLogIndexPrefix = strings.TrimSpace(config.CommandLogIndexPrefix)
		if indexBackend == "redis" {
			runtime.RedisEndpoint = runevidence.SanitizeEndpoint(config.RedisURL, "redis")
		}
	}
	if backend == "nats" {
		runtime.NATSEndpoint = runevidence.SanitizeEndpoint(config.NATSURL, "nats")
		runtime.CommandNATSStream = strings.TrimSpace(config.CommandNATSStream)
		runtime.CommandNATSReplicas = max(config.CommandNATSReplicas, 1)
	}
	if eventBackend == "nats" {
		runtime.EventNATSStream = strings.TrimSpace(config.EventNATSStream)
		runtime.EventNATSReplicas = max(config.EventNATSReplicas, 1)
	}
	if backend == "kafka" {
		kafka := config.Kafka.Normalize()
		runtime.KafkaBrokers = runevidence.SanitizeKafkaBrokerEndpoints(kafka.Brokers)
		runtime.KafkaTLS = kafka.TLS
		runtime.KafkaSASLMechanism = kafka.SASLMechanism
		runtime.KafkaCommandTopic = kafka.CommandTopic
		runtime.KafkaConsumerGroup = kafka.ConsumerGroup
		runtime.KafkaCommandPartitions = int(config.KafkaPartitions)
	}
	if eventBackend == "kafka" {
		kafka := config.Kafka.Normalize()
		runtime.KafkaEventTopic = kafka.EventTopic
		runtime.KafkaEventPartitions = int(config.KafkaEventPartitions)
	}
	runtime.DurableStaging = (backend == "nats" || backend == "kafka") && materialization == "postgres" &&
		(config.ExecutorMode != loadmodel.CommandLogDrainExecutorNative || eventBackend == backend)
	return runtime
}

func NormalizeCommandLogLoadIndexBackend(raw string) string {
	backend := strings.ToLower(strings.TrimSpace(raw))
	switch backend {
	case "", "none", "off", "disabled":
		return ""
	case "redis":
		return "redis"
	default:
		return backend
	}
}

func commandLogLoadScalarCompatibilityAllocator(backend, executorMode, raw string) string {
	if executorMode != loadmodel.CommandLogDrainExecutorNative {
		return loadmodel.CommandLogDrainScalarAllocatorPostgresEventSeq
	}
	backend = NormalizeCommandLogLoadBackend(backend)
	switch backend {
	case "kafka":
		allocator := loadmodel.NormalizeCommandLogDrainScalarAllocator(raw)
		if allocator == "" {
			return loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets
		}
		return allocator
	case "nats":
		return loadmodel.CommandLogDrainScalarAllocatorBrokerStreamSequence
	case "", "memory":
		return loadmodel.CommandLogDrainScalarAllocatorMemoryStreamSequence
	default:
		return backend
	}
}

func AttachCommandLogLoadScalarCompatibilityAudit(ctx context.Context, db *sql.DB, report *loadmodel.CommandLogDrainLoadReport) error {
	if report == nil || db == nil {
		return nil
	}
	if NormalizeCommandLogLoadBackend(report.Runtime.MaterializationStore) != "postgres" {
		return nil
	}
	var offset int64
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE((
		     SELECT last_seq
		       FROM event_scalar_offsets
		      WHERE id='broker_event_log'
		   ), 0)`,
	).Scan(&offset); err != nil {
		return err
	}
	report.ScalarCompatibilityAudit = loadmodel.CommandLogScalarCompatibilityAudit{
		Enabled:                    true,
		Store:                      "event_scalar_offsets",
		OffsetID:                   "broker_event_log",
		LegacySQLScalarOffsetAfter: offset,
	}
	return nil
}

func NormalizeCommandLogLoadBackend(raw string) string {
	backend := runconfig.NormalizeBackendAlias(raw)
	if backend == "" {
		return "memory"
	}
	return backend
}
