package loadtest

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

const (
	defaultGatewayLoadHotSubscribers  = 128
	defaultGatewayLoadIdleSubscribers = 2500
	defaultGatewayLoadBufferSize      = 2
)

func TestGatewayFanoutManyIdleSubscribers(t *testing.T) {
	hotCount := runconfig.EnvInt("BUDGIE_GATEWAY_LOAD_HOT_SUBSCRIBERS", defaultGatewayLoadHotSubscribers)
	idleCount := runconfig.EnvInt("BUDGIE_GATEWAY_LOAD_IDLE_SUBSCRIBERS", defaultGatewayLoadIdleSubscribers)
	bufferSize := runconfig.EnvInt("BUDGIE_GATEWAY_LOAD_BUFFER_SIZE", defaultGatewayLoadBufferSize)
	if hotCount <= 0 {
		t.Fatalf("hot subscriber count must be positive, got %d", hotCount)
	}
	if idleCount < 0 {
		t.Fatalf("idle subscriber count must be non-negative, got %d", idleCount)
	}

	report := runGatewayFanoutLoad(t, GatewayFanoutLoadConfig{
		HotSubscribers:  hotCount,
		IdleSubscribers: idleCount,
		BufferSize:      bufferSize,
		Events:          1,
	})
	requireInt(t, "subscribers", report.Subscribers, hotCount+idleCount)
	requireInt(t, "queue depth total", report.QueueDepthTotal, hotCount)
	requireInt(t, "queue depth max", report.QueueDepthMax, 1)
	if report.HotScopeSubscribers != hotCount || report.HotScopeQueueDepth != hotCount {
		t.Fatalf("hot scope report = subscribers %d depth %d, want %d", report.HotScopeSubscribers, report.HotScopeQueueDepth, hotCount)
	}
	requireInt(t, "idle sample delivered", report.IdleSampleDelivered, 0)
	t.Logf("gateway fanout fixture: hot=%d idle=%d buffer=%d publish=%.3fms",
		hotCount, idleCount, bufferSize, report.PublishDurationMS)
}

func TestGatewayFanoutLoadReportsSlowClientDrops(t *testing.T) {
	report := runGatewayFanoutLoad(t, GatewayFanoutLoadConfig{
		HotSubscribers:  8,
		IdleSubscribers: 32,
		BufferSize:      2,
		Events:          3,
	})
	requireInt(t, "queued deliveries", report.QueuedDeliveries, 16)
	requireInt(t, "estimated drops", report.EstimatedDrops, 8)
	requireInt(t, "queue depth max", report.QueueDepthMax, 2)
}

func TestGatewayFanoutLoadReportsTargetGatewayCount(t *testing.T) {
	report := runGatewayFanoutLoad(t, GatewayFanoutLoadConfig{
		HotSubscribers:    8,
		IdleSubscribers:   32,
		BufferSize:        2,
		Events:            1,
		TargetConnections: 100,
	})
	requireInt(t, "subscribers", report.Subscribers, 40)
	requireInt(t, "target connections", report.TargetConnections, 100)
	requireInt(t, "gateway nodes for target", report.GatewayNodesForTarget, 3)
	requireInt(t, "projected connection capacity", report.ProjectedConnectionCapacity, 120)
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
	budget := &scalebudget.GatewayFanoutBudget{
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
	requireScaleBudgetPath(t, violations, "gatewayFanout.maxEstimatedDrops")
}

func runGatewayFanoutLoad(t *testing.T, config GatewayFanoutLoadConfig) GatewayFanoutLoadReport {
	t.Helper()
	report, err := RunGatewayFanoutLoad(context.Background(), config)
	if err != nil {
		t.Fatalf("RunGatewayFanoutLoad: %v", err)
	}
	return report
}

func requireInt(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %d, want %d", name, got, want)
	}
}

func validGatewayFanoutReportEvidence() runevidence.Evidence {
	return runevidence.Evidence{
		Tool:         "budgie-gateway-loadgen",
		BudgetFile:   "ops/internet-scale-budgets.example.json",
		BudgetSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		GitRevision:  "0123456789abcdef",
	}
}

func requireScaleBudgetPaths(t *testing.T, violations []scalebudget.ScaleBudgetViolation, paths ...string) {
	t.Helper()
	if len(violations) != len(paths) {
		t.Fatalf("violations = %+v, want %d", violations, len(paths))
	}
	got := scaleBudgetPathSet(violations)
	for _, path := range paths {
		if !got[path] {
			t.Fatalf("violations = %+v, missing %s", violations, path)
		}
	}
}

func requireScaleBudgetPath(t *testing.T, violations []scalebudget.ScaleBudgetViolation, path string) {
	t.Helper()
	if !scaleBudgetPathSet(violations)[path] {
		t.Fatalf("violations = %+v, missing %s", violations, path)
	}
}

func scaleBudgetPathSet(violations []scalebudget.ScaleBudgetViolation) map[string]bool {
	got := make(map[string]bool, len(violations))
	for _, violation := range violations {
		got[violation.Path] = true
	}
	return got
}
