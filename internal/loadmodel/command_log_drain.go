package loadmodel

import (
	"fmt"
	"strings"
)

type CommandLogDrainLoadConfig struct {
	Boards               int    `json:"boards"`
	CommandsPerBoard     int    `json:"commandsPerBoard"`
	RepliesPerThread     int    `json:"repliesPerThread"`
	DirectedReplies      bool   `json:"directedReplies"`
	SubmitConcurrency    int    `json:"submitConcurrency"`
	Writers              int    `json:"writers"`
	BatchSize            int    `json:"batchSize"`
	PartitionConcurrency int    `json:"partitionConcurrency"`
	BodyBytes            int    `json:"bodyBytes"`
	BoardPrefix          string `json:"boardPrefix"`
	UserName             string `json:"userName"`
	AssignmentMode       string `json:"assignmentMode"`
	ExecutorMode         string `json:"executorMode"`
	AuthoritativeSubmit  bool   `json:"authoritativeSubmit"`
}

const (
	CommandLogDrainAssignmentHash     = "hash-assignment"
	CommandLogDrainAssignmentSnapshot = "snapshot-assignment"
	CommandLogDrainExecutorSQL        = "sql"
	CommandLogDrainExecutorNative     = "native"

	CommandLogDrainScalarAllocatorBrokerStreamSequence = "broker-stream-sequence"
	CommandLogDrainScalarAllocatorMemoryStreamSequence = "memory-stream-sequence"
	CommandLogDrainScalarAllocatorPostgresEventSeq     = "postgres-event-seq"
	CommandLogDrainScalarAllocatorSQLEventPartitions   = "sql-event-partition-offsets"
	CommandLogDrainScalarAllocatorSQLEventOffsets      = "sql-event-scalar-offsets"
)

func NormalizeCommandLogDrainScalarAllocator(raw string) string {
	allocator := strings.ToLower(strings.TrimSpace(raw))
	switch allocator {
	case "":
		return ""
	case "broker", "broker-seq", "broker-stream", "stream-sequence", CommandLogDrainScalarAllocatorBrokerStreamSequence:
		return CommandLogDrainScalarAllocatorBrokerStreamSequence
	case "memory", CommandLogDrainScalarAllocatorMemoryStreamSequence:
		return CommandLogDrainScalarAllocatorMemoryStreamSequence
	case "postgres", "postgres-seq", "postgres-events-seq", CommandLogDrainScalarAllocatorPostgresEventSeq:
		return CommandLogDrainScalarAllocatorPostgresEventSeq
	case "partition", "partition-only", "sql-partition", "sql-partition-offsets", "sql-event-partition-offset", CommandLogDrainScalarAllocatorSQLEventPartitions:
		return CommandLogDrainScalarAllocatorSQLEventPartitions
	case "sql", "sql-scalar", "sql-event-scalar-offset", CommandLogDrainScalarAllocatorSQLEventOffsets:
		return CommandLogDrainScalarAllocatorSQLEventOffsets
	default:
		return allocator
	}
}

func NormalizeCommandLogExecutor(raw string) string {
	executor := strings.ToLower(strings.TrimSpace(raw))
	switch executor {
	case "", "sql", "postgres", "postgresql":
		return CommandLogDrainExecutorSQL
	case "native", "broker-native", "event-transaction":
		return CommandLogDrainExecutorNative
	default:
		return executor
	}
}

func NormalizeCommandLogDrainAssignmentMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "", "hash", CommandLogDrainAssignmentHash:
		return CommandLogDrainAssignmentHash
	case "snapshot", CommandLogDrainAssignmentSnapshot, "broker-snapshot", "consumer-group":
		return CommandLogDrainAssignmentSnapshot
	default:
		return mode
	}
}

func IsSupportedCommandLogDrainExecutorMode(mode string) bool {
	return mode == CommandLogDrainExecutorSQL || mode == CommandLogDrainExecutorNative
}

func ValidateCommandLogDrainLoadConfig(config CommandLogDrainLoadConfig) error {
	if config.AssignmentMode != CommandLogDrainAssignmentHash && config.AssignmentMode != CommandLogDrainAssignmentSnapshot {
		return fmt.Errorf("command log drain load: unsupported assignment mode %q", config.AssignmentMode)
	}
	if !IsSupportedCommandLogDrainExecutorMode(config.ExecutorMode) {
		return fmt.Errorf("command log drain load: unsupported executor mode %q", config.ExecutorMode)
	}
	return nil
}

func DefaultCommandLogDrainLoadConfig() CommandLogDrainLoadConfig {
	return CommandLogDrainLoadConfig{
		Boards:               8,
		CommandsPerBoard:     50,
		RepliesPerThread:     0,
		SubmitConcurrency:    32,
		Writers:              4,
		BatchSize:            25,
		PartitionConcurrency: 1,
		BodyBytes:            256,
		BoardPrefix:          "cmdlogload",
		UserName:             "command_log_load_admin",
		AssignmentMode:       CommandLogDrainAssignmentHash,
		ExecutorMode:         CommandLogDrainExecutorSQL,
	}
}

func NormalizeCommandLogDrainLoadConfig(config CommandLogDrainLoadConfig) CommandLogDrainLoadConfig {
	def := DefaultCommandLogDrainLoadConfig()
	config.Boards = positiveOrDefault(config.Boards, def.Boards)
	config.CommandsPerBoard = positiveOrDefault(config.CommandsPerBoard, def.CommandsPerBoard)
	if config.RepliesPerThread < 0 {
		config.RepliesPerThread = 0
	}
	config.SubmitConcurrency = positiveOrDefault(config.SubmitConcurrency, def.SubmitConcurrency)
	config.Writers = positiveOrDefault(config.Writers, def.Writers)
	config.BatchSize = positiveOrDefault(config.BatchSize, def.BatchSize)
	config.PartitionConcurrency = positiveOrDefault(config.PartitionConcurrency, def.PartitionConcurrency)
	if config.BodyBytes < 0 {
		config.BodyBytes = def.BodyBytes
	}
	if config.BoardPrefix == "" {
		config.BoardPrefix = def.BoardPrefix
	}
	if config.UserName == "" {
		config.UserName = def.UserName
	}
	config.AssignmentMode = NormalizeCommandLogDrainAssignmentMode(config.AssignmentMode)
	config.ExecutorMode = NormalizeCommandLogExecutor(config.ExecutorMode)
	config.SubmitConcurrency = min(config.SubmitConcurrency, CommandLogDrainLoadTotalCommands(config))
	config.Writers = min(config.Writers, CommandLogDrainLoadCommandPartitionLimit(config))
	return config
}

type CommandLogDrainLoadRuntime struct {
	CommandLogBackend            string   `json:"commandLogBackend,omitempty"`
	EventLogBackend              string   `json:"eventLogBackend,omitempty"`
	MaterializationStore         string   `json:"materializationStore,omitempty"`
	ScalarCompatibilityAllocator string   `json:"scalarCompatibilityAllocator,omitempty"`
	NATSEndpoint                 string   `json:"natsEndpoint,omitempty"`
	PostgresEndpoint             string   `json:"postgresEndpoint,omitempty"`
	RequirePostgres              bool     `json:"requirePostgres"`
	DurableStaging               bool     `json:"durableStaging"`
	PostgresSchema               string   `json:"postgresSchema,omitempty"`
	KeepPostgresSchema           bool     `json:"keepPostgresSchema"`
	CommandLogIndexBackend       string   `json:"commandLogIndexBackend,omitempty"`
	CommandLogIndexPrefix        string   `json:"commandLogIndexPrefix,omitempty"`
	RedisEndpoint                string   `json:"redisEndpoint,omitempty"`
	CommandNATSStream            string   `json:"commandNatsStream,omitempty"`
	CommandNATSReplicas          int      `json:"commandNatsReplicas,omitempty"`
	EventNATSStream              string   `json:"eventNatsStream,omitempty"`
	EventNATSReplicas            int      `json:"eventNatsReplicas,omitempty"`
	KafkaBrokers                 []string `json:"kafkaBrokers,omitempty"`
	KafkaTLS                     bool     `json:"kafkaTls,omitempty"`
	KafkaSASLMechanism           string   `json:"kafkaSaslMechanism,omitempty"`
	KafkaCommandTopic            string   `json:"kafkaCommandTopic,omitempty"`
	KafkaEventTopic              string   `json:"kafkaEventTopic,omitempty"`
	KafkaConsumerGroup           string   `json:"kafkaConsumerGroup,omitempty"`
	KafkaCommandPartitions       int      `json:"kafkaCommandPartitions,omitempty"`
	KafkaEventPartitions         int      `json:"kafkaEventPartitions,omitempty"`
}

type CommandLogScalarCompatibilityAudit struct {
	Enabled                    bool   `json:"enabled"`
	Store                      string `json:"store,omitempty"`
	OffsetID                   string `json:"offsetId,omitempty"`
	LegacySQLScalarOffsetAfter int64  `json:"legacySqlScalarOffsetAfter"`
}

type CommandLogLoadStage struct {
	Commands        int      `json:"commands"`
	Succeeded       int      `json:"succeeded"`
	Failed          int      `json:"failed"`
	DurationMS      int64    `json:"durationMs"`
	CommandsPerSec  float64  `json:"commandsPerSec"`
	SampleErrorText []string `json:"sampleErrorText,omitempty"`
}

type CommandLogDrainStage struct {
	Commands          int      `json:"commands"`
	Processed         int      `json:"processed"`
	Applied           int      `json:"applied"`
	TerminalFailures  int      `json:"terminalFailures"`
	RetryableFailures int      `json:"retryableFailures"`
	CommitFailures    int      `json:"commitFailures"`
	AssignmentLosses  int      `json:"assignmentLosses"`
	ClaimLosses       int      `json:"claimLosses"`
	Rounds            int      `json:"rounds"`
	DurationMS        int64    `json:"durationMs"`
	CommandsPerSec    float64  `json:"commandsPerSec"`
	SampleErrorText   []string `json:"sampleErrorText,omitempty"`
}

type EventStoreProjectionLoadStage struct {
	Enabled                bool     `json:"enabled"`
	Partitions             int      `json:"partitions"`
	PartitionLimit         int      `json:"partitionLimit,omitempty"`
	PartitionLimitExceeded bool     `json:"partitionLimitExceeded"`
	ExpectedEvents         int      `json:"expectedEvents,omitempty"`
	AppliedEvents          int      `json:"appliedEvents"`
	Rounds                 int      `json:"rounds"`
	DurationMS             int64    `json:"durationMs"`
	EventsPerSec           float64  `json:"eventsPerSec"`
	SampleErrorText        []string `json:"sampleErrorText,omitempty"`
}
