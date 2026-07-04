package loadtest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

type GatewayFanoutLoadConfig struct {
	HotSubscribers    int    `json:"hotSubscribers"`
	IdleSubscribers   int    `json:"idleSubscribers"`
	BufferSize        int    `json:"bufferSize"`
	Events            int    `json:"events"`
	HotScope          string `json:"hotScope"`
	IdleScopePrefix   string `json:"idleScopePrefix"`
	TargetConnections int    `json:"targetConnections,omitempty"`
}

type GatewayFanoutLoadReport struct {
	Config                      GatewayFanoutLoadConfig `json:"config"`
	Evidence                    runevidence.Evidence    `json:"evidence"`
	StartedAt                   int64                   `json:"startedAt"`
	FinishedAt                  int64                   `json:"finishedAt"`
	Subscribers                 int                     `json:"subscribers"`
	PublishDurationMS           float64                 `json:"publishDurationMs"`
	AttemptedDeliveries         int                     `json:"attemptedDeliveries"`
	QueuedDeliveries            int                     `json:"queuedDeliveries"`
	EstimatedDrops              int                     `json:"estimatedDrops"`
	QueueDepthTotal             int                     `json:"queueDepthTotal"`
	QueueDepthMax               int                     `json:"queueDepthMax"`
	QueueCapacityTotal          int                     `json:"queueCapacityTotal"`
	QueueCapacityMax            int                     `json:"queueCapacityMax"`
	HotScopeSubscribers         int                     `json:"hotScopeSubscribers"`
	HotScopeQueueDepth          int                     `json:"hotScopeQueueDepth"`
	IdleSampleChecked           int                     `json:"idleSampleChecked"`
	IdleSampleDelivered         int                     `json:"idleSampleDelivered"`
	TargetConnections           int                     `json:"targetConnections,omitempty"`
	GatewayNodesForTarget       int                     `json:"gatewayNodesForTarget,omitempty"`
	ProjectedConnectionCapacity int                     `json:"projectedConnectionCapacity,omitempty"`
}

func DefaultGatewayFanoutLoadConfig() GatewayFanoutLoadConfig {
	return GatewayFanoutLoadConfig{
		HotSubscribers:  128,
		IdleSubscribers: 2500,
		BufferSize:      2,
		Events:          1,
		HotScope:        "board:hot",
		IdleScopePrefix: "board:idle",
	}
}

func RunGatewayFanoutLoad(ctx context.Context, config GatewayFanoutLoadConfig) (GatewayFanoutLoadReport, error) {
	config = normalizeGatewayFanoutLoadConfig(config)
	report := GatewayFanoutLoadReport{
		Config:    config,
		StartedAt: time.Now().UnixMilli(),
	}
	bus := core.NewMemBusWithBuffer(config.BufferSize)
	hotSubs := make([]*core.Subscription, 0, config.HotSubscribers)
	idleSubs := make([]*core.Subscription, 0, config.IdleSubscribers)
	defer func() {
		for _, subs := range [][]*core.Subscription{hotSubs, idleSubs} {
			for _, sub := range subs {
				bus.Unsubscribe(sub)
			}
		}
	}()

	for i := 0; i < config.HotSubscribers; i++ {
		hotSubs = append(hotSubs, bus.Subscribe([]string{config.HotScope}))
	}
	for i := 0; i < config.IdleSubscribers; i++ {
		idleSubs = append(idleSubs, bus.Subscribe([]string{fmt.Sprintf("%s:%d", config.IdleScopePrefix, i)}))
	}

	before := bus.QueueStats()
	if before.Subscribers != config.HotSubscribers+config.IdleSubscribers {
		return report, fmt.Errorf("gateway fanout load: subscribers = %d, want %d", before.Subscribers, config.HotSubscribers+config.IdleSubscribers)
	}
	if before.QueueDepthTotal != 0 {
		return report, fmt.Errorf("gateway fanout load: initial queue depth = %d, want 0", before.QueueDepthTotal)
	}

	start := time.Now()
	for i := 0; i < config.Events; i++ {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}
		bus.PublishLocal(&proto.Event{
			Kind:    proto.EvtChatLine,
			Seq:     int64(i + 1),
			Scopes:  []string{config.HotScope},
			Payload: &proto.ChatLinePayload{Room: "hot", User: "load", Text: "fanout", TS: int64(i + 1)},
			TS:      int64(i + 1),
		})
	}
	publishDuration := time.Since(start)
	after := bus.QueueStats()

	report.FinishedAt = time.Now().UnixMilli()
	report.Subscribers = after.Subscribers
	report.PublishDurationMS = float64(publishDuration.Microseconds()) / 1000
	report.AttemptedDeliveries = config.HotSubscribers * config.Events
	report.QueueDepthTotal = after.QueueDepthTotal
	report.QueueDepthMax = after.QueueDepthMax
	report.QueueCapacityTotal = after.QueueCapacityTotal
	report.QueueCapacityMax = after.QueueCapacityMax
	report.QueuedDeliveries = max(after.QueueDepthTotal-before.QueueDepthTotal, 0)
	report.TargetConnections = config.TargetConnections
	report.GatewayNodesForTarget = gatewayFanoutNodesForTarget(report.Subscribers, config.TargetConnections)
	if report.GatewayNodesForTarget > 0 {
		report.ProjectedConnectionCapacity = report.GatewayNodesForTarget * report.Subscribers
	}
	report.EstimatedDrops = max(report.AttemptedDeliveries-report.QueuedDeliveries, 0)
	for _, scope := range after.Scopes {
		if scope.Scope == config.HotScope {
			report.HotScopeSubscribers = scope.Subscribers
			report.HotScopeQueueDepth = scope.QueueDepthTotal
			break
		}
	}

	report.IdleSampleChecked = min(len(idleSubs), 64)
	for i := 0; i < report.IdleSampleChecked; i++ {
		select {
		case <-idleSubs[i].Ch:
			report.IdleSampleDelivered++
		default:
		}
	}
	if report.IdleSampleDelivered != 0 {
		return report, fmt.Errorf("gateway fanout load: %d/%d sampled idle subscribers received unrelated events", report.IdleSampleDelivered, report.IdleSampleChecked)
	}
	return report, nil
}

func EvaluateGatewayFanoutBudget(report GatewayFanoutLoadReport, budget *scalebudget.GatewayFanoutBudget) []scalebudget.ScaleBudgetViolation {
	if budget == nil {
		return nil
	}
	var out []scalebudget.ScaleBudgetViolation
	if strings.TrimSpace(budget.RequiredReportBudgetFile) != "" {
		out = append(out, evaluateGatewayFanoutReportEvidence(report, budget)...)
	}
	out = scalebudget.AddPositiveMaxViolation(out, "gatewayFanout.maxPublishMs", report.PublishDurationMS, budget.MaxPublishMS,
		"hot-scope publish duration is above budget")
	out = scalebudget.AddMinViolation(out, "gatewayFanout.minEstimatedDrops", report.EstimatedDrops, budget.MinEstimatedDrops,
		"estimated slow-client drops are below budget")
	out = scalebudget.AddPositiveMaxViolation(out, "gatewayFanout.maxEstimatedDrops", report.EstimatedDrops, budget.MaxEstimatedDrops,
		"estimated slow-client drops are above budget")
	out = scalebudget.AddZeroOrPositiveMaxIntViolation(out, "gatewayFanout.maxIdleDeliveries", report.IdleSampleDelivered, budget.MaxIdleDeliveries,
		"idle subscribers received unrelated events", "idle subscribers received unrelated events")
	out = scalebudget.AddPositiveMaxViolation(out, "gatewayFanout.maxQueueDepthMax", report.QueueDepthMax, budget.MaxQueueDepthMax,
		"maximum queue depth is above budget")
	out = scalebudget.AddMinViolation(out, "gatewayFanout.minQueuedDeliveries", report.QueuedDeliveries, budget.MinQueuedDeliveries,
		"queued hot-scope deliveries are below budget")
	out = scalebudget.AddMinViolation(out, "gatewayFanout.minSubscribers", report.Subscribers, budget.MinSubscribers,
		"per-gateway subscriber capacity is below budget")
	out = scalebudget.AddMinViolation(out, "gatewayFanout.minHotScopeSubscribers", report.HotScopeSubscribers, budget.MinHotScopeSubscribers,
		"hot-scope subscriber capacity is below budget")
	targetConnections := report.TargetConnections
	if budget.TargetConnections > 0 {
		out = scalebudget.AddMinViolation(out, "gatewayFanout.targetConnections", targetConnections, budget.TargetConnections,
			"gateway fanout target connection count is below budget")
		targetConnections = budget.TargetConnections
	}
	if budget.MaxGatewayNodesForTarget > 0 && targetConnections > 0 {
		nodes := gatewayFanoutNodesForTarget(report.Subscribers, targetConnections)
		if nodes == 0 || nodes > budget.MaxGatewayNodesForTarget {
			out = append(out, scalebudget.NewViolation("gatewayFanout.maxGatewayNodesForTarget", nodes, budget.MaxGatewayNodesForTarget,
				"projected gateway node count for the target is above budget"))
		}
	}
	return out
}

func normalizeGatewayFanoutLoadConfig(config GatewayFanoutLoadConfig) GatewayFanoutLoadConfig {
	def := DefaultGatewayFanoutLoadConfig()
	config.HotSubscribers = positiveOrDefault(config.HotSubscribers, def.HotSubscribers)
	config.IdleSubscribers = nonNegativeOrDefault(config.IdleSubscribers, def.IdleSubscribers)
	config.BufferSize = positiveOrDefault(config.BufferSize, def.BufferSize)
	config.Events = positiveOrDefault(config.Events, def.Events)
	if config.HotScope == "" {
		config.HotScope = def.HotScope
	}
	if config.IdleScopePrefix == "" {
		config.IdleScopePrefix = def.IdleScopePrefix
	}
	config.TargetConnections = nonNegativeOrDefault(config.TargetConnections, def.TargetConnections)
	return config
}

func evaluateGatewayFanoutReportEvidence(report GatewayFanoutLoadReport, budget *scalebudget.GatewayFanoutBudget) []scalebudget.ScaleBudgetViolation {
	return scalebudget.ReportEvidenceViolations("gatewayFanout.evidence.", runevidence.ValidateReportEvidence(report.Evidence, runevidence.ReportEvidencePolicy{
		Tool:               "budgie-gateway-loadgen",
		RequiredBudgetFile: budget.RequiredReportBudgetFile,
		ReportName:         "gateway fanout report",
	}))
}

func gatewayFanoutNodesForTarget(subscribers, targetConnections int) int {
	if subscribers <= 0 || targetConnections <= 0 {
		return 0
	}
	return (targetConnections + subscribers - 1) / subscribers
}
