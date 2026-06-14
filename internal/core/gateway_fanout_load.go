package core

import (
	"context"
	"fmt"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
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
	Config                      GatewayFanoutLoadConfig   `json:"config"`
	Evidence                    GatewayFanoutLoadEvidence `json:"evidence"`
	StartedAt                   int64                     `json:"startedAt"`
	FinishedAt                  int64                     `json:"finishedAt"`
	Subscribers                 int                       `json:"subscribers"`
	PublishDurationMS           float64                   `json:"publishDurationMs"`
	AttemptedDeliveries         int                       `json:"attemptedDeliveries"`
	QueuedDeliveries            int                       `json:"queuedDeliveries"`
	EstimatedDrops              int                       `json:"estimatedDrops"`
	QueueDepthTotal             int                       `json:"queueDepthTotal"`
	QueueDepthMax               int                       `json:"queueDepthMax"`
	QueueCapacityTotal          int                       `json:"queueCapacityTotal"`
	QueueCapacityMax            int                       `json:"queueCapacityMax"`
	HotScopeSubscribers         int                       `json:"hotScopeSubscribers"`
	HotScopeQueueDepth          int                       `json:"hotScopeQueueDepth"`
	IdleSampleChecked           int                       `json:"idleSampleChecked"`
	IdleSampleDelivered         int                       `json:"idleSampleDelivered"`
	TargetConnections           int                       `json:"targetConnections,omitempty"`
	GatewayNodesForTarget       int                       `json:"gatewayNodesForTarget,omitempty"`
	ProjectedConnectionCapacity int                       `json:"projectedConnectionCapacity,omitempty"`
}

type GatewayFanoutLoadEvidence struct {
	Tool         string `json:"tool,omitempty"`
	BudgetFile   string `json:"budgetFile,omitempty"`
	BudgetSHA256 string `json:"budgetSha256,omitempty"`
	GitRevision  string `json:"gitRevision,omitempty"`
	GitModified  bool   `json:"gitModified"`
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
		StartedAt: nowMS(),
	}
	bus := NewMemBusWithBuffer(config.BufferSize)
	hotSubs := make([]*Subscription, 0, config.HotSubscribers)
	idleSubs := make([]*Subscription, 0, config.IdleSubscribers)
	defer func() {
		for _, sub := range hotSubs {
			bus.Unsubscribe(sub)
		}
		for _, sub := range idleSubs {
			bus.Unsubscribe(sub)
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

	report.FinishedAt = nowMS()
	report.Subscribers = after.Subscribers
	report.PublishDurationMS = float64(publishDuration.Microseconds()) / 1000
	report.AttemptedDeliveries = config.HotSubscribers * config.Events
	report.QueueDepthTotal = after.QueueDepthTotal
	report.QueueDepthMax = after.QueueDepthMax
	report.QueueCapacityTotal = after.QueueCapacityTotal
	report.QueueCapacityMax = after.QueueCapacityMax
	report.QueuedDeliveries = after.QueueDepthTotal - before.QueueDepthTotal
	if report.QueuedDeliveries < 0 {
		report.QueuedDeliveries = 0
	}
	report.TargetConnections = config.TargetConnections
	report.GatewayNodesForTarget = gatewayFanoutNodesForTarget(report.Subscribers, config.TargetConnections)
	if report.GatewayNodesForTarget > 0 {
		report.ProjectedConnectionCapacity = report.GatewayNodesForTarget * report.Subscribers
	}
	report.EstimatedDrops = report.AttemptedDeliveries - report.QueuedDeliveries
	if report.EstimatedDrops < 0 {
		report.EstimatedDrops = 0
	}
	for _, scope := range after.Scopes {
		if scope.Scope == config.HotScope {
			report.HotScopeSubscribers = scope.Subscribers
			report.HotScopeQueueDepth = scope.QueueDepthTotal
			break
		}
	}

	report.IdleSampleChecked = len(idleSubs)
	if report.IdleSampleChecked > 64 {
		report.IdleSampleChecked = 64
	}
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

func normalizeGatewayFanoutLoadConfig(config GatewayFanoutLoadConfig) GatewayFanoutLoadConfig {
	def := DefaultGatewayFanoutLoadConfig()
	if config.HotSubscribers <= 0 {
		config.HotSubscribers = def.HotSubscribers
	}
	if config.IdleSubscribers < 0 {
		config.IdleSubscribers = def.IdleSubscribers
	}
	if config.BufferSize <= 0 {
		config.BufferSize = def.BufferSize
	}
	if config.Events <= 0 {
		config.Events = def.Events
	}
	if config.HotScope == "" {
		config.HotScope = def.HotScope
	}
	if config.IdleScopePrefix == "" {
		config.IdleScopePrefix = def.IdleScopePrefix
	}
	if config.TargetConnections < 0 {
		config.TargetConnections = 0
	}
	return config
}

func gatewayFanoutNodesForTarget(subscribers, targetConnections int) int {
	if subscribers <= 0 || targetConnections <= 0 {
		return 0
	}
	return (targetConnections + subscribers - 1) / subscribers
}
