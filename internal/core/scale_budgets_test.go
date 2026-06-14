package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadScaleBudgets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budgets.json")
	if err := os.WriteFile(path, []byte(`{
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
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	budgets, err := LoadScaleBudgets(path)
	if err != nil {
		t.Fatalf("LoadScaleBudgets: %v", err)
	}
	if budgets.PostgresWrites == nil || budgets.PostgresWrites.MinSpreadSpeedup != 1.5 {
		t.Fatalf("postgres budget = %+v, want min spread speedup", budgets.PostgresWrites)
	}
	if budgets.GatewayFanout == nil || budgets.GatewayFanout.MinQueuedDeliveries != 128 {
		t.Fatalf("gateway budget = %+v, want min queued deliveries", budgets.GatewayFanout)
	}
	if budgets.GatewayFanout.TargetConnections != 1000000 || budgets.GatewayFanout.MaxGatewayNodesForTarget != 20 {
		t.Fatalf("gateway budget = %+v, want target connection budget", budgets.GatewayFanout)
	}
	if budgets.GatewayFanout.RequiredReportBudgetFile != "ops/internet-scale-budgets.example.json" {
		t.Fatalf("gateway budget = %+v, want required report budget file", budgets.GatewayFanout)
	}
	if budgets.CommandLogDrain == nil || budgets.CommandLogDrain.MinDrainCommandsPerSec != 75 {
		t.Fatalf("command-log budget = %+v, want min drain commands/sec", budgets.CommandLogDrain)
	}
	if !budgets.CommandLogDrain.RequireReportEvidence {
		t.Fatalf("command-log budget = %+v, want report evidence required", budgets.CommandLogDrain)
	}
	if budgets.CommandLogDrain.RequiredReportBudgetFile != "ops/internet-scale-budgets.example.json" {
		t.Fatalf("command-log budget = %+v, want required report budget file", budgets.CommandLogDrain)
	}
	if budgets.CommandLogDrain.RequiredCommandLogBackend != "kafka" || budgets.CommandLogDrain.RequiredEventLogBackend != "kafka" {
		t.Fatalf("command-log budget = %+v, want required Kafka backends", budgets.CommandLogDrain)
	}
	if budgets.CommandLogDrain.RequiredScalarCompatibilityAllocator != CommandLogDrainScalarAllocatorSQLEventOffsets {
		t.Fatalf("command-log budget = %+v, want scalar compatibility allocator pin", budgets.CommandLogDrain)
	}
	if !budgets.CommandLogDrain.RequirePostgresFlag || budgets.CommandLogDrain.RequiredExecutorMode != "native" || budgets.CommandLogDrain.RequiredAssignmentMode != "snapshot-assignment" {
		t.Fatalf("command-log budget = %+v, want required native snapshot staging shape", budgets.CommandLogDrain)
	}
	if !budgets.CommandLogDrain.RequireRuntimeEndpoints {
		t.Fatalf("command-log budget = %+v, want runtime endpoints required", budgets.CommandLogDrain)
	}
	if !budgets.CommandLogDrain.RequireNonLocalRuntimeEndpoints {
		t.Fatalf("command-log budget = %+v, want non-local runtime endpoints required", budgets.CommandLogDrain)
	}
	if !budgets.CommandLogDrain.RequireDisposablePostgresSchema || budgets.CommandLogDrain.RequiredPostgresSchemaPrefix != "budgie_cmdlog_load_" {
		t.Fatalf("command-log budget = %+v, want disposable postgres schema requirement", budgets.CommandLogDrain)
	}
	if budgets.CommandLogDrain.RequiredCommandNATSStreamPrefix != "BUDGIE_COMMAND_LOG_LOAD_" || budgets.CommandLogDrain.RequiredEventNATSStreamPrefix != "BUDGIE_EVENT_LOG_LOAD_" {
		t.Fatalf("command-log budget = %+v, want staging NATS stream prefix requirements", budgets.CommandLogDrain)
	}
	if budgets.CommandLogDrain.MinCommandNATSReplicas != 1 || budgets.CommandLogDrain.MinEventNATSReplicas != 1 {
		t.Fatalf("command-log budget = %+v, want minimum NATS replica evidence", budgets.CommandLogDrain)
	}
	if budgets.CommandLogDrain.RequiredCommandKafkaTopicPrefix != "budgie.commands.load." ||
		budgets.CommandLogDrain.RequiredEventKafkaTopicPrefix != "budgie.events.load." ||
		budgets.CommandLogDrain.MinKafkaCommandPartitions != 32 ||
		budgets.CommandLogDrain.MinKafkaEventPartitions != 32 {
		t.Fatalf("command-log budget = %+v, want Kafka topic and partition evidence", budgets.CommandLogDrain)
	}
	if !budgets.CommandLogDrain.RequireAuthoritativeSubmit || !budgets.CommandLogDrain.RequireDirectedReplies || budgets.CommandLogDrain.MinRepliesPerThread != 2 {
		t.Fatalf("command-log budget = %+v, want authoritative directed reply coverage", budgets.CommandLogDrain)
	}
	if budgets.CommandLogDrain.MinBoards != 8 || budgets.CommandLogDrain.MinCommandsPerBoard != 100 || budgets.CommandLogDrain.MinWriters != 8 || budgets.CommandLogDrain.MinBatchSize != 25 || budgets.CommandLogDrain.MinTotalCommands != 2400 {
		t.Fatalf("command-log budget = %+v, want minimum staged load size", budgets.CommandLogDrain)
	}
	if !budgets.CommandLogDrain.RequireNativeExpectedEvents ||
		budgets.CommandLogDrain.MinEventProjectionEventsPerSec != 75 ||
		budgets.CommandLogDrain.MaxEventProjectionDurationMS != 30000 {
		t.Fatalf("command-log budget = %+v, want native expected-event evidence and event-projection thresholds", budgets.CommandLogDrain)
	}
	if budgets.CommandLogDrain.MaxPromotionTotalLag != 0 || budgets.CommandLogDrain.MaxMissingMaterialization != 0 || budgets.CommandLogDrain.MaxClaimLosses != 0 {
		t.Fatalf("command-log budget = %+v, want zero-lag and zero-missing defaults", budgets.CommandLogDrain)
	}
}

func TestPromotedInternetScaleBudgetPinsGatewayFanoutReportEvidence(t *testing.T) {
	budgets, err := LoadScaleBudgets(filepath.Join("..", "..", "ops", "internet-scale-budgets.example.json"))
	if err != nil {
		t.Fatalf("LoadScaleBudgets promoted file: %v", err)
	}
	budget := budgets.GatewayFanout
	if budget == nil {
		t.Fatal("promoted budget missing gatewayFanout")
	}
	if budget.RequiredReportBudgetFile != "ops/internet-scale-budgets.example.json" {
		t.Fatalf("gateway fanout budget = %+v, want promoted report budget file pinned", budget)
	}
	if budget.MinSubscribers != 100000 ||
		budget.MinHotScopeSubscribers != 10000 ||
		budget.MinQueuedDeliveries != 10000 ||
		budget.TargetConnections != 1000000 ||
		budget.MaxGatewayNodesForTarget != 20 {
		t.Fatalf("gateway fanout budget = %+v, want promoted million-connection shape", budget)
	}
}

func TestPromotedInternetScaleBudgetPinsCommandLogDrainGate(t *testing.T) {
	budgets, err := LoadScaleBudgets(filepath.Join("..", "..", "ops", "internet-scale-budgets.example.json"))
	if err != nil {
		t.Fatalf("LoadScaleBudgets promoted file: %v", err)
	}
	budget := budgets.CommandLogDrain
	if budget == nil {
		t.Fatal("promoted budget missing commandLogDrain")
	}
	if !budget.RequireDurableStaging || !budget.RequireReportEvidence {
		t.Fatalf("command-log budget = %+v, want durable staging and report evidence required", budget)
	}
	if budget.RequiredReportBudgetFile != "ops/internet-scale-budgets.example.json" {
		t.Fatalf("command-log budget = %+v, want promoted report budget file pinned", budget)
	}
	if budget.RequiredCommandLogBackend != "nats" || budget.RequiredEventLogBackend != "nats" {
		t.Fatalf("command-log budget = %+v, want promoted NATS backend pins", budget)
	}
	if budget.RequiredScalarCompatibilityAllocator != CommandLogDrainScalarAllocatorBrokerStreamSequence {
		t.Fatalf("command-log budget = %+v, want promoted scalar compatibility allocator pin", budget)
	}
	if !budget.RequirePostgresFlag || !budget.RequireRuntimeEndpoints || !budget.RequireDisposablePostgresSchema {
		t.Fatalf("command-log budget = %+v, want fail-closed postgres/runtime/schema requirements", budget)
	}
	if budget.RequiredPostgresSchemaPrefix != "budgie_cmdlog_load_" ||
		budget.RequiredCommandNATSStreamPrefix != "BUDGIE_COMMAND_LOG_LOAD_" ||
		budget.RequiredEventNATSStreamPrefix != "BUDGIE_EVENT_LOG_LOAD_" {
		t.Fatalf("command-log budget = %+v, want staging schema and stream prefixes", budget)
	}
	if budget.MinCommandNATSReplicas != 1 || budget.MinEventNATSReplicas != 1 {
		t.Fatalf("command-log budget = %+v, want promoted minimum NATS replica evidence", budget)
	}
	if budget.RequiredExecutorMode != CommandLogDrainExecutorNative ||
		budget.RequiredAssignmentMode != CommandLogDrainAssignmentSnapshot ||
		!budget.RequireAuthoritativeSubmit ||
		!budget.RequireDirectedReplies ||
		budget.MinRepliesPerThread != 2 {
		t.Fatalf("command-log budget = %+v, want native authoritative directed-reply gate shape", budget)
	}
	if budget.MinBoards != 8 || budget.MinCommandsPerBoard != 100 ||
		budget.MinWriters != 8 || budget.MinBatchSize != 25 || budget.MinTotalCommands != 2400 {
		t.Fatalf("command-log budget = %+v, want promoted minimum staging load", budget)
	}
	if budget.MinSubmitCommandsPerSec != 100 ||
		budget.MinDrainCommandsPerSec != 75 ||
		budget.MaxDrainDurationMS != 30000 ||
		!budget.RequireNativeExpectedEvents ||
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
	budgets, err := LoadScaleBudgets(filepath.Join("..", "..", "ops", "internet-scale-remote-staging-budgets.example.json"))
	if err != nil {
		t.Fatalf("LoadScaleBudgets remote staging file: %v", err)
	}
	budget := budgets.CommandLogDrain
	if budget == nil {
		t.Fatal("remote staging budget missing commandLogDrain")
	}
	if !budget.RequireDurableStaging ||
		!budget.RequireReportEvidence ||
		!budget.RequireRuntimeEndpoints ||
		!budget.RequireNonLocalRuntimeEndpoints {
		t.Fatalf("remote staging budget = %+v, want durable report evidence with non-local runtime endpoints", budget)
	}
	if budget.RequiredCommandNATSStreamPrefix != "BUDGIE_COMMAND_LOG_LOAD_" ||
		budget.RequiredEventNATSStreamPrefix != "BUDGIE_EVENT_LOG_LOAD_" ||
		!budget.RequireNativeExpectedEvents {
		t.Fatalf("remote staging budget = %+v, want promoted stream prefixes and exact native event counts", budget)
	}
	if budget.RequiredCommandLogBackend != "nats" || budget.RequiredEventLogBackend != "nats" {
		t.Fatalf("remote staging budget = %+v, want NATS backend pins", budget)
	}
	if budget.RequiredScalarCompatibilityAllocator != CommandLogDrainScalarAllocatorBrokerStreamSequence {
		t.Fatalf("remote staging budget = %+v, want scalar compatibility allocator pin", budget)
	}
	if gateway := budgets.GatewayFanout; gateway == nil ||
		gateway.RequiredReportBudgetFile != "ops/internet-scale-remote-staging-budgets.example.json" ||
		gateway.MinSubscribers != 100000 ||
		gateway.TargetConnections != 1000000 {
		t.Fatalf("remote staging gateway budget = %+v, want remote report evidence and million-connection shape", gateway)
	}
}

func TestKafkaInternetScaleBudgetPinsKafkaCommandLogDrainGate(t *testing.T) {
	budgets, err := LoadScaleBudgets(filepath.Join("..", "..", "ops", "internet-scale-kafka-budgets.example.json"))
	if err != nil {
		t.Fatalf("LoadScaleBudgets Kafka file: %v", err)
	}
	budget := budgets.CommandLogDrain
	if budget == nil {
		t.Fatal("Kafka budget missing commandLogDrain")
	}
	if !budget.RequireDurableStaging ||
		!budget.RequireReportEvidence ||
		budget.RequiredReportBudgetFile != "ops/internet-scale-kafka-budgets.example.json" ||
		budget.RequiredCommandLogBackend != "kafka" ||
		budget.RequiredEventLogBackend != "kafka" ||
		budget.RequiredScalarCompatibilityAllocator != CommandLogDrainScalarAllocatorSQLEventPartitions {
		t.Fatalf("Kafka budget = %+v, want durable report evidence pinned to Kafka", budget)
	}
	if budget.RequiredCommandKafkaTopicPrefix != "budgie.commands.load." ||
		budget.RequiredEventKafkaTopicPrefix != "budgie.events.load." ||
		budget.MinKafkaCommandPartitions != 32 ||
		budget.MinKafkaEventPartitions != 32 {
		t.Fatalf("Kafka budget = %+v, want Kafka topic prefixes and partition floors", budget)
	}
	if budget.RequiredExecutorMode != CommandLogDrainExecutorNative ||
		budget.RequiredAssignmentMode != CommandLogDrainAssignmentSnapshot ||
		!budget.RequireAuthoritativeSubmit ||
		!budget.RequireDirectedReplies ||
		!budget.RequireNativeExpectedEvents {
		t.Fatalf("Kafka budget = %+v, want native authoritative directed-reply gate shape", budget)
	}
	if !budget.RequireScalarCompatibilityAudit ||
		budget.MaxLegacySQLScalarOffsetAfter == nil ||
		*budget.MaxLegacySQLScalarOffsetAfter != 0 {
		t.Fatalf("Kafka budget = %+v, want partition-only scalar compatibility audit pinned to zero", budget)
	}
}

func TestKafkaRemoteStagingInternetScaleBudgetRequiresNonLocalEndpoints(t *testing.T) {
	budgets, err := LoadScaleBudgets(filepath.Join("..", "..", "ops", "internet-scale-kafka-remote-staging-budgets.example.json"))
	if err != nil {
		t.Fatalf("LoadScaleBudgets Kafka remote file: %v", err)
	}
	budget := budgets.CommandLogDrain
	if budget == nil {
		t.Fatal("Kafka remote budget missing commandLogDrain")
	}
	if !budget.RequireDurableStaging ||
		!budget.RequireReportEvidence ||
		!budget.RequireRuntimeEndpoints ||
		!budget.RequireNonLocalRuntimeEndpoints ||
		budget.RequiredReportBudgetFile != "ops/internet-scale-kafka-remote-staging-budgets.example.json" ||
		budget.RequiredCommandLogBackend != "kafka" ||
		budget.RequiredEventLogBackend != "kafka" ||
		budget.RequiredScalarCompatibilityAllocator != CommandLogDrainScalarAllocatorSQLEventPartitions {
		t.Fatalf("Kafka remote budget = %+v, want durable Kafka report evidence with non-local runtime endpoints", budget)
	}
	if budget.RequiredCommandKafkaTopicPrefix != "budgie.commands.load." ||
		budget.RequiredEventKafkaTopicPrefix != "budgie.events.load." ||
		budget.MinKafkaCommandPartitions != 32 ||
		budget.MinKafkaEventPartitions != 32 ||
		!budget.RequireNativeExpectedEvents {
		t.Fatalf("Kafka remote budget = %+v, want Kafka topic prefixes, partition floors, and exact native event counts", budget)
	}
	if !budget.RequireScalarCompatibilityAudit ||
		budget.MaxLegacySQLScalarOffsetAfter == nil ||
		*budget.MaxLegacySQLScalarOffsetAfter != 0 {
		t.Fatalf("Kafka remote budget = %+v, want partition-only scalar compatibility audit pinned to zero", budget)
	}
}

func TestLoadScaleBudgetsRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budgets.json")
	if err := os.WriteFile(path, []byte(`{"postgresWrites":{"minSpreadSpeeedup":1.5}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := LoadScaleBudgets(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadScaleBudgets err = %v, want unknown field", err)
	}
}

func TestEvaluateCommandLogDrainBudgetValidatesNonLocalRemoteStagingEndpoints(t *testing.T) {
	budget := &CommandLogDrainBudget{RequireNonLocalRuntimeEndpoints: true}
	report := CommandLogDrainLoadReport{
		Runtime: validCommandLogDrainDurableRuntime(),
		PromotionReadiness: CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
	report.Runtime.NATSEndpoint = "nats://127.0.0.1:4222"
	report.Runtime.PostgresEndpoint = "postgres://localhost:55432/budgie_staging"
	violations := EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.runtime.natsEndpointLocal",
		"commandLogDrain.runtime.postgresEndpointLocal",
	)

	report.Runtime.NATSEndpoint = "nats://nats.internal:4222"
	report.Runtime.PostgresEndpoint = "postgres://postgres.internal:5432/budgie_staging"
	if violations := EvaluateCommandLogDrainBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want non-local staging endpoints accepted", violations)
	}

	report.Runtime = validKafkaCommandLogDrainDurableRuntime()
	report.Runtime.KafkaBrokers = []string{"127.0.0.1:9092"}
	report.Runtime.PostgresEndpoint = "postgres://localhost:55432/budgie_staging"
	violations = EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.runtime.kafkaBrokersLocal",
		"commandLogDrain.runtime.postgresEndpointLocal",
	)

	report.Runtime.KafkaBrokers = []string{"kafka.internal:9092", "redpanda.internal:9092"}
	report.Runtime.PostgresEndpoint = "postgres://postgres.internal:5432/budgie_staging"
	if violations := EvaluateCommandLogDrainBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want non-local Kafka staging endpoints accepted", violations)
	}
}

func TestEvaluatePartitionWriteBudgetPassesAndFails(t *testing.T) {
	report := PartitionWriteLoadReport{
		SamePartition: PartitionWriteLoadCase{
			WritesPerSec: 100,
			LatencyP95MS: 80,
		},
		SpreadPartitions: PartitionWriteLoadCase{
			WritesPerSec: 225,
			LatencyP95MS: 40,
		},
		SpreadSpeedup: 2.25,
	}
	budget := &PostgresWriteBudget{
		MinSpreadSpeedup:        2,
		MinSpreadWritesPerSec:   200,
		MaxSamePartitionP95MS:   100,
		MaxSpreadPartitionP95MS: 60,
	}
	if violations := EvaluatePartitionWriteBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none", violations)
	}

	report.SpreadSpeedup = 1.1
	report.SpreadPartitions.WritesPerSec = 90
	report.SamePartition.LatencyP95MS = 125
	report.SpreadPartitions.LatencyP95MS = 75
	report.SpreadPartitions.Failed = 1
	violations := EvaluatePartitionWriteBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"postgresWrites.minSpreadSpeedup",
		"postgresWrites.minSpreadWritesPerSec",
		"postgresWrites.maxSamePartitionP95Ms",
		"postgresWrites.maxSpreadPartitionP95Ms",
		"postgresWrites.maxFailedWrites",
	)
	formatted := FormatScaleBudgetViolations(violations)
	if !strings.Contains(formatted, "postgresWrites.minSpreadSpeedup") || !strings.Contains(formatted, "value=1.1") {
		t.Fatalf("formatted violations = %q, want path and value", formatted)
	}
}

func TestEvaluateGatewayFanoutBudgetPassesAndFails(t *testing.T) {
	report := GatewayFanoutLoadReport{
		Evidence:            validGatewayFanoutReportEvidence(),
		PublishDurationMS:   45,
		QueuedDeliveries:    16,
		EstimatedDrops:      8,
		QueueDepthMax:       2,
		IdleSampleDelivered: 0,
		Subscribers:         40,
		HotScopeSubscribers: 8,
		TargetConnections:   100,
	}
	budget := &GatewayFanoutBudget{
		MaxPublishMS:             100,
		MinEstimatedDrops:        8,
		MaxEstimatedDrops:        12,
		MaxIdleDeliveries:        0,
		MaxQueueDepthMax:         2,
		MinQueuedDeliveries:      16,
		MinSubscribers:           40,
		MinHotScopeSubscribers:   8,
		TargetConnections:        100,
		MaxGatewayNodesForTarget: 3,
		RequiredReportBudgetFile: "ops/internet-scale-budgets.example.json",
	}
	if violations := EvaluateGatewayFanoutBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none", violations)
	}

	report.Evidence.BudgetFile = "ops/other-budget.json"
	violations := EvaluateGatewayFanoutBudget(report, budget)
	requireScaleBudgetPaths(t, violations, "gatewayFanout.evidence.budgetFile")
	report.Evidence = validGatewayFanoutReportEvidence()

	report.PublishDurationMS = 125
	report.EstimatedDrops = 4
	report.QueueDepthMax = 3
	report.QueuedDeliveries = 8
	report.IdleSampleDelivered = 1
	report.Subscribers = 30
	report.HotScopeSubscribers = 6
	report.TargetConnections = 90
	violations = EvaluateGatewayFanoutBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"gatewayFanout.maxPublishMs",
		"gatewayFanout.minEstimatedDrops",
		"gatewayFanout.maxIdleDeliveries",
		"gatewayFanout.maxQueueDepthMax",
		"gatewayFanout.minQueuedDeliveries",
		"gatewayFanout.minSubscribers",
		"gatewayFanout.minHotScopeSubscribers",
		"gatewayFanout.targetConnections",
		"gatewayFanout.maxGatewayNodesForTarget",
	)

	report.EstimatedDrops = 16
	violations = EvaluateGatewayFanoutBudget(report, budget)
	if !hasScaleBudgetPath(violations, "gatewayFanout.maxEstimatedDrops") {
		t.Fatalf("violations = %+v, want max estimated drops violation", violations)
	}
}

func validGatewayFanoutReportEvidence() GatewayFanoutLoadEvidence {
	return GatewayFanoutLoadEvidence{
		Tool:         "budgie-gateway-loadgen",
		BudgetFile:   "ops/internet-scale-budgets.example.json",
		BudgetSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		GitRevision:  "0123456789abcdef",
	}
}

func TestEvaluateCommandLogDrainBudgetPassesAndFails(t *testing.T) {
	report := CommandLogDrainLoadReport{
		Runtime: validCommandLogDrainDurableRuntime(),
		Submit: CommandLogLoadStage{
			CommandsPerSec: 250,
		},
		Drain: CommandLogDrainStage{
			CommandsPerSec: 200,
			DurationMS:     75,
		},
		MaxPartitionLagAfterDrain: 0,
		PromotionReadiness: CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
	budget := &CommandLogDrainBudget{
		RequireDurableStaging:   true,
		MinSubmitCommandsPerSec: 200,
		MinDrainCommandsPerSec:  150,
		MaxDrainDurationMS:      100,
		MaxPartitionLagAfter:    0,
	}
	if violations := EvaluateCommandLogDrainBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none", violations)
	}

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
	violations := EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
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
	report := CommandLogDrainLoadReport{
		Config: CommandLogDrainLoadConfig{
			ExecutorMode: CommandLogDrainExecutorNative,
		},
		Runtime: CommandLogDrainLoadRuntime{
			DurableStaging:       true,
			CommandLogBackend:    "memory",
			EventLogBackend:      "memory",
			MaterializationStore: "sqlite",
		},
		PromotionReadiness: CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
	violations := EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.runtime.commandLogBackend",
		"commandLogDrain.runtime.materializationStore",
		"commandLogDrain.runtime.eventLogBackend",
		"commandLogDrain.eventProjection.enabled",
	)

	report.Runtime = validCommandLogDrainDurableRuntime()
	report.Runtime.EventNATSStream = report.Runtime.CommandNATSStream
	report.EventProjection = EventStoreProjectionLoadStage{
		Enabled:       true,
		AppliedEvents: 1,
	}
	report.TotalCommands = 1
	violations = EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.runtime.distinctNatsStreams",
	)

	report.Runtime = validKafkaCommandLogDrainDurableRuntime()
	report.Runtime.KafkaBrokers = nil
	report.Runtime.KafkaCommandTopic = ""
	report.Runtime.KafkaEventTopic = ""
	report.Runtime.KafkaCommandPartitions = 0
	report.Runtime.KafkaEventPartitions = 0
	report.EventProjection = EventStoreProjectionLoadStage{
		Enabled:       true,
		AppliedEvents: 1,
	}
	report.TotalCommands = 1
	violations = EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.runtime.kafkaBrokers",
		"commandLogDrain.runtime.kafkaCommandTopic",
		"commandLogDrain.runtime.kafkaCommandPartitions",
		"commandLogDrain.runtime.kafkaEventTopic",
		"commandLogDrain.runtime.kafkaEventPartitions",
	)

	report.Runtime = validKafkaCommandLogDrainDurableRuntime()
	report.Runtime.KafkaEventTopic = report.Runtime.KafkaCommandTopic
	violations = EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.runtime.distinctKafkaTopics",
	)
}

func TestEvaluateCommandLogDrainBudgetValidatesReportEvidence(t *testing.T) {
	budget := &CommandLogDrainBudget{
		RequireReportEvidence:    true,
		RequiredReportBudgetFile: "ops/internet-scale-budgets.example.json",
	}
	report := CommandLogDrainLoadReport{
		Evidence: validCommandLogDrainReportEvidence(),
		PromotionReadiness: CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
	if violations := EvaluateCommandLogDrainBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none", violations)
	}

	report.Evidence = CommandLogDrainLoadEvidence{
		Tool:        "other-tool",
		GitModified: true,
	}
	violations := EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.evidence.tool",
		"commandLogDrain.evidence.budgetFile",
		"commandLogDrain.evidence.budgetSha256",
		"commandLogDrain.evidence.gitRevision",
		"commandLogDrain.evidence.gitModified",
	)

	report.Evidence = validCommandLogDrainReportEvidence()
	report.Evidence.BudgetFile = "ops/other-budget.json"
	violations = EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
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
		RequiredScalarCompatibilityAllocator: CommandLogDrainScalarAllocatorBrokerStreamSequence,
		MinCommandNATSReplicas:               1,
		MinEventNATSReplicas:                 1,
		RequiredExecutorMode:                 CommandLogDrainExecutorNative,
		RequiredAssignmentMode:               CommandLogDrainAssignmentSnapshot,
		RequireAuthoritativeSubmit:           true,
		RequireDirectedReplies:               true,
		MinRepliesPerThread:                  2,
		MinBoards:                            8,
		MinCommandsPerBoard:                  100,
		MinWriters:                           8,
		MinBatchSize:                         25,
		MinTotalCommands:                     2400,
	}
	report := CommandLogDrainLoadReport{
		Config: CommandLogDrainLoadConfig{
			ExecutorMode:        CommandLogDrainExecutorNative,
			AssignmentMode:      CommandLogDrainAssignmentSnapshot,
			AuthoritativeSubmit: true,
			DirectedReplies:     true,
			RepliesPerThread:    2,
			Boards:              8,
			CommandsPerBoard:    100,
			Writers:             8,
			BatchSize:           25,
		},
		Runtime:       validCommandLogDrainDurableRuntime(),
		TotalCommands: 2400,
		EventProjection: EventStoreProjectionLoadStage{
			Enabled:        true,
			ExpectedEvents: 3200,
			AppliedEvents:  3200,
		},
		PromotionReadiness: CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
	report.Runtime.RequirePostgres = true
	report.Runtime.PostgresSchema = "budgie_cmdlog_load_123"
	if violations := EvaluateCommandLogDrainBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none", violations)
	}

	report.Config.ExecutorMode = CommandLogDrainExecutorSQL
	report.Config.AssignmentMode = CommandLogDrainAssignmentHash
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
	report.Runtime.ScalarCompatibilityAllocator = CommandLogDrainScalarAllocatorSQLEventOffsets
	violations := EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
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
		RequiredScalarCompatibilityAllocator: CommandLogDrainScalarAllocatorSQLEventOffsets,
		RequireRuntimeEndpoints:              true,
		RequiredCommandKafkaTopicPrefix:      "budgie.commands.load.",
		RequiredEventKafkaTopicPrefix:        "budgie.events.load.",
		MinKafkaCommandPartitions:            32,
		MinKafkaEventPartitions:              32,
	}
	report := CommandLogDrainLoadReport{
		Runtime: validKafkaCommandLogDrainDurableRuntime(),
		Config: CommandLogDrainLoadConfig{
			ExecutorMode: CommandLogDrainExecutorNative,
		},
		EventProjection: EventStoreProjectionLoadStage{
			Enabled:       true,
			AppliedEvents: 1,
		},
		TotalCommands: 1,
		PromotionReadiness: CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
	if violations := EvaluateCommandLogDrainBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want Kafka report accepted", violations)
	}

	budget.RequiredScalarCompatibilityAllocator = "partition-only"
	report.Runtime.ScalarCompatibilityAllocator = CommandLogDrainScalarAllocatorSQLEventPartitions
	if violations := EvaluateCommandLogDrainBudget(report, budget); len(violations) != 0 {
		t.Fatalf("partition-only violations = %+v, want Kafka report accepted", violations)
	}
	report.Runtime.ScalarCompatibilityAllocator = CommandLogDrainScalarAllocatorSQLEventOffsets
	violations := EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations, "commandLogDrain.runtime.scalarCompatibilityAllocator")
	budget.RequiredScalarCompatibilityAllocator = CommandLogDrainScalarAllocatorSQLEventOffsets

	report.Runtime.CommandLogBackend = "nats"
	report.Runtime.EventLogBackend = "nats"
	report.Runtime.ScalarCompatibilityAllocator = CommandLogDrainScalarAllocatorBrokerStreamSequence
	report.Runtime.NATSEndpoint = "nats://nats.internal:4222"
	report.Runtime.KafkaBrokers = []string{"kafka://user:secret@kafka.internal:9092?token=secret"}
	report.Runtime.KafkaCommandTopic = "budgie.commands"
	report.Runtime.KafkaEventTopic = "budgie.events"
	report.Runtime.KafkaCommandPartitions = 8
	report.Runtime.KafkaEventPartitions = 8
	violations = EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.runtime.commandLogBackend",
		"commandLogDrain.runtime.eventLogBackend",
		"commandLogDrain.runtime.scalarCompatibilityAllocator",
	)

	report.Runtime.CommandLogBackend = "kafka"
	report.Runtime.EventLogBackend = "kafka"
	report.Runtime.ScalarCompatibilityAllocator = CommandLogDrainScalarAllocatorSQLEventOffsets
	violations = EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.runtime.kafkaBrokers",
		"commandLogDrain.runtime.kafkaCommandTopicPrefix",
		"commandLogDrain.runtime.kafkaEventTopicPrefix",
		"commandLogDrain.runtime.kafkaCommandPartitions",
		"commandLogDrain.runtime.kafkaEventPartitions",
	)
}

func TestEvaluateCommandLogDrainBudgetChecksNativeEventProjection(t *testing.T) {
	report := CommandLogDrainLoadReport{
		Config: CommandLogDrainLoadConfig{
			ExecutorMode: CommandLogDrainExecutorNative,
		},
		TotalCommands: 4,
		Submit: CommandLogLoadStage{
			CommandsPerSec: 250,
		},
		Drain: CommandLogDrainStage{
			CommandsPerSec: 200,
			DurationMS:     75,
		},
		PromotionReadiness: CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
	budget := &CommandLogDrainBudget{
		MinEventProjectionEventsPerSec: 100,
		MaxEventProjectionDurationMS:   100,
	}
	violations := EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.eventProjection.enabled",
		"commandLogDrain.eventProjection.appliedEvents",
	)

	report.EventProjection = EventStoreProjectionLoadStage{
		Enabled:                true,
		PartitionLimitExceeded: true,
		AppliedEvents:          4,
		DurationMS:             125,
		EventsPerSec:           80,
	}
	violations = EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"commandLogDrain.eventProjection.partitionLimitExceeded",
		"commandLogDrain.minEventProjectionEventsPerSec",
		"commandLogDrain.maxEventProjectionDurationMs",
	)

	report.EventProjection.PartitionLimitExceeded = false
	report.EventProjection.DurationMS = 90
	report.EventProjection.EventsPerSec = 125
	if violations := EvaluateCommandLogDrainBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none", violations)
	}

	report.Config.Boards = 2
	report.Config.CommandsPerBoard = 1
	report.Config.RepliesPerThread = 1
	report.TotalCommands = 4
	report.EventProjection.ExpectedEvents = 6
	report.EventProjection.AppliedEvents = 6
	budget.RequireNativeExpectedEvents = true
	if violations := EvaluateCommandLogDrainBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want exact expected-event pass", violations)
	}
	report.EventProjection.ExpectedEvents = 3
	report.EventProjection.AppliedEvents = 3
	violations = EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
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
	report := CommandLogDrainLoadReport{
		PromotionReadiness: CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
	violations := EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations, "commandLogDrain.scalarCompatibilityAudit.enabled")

	report.ScalarCompatibilityAudit = CommandLogScalarCompatibilityAudit{
		Enabled:                    true,
		Store:                      "event_scalar_offsets",
		OffsetID:                   "broker_event_log",
		LegacySQLScalarOffsetAfter: 0,
	}
	if violations := EvaluateCommandLogDrainBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want zero legacy scalar offset accepted", violations)
	}

	report.ScalarCompatibilityAudit.LegacySQLScalarOffsetAfter = 1
	violations = EvaluateCommandLogDrainBudget(report, budget)
	requireScaleBudgetPaths(t, violations, "commandLogDrain.scalarCompatibilityAudit.legacySqlScalarOffsetAfter")
}

func validCommandLogDrainReportEvidence() CommandLogDrainLoadEvidence {
	return CommandLogDrainLoadEvidence{
		Tool:         "budgie-commandlog-loadgen",
		BudgetFile:   "ops/internet-scale-budgets.example.json",
		BudgetSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		GitRevision:  "0123456789abcdef",
	}
}

func validCommandLogDrainDurableRuntime() CommandLogDrainLoadRuntime {
	return CommandLogDrainLoadRuntime{
		CommandLogBackend:            "nats",
		EventLogBackend:              "nats",
		MaterializationStore:         "postgres",
		ScalarCompatibilityAllocator: CommandLogDrainScalarAllocatorBrokerStreamSequence,
		NATSEndpoint:                 "nats://nats.internal:4222",
		PostgresEndpoint:             "postgres://postgres.internal:5432/budgie",
		DurableStaging:               true,
		CommandNATSStream:            "BUDGIE_COMMAND_LOG_LOAD_STAGING",
		CommandNATSReplicas:          1,
		EventNATSStream:              "BUDGIE_EVENT_LOG_LOAD_STAGING",
		EventNATSReplicas:            1,
	}
}

func validKafkaCommandLogDrainDurableRuntime() CommandLogDrainLoadRuntime {
	return CommandLogDrainLoadRuntime{
		CommandLogBackend:            "kafka",
		EventLogBackend:              "kafka",
		MaterializationStore:         "postgres",
		ScalarCompatibilityAllocator: CommandLogDrainScalarAllocatorSQLEventOffsets,
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
	for _, path := range paths {
		if !hasScaleBudgetPath(violations, path) {
			t.Fatalf("violations = %+v, missing %s", violations, path)
		}
	}
}

func hasScaleBudgetPath(violations []ScaleBudgetViolation, path string) bool {
	for _, violation := range violations {
		if violation.Path == path {
			return true
		}
	}
	return false
}
