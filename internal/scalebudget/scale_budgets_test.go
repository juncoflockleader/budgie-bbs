package scalebudget

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
)

func TestLoadScaleBudgets(t *testing.T) {
	path := writeScaleBudgetFixture(t, "budgets.json", []byte(`{
		"postgresWrites": {
			"minSpreadSpeedup": 1.5,
			"minSpreadWritesPerSec": 100,
			"maxFailedWrites": 0
		},
		"gatewayFanout": {
			"maxPublishMs": 100,
			"minQueuedDeliveries": 128,
			"targetConnections": 1000000,
			"maxGatewayNodesForTarget": 20,
			"requiredReportBudgetFile": "ops/internet-scale-budgets.example.json"
		},
		"commandLogDrain": {
			"requireReportEvidence": true,
			"requiredReportBudgetFile": "ops/internet-scale-budgets.example.json",
			"requiredCommandLogBackend": "kafka",
			"requiredEventLogBackend": "kafka",
			"requiredScalarCompatibilityAllocator": "sql-event-scalar-offsets",
			"requirePostgresFlag": true,
			"requireRuntimeEndpoints": true,
			"requireNonLocalRuntimeEndpoints": true,
			"requireDisposablePostgresSchema": true,
			"requiredPostgresSchemaPrefix": "budgie_cmdlog_load_",
			"requiredCommandNatsStreamPrefix": "BUDGIE_COMMAND_LOG_LOAD_",
			"requiredEventNatsStreamPrefix": "BUDGIE_EVENT_LOG_LOAD_",
			"minCommandNatsReplicas": 1,
			"minEventNatsReplicas": 1,
			"requiredCommandKafkaTopicPrefix": "budgie.commands.load.",
			"requiredEventKafkaTopicPrefix": "budgie.events.load.",
			"minKafkaCommandPartitions": 32,
			"minKafkaEventPartitions": 32,
			"requiredExecutorMode": "native",
			"requiredAssignmentMode": "snapshot-assignment",
			"requireAuthoritativeSubmit": true,
			"requireDirectedReplies": true,
			"minRepliesPerThread": 2,
			"minBoards": 8,
			"minCommandsPerBoard": 100,
			"minWriters": 8,
			"minBatchSize": 25,
			"minTotalCommands": 2400,
			"minDrainCommandsPerSec": 75,
			"maxPartitionLagAfter": 0,
			"requireNativeExpectedEvents": true,
			"minEventProjectionEventsPerSec": 75,
			"maxEventProjectionDurationMs": 30000,
			"maxPromotionTotalLag": 0,
			"maxMissingMaterialization": 0,
			"maxClaimLosses": 0
		}
	}`))

	budgets, err := LoadScaleBudgets(path)
	if err != nil {
		t.Fatalf("LoadScaleBudgets: %v", err)
	}
	if budgets.PostgresWrites == nil || budgets.PostgresWrites.MinSpreadSpeedup != 1.5 {
		t.Fatalf("postgres budget = %+v, want min spread speedup", budgets.PostgresWrites)
	}
	gateway := requireGatewayFanoutBudget(t, budgets, "loaded")
	if gateway.MinQueuedDeliveries != 128 ||
		gateway.TargetConnections != 1000000 ||
		gateway.MaxGatewayNodesForTarget != 20 ||
		gateway.RequiredReportBudgetFile != "ops/internet-scale-budgets.example.json" {
		t.Fatalf("gateway budget = %+v, want queued-delivery, target-connection, and report-file pins", gateway)
	}
	drain := requireCommandLogDrainBudget(t, budgets, "loaded")
	if drain.MinDrainCommandsPerSec != 75 ||
		!drain.RequireReportEvidence ||
		drain.RequiredReportBudgetFile != "ops/internet-scale-budgets.example.json" ||
		drain.RequiredCommandLogBackend != "kafka" ||
		drain.RequiredEventLogBackend != "kafka" ||
		drain.RequiredScalarCompatibilityAllocator != loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets {
		t.Fatalf("command-log budget = %+v, want Kafka report-evidence throughput pins", drain)
	}
	if !drain.RequirePostgresFlag ||
		!drain.RequireRuntimeEndpoints ||
		!drain.RequireNonLocalRuntimeEndpoints ||
		!drain.RequireDisposablePostgresSchema ||
		drain.RequiredPostgresSchemaPrefix != "budgie_cmdlog_load_" {
		t.Fatalf("command-log budget = %+v, want fail-closed runtime endpoint and schema requirements", drain)
	}
	if drain.RequiredCommandNATSStreamPrefix != "BUDGIE_COMMAND_LOG_LOAD_" || drain.RequiredEventNATSStreamPrefix != "BUDGIE_EVENT_LOG_LOAD_" {
		t.Fatalf("command-log budget = %+v, want staging NATS stream prefix requirements", drain)
	}
	if drain.MinCommandNATSReplicas != 1 || drain.MinEventNATSReplicas != 1 {
		t.Fatalf("command-log budget = %+v, want minimum NATS replica evidence", drain)
	}
	if drain.RequiredCommandKafkaTopicPrefix != "budgie.commands.load." ||
		drain.RequiredEventKafkaTopicPrefix != "budgie.events.load." ||
		drain.MinKafkaCommandPartitions != 32 ||
		drain.MinKafkaEventPartitions != 32 {
		t.Fatalf("command-log budget = %+v, want Kafka topic and partition evidence", drain)
	}
	requireCommandLogDrainNativeGateShape(t, drain, "command-log")
	requireCommandLogDrainStagedLoadFloor(t, drain, "command-log")
	if !drain.RequireNativeExpectedEvents ||
		drain.MinEventProjectionEventsPerSec != 75 ||
		drain.MaxEventProjectionDurationMS != 30000 {
		t.Fatalf("command-log budget = %+v, want native expected-event evidence and event-projection thresholds", drain)
	}
	if drain.MaxPromotionTotalLag != 0 || drain.MaxMissingMaterialization != 0 || drain.MaxClaimLosses != 0 {
		t.Fatalf("command-log budget = %+v, want zero-lag and zero-missing defaults", drain)
	}
}

func TestReportEvidenceViolations(t *testing.T) {
	violations := ReportEvidenceViolations("gatewayFanout.evidence.", []runevidence.ReportEvidenceViolation{
		{
			Field:   "budgetSHA256",
			Value:   "old",
			Want:    "new",
			Message: "budget hash mismatch",
		},
	})
	if len(violations) != 1 {
		t.Fatalf("violations = %+v, want one violation", violations)
	}
	got := violations[0]
	if got.Path != "gatewayFanout.evidence.budgetSHA256" ||
		got.Value != "old" ||
		got.Limit != "new" ||
		got.Message != "budget hash mismatch" {
		t.Fatalf("violation = %+v, want evidence fields mapped to scale-budget violation", got)
	}
}

func TestPromotedInternetScaleBudgetPinsGatewayFanoutReportEvidence(t *testing.T) {
	budgets := loadScaleBudgetsFixture(t, "internet-scale-budgets.example.json")
	budget := requireGatewayFanoutBudget(t, budgets, "promoted")
	requireGatewayFanoutBudgetPins(t, budget, "promoted", "ops/internet-scale-budgets.example.json")
}

func TestPromotedInternetScaleBudgetPinsCommandLogDrainGate(t *testing.T) {
	budgets := loadScaleBudgetsFixture(t, "internet-scale-budgets.example.json")
	budget := requireCommandLogDrainBudget(t, budgets, "promoted")
	requireNATSCommandLogDrainBudgetPins(t, budget, "promoted", "ops/internet-scale-budgets.example.json", false)
	requireCommandLogDrainStagedLoadFloor(t, budget, "promoted")
	if budget.MinSubmitCommandsPerSec != 100 ||
		budget.MinDrainCommandsPerSec != 75 ||
		budget.MaxDrainDurationMS != 30000 ||
		budget.MinEventProjectionEventsPerSec != 75 ||
		budget.MaxEventProjectionDurationMS != 30000 {
		t.Fatalf("command-log budget = %+v, want promoted throughput, exact event-count, and duration thresholds", budget)
	}
	if budget.MaxPartitionLagAfter != 0 ||
		budget.MaxPromotionTotalLag != 0 ||
		budget.MaxLaggingPartitions != 0 ||
		budget.MaxMissingMaterialization != 0 ||
		budget.MaxRetryingCommitted != 0 ||
		budget.MaxMissingRecords != 0 ||
		budget.MaxFailedCommands != 0 ||
		budget.MaxCommitFailures != 0 ||
		budget.MaxAssignmentLosses != 0 ||
		budget.MaxClaimLosses != 0 {
		t.Fatalf("command-log budget = %+v, want zero-lag, zero-loss, and zero-failure promotion thresholds", budget)
	}
}

func TestRemoteStagingInternetScaleBudgetRequiresNonLocalEndpoints(t *testing.T) {
	budgets := loadScaleBudgetsFixture(t, "internet-scale-remote-staging-budgets.example.json")
	budget := requireCommandLogDrainBudget(t, budgets, "remote staging")
	requireNATSCommandLogDrainBudgetPins(t, budget, "remote staging", "ops/internet-scale-remote-staging-budgets.example.json", true)
	gateway := requireGatewayFanoutBudget(t, budgets, "remote staging")
	requireGatewayFanoutBudgetPins(t, gateway, "remote staging", "ops/internet-scale-remote-staging-budgets.example.json")
}

func TestKafkaInternetScaleBudgetPinsKafkaCommandLogDrainGate(t *testing.T) {
	budgets := loadScaleBudgetsFixture(t, "internet-scale-kafka-budgets.example.json")
	budget := requireCommandLogDrainBudget(t, budgets, "Kafka")
	requireKafkaCommandLogDrainBudgetPins(t, budget, "Kafka", "ops/internet-scale-kafka-budgets.example.json", false)
}

func TestKafkaRemoteStagingInternetScaleBudgetRequiresNonLocalEndpoints(t *testing.T) {
	budgets := loadScaleBudgetsFixture(t, "internet-scale-kafka-remote-staging-budgets.example.json")
	budget := requireCommandLogDrainBudget(t, budgets, "Kafka remote")
	requireKafkaCommandLogDrainBudgetPins(t, budget, "Kafka remote", "ops/internet-scale-kafka-remote-staging-budgets.example.json", true)
}

func TestLoadScaleBudgetsRejectsUnknownFields(t *testing.T) {
	path := writeScaleBudgetFixture(t, "budgets.json", []byte(`{"postgresWrites":{"minSpreadSpeeedup":1.5}}`))

	_, err := LoadScaleBudgets(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadScaleBudgets err = %v, want unknown field", err)
	}
}

func TestEvaluateCommandLogDrainBudgetValidatesNonLocalRemoteStagingEndpoints(t *testing.T) {
	budget := &CommandLogDrainBudget{RequireNonLocalRuntimeEndpoints: true}
	report := validCommandLogDrainReport()
	report.Runtime = validCommandLogDrainDurableRuntime()
	report.Runtime.NATSEndpoint = "nats://127.0.0.1:4222"
	report.Runtime.PostgresEndpoint = "postgres://localhost:55432/budgie_staging"
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.runtime.natsEndpointLocal",
		"commandLogDrain.runtime.postgresEndpointLocal",
	)

	report.Runtime.NATSEndpoint = "nats://nats.internal:4222"
	report.Runtime.PostgresEndpoint = "postgres://postgres.internal:5432/budgie_staging"
	requireNoScaleBudgetViolations(t, EvaluateCommandLogDrainBudget(report, budget), "non-local staging endpoints")

	report.Runtime.NATSEndpoint = "localhost:4222"
	report.Runtime.PostgresEndpoint = "host=127.0.0.1 port=55432 dbname=budgie_staging"
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.runtime.natsEndpointLocal",
		"commandLogDrain.runtime.postgresEndpointLocal",
	)

	report.Runtime = validKafkaCommandLogDrainDurableRuntime()
	report.Runtime.KafkaBrokers = []string{"127.0.0.1:9092"}
	report.Runtime.PostgresEndpoint = "postgres://localhost:55432/budgie_staging"
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.runtime.kafkaBrokersLocal",
		"commandLogDrain.runtime.postgresEndpointLocal",
	)

	report.Runtime.KafkaBrokers = []string{"kafka.internal:9092", "redpanda.internal:9092"}
	report.Runtime.PostgresEndpoint = "postgres://postgres.internal:5432/budgie_staging"
	requireNoScaleBudgetViolations(t, EvaluateCommandLogDrainBudget(report, budget), "non-local Kafka staging endpoints")
}

func TestEvaluateCommandLogDrainBudgetPassesAndFails(t *testing.T) {
	report := validCommandLogDrainReport()
	report.Runtime = validCommandLogDrainDurableRuntime()
	report.Submit = loadmodel.CommandLogLoadStage{CommandsPerSec: 250}
	report.Drain = loadmodel.CommandLogDrainStage{CommandsPerSec: 200, DurationMS: 75}
	budget := &CommandLogDrainBudget{
		RequireDurableStaging:   true,
		MinSubmitCommandsPerSec: 200,
		MinDrainCommandsPerSec:  150,
		MaxDrainDurationMS:      100,
		MaxPartitionLagAfter:    0,
	}
	requireNoScaleBudgetViolations(t, EvaluateCommandLogDrainBudget(report, budget), "baseline command-log drain budget")

	report.Runtime.DurableStaging = false
	report.Submit.CommandsPerSec = 100
	report.Submit.Failed = 1
	report.Drain.CommandsPerSec = 90
	report.Drain.DurationMS = 125
	report.Drain.CommitFailures = 1
	report.Drain.AssignmentLosses = 1
	report.Drain.ClaimLosses = 1
	report.MaxPartitionLagAfterDrain = 2
	report.PromotionReadiness.Ready = false
	report.PromotionReadiness.PartitionLimitExceeded = true
	report.PromotionReadiness.TotalLag = 3
	report.PromotionReadiness.LaggingPartitions = 2
	report.MaterializationAudit.PartitionLimitExceeded = true
	report.MaterializationAudit.MissingMaterialization = 1
	report.MaterializationAudit.RetryingCommitted = 1
	report.MaterializationAudit.MissingRecords = 1
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.requireDurableStaging",
		"commandLogDrain.minSubmitCommandsPerSec",
		"commandLogDrain.minDrainCommandsPerSec",
		"commandLogDrain.maxDrainDurationMs",
		"commandLogDrain.maxPartitionLagAfter",
		"commandLogDrain.promotionReadiness.ready",
		"commandLogDrain.promotionReadiness.partitionLimitExceeded",
		"commandLogDrain.maxPromotionTotalLag",
		"commandLogDrain.maxLaggingPartitions",
		"commandLogDrain.materializationAudit.partitionLimitExceeded",
		"commandLogDrain.maxMissingMaterialization",
		"commandLogDrain.maxRetryingCommitted",
		"commandLogDrain.maxMissingRecords",
		"commandLogDrain.maxFailedCommands",
		"commandLogDrain.maxCommitFailures",
		"commandLogDrain.maxAssignmentLosses",
		"commandLogDrain.maxClaimLosses",
	)
}

func TestEvaluateCommandLogDrainBudgetValidatesDurableRuntimeShape(t *testing.T) {
	budget := &CommandLogDrainBudget{RequireDurableStaging: true}
	report := validCommandLogDrainReport()
	report.Config.ExecutorMode = loadmodel.CommandLogDrainExecutorNative
	report.Runtime = loadmodel.CommandLogDrainLoadRuntime{
		DurableStaging:       true,
		CommandLogBackend:    "memory",
		EventLogBackend:      "memory",
		MaterializationStore: "sqlite",
	}
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.runtime.commandLogBackend",
		"commandLogDrain.runtime.materializationStore",
		"commandLogDrain.runtime.eventLogBackend",
		"commandLogDrain.eventProjection.enabled",
	)

	report.Runtime = validCommandLogDrainDurableRuntime()
	report.Runtime.EventNATSStream = report.Runtime.CommandNATSStream
	report.EventProjection = loadmodel.EventStoreProjectionLoadStage{
		Enabled:       true,
		AppliedEvents: 1,
	}
	report.TotalCommands = 1
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.runtime.distinctNatsStreams",
	)

	report.Runtime = validKafkaCommandLogDrainDurableRuntime()
	report.Runtime.KafkaBrokers = nil
	report.Runtime.KafkaCommandTopic = ""
	report.Runtime.KafkaEventTopic = ""
	report.Runtime.KafkaCommandPartitions = 0
	report.Runtime.KafkaEventPartitions = 0
	report.EventProjection = loadmodel.EventStoreProjectionLoadStage{
		Enabled:       true,
		AppliedEvents: 1,
	}
	report.TotalCommands = 1
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.runtime.kafkaBrokers",
		"commandLogDrain.runtime.kafkaCommandTopic",
		"commandLogDrain.runtime.kafkaCommandPartitions",
		"commandLogDrain.runtime.kafkaEventTopic",
		"commandLogDrain.runtime.kafkaEventPartitions",
	)

	report.Runtime = validKafkaCommandLogDrainDurableRuntime()
	report.Runtime.KafkaEventTopic = report.Runtime.KafkaCommandTopic
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.runtime.distinctKafkaTopics",
	)
}

func TestEvaluateCommandLogDrainBudgetValidatesReportEvidence(t *testing.T) {
	budget := &CommandLogDrainBudget{
		RequireReportEvidence:    true,
		RequiredReportBudgetFile: "ops/internet-scale-budgets.example.json",
	}
	report := validCommandLogDrainReport()
	report.Evidence = validCommandLogDrainReportEvidence()
	requireNoScaleBudgetViolations(t, EvaluateCommandLogDrainBudget(report, budget), "valid report evidence")

	report.Evidence = runevidence.Evidence{
		Tool:        "other-tool",
		GitModified: true,
	}
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.evidence.tool",
		"commandLogDrain.evidence.budgetFile",
		"commandLogDrain.evidence.budgetSha256",
		"commandLogDrain.evidence.gitRevision",
		"commandLogDrain.evidence.gitModified",
	)

	report.Evidence = validCommandLogDrainReportEvidence()
	report.Evidence.BudgetFile = "ops/other-budget.json"
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.evidence.budgetFile",
	)
}

func TestEvaluateCommandLogDrainBudgetValidatesRequiredGateConfig(t *testing.T) {
	budget := &CommandLogDrainBudget{
		RequirePostgresFlag:                  true,
		RequireRuntimeEndpoints:              true,
		RequireDisposablePostgresSchema:      true,
		RequiredPostgresSchemaPrefix:         "budgie_cmdlog_load_",
		RequiredCommandNATSStreamPrefix:      "BUDGIE_COMMAND_LOG_LOAD_",
		RequiredEventNATSStreamPrefix:        "BUDGIE_EVENT_LOG_LOAD_",
		RequiredScalarCompatibilityAllocator: loadmodel.CommandLogDrainScalarAllocatorBrokerStreamSequence,
		MinCommandNATSReplicas:               1,
		MinEventNATSReplicas:                 1,
		RequiredExecutorMode:                 loadmodel.CommandLogDrainExecutorNative,
		RequiredAssignmentMode:               loadmodel.CommandLogDrainAssignmentSnapshot,
		RequireAuthoritativeSubmit:           true,
		RequireDirectedReplies:               true,
		MinRepliesPerThread:                  2,
		MinBoards:                            8,
		MinCommandsPerBoard:                  100,
		MinWriters:                           8,
		MinBatchSize:                         25,
		MinTotalCommands:                     2400,
	}
	report := validCommandLogDrainReport()
	report.Config = loadmodel.CommandLogDrainLoadConfig{
		ExecutorMode:        loadmodel.CommandLogDrainExecutorNative,
		AssignmentMode:      loadmodel.CommandLogDrainAssignmentSnapshot,
		AuthoritativeSubmit: true,
		DirectedReplies:     true,
		RepliesPerThread:    2,
		Boards:              8,
		CommandsPerBoard:    100,
		Writers:             8,
		BatchSize:           25,
	}
	report.Runtime = validCommandLogDrainDurableRuntime()
	report.TotalCommands = 2400
	report.EventProjection = loadmodel.EventStoreProjectionLoadStage{Enabled: true, ExpectedEvents: 3200, AppliedEvents: 3200}
	report.Runtime.RequirePostgres = true
	report.Runtime.PostgresSchema = "budgie_cmdlog_load_123"
	requireNoScaleBudgetViolations(t, EvaluateCommandLogDrainBudget(report, budget), "required gate config")

	report.Config.ExecutorMode = loadmodel.CommandLogDrainExecutorSQL
	report.Config.AssignmentMode = loadmodel.CommandLogDrainAssignmentHash
	report.Config.AuthoritativeSubmit = false
	report.Config.DirectedReplies = false
	report.Config.RepliesPerThread = 1
	report.Config.Boards = 4
	report.Config.CommandsPerBoard = 50
	report.Config.Writers = 4
	report.Config.BatchSize = 10
	report.TotalCommands = 100
	report.Runtime.RequirePostgres = false
	report.Runtime.PostgresSchema = "public"
	report.Runtime.KeepPostgresSchema = true
	report.Runtime.CommandNATSStream = "BUDGIE_COMMAND_LOG"
	report.Runtime.EventNATSStream = "BUDGIE_EVENT_LOG"
	report.Runtime.CommandNATSReplicas = 0
	report.Runtime.EventNATSReplicas = 0
	report.Runtime.NATSEndpoint = "nats://user:secret@nats.internal:4222?token=secret"
	report.Runtime.PostgresEndpoint = "postgres://user:secret@postgres.internal/budgie?sslmode=require"
	report.Runtime.ScalarCompatibilityAllocator = loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.runtime.requirePostgres",
		"commandLogDrain.runtime.scalarCompatibilityAllocator",
		"commandLogDrain.runtime.natsEndpoint",
		"commandLogDrain.runtime.postgresEndpoint",
		"commandLogDrain.runtime.keepPostgresSchema",
		"commandLogDrain.runtime.postgresSchemaPrefix",
		"commandLogDrain.runtime.commandNatsStreamPrefix",
		"commandLogDrain.runtime.eventNatsStreamPrefix",
		"commandLogDrain.runtime.commandNatsReplicas",
		"commandLogDrain.runtime.eventNatsReplicas",
		"commandLogDrain.config.executorMode",
		"commandLogDrain.config.assignmentMode",
		"commandLogDrain.config.authoritativeSubmit",
		"commandLogDrain.config.directedReplies",
		"commandLogDrain.config.repliesPerThread",
		"commandLogDrain.config.boards",
		"commandLogDrain.config.commandsPerBoard",
		"commandLogDrain.config.writers",
		"commandLogDrain.config.batchSize",
		"commandLogDrain.totalCommands",
	)
}

func TestEvaluateCommandLogDrainBudgetValidatesKafkaGateConfig(t *testing.T) {
	budget := &CommandLogDrainBudget{
		RequiredCommandLogBackend:            "kafka",
		RequiredEventLogBackend:              "kafka",
		RequiredScalarCompatibilityAllocator: loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets,
		RequireRuntimeEndpoints:              true,
		RequiredCommandKafkaTopicPrefix:      "budgie.commands.load.",
		RequiredEventKafkaTopicPrefix:        "budgie.events.load.",
		MinKafkaCommandPartitions:            32,
		MinKafkaEventPartitions:              32,
	}
	report := validCommandLogDrainReport()
	report.Runtime = validKafkaCommandLogDrainDurableRuntime()
	report.Config.ExecutorMode = loadmodel.CommandLogDrainExecutorNative
	report.EventProjection = loadmodel.EventStoreProjectionLoadStage{Enabled: true, AppliedEvents: 1}
	report.TotalCommands = 1
	requireNoScaleBudgetViolations(t, EvaluateCommandLogDrainBudget(report, budget), "valid Kafka report")

	budget.RequiredScalarCompatibilityAllocator = "partition-only"
	report.Runtime.ScalarCompatibilityAllocator = loadmodel.CommandLogDrainScalarAllocatorSQLEventPartitions
	requireNoScaleBudgetViolations(t, EvaluateCommandLogDrainBudget(report, budget), "partition-only Kafka report")
	report.Runtime.ScalarCompatibilityAllocator = loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget), "commandLogDrain.runtime.scalarCompatibilityAllocator")
	budget.RequiredScalarCompatibilityAllocator = loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets

	report.Runtime.CommandLogBackend = "nats"
	report.Runtime.EventLogBackend = "nats"
	report.Runtime.ScalarCompatibilityAllocator = loadmodel.CommandLogDrainScalarAllocatorBrokerStreamSequence
	report.Runtime.NATSEndpoint = "nats://nats.internal:4222"
	report.Runtime.KafkaBrokers = []string{"kafka://user:secret@kafka.internal:9092?token=secret"}
	report.Runtime.KafkaCommandTopic = "budgie.commands"
	report.Runtime.KafkaEventTopic = "budgie.events"
	report.Runtime.KafkaCommandPartitions = 8
	report.Runtime.KafkaEventPartitions = 8
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.runtime.commandLogBackend",
		"commandLogDrain.runtime.eventLogBackend",
		"commandLogDrain.runtime.scalarCompatibilityAllocator",
	)

	report.Runtime.CommandLogBackend = "kafka"
	report.Runtime.EventLogBackend = "kafka"
	report.Runtime.ScalarCompatibilityAllocator = loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.runtime.kafkaBrokers",
		"commandLogDrain.runtime.kafkaCommandTopicPrefix",
		"commandLogDrain.runtime.kafkaEventTopicPrefix",
		"commandLogDrain.runtime.kafkaCommandPartitions",
		"commandLogDrain.runtime.kafkaEventPartitions",
	)
}

func TestEvaluateCommandLogDrainBudgetChecksNativeEventProjection(t *testing.T) {
	report := validCommandLogDrainReport()
	report.Config.ExecutorMode = loadmodel.CommandLogDrainExecutorNative
	report.TotalCommands = 4
	report.Submit = loadmodel.CommandLogLoadStage{CommandsPerSec: 250}
	report.Drain = loadmodel.CommandLogDrainStage{CommandsPerSec: 200, DurationMS: 75}
	budget := &CommandLogDrainBudget{
		MinEventProjectionEventsPerSec: 100,
		MaxEventProjectionDurationMS:   100,
	}
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.eventProjection.enabled",
		"commandLogDrain.eventProjection.appliedEvents",
	)

	report.EventProjection = loadmodel.EventStoreProjectionLoadStage{
		Enabled:                true,
		PartitionLimitExceeded: true,
		AppliedEvents:          4,
		DurationMS:             125,
		EventsPerSec:           80,
	}
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.eventProjection.partitionLimitExceeded",
		"commandLogDrain.minEventProjectionEventsPerSec",
		"commandLogDrain.maxEventProjectionDurationMs",
	)

	report.EventProjection.PartitionLimitExceeded = false
	report.EventProjection.DurationMS = 90
	report.EventProjection.EventsPerSec = 125
	requireNoScaleBudgetViolations(t, EvaluateCommandLogDrainBudget(report, budget), "native event projection throughput")

	report.Config.Boards = 2
	report.Config.CommandsPerBoard = 1
	report.Config.RepliesPerThread = 1
	report.TotalCommands = 4
	report.EventProjection.ExpectedEvents = 6
	report.EventProjection.AppliedEvents = 6
	budget.RequireNativeExpectedEvents = true
	requireNoScaleBudgetViolations(t, EvaluateCommandLogDrainBudget(report, budget), "native expected-event match")
	report.EventProjection.ExpectedEvents = 3
	report.EventProjection.AppliedEvents = 3
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget),
		"commandLogDrain.eventProjection.expectedEvents",
		"commandLogDrain.eventProjection.appliedEvents",
	)
}

func TestEvaluateCommandLogDrainBudgetChecksScalarCompatibilityAudit(t *testing.T) {
	maxLegacyOffset := int64(0)
	budget := &CommandLogDrainBudget{
		RequireScalarCompatibilityAudit: true,
		MaxLegacySQLScalarOffsetAfter:   &maxLegacyOffset,
	}
	report := validCommandLogDrainReport()
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget), "commandLogDrain.scalarCompatibilityAudit.enabled")

	report.ScalarCompatibilityAudit = loadmodel.CommandLogScalarCompatibilityAudit{
		Enabled:                    true,
		Store:                      "event_scalar_offsets",
		OffsetID:                   "broker_event_log",
		LegacySQLScalarOffsetAfter: 0,
	}
	requireNoScaleBudgetViolations(t, EvaluateCommandLogDrainBudget(report, budget), "zero legacy scalar offset")

	report.ScalarCompatibilityAudit.LegacySQLScalarOffsetAfter = 1
	requireScaleBudgetPaths(t, EvaluateCommandLogDrainBudget(report, budget), "commandLogDrain.scalarCompatibilityAudit.legacySqlScalarOffsetAfter")
}

func TestEvaluateReportBudgetHashEvidence(t *testing.T) {
	data := []byte(`{"gatewayFanout":{"requiredReportBudgetFile":"ops/budget.json"}}`)
	path := writeScaleBudgetFixture(t, "budget.json", data)
	wantHash := runevidence.BytesSHA256(data)

	violations := requireReportBudgetHashEvidence(t, "", path, "empty")
	requireNoScaleBudgetViolations(t, violations, "empty report hash")

	violations = requireReportBudgetHashEvidence(t, wantHash, path, "matching")
	requireNoScaleBudgetViolations(t, violations, "matching report hash")

	violations = requireReportBudgetHashEvidence(t, strings.Repeat("0", 64), path, "mismatched")
	requireScaleBudgetPaths(t, violations, "gatewayFanout.evidence.budgetSha256")
	if violations[0].Limit != wantHash {
		t.Fatalf("violation limit = %v, want %s", violations[0].Limit, wantHash)
	}
}

func validCommandLogDrainReportEvidence() runevidence.Evidence {
	return runevidence.Evidence{
		Tool:         "budgie-commandlog-loadgen",
		BudgetFile:   "ops/internet-scale-budgets.example.json",
		BudgetSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		GitRevision:  "0123456789abcdef",
	}
}

func writeScaleBudgetFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func requireReportBudgetHashEvidence(t *testing.T, reportSHA, path, label string) []ScaleBudgetViolation {
	t.Helper()
	violations, err := EvaluateReportBudgetHashEvidence(reportSHA, path, "gatewayFanout.evidence.budgetSha256", "budget hash mismatch")
	if err != nil {
		t.Fatalf("%s report hash: %v", label, err)
	}
	return violations
}

func loadScaleBudgetsFixture(t *testing.T, name string) ScaleBudgets {
	t.Helper()
	budgets, err := LoadScaleBudgets(filepath.Join("..", "..", "ops", name))
	if err != nil {
		t.Fatalf("LoadScaleBudgets %s: %v", name, err)
	}
	return budgets
}

func requireCommandLogDrainBudget(t *testing.T, budgets ScaleBudgets, label string) *CommandLogDrainBudget {
	t.Helper()
	if budgets.CommandLogDrain == nil {
		t.Fatalf("%s budget missing commandLogDrain", label)
	}
	return budgets.CommandLogDrain
}

func requireGatewayFanoutBudget(t *testing.T, budgets ScaleBudgets, label string) *GatewayFanoutBudget {
	t.Helper()
	if budgets.GatewayFanout == nil {
		t.Fatalf("%s budget missing gatewayFanout", label)
	}
	return budgets.GatewayFanout
}

func requireGatewayFanoutBudgetPins(t *testing.T, budget *GatewayFanoutBudget, label, budgetFile string) {
	t.Helper()
	if budget.RequiredReportBudgetFile != budgetFile ||
		budget.MinSubscribers != 100000 ||
		budget.MinHotScopeSubscribers != 10000 ||
		budget.MinQueuedDeliveries != 10000 ||
		budget.TargetConnections != 1000000 ||
		budget.MaxGatewayNodesForTarget != 20 {
		t.Fatalf("%s gateway fanout budget = %+v, want report evidence and million-connection shape", label, budget)
	}
}

func requireNATSCommandLogDrainBudgetPins(t *testing.T, budget *CommandLogDrainBudget, label, budgetFile string, requireNonLocal bool) {
	t.Helper()
	requireDurableCommandLogDrainBudgetPins(t, budget, label, budgetFile, requireNonLocal, "nats", loadmodel.CommandLogDrainScalarAllocatorBrokerStreamSequence)
	if !budget.RequirePostgresFlag ||
		!budget.RequireRuntimeEndpoints ||
		!budget.RequireDisposablePostgresSchema ||
		budget.RequiredPostgresSchemaPrefix != "budgie_cmdlog_load_" {
		t.Fatalf("%s budget = %+v, want fail-closed postgres runtime schema requirements", label, budget)
	}
	if budget.RequiredCommandNATSStreamPrefix != "BUDGIE_COMMAND_LOG_LOAD_" ||
		budget.RequiredEventNATSStreamPrefix != "BUDGIE_EVENT_LOG_LOAD_" ||
		budget.MinCommandNATSReplicas != 1 ||
		budget.MinEventNATSReplicas != 1 {
		t.Fatalf("%s budget = %+v, want NATS stream prefixes and replicas", label, budget)
	}
}

func requireCommandLogDrainNativeGateShape(t *testing.T, budget *CommandLogDrainBudget, label string) {
	t.Helper()
	if budget.RequiredExecutorMode != loadmodel.CommandLogDrainExecutorNative ||
		budget.RequiredAssignmentMode != loadmodel.CommandLogDrainAssignmentSnapshot ||
		!budget.RequireAuthoritativeSubmit ||
		!budget.RequireDirectedReplies ||
		budget.MinRepliesPerThread != 2 ||
		!budget.RequireNativeExpectedEvents {
		t.Fatalf("%s budget = %+v, want native authoritative directed-reply gate shape", label, budget)
	}
}

func requireCommandLogDrainStagedLoadFloor(t *testing.T, budget *CommandLogDrainBudget, label string) {
	t.Helper()
	if budget.MinBoards != 8 || budget.MinCommandsPerBoard != 100 ||
		budget.MinWriters != 8 || budget.MinBatchSize != 25 || budget.MinTotalCommands != 2400 {
		t.Fatalf("%s budget = %+v, want minimum staged load size", label, budget)
	}
}

func requireKafkaCommandLogDrainBudgetPins(t *testing.T, budget *CommandLogDrainBudget, label, budgetFile string, requireNonLocal bool) {
	t.Helper()
	requireDurableCommandLogDrainBudgetPins(t, budget, label, budgetFile, requireNonLocal, "kafka", loadmodel.CommandLogDrainScalarAllocatorSQLEventPartitions)
	if budget.RequiredCommandKafkaTopicPrefix != "budgie.commands.load." ||
		budget.RequiredEventKafkaTopicPrefix != "budgie.events.load." ||
		budget.MinKafkaCommandPartitions != 32 ||
		budget.MinKafkaEventPartitions != 32 {
		t.Fatalf("%s budget = %+v, want Kafka topic prefixes and partition floors", label, budget)
	}
	if !budget.RequireScalarCompatibilityAudit ||
		budget.MaxLegacySQLScalarOffsetAfter == nil ||
		*budget.MaxLegacySQLScalarOffsetAfter != 0 {
		t.Fatalf("%s budget = %+v, want partition-only scalar compatibility audit pinned to zero", label, budget)
	}
}

func requireDurableCommandLogDrainBudgetPins(t *testing.T, budget *CommandLogDrainBudget, label, budgetFile string, requireNonLocal bool, backend, allocator string) {
	t.Helper()
	requireCommandLogDrainNativeGateShape(t, budget, label)
	if !budget.RequireDurableStaging ||
		!budget.RequireReportEvidence ||
		(requireNonLocal && (!budget.RequireRuntimeEndpoints || !budget.RequireNonLocalRuntimeEndpoints)) ||
		budget.RequiredReportBudgetFile != budgetFile ||
		budget.RequiredCommandLogBackend != backend ||
		budget.RequiredEventLogBackend != backend ||
		budget.RequiredScalarCompatibilityAllocator != allocator {
		t.Fatalf("%s budget = %+v, want durable report evidence pinned to %s", label, budget, backend)
	}
}

func validCommandLogDrainReport() loadmodel.CommandLogDrainLoadReport {
	return loadmodel.CommandLogDrainLoadReport{
		PromotionReadiness: loadmodel.CommandLogPromotionReadinessReport{Ready: true},
		MaterializationAudit: loadmodel.CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
}

func validCommandLogDrainDurableRuntime() loadmodel.CommandLogDrainLoadRuntime {
	return loadmodel.CommandLogDrainLoadRuntime{
		CommandLogBackend:            "nats",
		EventLogBackend:              "nats",
		MaterializationStore:         "postgres",
		ScalarCompatibilityAllocator: loadmodel.CommandLogDrainScalarAllocatorBrokerStreamSequence,
		NATSEndpoint:                 "nats://nats.internal:4222",
		PostgresEndpoint:             "postgres://postgres.internal:5432/budgie",
		DurableStaging:               true,
		CommandNATSStream:            "BUDGIE_COMMAND_LOG_LOAD_STAGING",
		CommandNATSReplicas:          1,
		EventNATSStream:              "BUDGIE_EVENT_LOG_LOAD_STAGING",
		EventNATSReplicas:            1,
	}
}

func validKafkaCommandLogDrainDurableRuntime() loadmodel.CommandLogDrainLoadRuntime {
	return loadmodel.CommandLogDrainLoadRuntime{
		CommandLogBackend:            "kafka",
		EventLogBackend:              "kafka",
		MaterializationStore:         "postgres",
		ScalarCompatibilityAllocator: loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets,
		PostgresEndpoint:             "postgres://postgres.internal:5432/budgie",
		DurableStaging:               true,
		KafkaBrokers:                 []string{"kafka.internal:9092", "redpanda.internal:9092"},
		KafkaCommandTopic:            "budgie.commands.load.20260613",
		KafkaEventTopic:              "budgie.events.load.20260613",
		KafkaConsumerGroup:           "budgie-writers-load",
		KafkaCommandPartitions:       32,
		KafkaEventPartitions:         32,
	}
}

func requireScaleBudgetPaths(t *testing.T, violations []ScaleBudgetViolation, paths ...string) {
	t.Helper()
	if len(violations) != len(paths) {
		t.Fatalf("violations = %+v, want %d", violations, len(paths))
	}

	got := make(map[string]bool, len(violations))
	for _, violation := range violations {
		got[violation.Path] = true
	}
	for _, path := range paths {
		if !got[path] {
			t.Fatalf("violations = %+v, missing %s", violations, path)
		}
	}
}

func requireNoScaleBudgetViolations(t *testing.T, violations []ScaleBudgetViolation, label string) {
	t.Helper()
	if len(violations) != 0 {
		t.Fatalf("%s violations = %+v, want none", label, violations)
	}
}
