package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

func TestCommandLogReportCheckPassesDurableReport(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	reportPath := writeCommandLogReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 0)
	requireReportCheckOutputContains(t, "stdout", result.Stdout, "satisfies commandLogDrain budget")
}

func TestCommandLogReportCheckFailsNonDurableReport(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Runtime.DurableStaging = false
	report.Runtime.MaterializationStore = "sqlite"
	reportPath := writeCommandLogReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "commandLogDrain.requireDurableStaging")
}

func TestCommandLogReportCheckFailsInconsistentDurableRuntime(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Runtime.CommandLogBackend = "memory"
	report.Runtime.CommandNATSStream = ""
	reportPath := writeCommandLogReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "commandLogDrain.runtime.commandLogBackend")
}

func TestCommandLogReportCheckFailsMissingReportEvidence(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence = runevidence.Evidence{}
	reportPath := writeReportCheckJSON(t, "report.json", report)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "commandLogDrain.evidence.gitRevision")
}

func TestCommandLogReportCheckFailsWrongReportBudgetEvidence(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence.BudgetFile = "ops/local-relaxed-budget.json"
	reportPath := writeCommandLogReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "commandLogDrain.evidence.budgetFile")
}

func TestCommandLogReportCheckFailsWrongReportBudgetHashEvidence(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence.BudgetSHA256 = strings.Repeat("0", 64)
	reportPath := writeReportCheckJSON(t, "report.json", report)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "commandLogDrain.evidence.budgetSha256")
}

func TestCommandLogReportCheckFailsWrongGateConfig(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Config.AuthoritativeSubmit = false
	report.Config.AssignmentMode = loadmodel.CommandLogDrainAssignmentHash
	report.Runtime.RequirePostgres = false
	report.Runtime.PostgresSchema = "public"
	report.Runtime.KeepPostgresSchema = true
	report.Runtime.NATSEndpoint = "nats://user:secret@nats.internal:4222?token=secret"
	report.Runtime.PostgresEndpoint = "postgres://user:secret@postgres.internal/budgie?sslmode=require"
	report.Runtime.ScalarCompatibilityAllocator = loadmodel.CommandLogDrainScalarAllocatorSQLEventOffsets
	report.Runtime.CommandNATSStream = "BUDGIE_COMMAND_LOG"
	report.Runtime.EventNATSStream = "BUDGIE_EVENT_LOG"
	report.Runtime.CommandNATSReplicas = 0
	report.Runtime.EventNATSReplicas = 0
	reportPath := writeCommandLogReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr,
		"commandLogDrain.runtime.requirePostgres",
		"commandLogDrain.runtime.natsEndpoint",
		"commandLogDrain.runtime.postgresEndpoint",
		"commandLogDrain.runtime.scalarCompatibilityAllocator",
		"commandLogDrain.runtime.keepPostgresSchema",
		"commandLogDrain.runtime.postgresSchemaPrefix",
		"commandLogDrain.runtime.commandNatsStreamPrefix",
		"commandLogDrain.runtime.eventNatsStreamPrefix",
		"commandLogDrain.runtime.commandNatsReplicas",
		"commandLogDrain.runtime.eventNatsReplicas",
		"commandLogDrain.config.assignmentMode",
		"commandLogDrain.config.authoritativeSubmit",
	)
}

func TestCommandLogReportCheckFailsTinyGateLoad(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Config.Boards = 1
	report.Config.CommandsPerBoard = 1
	report.Config.Writers = 1
	report.Config.BatchSize = 1
	report.TotalCommands = 3
	reportPath := writeCommandLogReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr,
		"commandLogDrain.config.boards",
		"commandLogDrain.config.commandsPerBoard",
		"commandLogDrain.config.writers",
		"commandLogDrain.config.batchSize",
		"commandLogDrain.totalCommands",
	)
}

func TestCommandLogReportCheckFailsWrongNativeEventProjectionCount(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.EventProjection.ExpectedEvents = 2400
	report.EventProjection.AppliedEvents = 2400
	reportPath := writeCommandLogReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr,
		"commandLogDrain.eventProjection.expectedEvents",
		"commandLogDrain.eventProjection.appliedEvents",
	)
}

func TestCommandLogReportCheckRemoteBudgetRejectsLocalEndpoints(t *testing.T) {
	budgetPath := filepath.Join("..", "..", "ops", "internet-scale-remote-staging-budgets.example.json")
	report := passingCommandLogReport()
	report.Evidence.BudgetFile = "ops/internet-scale-remote-staging-budgets.example.json"
	report.Runtime.NATSEndpoint = "nats://127.0.0.1:4222"
	report.Runtime.PostgresEndpoint = "postgres://localhost:55432/budgie_staging"
	reportPath := writeCommandLogReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr,
		"commandLogDrain.runtime.natsEndpointLocal",
		"commandLogDrain.runtime.postgresEndpointLocal",
	)

	report.Runtime.NATSEndpoint = "nats://nats.internal:4222"
	report.Runtime.PostgresEndpoint = "postgres://postgres.internal:5432/budgie_staging"
	reportPath = writeCommandLogReportWithBudgetHash(t, "remote-report.json", budgetPath, report)
	result = runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 0)
}

func TestCommandLogReportCheckRejectsUnknownReportFields(t *testing.T) {
	reportPath := writeReportCheckRaw(t, "report.json", []byte(`{"config":{},"unexpected":true}`))
	budgetPath := writeCommandLogReportCheckBudget(t)

	result := runBudgetReportCheckForTest(t, runCommandLog, reportPath, budgetPath)
	requireReportCheckExit(t, result, 2)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, `unknown field "unexpected"`)
}

func passingCommandLogReport() loadmodel.CommandLogDrainLoadReport {
	return loadmodel.CommandLogDrainLoadReport{
		Config: loadmodel.CommandLogDrainLoadConfig{
			ExecutorMode:        loadmodel.CommandLogDrainExecutorNative,
			AssignmentMode:      loadmodel.CommandLogDrainAssignmentSnapshot,
			AuthoritativeSubmit: true,
			Boards:              8,
			CommandsPerBoard:    100,
			RepliesPerThread:    2,
			DirectedReplies:     true,
			Writers:             8,
			BatchSize:           25,
		},
		Runtime: loadmodel.CommandLogDrainLoadRuntime{
			CommandLogBackend:            "nats",
			EventLogBackend:              "nats",
			MaterializationStore:         "postgres",
			ScalarCompatibilityAllocator: loadmodel.CommandLogDrainScalarAllocatorBrokerStreamSequence,
			NATSEndpoint:                 "nats://nats.internal:4222",
			PostgresEndpoint:             "postgres://postgres.internal:5432/budgie",
			RequirePostgres:              true,
			DurableStaging:               true,
			PostgresSchema:               "budgie_cmdlog_load_123",
			KeepPostgresSchema:           false,
			CommandNATSStream:            "BUDGIE_COMMAND_LOG_LOAD_STAGING",
			CommandNATSReplicas:          1,
			EventNATSStream:              "BUDGIE_EVENT_LOG_LOAD_STAGING",
			EventNATSReplicas:            1,
		},
		Evidence: runevidence.Evidence{
			Tool:         "budgie-commandlog-loadgen",
			BudgetFile:   "ops/internet-scale-budgets.example.json",
			BudgetSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			GitRevision:  "0123456789abcdef",
		},
		TotalCommands: 2400,
		Submit: loadmodel.CommandLogLoadStage{
			Commands:       2400,
			Succeeded:      2400,
			CommandsPerSec: 150,
		},
		Drain: loadmodel.CommandLogDrainStage{
			Commands:       2400,
			Processed:      2400,
			Applied:        2400,
			DurationMS:     500,
			CommandsPerSec: 125,
		},
		EventProjection: loadmodel.EventStoreProjectionLoadStage{
			Enabled:        true,
			ExpectedEvents: 3200,
			AppliedEvents:  3200,
			DurationMS:     400,
			EventsPerSec:   200,
		},
		MaxPartitionLagAfterDrain: 0,
		PromotionReadiness: loadmodel.CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: loadmodel.CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
}

func writeCommandLogReportCheckBudget(t *testing.T) string {
	t.Helper()
	return writeReportCheckJSON(t, "budget.json", scalebudget.ScaleBudgets{
		CommandLogDrain: &scalebudget.CommandLogDrainBudget{
			RequireDurableStaging:                true,
			RequireReportEvidence:                true,
			RequiredReportBudgetFile:             "ops/internet-scale-budgets.example.json",
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
			MinSubmitCommandsPerSec:              100,
			MinDrainCommandsPerSec:               100,
			MaxDrainDurationMS:                   1000,
			MaxPartitionLagAfter:                 0,
			RequireNativeExpectedEvents:          true,
			MinEventProjectionEventsPerSec:       100,
			MaxEventProjectionDurationMS:         1000,
			MaxPromotionTotalLag:                 0,
			MaxLaggingPartitions:                 0,
			MaxMissingMaterialization:            0,
			MaxRetryingCommitted:                 0,
			MaxMissingRecords:                    0,
			MaxFailedCommands:                    0,
			MaxCommitFailures:                    0,
			MaxAssignmentLosses:                  0,
			MaxClaimLosses:                       0,
		},
	})
}

func writeCommandLogReportWithBudgetHash(t *testing.T, name, budgetPath string, report loadmodel.CommandLogDrainLoadReport) string {
	t.Helper()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	return writeReportCheckJSON(t, name, report)
}
