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

func TestGatewayReportCheckPassesPromotedReport(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	reportPath := writeGatewayReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "satisfies gatewayFanout budget") {
		t.Fatalf("stdout = %q, want success message", stdout.String())
	}
}

func TestGatewayReportCheckPassesReportFromStdin(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", "-",
		"-budget-file", budgetPath,
	}, bytes.NewReader(data), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
}

func TestGatewayReportCheckFailsMissingEvidence(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.Evidence = core.GatewayFanoutLoadEvidence{}
	reportPath := writeGatewayReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "gatewayFanout.evidence.gitRevision") {
		t.Fatalf("stderr = %q, want evidence violation", stderr.String())
	}
}

func TestGatewayReportCheckFailsWrongBudgetEvidence(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.Evidence.BudgetFile = "ops/other-budget.json"
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	reportPath := writeGatewayReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "gatewayFanout.evidence.budgetFile") {
		t.Fatalf("stderr = %q, want budget-file evidence violation", stderr.String())
	}
}

func TestGatewayReportCheckFailsWrongBudgetHashEvidence(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.Evidence.BudgetSHA256 = strings.Repeat("0", 64)
	reportPath := writeGatewayReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "gatewayFanout.evidence.budgetSha256") {
		t.Fatalf("stderr = %q, want budget hash evidence violation", stderr.String())
	}
}

func TestGatewayReportCheckFailsUnderBudgetFanout(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	report.PublishDurationMS = 125
	report.QueueDepthMax = 3
	report.QueuedDeliveries = 9999
	report.IdleSampleDelivered = 1
	report.Subscribers = 99999
	report.HotScopeSubscribers = 9999
	report.TargetConnections = 999999
	reportPath := writeGatewayReportCheckJSON(t, "report.json", report)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	for _, want := range []string{
		"gatewayFanout.maxPublishMs",
		"gatewayFanout.maxIdleDeliveries",
		"gatewayFanout.maxQueueDepthMax",
		"gatewayFanout.minQueuedDeliveries",
		"gatewayFanout.minSubscribers",
		"gatewayFanout.minHotScopeSubscribers",
		"gatewayFanout.targetConnections",
		"gatewayFanout.maxGatewayNodesForTarget",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %s", stderr.String(), want)
		}
	}
}

func TestGatewayReportCheckRejectsUnknownReportFields(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(reportPath, []byte(`{"config":{},"unexpected":true}`), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	budgetPath := writeGatewayReportCheckBudget(t)

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

func passingGatewayReport() core.GatewayFanoutLoadReport {
	return core.GatewayFanoutLoadReport{
		Config: core.GatewayFanoutLoadConfig{
			HotSubscribers:    10000,
			IdleSubscribers:   90000,
			BufferSize:        2,
			Events:            1,
			HotScope:          "board:hot",
			IdleScopePrefix:   "board:idle",
			TargetConnections: 1000000,
		},
		Evidence: core.GatewayFanoutLoadEvidence{
			Tool:         "budgie-gateway-loadgen",
			BudgetFile:   "ops/internet-scale-budgets.example.json",
			BudgetSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			GitRevision:  "0123456789abcdef",
		},
		PublishDurationMS:           45,
		Subscribers:                 100000,
		AttemptedDeliveries:         10000,
		QueuedDeliveries:            10000,
		EstimatedDrops:              0,
		QueueDepthTotal:             10000,
		QueueDepthMax:               1,
		QueueCapacityTotal:          200000,
		QueueCapacityMax:            2,
		HotScopeSubscribers:         10000,
		HotScopeQueueDepth:          10000,
		IdleSampleChecked:           64,
		IdleSampleDelivered:         0,
		TargetConnections:           1000000,
		GatewayNodesForTarget:       10,
		ProjectedConnectionCapacity: 1000000,
	}
}

func writeGatewayReportCheckBudget(t *testing.T) string {
	t.Helper()
	return writeGatewayReportCheckJSON(t, "budget.json", core.ScaleBudgets{
		GatewayFanout: &core.GatewayFanoutBudget{
			MaxPublishMS:             100,
			MaxEstimatedDrops:        20000,
			MaxIdleDeliveries:        0,
			MaxQueueDepthMax:         2,
			MinQueuedDeliveries:      10000,
			MinSubscribers:           100000,
			MinHotScopeSubscribers:   10000,
			TargetConnections:        1000000,
			MaxGatewayNodesForTarget: 10,
			RequiredReportBudgetFile: "ops/internet-scale-budgets.example.json",
		},
	})
}

func writeGatewayReportCheckJSON(t *testing.T, name string, value any) string {
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
