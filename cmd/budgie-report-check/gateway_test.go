package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/loadtest"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

func TestGatewayReportCheckPassesPromotedReport(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	reportPath := writeGatewayReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runGateway, reportPath, budgetPath)
	requireReportCheckExit(t, result, 0)
	requireReportCheckOutputContains(t, "stdout", result.Stdout, "satisfies gatewayFanout budget")
}

func TestGatewayReportCheckPassesReportFromStdin(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}

	result := runReportCheckForTest(t, runGateway, []string{
		"-report-file", "-",
		"-budget-file", budgetPath,
	}, bytes.NewReader(data))
	requireReportCheckExit(t, result, 0)
}

func TestGatewayReportCheckFailsMissingEvidence(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.Evidence = runevidence.Evidence{}
	reportPath := writeReportCheckJSON(t, "report.json", report)

	result := runBudgetReportCheckForTest(t, runGateway, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "gatewayFanout.evidence.gitRevision")
}

func TestGatewayReportCheckFailsWrongBudgetEvidence(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.Evidence.BudgetFile = "ops/other-budget.json"
	reportPath := writeGatewayReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runGateway, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "gatewayFanout.evidence.budgetFile")
}

func TestGatewayReportCheckFailsWrongBudgetHashEvidence(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.Evidence.BudgetSHA256 = strings.Repeat("0", 64)
	reportPath := writeReportCheckJSON(t, "report.json", report)

	result := runBudgetReportCheckForTest(t, runGateway, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "gatewayFanout.evidence.budgetSha256")
}

func TestGatewayReportCheckFailsUnderBudgetFanout(t *testing.T) {
	budgetPath := writeGatewayReportCheckBudget(t)
	report := passingGatewayReport()
	report.PublishDurationMS = 125
	report.QueueDepthMax = 3
	report.QueuedDeliveries = 9999
	report.IdleSampleDelivered = 1
	report.Subscribers = 99999
	report.HotScopeSubscribers = 9999
	report.TargetConnections = 999999
	reportPath := writeGatewayReportWithBudgetHash(t, "report.json", budgetPath, report)

	result := runBudgetReportCheckForTest(t, runGateway, reportPath, budgetPath)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr,
		"gatewayFanout.maxPublishMs",
		"gatewayFanout.maxIdleDeliveries",
		"gatewayFanout.maxQueueDepthMax",
		"gatewayFanout.minQueuedDeliveries",
		"gatewayFanout.minSubscribers",
		"gatewayFanout.minHotScopeSubscribers",
		"gatewayFanout.targetConnections",
		"gatewayFanout.maxGatewayNodesForTarget",
	)
}

func TestGatewayReportCheckRejectsUnknownReportFields(t *testing.T) {
	reportPath := writeReportCheckRaw(t, "report.json", []byte(`{"config":{},"unexpected":true}`))
	budgetPath := writeGatewayReportCheckBudget(t)

	result := runBudgetReportCheckForTest(t, runGateway, reportPath, budgetPath)
	requireReportCheckExit(t, result, 2)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, `unknown field "unexpected"`)
}

func passingGatewayReport() loadtest.GatewayFanoutLoadReport {
	return loadtest.GatewayFanoutLoadReport{
		Config: loadtest.GatewayFanoutLoadConfig{
			HotSubscribers:    10000,
			IdleSubscribers:   90000,
			BufferSize:        2,
			Events:            1,
			HotScope:          "board:hot",
			IdleScopePrefix:   "board:idle",
			TargetConnections: 1000000,
		},
		Evidence: runevidence.Evidence{
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
	return writeReportCheckJSON(t, "budget.json", scalebudget.ScaleBudgets{
		GatewayFanout: &scalebudget.GatewayFanoutBudget{
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

func writeGatewayReportWithBudgetHash(t *testing.T, name, budgetPath string, report loadtest.GatewayFanoutLoadReport) string {
	t.Helper()
	report.Evidence.BudgetSHA256 = fileSHA256(t, budgetPath)
	return writeReportCheckJSON(t, name, report)
}
