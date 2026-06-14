package core

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestMemBusQueueStatsAndDropScopeMetrics(t *testing.T) {
	bus := NewMemBus()
	sub := bus.Subscribe([]string{"board:general"})
	defer bus.Unsubscribe(sub)

	for i := 0; i < cap(sub.Ch); i++ {
		sub.Ch <- &proto.Event{Kind: proto.EvtChatLine, Scopes: []string{"board:general"}}
	}

	stats := bus.QueueStats()
	if stats.Subscribers != 1 {
		t.Fatalf("subscribers = %d, want 1", stats.Subscribers)
	}
	if stats.QueueDepthTotal != cap(sub.Ch) || stats.QueueDepthMax != cap(sub.Ch) {
		t.Fatalf("queue depth total/max = %d/%d, want %d/%d",
			stats.QueueDepthTotal, stats.QueueDepthMax, cap(sub.Ch), cap(sub.Ch))
	}
	if stats.QueueCapacityTotal != cap(sub.Ch) || stats.QueueCapacityMax != cap(sub.Ch) {
		t.Fatalf("queue capacity total/max = %d/%d, want %d/%d",
			stats.QueueCapacityTotal, stats.QueueCapacityMax, cap(sub.Ch), cap(sub.Ch))
	}
	generalStats := scopeQueueStats(stats, "board:general")
	if generalStats.Subscribers != 1 || generalStats.QueueDepthTotal != cap(sub.Ch) || generalStats.QueueDepthMax != cap(sub.Ch) {
		t.Fatalf("board:general scope stats = %+v, want subscriber and full queue depth", generalStats)
	}

	dropsBefore := metrics.DroppedSubscriberSends.Value()
	scopeDropsBefore := metrics.GatewayDroppedSendsByScope.Value(map[string]string{"scope": "board:general"})
	bus.PublishLocal(&proto.Event{
		Kind:   proto.EvtChatLine,
		Scopes: []string{"board:general", "chat:lobby"},
	})

	if got := metrics.DroppedSubscriberSends.Value() - dropsBefore; got != 1 {
		t.Fatalf("dropped subscriber sends delta = %d, want 1", got)
	}
	if got := metrics.GatewayDroppedSendsByScope.Value(map[string]string{"scope": "board:general"}) - scopeDropsBefore; got != 1 {
		t.Fatalf("gateway dropped sends for board:general delta = %d, want 1", got)
	}
}

func TestGatewayScopeFanoutSamples(t *testing.T) {
	samples := gatewayScopeFanoutSamples(BusQueueStats{Scopes: []BusScopeQueueStats{
		{Scope: "board:general", Subscribers: 2, QueueDepthTotal: 1},
		{Scope: "thread:thr_1", Subscribers: 3, QueueDepthTotal: 0},
		{Scope: "board:life", Subscribers: 1, QueueDepthTotal: 10},
	}}, 2)

	if got := scopeSubscriberSample(samples, "thread:thr_1"); got != 3 {
		t.Fatalf("thread subscriber sample = %v, want 3", got)
	}
	if got := hotPartitionSignalSample(samples, partitionThread, "thr_1", "gateway_subscribers"); got != 3 {
		t.Fatalf("thread fanout candidate = %v, want 3", got)
	}
	if got := scopeSubscriberSample(samples, "board:general"); got != 2 {
		t.Fatalf("board subscriber sample = %v, want 2", got)
	}
	if got := hotPartitionSignalSample(samples, partitionBoard, "general", "gateway_subscribers"); got != 2 {
		t.Fatalf("board fanout candidate = %v, want 2", got)
	}
	if got := scopeSubscriberSample(samples, "board:life"); got != -1 {
		t.Fatalf("lower-fanout scope sample = %v, want absent", got)
	}
}

func TestGatewayDropHotPartitionSamples(t *testing.T) {
	samples := gatewayDropHotPartitionSamples([]metrics.Sample{
		{
			Labels: map[string]string{"scope": "board:general"},
			Value:  3,
		},
		{
			Labels: map[string]string{"scope": "thread:thr_1"},
			Value:  2,
		},
		{
			Labels: map[string]string{"scope": "chat:lobby"},
			Value:  0,
		},
		{
			Labels: map[string]string{"scope": "unknown"},
			Value:  4,
		},
	})

	if got := hotPartitionSignalSample(samples, partitionBoard, "general", "gateway_drops"); got != 3 {
		t.Fatalf("board gateway drop candidate = %v, want 3", got)
	}
	if got := hotPartitionSignalSample(samples, partitionThread, "thr_1", "gateway_drops"); got != 2 {
		t.Fatalf("thread gateway drop candidate = %v, want 2", got)
	}
	if got := hotPartitionSignalSample(samples, partitionChat, "lobby", "gateway_drops"); got != -1 {
		t.Fatalf("zero-value chat candidate = %v, want absent", got)
	}
	if got := hotPartitionSignalSample(samples, partitionGlobal, "unknown", "gateway_drops"); got != -1 {
		t.Fatalf("unknown-scope candidate = %v, want absent", got)
	}
}

func hotPartitionSignalSample(samples []metrics.Sample, kind, key, signal string) float64 {
	for _, sample := range samples {
		if sample.Name == "budgie_hot_partition_candidate" &&
			sample.Labels["kind"] == kind &&
			sample.Labels["key"] == key &&
			sample.Labels["signal"] == signal {
			return sample.Value
		}
	}
	return -1
}

func scopeQueueStats(stats BusQueueStats, scope string) BusScopeQueueStats {
	for _, scopeStats := range stats.Scopes {
		if scopeStats.Scope == scope {
			return scopeStats
		}
	}
	return BusScopeQueueStats{}
}

func scopeSubscriberSample(samples []metrics.Sample, scope string) float64 {
	for _, sample := range samples {
		if sample.Name == "budgie_gateway_scope_subscribers" && sample.Labels["scope"] == scope {
			return sample.Value
		}
	}
	return -1
}
