package core

import (
	"sort"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// Bus is the interface for the event pub/sub system.
// The in-process implementation can be swapped for a Redis/NATS backend
// without changing the Handler or transport code.
type Bus interface {
	Subscribe(scopes []string) *Subscription
	Unsubscribe(s *Subscription)
	AddScopes(s *Subscription, scopes []string)
	RemoveScopes(s *Subscription, scopes []string)
	Publish(evt *proto.Event)
}

type localOnlyBus interface {
	PublishLocal(evt *proto.Event)
}

const defaultSubscriptionBuffer = 512

type busQueueStatsProvider interface {
	QueueStats() BusQueueStats
}

// BusQueueStats snapshots the local gateway fanout buffers. It deliberately
// avoids per-connection labels so metrics stay bounded at scale.
type BusQueueStats struct {
	Subscribers        int
	QueueDepthTotal    int
	QueueDepthMax      int
	QueueCapacityTotal int
	QueueCapacityMax   int
	Scopes             []BusScopeQueueStats
}

type BusScopeQueueStats struct {
	Scope              string
	Subscribers        int
	QueueDepthTotal    int
	QueueDepthMax      int
	QueueCapacityTotal int
	QueueCapacityMax   int
}

func publishLocal(bus Bus, evt *proto.Event) {
	if b, ok := bus.(localOnlyBus); ok {
		b.PublishLocal(evt)
		return
	}
	bus.Publish(evt)
}

// Subscription is a live subscriber. Read events from Ch; call
// Unsubscribe when the connection closes to avoid a leak.
type Subscription struct {
	Ch chan *proto.Event

	mu     sync.RWMutex
	scopes map[string]struct{}
}

func (s *Subscription) hasScope(scope string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.scopes[scope]
	return ok
}

// MemBus is the in-process pub/sub bus.
type MemBus struct {
	mu                 sync.RWMutex
	subs               []*Subscription
	subscriptionBuffer int
}

var _ Bus = (*MemBus)(nil)

func NewMemBus() *MemBus { return NewMemBusWithBuffer(defaultSubscriptionBuffer) }

func NewMemBusWithBuffer(bufferSize int) *MemBus {
	if bufferSize <= 0 {
		bufferSize = defaultSubscriptionBuffer
	}
	return &MemBus{subscriptionBuffer: bufferSize}
}

func (b *MemBus) Subscribe(scopes []string) *Subscription {
	s := &Subscription{
		Ch:     make(chan *proto.Event, b.subscriptionBuffer),
		scopes: make(map[string]struct{}, len(scopes)),
	}
	for _, sc := range scopes {
		s.scopes[sc] = struct{}{}
	}
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()
	metrics.LocalSubscribers.Inc()
	return s
}

func (b *MemBus) Unsubscribe(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, sub := range b.subs {
		if sub == s {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			close(s.Ch)
			metrics.LocalSubscribers.Dec()
			return
		}
	}
}

func (b *MemBus) AddScopes(s *Subscription, scopes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range scopes {
		s.scopes[sc] = struct{}{}
	}
}

func (b *MemBus) RemoveScopes(s *Subscription, scopes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range scopes {
		delete(s.scopes, sc)
	}
}

func (b *MemBus) Publish(evt *proto.Event) {
	b.PublishLocal(evt)
}

func (b *MemBus) PublishLocal(evt *proto.Event) {
	ensureEventPartition(evt)

	b.mu.RLock()
	subs := make([]*Subscription, len(b.subs))
	copy(subs, b.subs)
	b.mu.RUnlock()

	for _, s := range subs {
		if matchesAny(s, evt.Scopes) {
			select {
			case s.Ch <- evt:
				metrics.EventsPublishedLocal.Inc()
			default:
				// Slow consumer — drop rather than block. The cursor
				// mechanism handles catch-up; the subscriber will
				// re-request missed events on reconnect.
				metrics.DroppedSubscriberSends.Inc()
				recordGatewayDropScopes(evt.Scopes)
			}
		}
	}
}

func (b *MemBus) QueueStats() BusQueueStats {
	b.mu.RLock()
	subs := make([]*Subscription, len(b.subs))
	copy(subs, b.subs)
	b.mu.RUnlock()

	stats := BusQueueStats{Subscribers: len(subs)}
	scopeStats := map[string]*BusScopeQueueStats{}
	for _, s := range subs {
		depth := len(s.Ch)
		capacity := cap(s.Ch)
		stats.QueueDepthTotal += depth
		stats.QueueCapacityTotal += capacity
		if depth > stats.QueueDepthMax {
			stats.QueueDepthMax = depth
		}
		if capacity > stats.QueueCapacityMax {
			stats.QueueCapacityMax = capacity
		}

		s.mu.RLock()
		scopes := make([]string, 0, len(s.scopes))
		for scope := range s.scopes {
			scopes = append(scopes, scope)
		}
		s.mu.RUnlock()
		for _, scope := range scopes {
			if scope == "" {
				continue
			}
			scopeStat := scopeStats[scope]
			if scopeStat == nil {
				scopeStat = &BusScopeQueueStats{Scope: scope}
				scopeStats[scope] = scopeStat
			}
			scopeStat.Subscribers++
			scopeStat.QueueDepthTotal += depth
			scopeStat.QueueCapacityTotal += capacity
			if depth > scopeStat.QueueDepthMax {
				scopeStat.QueueDepthMax = depth
			}
			if capacity > scopeStat.QueueCapacityMax {
				scopeStat.QueueCapacityMax = capacity
			}
		}
	}
	stats.Scopes = make([]BusScopeQueueStats, 0, len(scopeStats))
	for _, scopeStat := range scopeStats {
		stats.Scopes = append(stats.Scopes, *scopeStat)
	}
	sort.Slice(stats.Scopes, func(i, j int) bool {
		return stats.Scopes[i].Scope < stats.Scopes[j].Scope
	})
	return stats
}

func recordGatewayDropScopes(scopes []string) {
	recorded := false
	for _, scope := range scopes {
		if scope == "" {
			continue
		}
		metrics.GatewayDroppedSendsByScope.Inc(map[string]string{"scope": scope})
		recorded = true
	}
	if !recorded {
		metrics.GatewayDroppedSendsByScope.Inc(map[string]string{"scope": "unknown"})
	}
}

func matchesAny(s *Subscription, scopes []string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sc := range scopes {
		if _, ok := s.scopes[sc]; ok {
			return true
		}
	}
	return false
}
