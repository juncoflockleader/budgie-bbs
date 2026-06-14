package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

func TestCommandLogReportCheckPassesDurableReport(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	reportPath := writeCommandLogReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "satisfies commandLogDrain budget") {
		t.Fatalf("stdout = %q, want success message", stdout.String())
	}
}

func TestCommandLogReportCheckFailsNonDurableReport(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	report.Runtime.DurableStaging = false
	report.Runtime.MaterializationStore = "sqlite"
	reportPath := writeCommandLogReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "commandLogDrain.requireDurableStaging") {
		t.Fatalf("stderr = %q, want durable-staging violation", stderr.String())
	}
}

func TestCommandLogReportCheckFailsInconsistentDurableRuntime(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	report.Runtime.CommandLogBackend = "memory"
	report.Runtime.CommandNATSStream = ""
	reportPath := writeCommandLogReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "commandLogDrain.runtime.commandLogBackend") {
		t.Fatalf("stderr = %q, want command backend violation", stderr.String())
	}
}

func TestCommandLogReportCheckFailsMissingReportEvidence(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence = core.CommandLogDrainLoadEvidence{}
	reportPath := writeCommandLogReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "commandLogDrain.evidence.gitRevision") {
		t.Fatalf("stderr = %q, want evidence violation", stderr.String())
	}
}

func TestCommandLogReportCheckFailsWrongReportBudgetEvidence(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence.BudgetFile = "ops/local-relaxed-budget.json"
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	reportPath := writeCommandLogReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "commandLogDrain.evidence.budgetFile") {
		t.Fatalf("stderr = %q, want budget-file evidence violation", stderr.String())
	}
}

func TestCommandLogReportCheckFailsWrongReportBudgetHashEvidence(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence.BudgetSHA256 = strings.Repeat("0", 64)
	reportPath := writeCommandLogReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "commandLogDrain.evidence.budgetSha256") {
		t.Fatalf("stderr = %q, want budget hash evidence violation", stderr.String())
	}
}

func TestCommandLogReportCheckFailsWrongGateConfig(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	report.Config.AuthoritativeSubmit = false
	report.Config.AssignmentMode = core.CommandLogDrainAssignmentHash
	report.Runtime.RequirePostgres = false
	report.Runtime.PostgresSchema = "public"
	report.Runtime.KeepPostgresSchema = true
	report.Runtime.NATSEndpoint = "nats://user:secret@nats.internal:4222?token=secret"
	report.Runtime.PostgresEndpoint = "postgres://user:secret@postgres.internal/budgie?sslmode=require"
	report.Runtime.ScalarCompatibilityAllocator = core.CommandLogDrainScalarAllocatorSQLEventOffsets
	report.Runtime.CommandNATSStream = "BUDGIE_COMMAND_LOG"
	report.Runtime.EventNATSStream = "BUDGIE_EVENT_LOG"
	report.Runtime.CommandNATSReplicas = 0
	report.Runtime.EventNATSReplicas = 0
	reportPath := writeCommandLogReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	for _, want := range []string{
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
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %s", stderr.String(), want)
		}
	}
}

func TestCommandLogReportCheckFailsTinyGateLoad(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	report.Config.Boards = 1
	report.Config.CommandsPerBoard = 1
	report.Config.Writers = 1
	report.Config.BatchSize = 1
	report.TotalCommands = 3
	reportPath := writeCommandLogReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	for _, want := range []string{
		"commandLogDrain.config.boards",
		"commandLogDrain.config.commandsPerBoard",
		"commandLogDrain.config.writers",
		"commandLogDrain.config.batchSize",
		"commandLogDrain.totalCommands",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %s", stderr.String(), want)
		}
	}
}

func TestCommandLogReportCheckFailsWrongNativeEventProjectionCount(t *testing.T) {
	budgetPath := writeCommandLogReportCheckBudget(t)
	report := passingCommandLogReport()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	report.EventProjection.ExpectedEvents = 2400
	report.EventProjection.AppliedEvents = 2400
	reportPath := writeCommandLogReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	for _, want := range []string{
		"commandLogDrain.eventProjection.expectedEvents",
		"commandLogDrain.eventProjection.appliedEvents",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %s", stderr.String(), want)
		}
	}
}

func TestCommandLogReportCheckRemoteBudgetRejectsLocalEndpoints(t *testing.T) {
	budgetPath := filepath.Join("..", "..", "ops", "internet-scale-remote-staging-budgets.example.json")
	report := passingCommandLogReport()
	report.Evidence.BudgetFile = "ops/internet-scale-remote-staging-budgets.example.json"
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	report.Runtime.NATSEndpoint = "nats://127.0.0.1:4222"
	report.Runtime.PostgresEndpoint = "postgres://localhost:55432/budgie_staging"
	reportPath := writeCommandLogReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	for _, want := range []string{
		"commandLogDrain.runtime.natsEndpointLocal",
		"commandLogDrain.runtime.postgresEndpointLocal",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %s", stderr.String(), want)
		}
	}

	report.Runtime.NATSEndpoint = "nats://nats.internal:4222"
	report.Runtime.PostgresEndpoint = "postgres://postgres.internal:5432/budgie_staging"
	reportPath = writeCommandLogReportCheckJSON(t, "remote-report.json", report)
	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
}

func TestCommandLogReportCheckRejectsUnknownReportFields(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(reportPath, []byte(`{"config":{},"unexpected":true}`), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	budgetPath := writeCommandLogReportCheckBudget(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), `unknown field "unexpected"`) {
		t.Fatalf("stderr = %q, want unknown-field error", stderr.String())
	}
}

func passingCommandLogReport() core.CommandLogDrainLoadReport {
	return core.CommandLogDrainLoadReport{
		Config: core.CommandLogDrainLoadConfig{
			ExecutorMode:        core.CommandLogDrainExecutorNative,
			AssignmentMode:      core.CommandLogDrainAssignmentSnapshot,
			AuthoritativeSubmit: true,
			Boards:              8,
			CommandsPerBoard:    100,
			RepliesPerThread:    2,
			DirectedReplies:     true,
			Writers:             8,
			BatchSize:           25,
		},
		Runtime: core.CommandLogDrainLoadRuntime{
			CommandLogBackend:            "nats",
			EventLogBackend:              "nats",
			MaterializationStore:         "postgres",
			ScalarCompatibilityAllocator: core.CommandLogDrainScalarAllocatorBrokerStreamSequence,
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
		Evidence: core.CommandLogDrainLoadEvidence{
			Tool:         "budgie-commandlog-loadgen",
			BudgetFile:   "ops/internet-scale-budgets.example.json",
			BudgetSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			GitRevision:  "0123456789abcdef",
		},
		TotalCommands: 2400,
		Submit: core.CommandLogLoadStage{
			Commands:       2400,
			Succeeded:      2400,
			CommandsPerSec: 150,
		},
		Drain: core.CommandLogDrainStage{
			Commands:       2400,
			Processed:      2400,
			Applied:        2400,
			DurationMS:     500,
			CommandsPerSec: 125,
		},
		EventProjection: core.EventStoreProjectionLoadStage{
			Enabled:        true,
			ExpectedEvents: 3200,
			AppliedEvents:  3200,
			DurationMS:     400,
			EventsPerSec:   200,
		},
		MaxPartitionLagAfterDrain: 0,
		PromotionReadiness: core.CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: core.CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
}

func writeCommandLogReportCheckBudget(t *testing.T) string {
	t.Helper()
	return writeCommandLogReportCheckJSON(t, "budget.json", core.ScaleBudgets{
		CommandLogDrain: &core.CommandLogDrainBudget{
			RequireDurableStaging:                true,
			RequireReportEvidence:                true,
			RequiredReportBudgetFile:             "ops/internet-scale-budgets.example.json",
			RequirePostgresFlag:                  true,
			RequireRuntimeEndpoints:              true,
			RequireDisposablePostgresSchema:      true,
			RequiredPostgresSchemaPrefix:         "budgie_cmdlog_load_",
			RequiredCommandNATSStreamPrefix:      "BUDGIE_COMMAND_LOG_LOAD_",
			RequiredEventNATSStreamPrefix:        "BUDGIE_EVENT_LOG_LOAD_",
			RequiredScalarCompatibilityAllocator: core.CommandLogDrainScalarAllocatorBrokerStreamSequence,
			MinCommandNATSReplicas:               1,
			MinEventNATSReplicas:                 1,
			RequiredExecutorMode:                 core.CommandLogDrainExecutorNative,
			RequiredAssignmentMode:               core.CommandLogDrainAssignmentSnapshot,
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

func writeCommandLogReportCheckJSON(t *testing.T, name string, value any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
