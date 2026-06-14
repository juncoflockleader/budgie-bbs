package core

import (
	"context"
	"os"
	"strconv"
	"testing"
)

const (
	defaultGatewayLoadHotSubscribers  = 128
	defaultGatewayLoadIdleSubscribers = 2500
	defaultGatewayLoadBufferSize      = 2
)

func TestGatewayFanoutManyIdleSubscribers(t *testing.T) {
	hotCount := gatewayLoadEnvInt("BUDGIE_GATEWAY_LOAD_HOT_SUBSCRIBERS", defaultGatewayLoadHotSubscribers)
	idleCount := gatewayLoadEnvInt("BUDGIE_GATEWAY_LOAD_IDLE_SUBSCRIBERS", defaultGatewayLoadIdleSubscribers)
	bufferSize := gatewayLoadEnvInt("BUDGIE_GATEWAY_LOAD_BUFFER_SIZE", defaultGatewayLoadBufferSize)
	if hotCount <= 0 {
		t.Fatalf("hot subscriber count must be positive, got %d", hotCount)
	}
	if idleCount < 0 {
		t.Fatalf("idle subscriber count must be non-negative, got %d", idleCount)
	}

	report, err := RunGatewayFanoutLoad(context.Background(), GatewayFanoutLoadConfig{
		HotSubscribers:  hotCount,
		IdleSubscribers: idleCount,
		BufferSize:      bufferSize,
		Events:          1,
	})
	if err != nil {
		t.Fatalf("RunGatewayFanoutLoad: %v", err)
	}
	if report.Subscribers != hotCount+idleCount {
		t.Fatalf("subscribers = %d, want %d", report.Subscribers, hotCount+idleCount)
	}
	if report.QueueDepthTotal != hotCount {
		t.Fatalf("queue depth total = %d, want %d hot deliveries", report.QueueDepthTotal, hotCount)
	}
	if report.QueueDepthMax != 1 {
		t.Fatalf("queue depth max = %d, want 1", report.QueueDepthMax)
	}
	if report.HotScopeSubscribers != hotCount || report.HotScopeQueueDepth != hotCount {
		t.Fatalf("hot scope report = subscribers %d depth %d, want %d", report.HotScopeSubscribers, report.HotScopeQueueDepth, hotCount)
	}
	if report.IdleSampleDelivered != 0 {
		t.Fatalf("idle sample delivered = %d, want 0", report.IdleSampleDelivered)
	}
	t.Logf("gateway fanout fixture: hot=%d idle=%d buffer=%d publish=%.3fms",
		hotCount, idleCount, bufferSize, report.PublishDurationMS)
}

func TestGatewayFanoutLoadReportsSlowClientDrops(t *testing.T) {
	report, err := RunGatewayFanoutLoad(context.Background(), GatewayFanoutLoadConfig{
		HotSubscribers:  8,
		IdleSubscribers: 32,
		BufferSize:      2,
		Events:          3,
	})
	if err != nil {
		t.Fatalf("RunGatewayFanoutLoad: %v", err)
	}
	if report.QueuedDeliveries != 16 {
		t.Fatalf("queued deliveries = %d, want hot subscribers * buffer", report.QueuedDeliveries)
	}
	if report.EstimatedDrops != 8 {
		t.Fatalf("estimated drops = %d, want overflow event dropped for each hot subscriber", report.EstimatedDrops)
	}
	if report.QueueDepthMax != 2 {
		t.Fatalf("queue depth max = %d, want full buffer", report.QueueDepthMax)
	}
}

func TestGatewayFanoutLoadReportsTargetGatewayCount(t *testing.T) {
	report, err := RunGatewayFanoutLoad(context.Background(), GatewayFanoutLoadConfig{
		HotSubscribers:    8,
		IdleSubscribers:   32,
		BufferSize:        2,
		Events:            1,
		TargetConnections: 100,
	})
	if err != nil {
		t.Fatalf("RunGatewayFanoutLoad: %v", err)
	}
	if report.Subscribers != 40 {
		t.Fatalf("subscribers = %d, want 40", report.Subscribers)
	}
	if report.TargetConnections != 100 {
		t.Fatalf("target connections = %d, want 100", report.TargetConnections)
	}
	if report.GatewayNodesForTarget != 3 {
		t.Fatalf("gateway nodes for target = %d, want 3", report.GatewayNodesForTarget)
	}
	if report.ProjectedConnectionCapacity != 120 {
		t.Fatalf("projected connection capacity = %d, want 120", report.ProjectedConnectionCapacity)
	}
}

func gatewayLoadEnvInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}
