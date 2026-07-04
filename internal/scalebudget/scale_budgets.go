package scalebudget

import (
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
)

type ScaleBudgets struct {
	PostgresWrites  *PostgresWriteBudget   `json:"postgresWrites,omitempty"`
	GatewayFanout   *GatewayFanoutBudget   `json:"gatewayFanout,omitempty"`
	CommandLogDrain *CommandLogDrainBudget `json:"commandLogDrain,omitempty"`
}

type PostgresWriteBudget struct {
	MinSpreadSpeedup        float64 `json:"minSpreadSpeedup,omitempty"`
	MinSpreadWritesPerSec   float64 `json:"minSpreadWritesPerSec,omitempty"`
	MaxSamePartitionP95MS   float64 `json:"maxSamePartitionP95Ms,omitempty"`
	MaxSpreadPartitionP95MS float64 `json:"maxSpreadPartitionP95Ms,omitempty"`
	MaxFailedWrites         int     `json:"maxFailedWrites,omitempty"`
}

type GatewayFanoutBudget struct {
	MaxPublishMS             float64 `json:"maxPublishMs,omitempty"`
	MinEstimatedDrops        int     `json:"minEstimatedDrops,omitempty"`
	MaxEstimatedDrops        int     `json:"maxEstimatedDrops,omitempty"`
	MaxIdleDeliveries        int     `json:"maxIdleDeliveries,omitempty"`
	MaxQueueDepthMax         int     `json:"maxQueueDepthMax,omitempty"`
	MinQueuedDeliveries      int     `json:"minQueuedDeliveries,omitempty"`
	MinSubscribers           int     `json:"minSubscribers,omitempty"`
	MinHotScopeSubscribers   int     `json:"minHotScopeSubscribers,omitempty"`
	TargetConnections        int     `json:"targetConnections,omitempty"`
	MaxGatewayNodesForTarget int     `json:"maxGatewayNodesForTarget,omitempty"`
	RequiredReportBudgetFile string  `json:"requiredReportBudgetFile,omitempty"`
}

type CommandLogDrainBudget struct {
	RequireDurableStaging                bool    `json:"requireDurableStaging,omitempty"`
	RequireReportEvidence                bool    `json:"requireReportEvidence,omitempty"`
	RequiredReportBudgetFile             string  `json:"requiredReportBudgetFile,omitempty"`
	RequiredCommandLogBackend            string  `json:"requiredCommandLogBackend,omitempty"`
	RequiredEventLogBackend              string  `json:"requiredEventLogBackend,omitempty"`
	RequiredScalarCompatibilityAllocator string  `json:"requiredScalarCompatibilityAllocator,omitempty"`
	RequirePostgresFlag                  bool    `json:"requirePostgresFlag,omitempty"`
	RequireRuntimeEndpoints              bool    `json:"requireRuntimeEndpoints,omitempty"`
	RequireNonLocalRuntimeEndpoints      bool    `json:"requireNonLocalRuntimeEndpoints,omitempty"`
	RequireDisposablePostgresSchema      bool    `json:"requireDisposablePostgresSchema,omitempty"`
	RequiredPostgresSchemaPrefix         string  `json:"requiredPostgresSchemaPrefix,omitempty"`
	RequiredCommandNATSStreamPrefix      string  `json:"requiredCommandNatsStreamPrefix,omitempty"`
	RequiredEventNATSStreamPrefix        string  `json:"requiredEventNatsStreamPrefix,omitempty"`
	MinCommandNATSReplicas               int     `json:"minCommandNatsReplicas,omitempty"`
	MinEventNATSReplicas                 int     `json:"minEventNatsReplicas,omitempty"`
	RequiredCommandKafkaTopicPrefix      string  `json:"requiredCommandKafkaTopicPrefix,omitempty"`
	RequiredEventKafkaTopicPrefix        string  `json:"requiredEventKafkaTopicPrefix,omitempty"`
	MinKafkaCommandPartitions            int     `json:"minKafkaCommandPartitions,omitempty"`
	MinKafkaEventPartitions              int     `json:"minKafkaEventPartitions,omitempty"`
	RequiredExecutorMode                 string  `json:"requiredExecutorMode,omitempty"`
	RequiredAssignmentMode               string  `json:"requiredAssignmentMode,omitempty"`
	RequireAuthoritativeSubmit           bool    `json:"requireAuthoritativeSubmit,omitempty"`
	RequireDirectedReplies               bool    `json:"requireDirectedReplies,omitempty"`
	MinRepliesPerThread                  int     `json:"minRepliesPerThread,omitempty"`
	MinBoards                            int     `json:"minBoards,omitempty"`
	MinCommandsPerBoard                  int     `json:"minCommandsPerBoard,omitempty"`
	MinWriters                           int     `json:"minWriters,omitempty"`
	MinBatchSize                         int     `json:"minBatchSize,omitempty"`
	MinTotalCommands                     int     `json:"minTotalCommands,omitempty"`
	MinSubmitCommandsPerSec              float64 `json:"minSubmitCommandsPerSec,omitempty"`
	MinDrainCommandsPerSec               float64 `json:"minDrainCommandsPerSec,omitempty"`
	MaxDrainDurationMS                   int64   `json:"maxDrainDurationMs,omitempty"`
	MaxPartitionLagAfter                 int64   `json:"maxPartitionLagAfter,omitempty"`
	RequireNativeExpectedEvents          bool    `json:"requireNativeExpectedEvents,omitempty"`
	MinEventProjectionEventsPerSec       float64 `json:"minEventProjectionEventsPerSec,omitempty"`
	MaxEventProjectionDurationMS         int64   `json:"maxEventProjectionDurationMs,omitempty"`
	MaxPromotionTotalLag                 int64   `json:"maxPromotionTotalLag,omitempty"`
	MaxLaggingPartitions                 int     `json:"maxLaggingPartitions,omitempty"`
	MaxMissingMaterialization            int     `json:"maxMissingMaterialization,omitempty"`
	MaxRetryingCommitted                 int     `json:"maxRetryingCommitted,omitempty"`
	MaxMissingRecords                    int     `json:"maxMissingRecords,omitempty"`
	RequireScalarCompatibilityAudit      bool    `json:"requireScalarCompatibilityAudit,omitempty"`
	MaxLegacySQLScalarOffsetAfter        *int64  `json:"maxLegacySqlScalarOffsetAfter,omitempty"`
	MaxFailedCommands                    int     `json:"maxFailedCommands,omitempty"`
	MaxCommitFailures                    int     `json:"maxCommitFailures,omitempty"`
	MaxAssignmentLosses                  int     `json:"maxAssignmentLosses,omitempty"`
	MaxClaimLosses                       int     `json:"maxClaimLosses,omitempty"`
}

type ScaleBudgetViolation struct {
	Path    string `json:"path"`
	Value   any    `json:"value"`
	Limit   any    `json:"limit"`
	Message string `json:"message"`
}

// NewViolation constructs a scale-budget violation with the package's standard shape.
func NewViolation(path string, value, limit any, message string) ScaleBudgetViolation {
	return ScaleBudgetViolation{
		Path:    path,
		Value:   value,
		Limit:   limit,
		Message: message,
	}
}

// ReportEvidenceViolations converts report-evidence policy failures into scale-budget violations.
func ReportEvidenceViolations(prefix string, violations []runevidence.ReportEvidenceViolation) []ScaleBudgetViolation {
	out := make([]ScaleBudgetViolation, 0, len(violations))
	for _, violation := range violations {
		out = append(out, NewViolation(prefix+violation.Field, violation.Value, violation.Want, violation.Message))
	}
	return out
}

func LoadScaleBudgets(path string) (ScaleBudgets, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ScaleBudgets{}, nil
	}
	return runreport.ReadJSONFile[ScaleBudgets](path, true)
}

func EvaluateCommandLogDrainBudget(report loadmodel.CommandLogDrainLoadReport, budget *CommandLogDrainBudget) []ScaleBudgetViolation {
	if budget == nil {
		return nil
	}
	var out []ScaleBudgetViolation
	out = addViolation(out, budget.RequireDurableStaging && !report.Runtime.DurableStaging,
		"commandLogDrain.requireDurableStaging", report.Runtime.DurableStaging, true,
		"command-log drain report was not produced by a durable staging run")
	if budget.RequireDurableStaging {
		out = append(out, evaluateCommandLogDrainDurableRuntime(report)...)
	}
	if budget.RequireReportEvidence {
		out = append(out, evaluateCommandLogDrainReportEvidence(report, budget)...)
	}
	out = append(out, evaluateCommandLogDrainConfig(report, budget)...)
	out = AddMinViolation(out, "commandLogDrain.minSubmitCommandsPerSec", report.Submit.CommandsPerSec, budget.MinSubmitCommandsPerSec,
		"command-log production throughput is below budget")
	out = AddMinViolation(out, "commandLogDrain.minDrainCommandsPerSec", report.Drain.CommandsPerSec, budget.MinDrainCommandsPerSec,
		"command-log writer drain throughput is below budget")
	out = AddPositiveMaxViolation(out, "commandLogDrain.maxDrainDurationMs", report.Drain.DurationMS, budget.MaxDrainDurationMS,
		"command-log writer drain duration is above budget")
	out = addMaxViolation(out, "commandLogDrain.maxPartitionLagAfter", report.MaxPartitionLagAfterDrain, budget.MaxPartitionLagAfter,
		"command-log partition lag remains after drain")
	if report.Config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative {
		out = addViolation(out, !report.EventProjection.Enabled,
			"commandLogDrain.eventProjection.enabled", report.EventProjection.Enabled, true,
			"native command-log drain did not run event projection")
		out = addViolation(out, !budget.RequireNativeExpectedEvents && report.EventProjection.AppliedEvents < report.TotalCommands,
			"commandLogDrain.eventProjection.appliedEvents", report.EventProjection.AppliedEvents, report.TotalCommands,
			"native command-log event projection applied fewer events than drained commands")
		if budget.RequireNativeExpectedEvents {
			expectedEvents := loadmodel.CommandLogDrainLoadExpectedEventProjectionEvents(report.Config)
			out = addViolation(out, report.EventProjection.ExpectedEvents != expectedEvents,
				"commandLogDrain.eventProjection.expectedEvents", report.EventProjection.ExpectedEvents, expectedEvents,
				"native command-log drain report did not record the expected broker event count for the staged command shape")
			out = addViolation(out, report.EventProjection.AppliedEvents != expectedEvents,
				"commandLogDrain.eventProjection.appliedEvents", report.EventProjection.AppliedEvents, expectedEvents,
				"native command-log event projection did not apply the expected broker event count for the staged command shape")
		}
	}
	if report.EventProjection.Enabled {
		out = addViolation(out, report.EventProjection.PartitionLimitExceeded,
			"commandLogDrain.eventProjection.partitionLimitExceeded", report.EventProjection.PartitionLimitExceeded, false,
			"event-store projection did not cover every listed broker event partition")
		out = AddMinViolation(out, "commandLogDrain.minEventProjectionEventsPerSec", report.EventProjection.EventsPerSec, budget.MinEventProjectionEventsPerSec,
			"event-store projection throughput is below budget")
		out = AddPositiveMaxViolation(out, "commandLogDrain.maxEventProjectionDurationMs", report.EventProjection.DurationMS, budget.MaxEventProjectionDurationMS,
			"event-store projection duration is above budget")
	}
	out = addViolation(out, !report.PromotionReadiness.Ready,
		"commandLogDrain.promotionReadiness.ready", report.PromotionReadiness.Ready, true,
		"command-log promotion readiness did not pass")
	out = addViolation(out, report.PromotionReadiness.PartitionLimitExceeded,
		"commandLogDrain.promotionReadiness.partitionLimitExceeded", report.PromotionReadiness.PartitionLimitExceeded, false,
		"command-log promotion readiness did not cover every listed partition")
	out = addMaxViolation(out, "commandLogDrain.maxPromotionTotalLag", report.PromotionReadiness.TotalLag, budget.MaxPromotionTotalLag,
		"command-log promotion readiness total lag is above budget")
	out = addMaxViolation(out, "commandLogDrain.maxLaggingPartitions", report.PromotionReadiness.LaggingPartitions, budget.MaxLaggingPartitions,
		"command-log promotion readiness lagging partitions are above budget")
	out = addViolation(out, report.MaterializationAudit.PartitionLimitExceeded,
		"commandLogDrain.materializationAudit.partitionLimitExceeded", report.MaterializationAudit.PartitionLimitExceeded, false,
		"command-log materialization audit did not cover every listed partition")
	out = addMaxViolation(out, "commandLogDrain.maxMissingMaterialization", report.MaterializationAudit.MissingMaterialization, budget.MaxMissingMaterialization,
		"command-log materialization audit missing commands are above budget")
	out = addMaxViolation(out, "commandLogDrain.maxRetryingCommitted", report.MaterializationAudit.RetryingCommitted, budget.MaxRetryingCommitted,
		"command-log materialization audit retrying committed commands are above budget")
	out = addMaxViolation(out, "commandLogDrain.maxMissingRecords", report.MaterializationAudit.MissingRecords, budget.MaxMissingRecords,
		"command-log materialization audit missing command-log records are above budget")
	out = append(out, evaluateCommandLogDrainScalarCompatibilityAudit(report, budget)...)
	failed := report.Submit.Failed + report.Drain.TerminalFailures + report.Drain.RetryableFailures
	out = AddZeroOrPositiveMaxIntViolation(out, "commandLogDrain.maxFailedCommands", failed, budget.MaxFailedCommands,
		"command-log failed commands are above budget", "command-log failed commands are above zero-failure budget")
	out = AddZeroOrPositiveMaxIntViolation(out, "commandLogDrain.maxCommitFailures", report.Drain.CommitFailures, budget.MaxCommitFailures,
		"command-log commit failures are above budget", "command-log commit failures are above zero-failure budget")
	out = AddZeroOrPositiveMaxIntViolation(out, "commandLogDrain.maxAssignmentLosses", report.Drain.AssignmentLosses, budget.MaxAssignmentLosses,
		"command-log assignment losses are above budget", "command-log assignment losses are above zero-loss budget")
	out = AddZeroOrPositiveMaxIntViolation(out, "commandLogDrain.maxClaimLosses", report.Drain.ClaimLosses, budget.MaxClaimLosses,
		"command-log claim losses are above budget", "command-log claim losses are above zero-loss budget")
	return out
}

func evaluateCommandLogDrainScalarCompatibilityAudit(report loadmodel.CommandLogDrainLoadReport, budget *CommandLogDrainBudget) []ScaleBudgetViolation {
	if budget == nil {
		return nil
	}
	var out []ScaleBudgetViolation
	audit := report.ScalarCompatibilityAudit
	requiresAudit := budget.RequireScalarCompatibilityAudit || budget.MaxLegacySQLScalarOffsetAfter != nil
	if requiresAudit && !audit.Enabled {
		out = addBudgetViolation(out, "commandLogDrain.scalarCompatibilityAudit.enabled", audit.Enabled, true,
			"command-log drain report must include scalar compatibility audit evidence")
		return out
	}
	if budget.MaxLegacySQLScalarOffsetAfter != nil && audit.LegacySQLScalarOffsetAfter > *budget.MaxLegacySQLScalarOffsetAfter {
		out = addBudgetViolation(out, "commandLogDrain.scalarCompatibilityAudit.legacySqlScalarOffsetAfter", audit.LegacySQLScalarOffsetAfter, *budget.MaxLegacySQLScalarOffsetAfter,
			"legacy SQL scalar broker offset advanced during a partition-only command-log drain")
	}
	return out
}

func evaluateCommandLogDrainConfig(report loadmodel.CommandLogDrainLoadReport, budget *CommandLogDrainBudget) []ScaleBudgetViolation {
	var out []ScaleBudgetViolation
	commandBackend := runconfig.NormalizeBackendAlias(report.Runtime.CommandLogBackend)
	eventBackend := runconfig.NormalizeBackendAlias(report.Runtime.EventLogBackend)
	out = addRequiredStringMatchViolation(out, "commandLogDrain.runtime.commandLogBackend", report.Runtime.CommandLogBackend, commandBackend,
		runconfig.NormalizeBackendAlias(budget.RequiredCommandLogBackend), "command-log drain report used the wrong command-log backend")
	out = addRequiredStringMatchViolation(out, "commandLogDrain.runtime.eventLogBackend", report.Runtime.EventLogBackend, eventBackend,
		runconfig.NormalizeBackendAlias(budget.RequiredEventLogBackend), "command-log drain report used the wrong event-log backend")
	out = addRequiredStringMatchViolation(out, "commandLogDrain.runtime.scalarCompatibilityAllocator", report.Runtime.ScalarCompatibilityAllocator,
		loadmodel.NormalizeCommandLogDrainScalarAllocator(report.Runtime.ScalarCompatibilityAllocator),
		loadmodel.NormalizeCommandLogDrainScalarAllocator(budget.RequiredScalarCompatibilityAllocator),
		"command-log drain report used the wrong scalar compatibility allocator")
	out = addViolation(out, budget.RequirePostgresFlag && !report.Runtime.RequirePostgres,
		"commandLogDrain.runtime.requirePostgres", report.Runtime.RequirePostgres, true,
		"command-log drain report must come from a fail-closed Postgres-required run")
	if budget.RequireRuntimeEndpoints {
		out = append(out, evaluateCommandLogDrainRuntimeEndpoints(report)...)
	}
	if budget.RequireNonLocalRuntimeEndpoints {
		out = append(out, evaluateCommandLogDrainNonLocalRuntimeEndpoints(report)...)
	}
	if budget.RequireDisposablePostgresSchema {
		out = addRequiredNonEmptyStringViolation(out, "commandLogDrain.runtime.postgresSchema", report.Runtime.PostgresSchema,
			"command-log drain report must record the disposable Postgres schema")
		out = addViolation(out, report.Runtime.KeepPostgresSchema,
			"commandLogDrain.runtime.keepPostgresSchema", report.Runtime.KeepPostgresSchema, false,
			"command-log drain report must not keep the disposable Postgres schema")
	}
	out = addRequiredPrefixViolation(out, "commandLogDrain.runtime.postgresSchemaPrefix", report.Runtime.PostgresSchema,
		budget.RequiredPostgresSchemaPrefix, "command-log drain report used an unexpected Postgres schema prefix")
	if commandBackend == "nats" {
		out = addRequiredPrefixViolation(out, "commandLogDrain.runtime.commandNatsStreamPrefix", report.Runtime.CommandNATSStream,
			budget.RequiredCommandNATSStreamPrefix, "command-log drain report used an unexpected command stream prefix")
		out = AddMinViolation(out, "commandLogDrain.runtime.commandNatsReplicas", report.Runtime.CommandNATSReplicas, budget.MinCommandNATSReplicas,
			"command-log drain report used too few command stream replicas")
	}
	if eventBackend == "nats" {
		out = addRequiredPrefixViolation(out, "commandLogDrain.runtime.eventNatsStreamPrefix", report.Runtime.EventNATSStream,
			budget.RequiredEventNATSStreamPrefix, "command-log drain report used an unexpected event stream prefix")
		out = AddMinViolation(out, "commandLogDrain.runtime.eventNatsReplicas", report.Runtime.EventNATSReplicas, budget.MinEventNATSReplicas,
			"command-log drain report used too few event stream replicas")
	}
	if commandBackend == "kafka" {
		out = addRequiredPrefixViolation(out, "commandLogDrain.runtime.kafkaCommandTopicPrefix", report.Runtime.KafkaCommandTopic,
			budget.RequiredCommandKafkaTopicPrefix, "command-log drain report used an unexpected Kafka command topic prefix")
		out = AddMinViolation(out, "commandLogDrain.runtime.kafkaCommandPartitions", report.Runtime.KafkaCommandPartitions, budget.MinKafkaCommandPartitions,
			"command-log drain report used too few Kafka command topic partitions")
	}
	if eventBackend == "kafka" {
		out = addRequiredPrefixViolation(out, "commandLogDrain.runtime.kafkaEventTopicPrefix", report.Runtime.KafkaEventTopic,
			budget.RequiredEventKafkaTopicPrefix, "command-log drain report used an unexpected Kafka event topic prefix")
		out = AddMinViolation(out, "commandLogDrain.runtime.kafkaEventPartitions", report.Runtime.KafkaEventPartitions, budget.MinKafkaEventPartitions,
			"command-log drain report used too few Kafka event topic partitions")
	}
	out = addRequiredStringMatchViolation(out, "commandLogDrain.config.executorMode", report.Config.ExecutorMode, report.Config.ExecutorMode,
		strings.TrimSpace(budget.RequiredExecutorMode), "command-log drain report used the wrong writer executor")
	out = addRequiredStringMatchViolation(out, "commandLogDrain.config.assignmentMode", report.Config.AssignmentMode, report.Config.AssignmentMode,
		strings.TrimSpace(budget.RequiredAssignmentMode), "command-log drain report used the wrong writer assignment mode")
	out = addViolation(out, budget.RequireAuthoritativeSubmit && !report.Config.AuthoritativeSubmit,
		"commandLogDrain.config.authoritativeSubmit", report.Config.AuthoritativeSubmit, true,
		"command-log drain report must use authoritative command-log submission")
	out = addViolation(out, budget.RequireDirectedReplies && !report.Config.DirectedReplies,
		"commandLogDrain.config.directedReplies", report.Config.DirectedReplies, true,
		"command-log drain report must include directed reply coverage")
	out = AddMinViolation(out, "commandLogDrain.config.repliesPerThread", report.Config.RepliesPerThread, budget.MinRepliesPerThread,
		"command-log drain report has too little reply coverage per thread")
	out = AddMinViolation(out, "commandLogDrain.config.boards", report.Config.Boards, budget.MinBoards,
		"command-log drain report covers too few board partitions")
	out = AddMinViolation(out, "commandLogDrain.config.commandsPerBoard", report.Config.CommandsPerBoard, budget.MinCommandsPerBoard,
		"command-log drain report has too few commands per board partition")
	out = AddMinViolation(out, "commandLogDrain.config.writers", report.Config.Writers, budget.MinWriters,
		"command-log drain report uses too few writer lanes")
	out = AddMinViolation(out, "commandLogDrain.config.batchSize", report.Config.BatchSize, budget.MinBatchSize,
		"command-log drain report uses too small a writer batch")
	out = AddMinViolation(out, "commandLogDrain.totalCommands", report.TotalCommands, budget.MinTotalCommands,
		"command-log drain report includes too few total commands")
	return out
}

func evaluateCommandLogDrainRuntimeEndpoints(report loadmodel.CommandLogDrainLoadReport) []ScaleBudgetViolation {
	var out []ScaleBudgetViolation
	runtime := report.Runtime
	switch runconfig.NormalizeBackendAlias(runtime.CommandLogBackend) {
	case "nats":
		out = addRuntimeEndpointEvidenceViolation(out, "natsEndpoint", runtime.NATSEndpoint, "NATS")
	case "kafka":
		out = addViolation(out, len(runtime.KafkaBrokers) == 0,
			"commandLogDrain.runtime.kafkaBrokers", runtime.KafkaBrokers, "redacted non-empty",
			"command-log drain report must record redacted Kafka brokers")
		for _, broker := range runtime.KafkaBrokers {
			if runevidence.EndpointLooksSensitive(runevidence.KafkaBrokerEndpointURL(broker)) {
				out = addBudgetViolation(out, "commandLogDrain.runtime.kafkaBrokers", runtime.KafkaBrokers, "redacted brokers",
					"command-log drain report Kafka brokers must not include credentials or query material")
				break
			}
		}
	}
	if runconfig.NormalizeBackendAlias(runtime.MaterializationStore) == "postgres" {
		out = addRuntimeEndpointEvidenceViolation(out, "postgresEndpoint", runtime.PostgresEndpoint, "Postgres")
	}
	return out
}

func addRuntimeEndpointEvidenceViolation(out []ScaleBudgetViolation, pathSuffix, endpoint, label string) []ScaleBudgetViolation {
	path := "commandLogDrain.runtime." + pathSuffix
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return addBudgetViolation(out, path, endpoint, "redacted non-empty",
			fmt.Sprintf("command-log drain report must record the redacted %s endpoint", label))
	}
	if runevidence.EndpointLooksSensitive(trimmed) {
		return addBudgetViolation(out, path, endpoint, "redacted endpoint",
			fmt.Sprintf("command-log drain report %s endpoint must not include credentials or query material", label))
	}
	return out
}

func evaluateCommandLogDrainNonLocalRuntimeEndpoints(report loadmodel.CommandLogDrainLoadReport) []ScaleBudgetViolation {
	var out []ScaleBudgetViolation
	runtime := report.Runtime
	switch runconfig.NormalizeBackendAlias(runtime.CommandLogBackend) {
	case "nats":
		out = addViolation(out, runevidence.EndpointHostIsLocal(runtime.NATSEndpoint),
			"commandLogDrain.runtime.natsEndpointLocal", runtime.NATSEndpoint, "non-local endpoint",
			"remote staging evidence must not use a localhost NATS endpoint")
	case "kafka":
		for _, broker := range runtime.KafkaBrokers {
			if runevidence.EndpointHostIsLocal(runevidence.KafkaBrokerEndpointURL(broker)) {
				out = addBudgetViolation(out, "commandLogDrain.runtime.kafkaBrokersLocal", runtime.KafkaBrokers, "non-local brokers",
					"remote staging evidence must not use localhost Kafka brokers")
				break
			}
		}
	}
	out = addViolation(out, runconfig.NormalizeBackendAlias(runtime.MaterializationStore) == "postgres" && runevidence.EndpointHostIsLocal(runtime.PostgresEndpoint),
		"commandLogDrain.runtime.postgresEndpointLocal", runtime.PostgresEndpoint, "non-local endpoint",
		"remote staging evidence must not use a localhost Postgres endpoint")
	return out
}

func evaluateCommandLogDrainReportEvidence(report loadmodel.CommandLogDrainLoadReport, budget *CommandLogDrainBudget) []ScaleBudgetViolation {
	return ReportEvidenceViolations("commandLogDrain.evidence.", runevidence.ValidateReportEvidence(report.Evidence, runevidence.ReportEvidencePolicy{
		Tool:               "budgie-commandlog-loadgen",
		RequiredBudgetFile: budget.RequiredReportBudgetFile,
		ReportName:         "command-log drain report",
	}))
}

func evaluateCommandLogDrainDurableRuntime(report loadmodel.CommandLogDrainLoadReport) []ScaleBudgetViolation {
	var out []ScaleBudgetViolation
	runtime := report.Runtime
	commandBackend := runconfig.NormalizeBackendAlias(runtime.CommandLogBackend)
	out = addViolation(out, commandBackend != "nats" && commandBackend != "kafka",
		"commandLogDrain.runtime.commandLogBackend", runtime.CommandLogBackend, "nats|kafka",
		"durable command-log staging requires a NATS or Kafka command log")
	materialization := runconfig.NormalizeBackendAlias(runtime.MaterializationStore)
	out = addViolation(out, materialization != "postgres",
		"commandLogDrain.runtime.materializationStore", runtime.MaterializationStore, "postgres",
		"durable command-log staging requires Postgres materialization")
	commandStream := strings.TrimSpace(runtime.CommandNATSStream)
	out = addViolation(out, commandBackend == "nats" && commandStream == "",
		"commandLogDrain.runtime.commandNatsStream", runtime.CommandNATSStream, "non-empty",
		"durable command-log staging must record the NATS command stream")
	if commandBackend == "kafka" {
		out = addViolation(out, len(runtime.KafkaBrokers) == 0,
			"commandLogDrain.runtime.kafkaBrokers", runtime.KafkaBrokers, "non-empty",
			"durable command-log staging must record Kafka brokers")
		out = addRequiredNonEmptyStringViolation(out, "commandLogDrain.runtime.kafkaCommandTopic", runtime.KafkaCommandTopic,
			"durable command-log staging must record the Kafka command topic")
		out = addViolation(out, runtime.KafkaCommandPartitions <= 0,
			"commandLogDrain.runtime.kafkaCommandPartitions", runtime.KafkaCommandPartitions, "positive",
			"durable command-log staging must record Kafka command partitions")
	}
	if report.Config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative {
		eventBackend := runconfig.NormalizeBackendAlias(runtime.EventLogBackend)
		if eventBackend != "nats" && eventBackend != "kafka" {
			out = addBudgetViolation(out, "commandLogDrain.runtime.eventLogBackend", runtime.EventLogBackend, "nats|kafka",
				"native durable command-log staging requires a broker event log")
		} else if (commandBackend == "nats" || commandBackend == "kafka") && eventBackend != commandBackend {
			out = addBudgetViolation(out, "commandLogDrain.runtime.eventLogBackend", runtime.EventLogBackend, commandBackend,
				"native durable command-log staging requires a matching broker event log")
		}
		eventStream := strings.TrimSpace(runtime.EventNATSStream)
		out = addViolation(out, eventBackend == "nats" && eventStream == "",
			"commandLogDrain.runtime.eventNatsStream", runtime.EventNATSStream, "non-empty",
			"native durable command-log staging must record the NATS event stream")
		out = addViolation(out, commandStream != "" && eventStream != "" && commandStream == eventStream,
			"commandLogDrain.runtime.distinctNatsStreams", runtime.EventNATSStream, "distinct from commandNatsStream",
			"native durable command-log staging requires distinct command and event streams")
		if eventBackend == "kafka" {
			out = addRequiredNonEmptyStringViolation(out, "commandLogDrain.runtime.kafkaEventTopic", runtime.KafkaEventTopic,
				"native durable command-log staging must record the Kafka event topic")
			out = addViolation(out, runtime.KafkaEventPartitions <= 0,
				"commandLogDrain.runtime.kafkaEventPartitions", runtime.KafkaEventPartitions, "positive",
				"native durable command-log staging must record Kafka event partitions")
			out = addViolation(out, strings.TrimSpace(runtime.KafkaCommandTopic) != "" && strings.TrimSpace(runtime.KafkaCommandTopic) == strings.TrimSpace(runtime.KafkaEventTopic),
				"commandLogDrain.runtime.distinctKafkaTopics", runtime.KafkaEventTopic, "distinct from kafkaCommandTopic",
				"native durable command-log staging requires distinct Kafka command and event topics")
		}
	}
	return out
}

func FormatScaleBudgetViolations(violations []ScaleBudgetViolation) string {
	if len(violations) == 0 {
		return ""
	}
	lines := make([]string, 0, len(violations))
	for _, violation := range violations {
		lines = append(lines, fmt.Sprintf("%s: %s (value=%v limit=%v)", violation.Path, violation.Message, violation.Value, violation.Limit))
	}
	return strings.Join(lines, "; ")
}

// EvaluateReportBudgetHashEvidence checks that a report's recorded budget hash
// matches the budget file currently being checked. Empty recorded hashes are
// left to the caller's evidence policy.
func EvaluateReportBudgetHashEvidence(reportSHA, budgetFile, violationPath, message string) ([]ScaleBudgetViolation, error) {
	got := strings.ToLower(strings.TrimSpace(reportSHA))
	if got == "" {
		return nil, nil
	}
	want, err := runevidence.ReadFileSHA256(budgetFile)
	if err != nil {
		return nil, err
	}
	if got == want {
		return nil, nil
	}
	return []ScaleBudgetViolation{NewViolation(violationPath, reportSHA, want, message)}, nil
}

func addBudgetViolation(out []ScaleBudgetViolation, path string, value, limit any, message string) []ScaleBudgetViolation {
	return append(out, NewViolation(path, value, limit, message))
}

func addViolation(out []ScaleBudgetViolation, condition bool, path string, value, limit any, message string) []ScaleBudgetViolation {
	if condition {
		return addBudgetViolation(out, path, value, limit, message)
	}
	return out
}

func addRequiredStringMatchViolation(out []ScaleBudgetViolation, path, value, normalizedValue, required, message string) []ScaleBudgetViolation {
	return addViolation(out, required != "" && normalizedValue != required, path, value, required, message)
}

func addRequiredPrefixViolation(out []ScaleBudgetViolation, path, value, required, message string) []ScaleBudgetViolation {
	required = strings.TrimSpace(required)
	return addViolation(out, required != "" && !strings.HasPrefix(strings.TrimSpace(value), required), path, value, required, message)
}

func addRequiredNonEmptyStringViolation(out []ScaleBudgetViolation, path, value, message string) []ScaleBudgetViolation {
	return addViolation(out, strings.TrimSpace(value) == "", path, value, "non-empty", message)
}

// AddMinViolation appends a violation when min is positive and value is below it.
func AddMinViolation[T ~int | ~float64](out []ScaleBudgetViolation, path string, value, min T, message string) []ScaleBudgetViolation {
	return addViolation(out, min > 0 && value < min, path, value, min, message)
}

func addMaxViolation[T ~int | ~int64](out []ScaleBudgetViolation, path string, value, max T, message string) []ScaleBudgetViolation {
	return addViolation(out, value > max, path, value, max, message)
}

// AddPositiveMaxViolation appends a violation when max is positive and value is above it.
func AddPositiveMaxViolation[T ~int | ~int64 | ~float64](out []ScaleBudgetViolation, path string, value, max T, message string) []ScaleBudgetViolation {
	return addViolation(out, max > 0 && value > max, path, value, max, message)
}

// AddZeroOrPositiveMaxIntViolation appends positive-max or zero-budget violations for integer counters.
func AddZeroOrPositiveMaxIntViolation(out []ScaleBudgetViolation, path string, value, max int, overMessage, zeroMessage string) []ScaleBudgetViolation {
	if max > 0 && value > max {
		return append(out, NewViolation(path, value, max, overMessage))
	}
	if max == 0 && value > 0 {
		return append(out, NewViolation(path, value, 0, zeroMessage))
	}
	return out
}
