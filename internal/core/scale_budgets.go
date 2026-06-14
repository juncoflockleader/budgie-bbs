package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

func LoadScaleBudgets(path string) (ScaleBudgets, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ScaleBudgets{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ScaleBudgets{}, err
	}
	var budgets ScaleBudgets
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&budgets); err != nil {
		return ScaleBudgets{}, err
	}
	return budgets, nil
}

func EvaluatePartitionWriteBudget(report PartitionWriteLoadReport, budget *PostgresWriteBudget) []ScaleBudgetViolation {
	if budget == nil {
		return nil
	}
	var out []ScaleBudgetViolation
	if budget.MinSpreadSpeedup > 0 && report.SpreadSpeedup < budget.MinSpreadSpeedup {
		out = append(out, budgetViolation("postgresWrites.minSpreadSpeedup", report.SpreadSpeedup, budget.MinSpreadSpeedup,
			"spread-partition throughput speedup is below budget"))
	}
	if budget.MinSpreadWritesPerSec > 0 && report.SpreadPartitions.WritesPerSec < budget.MinSpreadWritesPerSec {
		out = append(out, budgetViolation("postgresWrites.minSpreadWritesPerSec", report.SpreadPartitions.WritesPerSec, budget.MinSpreadWritesPerSec,
			"spread-partition writes/sec is below budget"))
	}
	if budget.MaxSamePartitionP95MS > 0 && report.SamePartition.LatencyP95MS > budget.MaxSamePartitionP95MS {
		out = append(out, budgetViolation("postgresWrites.maxSamePartitionP95Ms", report.SamePartition.LatencyP95MS, budget.MaxSamePartitionP95MS,
			"same-partition p95 latency is above budget"))
	}
	if budget.MaxSpreadPartitionP95MS > 0 && report.SpreadPartitions.LatencyP95MS > budget.MaxSpreadPartitionP95MS {
		out = append(out, budgetViolation("postgresWrites.maxSpreadPartitionP95Ms", report.SpreadPartitions.LatencyP95MS, budget.MaxSpreadPartitionP95MS,
			"spread-partition p95 latency is above budget"))
	}
	failed := report.SamePartition.Failed + report.SpreadPartitions.Failed
	if budget.MaxFailedWrites > 0 && failed > budget.MaxFailedWrites {
		out = append(out, budgetViolation("postgresWrites.maxFailedWrites", failed, budget.MaxFailedWrites,
			"failed writes are above budget"))
	}
	if budget.MaxFailedWrites == 0 && failed > 0 {
		out = append(out, budgetViolation("postgresWrites.maxFailedWrites", failed, 0,
			"failed writes are above zero-failure budget"))
	}
	return out
}

func EvaluateGatewayFanoutBudget(report GatewayFanoutLoadReport, budget *GatewayFanoutBudget) []ScaleBudgetViolation {
	if budget == nil {
		return nil
	}
	var out []ScaleBudgetViolation
	if strings.TrimSpace(budget.RequiredReportBudgetFile) != "" {
		out = append(out, evaluateGatewayFanoutReportEvidence(report, budget)...)
	}
	if budget.MaxPublishMS > 0 && report.PublishDurationMS > budget.MaxPublishMS {
		out = append(out, budgetViolation("gatewayFanout.maxPublishMs", report.PublishDurationMS, budget.MaxPublishMS,
			"hot-scope publish duration is above budget"))
	}
	if budget.MinEstimatedDrops > 0 && report.EstimatedDrops < budget.MinEstimatedDrops {
		out = append(out, budgetViolation("gatewayFanout.minEstimatedDrops", report.EstimatedDrops, budget.MinEstimatedDrops,
			"estimated slow-client drops are below budget"))
	}
	if budget.MaxEstimatedDrops > 0 && report.EstimatedDrops > budget.MaxEstimatedDrops {
		out = append(out, budgetViolation("gatewayFanout.maxEstimatedDrops", report.EstimatedDrops, budget.MaxEstimatedDrops,
			"estimated slow-client drops are above budget"))
	}
	if report.IdleSampleDelivered > budget.MaxIdleDeliveries {
		out = append(out, budgetViolation("gatewayFanout.maxIdleDeliveries", report.IdleSampleDelivered, budget.MaxIdleDeliveries,
			"idle subscribers received unrelated events"))
	}
	if budget.MaxQueueDepthMax > 0 && report.QueueDepthMax > budget.MaxQueueDepthMax {
		out = append(out, budgetViolation("gatewayFanout.maxQueueDepthMax", report.QueueDepthMax, budget.MaxQueueDepthMax,
			"maximum queue depth is above budget"))
	}
	if budget.MinQueuedDeliveries > 0 && report.QueuedDeliveries < budget.MinQueuedDeliveries {
		out = append(out, budgetViolation("gatewayFanout.minQueuedDeliveries", report.QueuedDeliveries, budget.MinQueuedDeliveries,
			"queued hot-scope deliveries are below budget"))
	}
	if budget.MinSubscribers > 0 && report.Subscribers < budget.MinSubscribers {
		out = append(out, budgetViolation("gatewayFanout.minSubscribers", report.Subscribers, budget.MinSubscribers,
			"per-gateway subscriber capacity is below budget"))
	}
	if budget.MinHotScopeSubscribers > 0 && report.HotScopeSubscribers < budget.MinHotScopeSubscribers {
		out = append(out, budgetViolation("gatewayFanout.minHotScopeSubscribers", report.HotScopeSubscribers, budget.MinHotScopeSubscribers,
			"hot-scope subscriber capacity is below budget"))
	}
	targetConnections := report.TargetConnections
	if budget.TargetConnections > 0 {
		if targetConnections < budget.TargetConnections {
			out = append(out, budgetViolation("gatewayFanout.targetConnections", targetConnections, budget.TargetConnections,
				"gateway fanout target connection count is below budget"))
		}
		targetConnections = budget.TargetConnections
	}
	if budget.MaxGatewayNodesForTarget > 0 && targetConnections > 0 {
		nodes := gatewayFanoutNodesForTarget(report.Subscribers, targetConnections)
		if nodes == 0 || nodes > budget.MaxGatewayNodesForTarget {
			out = append(out, budgetViolation("gatewayFanout.maxGatewayNodesForTarget", nodes, budget.MaxGatewayNodesForTarget,
				"projected gateway node count for the target is above budget"))
		}
	}
	return out
}

func evaluateGatewayFanoutReportEvidence(report GatewayFanoutLoadReport, budget *GatewayFanoutBudget) []ScaleBudgetViolation {
	var out []ScaleBudgetViolation
	evidence := report.Evidence
	if strings.TrimSpace(evidence.Tool) != "budgie-gateway-loadgen" {
		out = append(out, budgetViolation("gatewayFanout.evidence.tool", evidence.Tool, "budgie-gateway-loadgen",
			"gateway fanout report must record the producing tool"))
	}
	budgetFile := strings.TrimSpace(evidence.BudgetFile)
	requiredBudgetFile := strings.TrimSpace(budget.RequiredReportBudgetFile)
	if budgetFile == "" {
		out = append(out, budgetViolation("gatewayFanout.evidence.budgetFile", evidence.BudgetFile, "non-empty",
			"gateway fanout report must record the budget file"))
	} else if requiredBudgetFile != "" && normalizeEvidenceBudgetPath(budgetFile) != normalizeEvidenceBudgetPath(requiredBudgetFile) {
		out = append(out, budgetViolation("gatewayFanout.evidence.budgetFile", evidence.BudgetFile, requiredBudgetFile,
			"gateway fanout report must record the required budget file"))
	}
	if strings.TrimSpace(evidence.BudgetSHA256) == "" {
		out = append(out, budgetViolation("gatewayFanout.evidence.budgetSha256", evidence.BudgetSHA256, "non-empty",
			"gateway fanout report must record the budget file hash"))
	}
	if strings.TrimSpace(evidence.GitRevision) == "" {
		out = append(out, budgetViolation("gatewayFanout.evidence.gitRevision", evidence.GitRevision, "non-empty",
			"gateway fanout report must record the git revision"))
	}
	if evidence.GitModified {
		out = append(out, budgetViolation("gatewayFanout.evidence.gitModified", evidence.GitModified, false,
			"gateway fanout report must come from a clean git tree"))
	}
	return out
}

func EvaluateCommandLogDrainBudget(report CommandLogDrainLoadReport, budget *CommandLogDrainBudget) []ScaleBudgetViolation {
	if budget == nil {
		return nil
	}
	var out []ScaleBudgetViolation
	if budget.RequireDurableStaging && !report.Runtime.DurableStaging {
		out = append(out, budgetViolation("commandLogDrain.requireDurableStaging", report.Runtime.DurableStaging, true,
			"command-log drain report was not produced by a durable staging run"))
	}
	if budget.RequireDurableStaging {
		out = append(out, evaluateCommandLogDrainDurableRuntime(report)...)
	}
	if budget.RequireReportEvidence {
		out = append(out, evaluateCommandLogDrainReportEvidence(report, budget)...)
	}
	out = append(out, evaluateCommandLogDrainConfig(report, budget)...)
	if budget.MinSubmitCommandsPerSec > 0 && report.Submit.CommandsPerSec < budget.MinSubmitCommandsPerSec {
		out = append(out, budgetViolation("commandLogDrain.minSubmitCommandsPerSec", report.Submit.CommandsPerSec, budget.MinSubmitCommandsPerSec,
			"command-log production throughput is below budget"))
	}
	if budget.MinDrainCommandsPerSec > 0 && report.Drain.CommandsPerSec < budget.MinDrainCommandsPerSec {
		out = append(out, budgetViolation("commandLogDrain.minDrainCommandsPerSec", report.Drain.CommandsPerSec, budget.MinDrainCommandsPerSec,
			"command-log writer drain throughput is below budget"))
	}
	if budget.MaxDrainDurationMS > 0 && report.Drain.DurationMS > budget.MaxDrainDurationMS {
		out = append(out, budgetViolation("commandLogDrain.maxDrainDurationMs", report.Drain.DurationMS, budget.MaxDrainDurationMS,
			"command-log writer drain duration is above budget"))
	}
	if report.MaxPartitionLagAfterDrain > budget.MaxPartitionLagAfter {
		out = append(out, budgetViolation("commandLogDrain.maxPartitionLagAfter", report.MaxPartitionLagAfterDrain, budget.MaxPartitionLagAfter,
			"command-log partition lag remains after drain"))
	}
	if report.Config.ExecutorMode == CommandLogDrainExecutorNative {
		if !report.EventProjection.Enabled {
			out = append(out, budgetViolation("commandLogDrain.eventProjection.enabled", report.EventProjection.Enabled, true,
				"native command-log drain did not run event projection"))
		}
		if !budget.RequireNativeExpectedEvents && report.EventProjection.AppliedEvents < report.TotalCommands {
			out = append(out, budgetViolation("commandLogDrain.eventProjection.appliedEvents", report.EventProjection.AppliedEvents, report.TotalCommands,
				"native command-log event projection applied fewer events than drained commands"))
		}
		if budget.RequireNativeExpectedEvents {
			expectedEvents := commandLogDrainLoadExpectedEventProjectionEvents(report.Config)
			if report.EventProjection.ExpectedEvents != expectedEvents {
				out = append(out, budgetViolation("commandLogDrain.eventProjection.expectedEvents", report.EventProjection.ExpectedEvents, expectedEvents,
					"native command-log drain report did not record the expected broker event count for the staged command shape"))
			}
			if report.EventProjection.AppliedEvents != expectedEvents {
				out = append(out, budgetViolation("commandLogDrain.eventProjection.appliedEvents", report.EventProjection.AppliedEvents, expectedEvents,
					"native command-log event projection did not apply the expected broker event count for the staged command shape"))
			}
		}
	}
	if report.EventProjection.Enabled {
		if report.EventProjection.PartitionLimitExceeded {
			out = append(out, budgetViolation("commandLogDrain.eventProjection.partitionLimitExceeded", report.EventProjection.PartitionLimitExceeded, false,
				"event-store projection did not cover every listed broker event partition"))
		}
		if budget.MinEventProjectionEventsPerSec > 0 && report.EventProjection.EventsPerSec < budget.MinEventProjectionEventsPerSec {
			out = append(out, budgetViolation("commandLogDrain.minEventProjectionEventsPerSec", report.EventProjection.EventsPerSec, budget.MinEventProjectionEventsPerSec,
				"event-store projection throughput is below budget"))
		}
		if budget.MaxEventProjectionDurationMS > 0 && report.EventProjection.DurationMS > budget.MaxEventProjectionDurationMS {
			out = append(out, budgetViolation("commandLogDrain.maxEventProjectionDurationMs", report.EventProjection.DurationMS, budget.MaxEventProjectionDurationMS,
				"event-store projection duration is above budget"))
		}
	}
	if !report.PromotionReadiness.Ready {
		out = append(out, budgetViolation("commandLogDrain.promotionReadiness.ready", report.PromotionReadiness.Ready, true,
			"command-log promotion readiness did not pass"))
	}
	if report.PromotionReadiness.PartitionLimitExceeded {
		out = append(out, budgetViolation("commandLogDrain.promotionReadiness.partitionLimitExceeded", report.PromotionReadiness.PartitionLimitExceeded, false,
			"command-log promotion readiness did not cover every listed partition"))
	}
	if report.PromotionReadiness.TotalLag > budget.MaxPromotionTotalLag {
		out = append(out, budgetViolation("commandLogDrain.maxPromotionTotalLag", report.PromotionReadiness.TotalLag, budget.MaxPromotionTotalLag,
			"command-log promotion readiness total lag is above budget"))
	}
	if report.PromotionReadiness.LaggingPartitions > budget.MaxLaggingPartitions {
		out = append(out, budgetViolation("commandLogDrain.maxLaggingPartitions", report.PromotionReadiness.LaggingPartitions, budget.MaxLaggingPartitions,
			"command-log promotion readiness lagging partitions are above budget"))
	}
	if report.MaterializationAudit.PartitionLimitExceeded {
		out = append(out, budgetViolation("commandLogDrain.materializationAudit.partitionLimitExceeded", report.MaterializationAudit.PartitionLimitExceeded, false,
			"command-log materialization audit did not cover every listed partition"))
	}
	if report.MaterializationAudit.MissingMaterialization > budget.MaxMissingMaterialization {
		out = append(out, budgetViolation("commandLogDrain.maxMissingMaterialization", report.MaterializationAudit.MissingMaterialization, budget.MaxMissingMaterialization,
			"command-log materialization audit missing commands are above budget"))
	}
	if report.MaterializationAudit.RetryingCommitted > budget.MaxRetryingCommitted {
		out = append(out, budgetViolation("commandLogDrain.maxRetryingCommitted", report.MaterializationAudit.RetryingCommitted, budget.MaxRetryingCommitted,
			"command-log materialization audit retrying committed commands are above budget"))
	}
	if report.MaterializationAudit.MissingRecords > budget.MaxMissingRecords {
		out = append(out, budgetViolation("commandLogDrain.maxMissingRecords", report.MaterializationAudit.MissingRecords, budget.MaxMissingRecords,
			"command-log materialization audit missing command-log records are above budget"))
	}
	out = append(out, evaluateCommandLogDrainScalarCompatibilityAudit(report, budget)...)
	failed := report.Submit.Failed + report.Drain.TerminalFailures + report.Drain.RetryableFailures
	if budget.MaxFailedCommands > 0 && failed > budget.MaxFailedCommands {
		out = append(out, budgetViolation("commandLogDrain.maxFailedCommands", failed, budget.MaxFailedCommands,
			"command-log failed commands are above budget"))
	}
	if budget.MaxFailedCommands == 0 && failed > 0 {
		out = append(out, budgetViolation("commandLogDrain.maxFailedCommands", failed, 0,
			"command-log failed commands are above zero-failure budget"))
	}
	if budget.MaxCommitFailures > 0 && report.Drain.CommitFailures > budget.MaxCommitFailures {
		out = append(out, budgetViolation("commandLogDrain.maxCommitFailures", report.Drain.CommitFailures, budget.MaxCommitFailures,
			"command-log commit failures are above budget"))
	}
	if budget.MaxCommitFailures == 0 && report.Drain.CommitFailures > 0 {
		out = append(out, budgetViolation("commandLogDrain.maxCommitFailures", report.Drain.CommitFailures, 0,
			"command-log commit failures are above zero-failure budget"))
	}
	if budget.MaxAssignmentLosses > 0 && report.Drain.AssignmentLosses > budget.MaxAssignmentLosses {
		out = append(out, budgetViolation("commandLogDrain.maxAssignmentLosses", report.Drain.AssignmentLosses, budget.MaxAssignmentLosses,
			"command-log assignment losses are above budget"))
	}
	if budget.MaxAssignmentLosses == 0 && report.Drain.AssignmentLosses > 0 {
		out = append(out, budgetViolation("commandLogDrain.maxAssignmentLosses", report.Drain.AssignmentLosses, 0,
			"command-log assignment losses are above zero-loss budget"))
	}
	if budget.MaxClaimLosses > 0 && report.Drain.ClaimLosses > budget.MaxClaimLosses {
		out = append(out, budgetViolation("commandLogDrain.maxClaimLosses", report.Drain.ClaimLosses, budget.MaxClaimLosses,
			"command-log claim losses are above budget"))
	}
	if budget.MaxClaimLosses == 0 && report.Drain.ClaimLosses > 0 {
		out = append(out, budgetViolation("commandLogDrain.maxClaimLosses", report.Drain.ClaimLosses, 0,
			"command-log claim losses are above zero-loss budget"))
	}
	return out
}

func evaluateCommandLogDrainScalarCompatibilityAudit(report CommandLogDrainLoadReport, budget *CommandLogDrainBudget) []ScaleBudgetViolation {
	if budget == nil {
		return nil
	}
	var out []ScaleBudgetViolation
	audit := report.ScalarCompatibilityAudit
	requiresAudit := budget.RequireScalarCompatibilityAudit || budget.MaxLegacySQLScalarOffsetAfter != nil
	if requiresAudit && !audit.Enabled {
		out = append(out, budgetViolation("commandLogDrain.scalarCompatibilityAudit.enabled", audit.Enabled, true,
			"command-log drain report must include scalar compatibility audit evidence"))
		return out
	}
	if budget.MaxLegacySQLScalarOffsetAfter != nil && audit.LegacySQLScalarOffsetAfter > *budget.MaxLegacySQLScalarOffsetAfter {
		out = append(out, budgetViolation("commandLogDrain.scalarCompatibilityAudit.legacySqlScalarOffsetAfter", audit.LegacySQLScalarOffsetAfter, *budget.MaxLegacySQLScalarOffsetAfter,
			"legacy SQL scalar broker offset advanced during a partition-only command-log drain"))
	}
	return out
}

func evaluateCommandLogDrainConfig(report CommandLogDrainLoadReport, budget *CommandLogDrainBudget) []ScaleBudgetViolation {
	var out []ScaleBudgetViolation
	commandBackend := normalizeCommandLogDrainBackend(report.Runtime.CommandLogBackend)
	eventBackend := normalizeCommandLogDrainBackend(report.Runtime.EventLogBackend)
	if required := normalizeCommandLogDrainBackend(budget.RequiredCommandLogBackend); required != "" && commandBackend != required {
		out = append(out, budgetViolation("commandLogDrain.runtime.commandLogBackend", report.Runtime.CommandLogBackend, required,
			"command-log drain report used the wrong command-log backend"))
	}
	if required := normalizeCommandLogDrainBackend(budget.RequiredEventLogBackend); required != "" && eventBackend != required {
		out = append(out, budgetViolation("commandLogDrain.runtime.eventLogBackend", report.Runtime.EventLogBackend, required,
			"command-log drain report used the wrong event-log backend"))
	}
	if required := normalizeCommandLogDrainScalarAllocator(budget.RequiredScalarCompatibilityAllocator); required != "" &&
		normalizeCommandLogDrainScalarAllocator(report.Runtime.ScalarCompatibilityAllocator) != required {
		out = append(out, budgetViolation("commandLogDrain.runtime.scalarCompatibilityAllocator", report.Runtime.ScalarCompatibilityAllocator, required,
			"command-log drain report used the wrong scalar compatibility allocator"))
	}
	if budget.RequirePostgresFlag && !report.Runtime.RequirePostgres {
		out = append(out, budgetViolation("commandLogDrain.runtime.requirePostgres", report.Runtime.RequirePostgres, true,
			"command-log drain report must come from a fail-closed Postgres-required run"))
	}
	if budget.RequireRuntimeEndpoints {
		out = append(out, evaluateCommandLogDrainRuntimeEndpoints(report)...)
	}
	if budget.RequireNonLocalRuntimeEndpoints {
		out = append(out, evaluateCommandLogDrainNonLocalRuntimeEndpoints(report)...)
	}
	if budget.RequireDisposablePostgresSchema {
		if strings.TrimSpace(report.Runtime.PostgresSchema) == "" {
			out = append(out, budgetViolation("commandLogDrain.runtime.postgresSchema", report.Runtime.PostgresSchema, "non-empty",
				"command-log drain report must record the disposable Postgres schema"))
		}
		if report.Runtime.KeepPostgresSchema {
			out = append(out, budgetViolation("commandLogDrain.runtime.keepPostgresSchema", report.Runtime.KeepPostgresSchema, false,
				"command-log drain report must not keep the disposable Postgres schema"))
		}
	}
	if required := strings.TrimSpace(budget.RequiredPostgresSchemaPrefix); required != "" && !strings.HasPrefix(strings.TrimSpace(report.Runtime.PostgresSchema), required) {
		out = append(out, budgetViolation("commandLogDrain.runtime.postgresSchemaPrefix", report.Runtime.PostgresSchema, required,
			"command-log drain report used an unexpected Postgres schema prefix"))
	}
	if required := strings.TrimSpace(budget.RequiredCommandNATSStreamPrefix); commandBackend == "nats" && required != "" && !strings.HasPrefix(strings.TrimSpace(report.Runtime.CommandNATSStream), required) {
		out = append(out, budgetViolation("commandLogDrain.runtime.commandNatsStreamPrefix", report.Runtime.CommandNATSStream, required,
			"command-log drain report used an unexpected command stream prefix"))
	}
	if required := strings.TrimSpace(budget.RequiredEventNATSStreamPrefix); eventBackend == "nats" && required != "" && !strings.HasPrefix(strings.TrimSpace(report.Runtime.EventNATSStream), required) {
		out = append(out, budgetViolation("commandLogDrain.runtime.eventNatsStreamPrefix", report.Runtime.EventNATSStream, required,
			"command-log drain report used an unexpected event stream prefix"))
	}
	if budget.MinCommandNATSReplicas > 0 && commandBackend == "nats" && report.Runtime.CommandNATSReplicas < budget.MinCommandNATSReplicas {
		out = append(out, budgetViolation("commandLogDrain.runtime.commandNatsReplicas", report.Runtime.CommandNATSReplicas, budget.MinCommandNATSReplicas,
			"command-log drain report used too few command stream replicas"))
	}
	if budget.MinEventNATSReplicas > 0 && eventBackend == "nats" && report.Runtime.EventNATSReplicas < budget.MinEventNATSReplicas {
		out = append(out, budgetViolation("commandLogDrain.runtime.eventNatsReplicas", report.Runtime.EventNATSReplicas, budget.MinEventNATSReplicas,
			"command-log drain report used too few event stream replicas"))
	}
	if required := strings.TrimSpace(budget.RequiredCommandKafkaTopicPrefix); commandBackend == "kafka" && required != "" && !strings.HasPrefix(strings.TrimSpace(report.Runtime.KafkaCommandTopic), required) {
		out = append(out, budgetViolation("commandLogDrain.runtime.kafkaCommandTopicPrefix", report.Runtime.KafkaCommandTopic, required,
			"command-log drain report used an unexpected Kafka command topic prefix"))
	}
	if required := strings.TrimSpace(budget.RequiredEventKafkaTopicPrefix); eventBackend == "kafka" && required != "" && !strings.HasPrefix(strings.TrimSpace(report.Runtime.KafkaEventTopic), required) {
		out = append(out, budgetViolation("commandLogDrain.runtime.kafkaEventTopicPrefix", report.Runtime.KafkaEventTopic, required,
			"command-log drain report used an unexpected Kafka event topic prefix"))
	}
	if budget.MinKafkaCommandPartitions > 0 && commandBackend == "kafka" && report.Runtime.KafkaCommandPartitions < budget.MinKafkaCommandPartitions {
		out = append(out, budgetViolation("commandLogDrain.runtime.kafkaCommandPartitions", report.Runtime.KafkaCommandPartitions, budget.MinKafkaCommandPartitions,
			"command-log drain report used too few Kafka command topic partitions"))
	}
	if budget.MinKafkaEventPartitions > 0 && eventBackend == "kafka" && report.Runtime.KafkaEventPartitions < budget.MinKafkaEventPartitions {
		out = append(out, budgetViolation("commandLogDrain.runtime.kafkaEventPartitions", report.Runtime.KafkaEventPartitions, budget.MinKafkaEventPartitions,
			"command-log drain report used too few Kafka event topic partitions"))
	}
	if required := strings.TrimSpace(budget.RequiredExecutorMode); required != "" && report.Config.ExecutorMode != required {
		out = append(out, budgetViolation("commandLogDrain.config.executorMode", report.Config.ExecutorMode, required,
			"command-log drain report used the wrong writer executor"))
	}
	if required := strings.TrimSpace(budget.RequiredAssignmentMode); required != "" && report.Config.AssignmentMode != required {
		out = append(out, budgetViolation("commandLogDrain.config.assignmentMode", report.Config.AssignmentMode, required,
			"command-log drain report used the wrong writer assignment mode"))
	}
	if budget.RequireAuthoritativeSubmit && !report.Config.AuthoritativeSubmit {
		out = append(out, budgetViolation("commandLogDrain.config.authoritativeSubmit", report.Config.AuthoritativeSubmit, true,
			"command-log drain report must use authoritative command-log submission"))
	}
	if budget.RequireDirectedReplies && !report.Config.DirectedReplies {
		out = append(out, budgetViolation("commandLogDrain.config.directedReplies", report.Config.DirectedReplies, true,
			"command-log drain report must include directed reply coverage"))
	}
	if budget.MinRepliesPerThread > 0 && report.Config.RepliesPerThread < budget.MinRepliesPerThread {
		out = append(out, budgetViolation("commandLogDrain.config.repliesPerThread", report.Config.RepliesPerThread, budget.MinRepliesPerThread,
			"command-log drain report has too little reply coverage per thread"))
	}
	if budget.MinBoards > 0 && report.Config.Boards < budget.MinBoards {
		out = append(out, budgetViolation("commandLogDrain.config.boards", report.Config.Boards, budget.MinBoards,
			"command-log drain report covers too few board partitions"))
	}
	if budget.MinCommandsPerBoard > 0 && report.Config.CommandsPerBoard < budget.MinCommandsPerBoard {
		out = append(out, budgetViolation("commandLogDrain.config.commandsPerBoard", report.Config.CommandsPerBoard, budget.MinCommandsPerBoard,
			"command-log drain report has too few commands per board partition"))
	}
	if budget.MinWriters > 0 && report.Config.Writers < budget.MinWriters {
		out = append(out, budgetViolation("commandLogDrain.config.writers", report.Config.Writers, budget.MinWriters,
			"command-log drain report uses too few writer lanes"))
	}
	if budget.MinBatchSize > 0 && report.Config.BatchSize < budget.MinBatchSize {
		out = append(out, budgetViolation("commandLogDrain.config.batchSize", report.Config.BatchSize, budget.MinBatchSize,
			"command-log drain report uses too small a writer batch"))
	}
	if budget.MinTotalCommands > 0 && report.TotalCommands < budget.MinTotalCommands {
		out = append(out, budgetViolation("commandLogDrain.totalCommands", report.TotalCommands, budget.MinTotalCommands,
			"command-log drain report includes too few total commands"))
	}
	return out
}

func evaluateCommandLogDrainRuntimeEndpoints(report CommandLogDrainLoadReport) []ScaleBudgetViolation {
	var out []ScaleBudgetViolation
	runtime := report.Runtime
	switch normalizeCommandLogDrainBackend(runtime.CommandLogBackend) {
	case "nats":
		endpoint := strings.TrimSpace(runtime.NATSEndpoint)
		if endpoint == "" {
			out = append(out, budgetViolation("commandLogDrain.runtime.natsEndpoint", runtime.NATSEndpoint, "redacted non-empty",
				"command-log drain report must record the redacted NATS endpoint"))
		} else if runtimeEndpointLooksSensitive(endpoint) {
			out = append(out, budgetViolation("commandLogDrain.runtime.natsEndpoint", runtime.NATSEndpoint, "redacted endpoint",
				"command-log drain report NATS endpoint must not include credentials or query material"))
		}
	case "kafka":
		if len(runtime.KafkaBrokers) == 0 {
			out = append(out, budgetViolation("commandLogDrain.runtime.kafkaBrokers", runtime.KafkaBrokers, "redacted non-empty",
				"command-log drain report must record redacted Kafka brokers"))
		}
		for _, broker := range runtime.KafkaBrokers {
			if runtimeKafkaBrokerLooksSensitive(broker) {
				out = append(out, budgetViolation("commandLogDrain.runtime.kafkaBrokers", runtime.KafkaBrokers, "redacted brokers",
					"command-log drain report Kafka brokers must not include credentials or query material"))
				break
			}
		}
	}
	if normalizeCommandLogDrainBackend(runtime.MaterializationStore) == "postgres" {
		endpoint := strings.TrimSpace(runtime.PostgresEndpoint)
		if endpoint == "" {
			out = append(out, budgetViolation("commandLogDrain.runtime.postgresEndpoint", runtime.PostgresEndpoint, "redacted non-empty",
				"command-log drain report must record the redacted Postgres endpoint"))
		} else if runtimeEndpointLooksSensitive(endpoint) {
			out = append(out, budgetViolation("commandLogDrain.runtime.postgresEndpoint", runtime.PostgresEndpoint, "redacted endpoint",
				"command-log drain report Postgres endpoint must not include credentials or query material"))
		}
	}
	return out
}

func evaluateCommandLogDrainNonLocalRuntimeEndpoints(report CommandLogDrainLoadReport) []ScaleBudgetViolation {
	var out []ScaleBudgetViolation
	runtime := report.Runtime
	switch normalizeCommandLogDrainBackend(runtime.CommandLogBackend) {
	case "nats":
		if runtimeEndpointHostIsLocal(runtime.NATSEndpoint) {
			out = append(out, budgetViolation("commandLogDrain.runtime.natsEndpointLocal", runtime.NATSEndpoint, "non-local endpoint",
				"remote staging evidence must not use a localhost NATS endpoint"))
		}
	case "kafka":
		for _, broker := range runtime.KafkaBrokers {
			if runtimeKafkaBrokerHostIsLocal(broker) {
				out = append(out, budgetViolation("commandLogDrain.runtime.kafkaBrokersLocal", runtime.KafkaBrokers, "non-local brokers",
					"remote staging evidence must not use localhost Kafka brokers"))
				break
			}
		}
	}
	if normalizeCommandLogDrainBackend(runtime.MaterializationStore) == "postgres" && runtimeEndpointHostIsLocal(runtime.PostgresEndpoint) {
		out = append(out, budgetViolation("commandLogDrain.runtime.postgresEndpointLocal", runtime.PostgresEndpoint, "non-local endpoint",
			"remote staging evidence must not use a localhost Postgres endpoint"))
	}
	return out
}

func runtimeEndpointLooksSensitive(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	if strings.Contains(strings.ToLower(endpoint), "password=") || strings.Contains(strings.ToLower(endpoint), "token=") {
		return true
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" {
		return false
	}
	return parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != ""
}

func runtimeEndpointHostIsLocal(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		host = strings.ToLower(strings.TrimSpace(endpoint))
	}
	switch host {
	case "localhost", "localhost.localdomain", "::1", "[::1]":
		return true
	}
	if strings.HasPrefix(host, "localhost.") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func runtimeKafkaBrokerLooksSensitive(broker string) bool {
	broker = strings.TrimSpace(broker)
	if broker == "" {
		return false
	}
	lower := strings.ToLower(broker)
	if strings.Contains(lower, "password=") || strings.Contains(lower, "token=") || strings.Contains(broker, "@") || strings.Contains(broker, "?") || strings.Contains(broker, "#") {
		return true
	}
	return runtimeEndpointLooksSensitive(runtimeKafkaBrokerURL(broker))
}

func runtimeKafkaBrokerHostIsLocal(broker string) bool {
	return runtimeEndpointHostIsLocal(runtimeKafkaBrokerURL(broker))
}

func runtimeKafkaBrokerURL(broker string) string {
	broker = strings.TrimSpace(broker)
	if broker == "" || strings.Contains(broker, "://") {
		return broker
	}
	return "kafka://" + broker
}

func evaluateCommandLogDrainReportEvidence(report CommandLogDrainLoadReport, budget *CommandLogDrainBudget) []ScaleBudgetViolation {
	var out []ScaleBudgetViolation
	evidence := report.Evidence
	if strings.TrimSpace(evidence.Tool) != "budgie-commandlog-loadgen" {
		out = append(out, budgetViolation("commandLogDrain.evidence.tool", evidence.Tool, "budgie-commandlog-loadgen",
			"command-log drain report must record the producing tool"))
	}
	budgetFile := strings.TrimSpace(evidence.BudgetFile)
	requiredBudgetFile := strings.TrimSpace(budget.RequiredReportBudgetFile)
	if budgetFile == "" {
		out = append(out, budgetViolation("commandLogDrain.evidence.budgetFile", evidence.BudgetFile, "non-empty",
			"command-log drain report must record the budget file"))
	} else if requiredBudgetFile != "" && normalizeEvidenceBudgetPath(budgetFile) != normalizeEvidenceBudgetPath(requiredBudgetFile) {
		out = append(out, budgetViolation("commandLogDrain.evidence.budgetFile", evidence.BudgetFile, requiredBudgetFile,
			"command-log drain report must record the required budget file"))
	}
	if strings.TrimSpace(evidence.BudgetSHA256) == "" {
		out = append(out, budgetViolation("commandLogDrain.evidence.budgetSha256", evidence.BudgetSHA256, "non-empty",
			"command-log drain report must record the budget file hash"))
	}
	if strings.TrimSpace(evidence.GitRevision) == "" {
		out = append(out, budgetViolation("commandLogDrain.evidence.gitRevision", evidence.GitRevision, "non-empty",
			"command-log drain report must record the git revision"))
	}
	if evidence.GitModified {
		out = append(out, budgetViolation("commandLogDrain.evidence.gitModified", evidence.GitModified, false,
			"command-log drain report must come from a clean git tree"))
	}
	return out
}

func normalizeEvidenceBudgetPath(path string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
}

func evaluateCommandLogDrainDurableRuntime(report CommandLogDrainLoadReport) []ScaleBudgetViolation {
	var out []ScaleBudgetViolation
	runtime := report.Runtime
	commandBackend := normalizeCommandLogDrainBackend(runtime.CommandLogBackend)
	if commandBackend != "nats" && commandBackend != "kafka" {
		out = append(out, budgetViolation("commandLogDrain.runtime.commandLogBackend", runtime.CommandLogBackend, "nats|kafka",
			"durable command-log staging requires a NATS or Kafka command log"))
	}
	materialization := normalizeCommandLogDrainBackend(runtime.MaterializationStore)
	if materialization != "postgres" {
		out = append(out, budgetViolation("commandLogDrain.runtime.materializationStore", runtime.MaterializationStore, "postgres",
			"durable command-log staging requires Postgres materialization"))
	}
	commandStream := strings.TrimSpace(runtime.CommandNATSStream)
	if commandBackend == "nats" && commandStream == "" {
		out = append(out, budgetViolation("commandLogDrain.runtime.commandNatsStream", runtime.CommandNATSStream, "non-empty",
			"durable command-log staging must record the NATS command stream"))
	}
	if commandBackend == "kafka" {
		if len(runtime.KafkaBrokers) == 0 {
			out = append(out, budgetViolation("commandLogDrain.runtime.kafkaBrokers", runtime.KafkaBrokers, "non-empty",
				"durable command-log staging must record Kafka brokers"))
		}
		if strings.TrimSpace(runtime.KafkaCommandTopic) == "" {
			out = append(out, budgetViolation("commandLogDrain.runtime.kafkaCommandTopic", runtime.KafkaCommandTopic, "non-empty",
				"durable command-log staging must record the Kafka command topic"))
		}
		if runtime.KafkaCommandPartitions <= 0 {
			out = append(out, budgetViolation("commandLogDrain.runtime.kafkaCommandPartitions", runtime.KafkaCommandPartitions, "positive",
				"durable command-log staging must record Kafka command partitions"))
		}
	}
	if report.Config.ExecutorMode == CommandLogDrainExecutorNative {
		eventBackend := normalizeCommandLogDrainBackend(runtime.EventLogBackend)
		if eventBackend != "nats" && eventBackend != "kafka" {
			out = append(out, budgetViolation("commandLogDrain.runtime.eventLogBackend", runtime.EventLogBackend, "nats|kafka",
				"native durable command-log staging requires a broker event log"))
		} else if (commandBackend == "nats" || commandBackend == "kafka") && eventBackend != commandBackend {
			out = append(out, budgetViolation("commandLogDrain.runtime.eventLogBackend", runtime.EventLogBackend, commandBackend,
				"native durable command-log staging requires a matching broker event log"))
		}
		eventStream := strings.TrimSpace(runtime.EventNATSStream)
		if eventBackend == "nats" && eventStream == "" {
			out = append(out, budgetViolation("commandLogDrain.runtime.eventNatsStream", runtime.EventNATSStream, "non-empty",
				"native durable command-log staging must record the NATS event stream"))
		}
		if commandStream != "" && eventStream != "" && commandStream == eventStream {
			out = append(out, budgetViolation("commandLogDrain.runtime.distinctNatsStreams", runtime.EventNATSStream, "distinct from commandNatsStream",
				"native durable command-log staging requires distinct command and event streams"))
		}
		if eventBackend == "kafka" {
			if strings.TrimSpace(runtime.KafkaEventTopic) == "" {
				out = append(out, budgetViolation("commandLogDrain.runtime.kafkaEventTopic", runtime.KafkaEventTopic, "non-empty",
					"native durable command-log staging must record the Kafka event topic"))
			}
			if runtime.KafkaEventPartitions <= 0 {
				out = append(out, budgetViolation("commandLogDrain.runtime.kafkaEventPartitions", runtime.KafkaEventPartitions, "positive",
					"native durable command-log staging must record Kafka event partitions"))
			}
			if strings.TrimSpace(runtime.KafkaCommandTopic) != "" && strings.TrimSpace(runtime.KafkaCommandTopic) == strings.TrimSpace(runtime.KafkaEventTopic) {
				out = append(out, budgetViolation("commandLogDrain.runtime.distinctKafkaTopics", runtime.KafkaEventTopic, "distinct from kafkaCommandTopic",
					"native durable command-log staging requires distinct Kafka command and event topics"))
			}
		}
	}
	return out
}

func normalizeCommandLogDrainBackend(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "jetstream":
		return "nats"
	case "redpanda":
		return "kafka"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func normalizeCommandLogDrainScalarAllocator(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
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
		return strings.ToLower(strings.TrimSpace(raw))
	}
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

func budgetViolation(path string, value, limit any, message string) ScaleBudgetViolation {
	return ScaleBudgetViolation{
		Path:    path,
		Value:   value,
		Limit:   limit,
		Message: message,
	}
}
