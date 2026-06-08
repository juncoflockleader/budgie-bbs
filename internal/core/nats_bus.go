package core

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// NATSPublisher is the minimal adapter the production runtime needs from a
// NATS client. Keeping this as a small interface lets the core compile without
// binding local development to a broker dependency.
type NATSPublisher interface {
	Publish(ctx context.Context, subject string, payload []byte) error
}

// NATSBus publishes every local event to NATS subjects while retaining the
// in-process subscriber behavior used by this single-binary runtime. A future
// production build can pair this publisher with a remote subscriber loop that
// replays exact event ranges from the EventStore after wakeup.
type NATSBus struct {
	local     *MemBus
	publisher NATSPublisher
}

var _ Bus = (*NATSBus)(nil)

func NewNATSBus(publisher NATSPublisher) *NATSBus {
	return &NATSBus{
		local:     NewMemBus(),
		publisher: publisher,
	}
}

func (b *NATSBus) Subscribe(scopes []string) *Subscription {
	return b.local.Subscribe(scopes)
}

func (b *NATSBus) Unsubscribe(s *Subscription) {
	b.local.Unsubscribe(s)
}

func (b *NATSBus) AddScopes(s *Subscription, scopes []string) {
	b.local.AddScopes(s, scopes)
}

func (b *NATSBus) RemoveScopes(s *Subscription, scopes []string) {
	b.local.RemoveScopes(s, scopes)
}

func (b *NATSBus) Publish(evt *proto.Event) {
	b.local.Publish(evt)
	if b.publisher == nil {
		return
	}
	raw, err := json.Marshal(proto.EventToOutbound(evt))
	if err != nil {
		return
	}
	metrics.EventsPublishedRemote.Inc()
	for _, scope := range evt.Scopes {
		subject := "budgie.events." + sanitizeNATSSubject(scope)
		_ = b.publisher.Publish(context.Background(), subject, raw)
	}
}

func sanitizeNATSSubject(scope string) string {
	scope = strings.ReplaceAll(scope, ":", ".")
	scope = strings.ReplaceAll(scope, "/", "_")
	scope = strings.ReplaceAll(scope, " ", "_")
	if scope == "" {
		return "global"
	}
	return scope
}
