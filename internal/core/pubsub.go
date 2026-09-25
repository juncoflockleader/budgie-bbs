package core

import (
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
	mu   sync.RWMutex
	subs []*Subscription
}

var _ Bus = (*MemBus)(nil)

func NewMemBus() *MemBus { return &MemBus{} }

func (b *MemBus) Subscribe(scopes []string) *Subscription {
	s := &Subscription{
		Ch:     make(chan *proto.Event, 512),
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
			}
		}
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
