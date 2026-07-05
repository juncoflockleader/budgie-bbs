package core

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// NATSConn is the minimal adapter the production runtime needs from a NATS
// client. Keeping this as a small interface lets the core compile without
// binding local development to a broker dependency.
type NATSConn interface {
	Publish(ctx context.Context, subject string, payload []byte) error
	Subscribe(subject string, handler func(data []byte)) (func() error, error)
}

const natsEventSubject = "budgie.events"
const natsEventScopeSubjectPrefix = natsEventSubject + ".scope."

const natsRemoteDedupeLimit = 4096

// NATSBus publishes local events to scope-specific NATS subjects while retaining
// the in-process subscriber behavior used by this single-binary runtime. Each
// process subscribes upstream only to the union of its local subscriber scopes.
// Remote messages carry full event bodies, so sibling nodes can deliver them
// from memory; the durable log remains the repair path for missed events.
type NATSBus struct {
	local  *MemBus
	conn   NATSConn
	nodeID string

	mu          sync.Mutex
	started     bool
	scopeRefs   map[string]int
	scopeUnsubs map[string]func() error
	subScopes   map[*Subscription]map[string]struct{}
	seenRemote  map[string]struct{}
	seenOrder   []string
}

var _ Bus = (*NATSBus)(nil)

func NewNATSBus(conn NATSConn, nodeID string) *NATSBus {
	return &NATSBus{
		local:       NewMemBus(),
		conn:        conn,
		nodeID:      nodeID,
		scopeRefs:   map[string]int{},
		scopeUnsubs: map[string]func() error{},
		subScopes:   map[*Subscription]map[string]struct{}{},
		seenRemote:  map[string]struct{}{},
	}
}

// Start subscribes to the current local scope union and republishes matching
// remote events into this node's local bus. It is idempotent.
func (b *NATSBus) Start(ctx context.Context) error {
	if b.conn == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return nil
	}
	b.started = true
	for scope, refs := range b.scopeRefs {
		if refs <= 0 {
			continue
		}
		if err := b.subscribeScopeLocked(scope); err != nil {
			b.started = false
			unsubscribes := b.unsubscribeAllLocked()
			for _, unsubscribe := range unsubscribes {
				_ = unsubscribe()
			}
			return err
		}
	}
	go func() {
		<-ctx.Done()
		b.stop()
	}()
	return nil
}

func (b *NATSBus) stop() {
	b.mu.Lock()
	b.started = false
	unsubscribes := b.unsubscribeAllLocked()
	b.mu.Unlock()
	for _, unsubscribe := range unsubscribes {
		_ = unsubscribe()
	}
}

func (b *NATSBus) Subscribe(scopes []string) *Subscription {
	sub := b.local.Subscribe(scopes)
	b.mu.Lock()
	set := map[string]struct{}{}
	for _, scope := range normalizedScopes(scopes) {
		set[scope] = struct{}{}
		b.addScopeRefLocked(scope)
	}
	b.subScopes[sub] = set
	b.mu.Unlock()
	return sub
}

func (b *NATSBus) Unsubscribe(s *Subscription) {
	b.local.Unsubscribe(s)
	b.mu.Lock()
	set := b.subScopes[s]
	delete(b.subScopes, s)
	for scope := range set {
		b.removeScopeRefLocked(scope)
	}
	b.mu.Unlock()
}

func (b *NATSBus) AddScopes(s *Subscription, scopes []string) {
	b.local.AddScopes(s, scopes)
	b.mu.Lock()
	set := b.subScopes[s]
	if set == nil {
		set = map[string]struct{}{}
		b.subScopes[s] = set
	}
	for _, scope := range normalizedScopes(scopes) {
		if _, ok := set[scope]; ok {
			continue
		}
		set[scope] = struct{}{}
		b.addScopeRefLocked(scope)
	}
	b.mu.Unlock()
}

func (b *NATSBus) RemoveScopes(s *Subscription, scopes []string) {
	b.local.RemoveScopes(s, scopes)
	b.mu.Lock()
	set := b.subScopes[s]
	for _, scope := range normalizedScopes(scopes) {
		if _, ok := set[scope]; !ok {
			continue
		}
		delete(set, scope)
		b.removeScopeRefLocked(scope)
	}
	b.mu.Unlock()
}

func (b *NATSBus) Publish(evt *proto.Event) {
	b.PublishLocal(evt)
	if b.conn == nil {
		return
	}
	body, err := json.Marshal(proto.EventToOutbound(evt))
	if err != nil {
		slog.Warn("nats bus: marshal event body failed", "event", evt.Kind, "seq", evt.Seq, "err", err)
		return
	}
	env := natsEnvelope{
		Node:   b.nodeID,
		Scopes: append([]string(nil), evt.Scopes...),
		Body:   body,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		slog.Warn("nats bus: marshal envelope failed", "event", evt.Kind, "seq", evt.Seq, "err", err)
		return
	}
	published := false
	for _, subject := range natsSubjectsForScopes(evt.Scopes) {
		if err := b.conn.Publish(context.Background(), subject, raw); err != nil {
			metrics.RemotePublishFailures.Inc()
			slog.Warn("nats bus: publish failed", "subject", subject, "event", evt.Kind, "seq", evt.Seq, "err", err)
			continue
		}
		published = true
	}
	if published {
		metrics.EventsPublishedRemote.Inc()
	}
}

func (b *NATSBus) PublishLocal(evt *proto.Event) {
	b.local.PublishLocal(evt)
}

func (b *NATSBus) QueueStats() BusQueueStats {
	return b.local.QueueStats()
}

type natsEnvelope struct {
	Node   string          `json:"node"`
	Scopes []string        `json:"scopes"`
	Body   json.RawMessage `json:"body"`
}

type natsWireEvent struct {
	Event           proto.EventKind `json:"event"`
	Seq             int64           `json:"seq"`
	ESeq            int64           `json:"eseq"`
	Payload         json.RawMessage `json:"payload"`
	TS              int64           `json:"ts"`
	PartitionKind   string          `json:"partitionKind"`
	PartitionKey    string          `json:"partitionKey"`
	PartitionOffset int64           `json:"partitionOffset"`
}

func (b *NATSBus) onRemote(data []byte) {
	var env natsEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		metrics.RemoteDecodeFailures.Inc()
		slog.Warn("nats bus: malformed envelope", "err", err)
		return
	}
	if env.Node != "" && env.Node == b.nodeID {
		return
	}
	var wire natsWireEvent
	if err := json.Unmarshal(env.Body, &wire); err != nil {
		metrics.RemoteDecodeFailures.Inc()
		slog.Warn("nats bus: malformed event body", "node", env.Node, "err", err)
		return
	}
	if !b.rememberRemote(remoteEventDedupeKey(env, wire)) {
		return
	}
	payload, err := unmarshalPayload(wire.Event, wire.Payload)
	if err != nil {
		metrics.RemoteDecodeFailures.Inc()
		slog.Warn("nats bus: malformed event payload", "node", env.Node, "event", wire.Event, "seq", wire.Seq, "err", err)
		return
	}
	recordRemoteWakeup(wire.TS)
	b.PublishLocal(&proto.Event{
		Kind:            wire.Event,
		Seq:             wire.Seq,
		ESeq:            wire.ESeq,
		Payload:         payload,
		TS:              wire.TS,
		PartitionKind:   wire.PartitionKind,
		PartitionKey:    wire.PartitionKey,
		PartitionOffset: wire.PartitionOffset,
		Scopes:          env.Scopes,
	})
}

func (b *NATSBus) addScopeRefLocked(scope string) {
	refs := b.scopeRefs[scope]
	b.scopeRefs[scope] = refs + 1
	if refs == 0 && b.started {
		if err := b.subscribeScopeLocked(scope); err != nil {
			slog.Warn("nats bus: subscribe failed", "scope", scope, "subject", natsSubjectForScope(scope), "err", err)
		}
	}
}

func (b *NATSBus) removeScopeRefLocked(scope string) {
	refs := b.scopeRefs[scope]
	if refs <= 1 {
		delete(b.scopeRefs, scope)
		if unsubscribe := b.scopeUnsubs[scope]; unsubscribe != nil {
			delete(b.scopeUnsubs, scope)
			_ = unsubscribe()
		}
		return
	}
	b.scopeRefs[scope] = refs - 1
}

func (b *NATSBus) subscribeScopeLocked(scope string) error {
	if b.conn == nil {
		return nil
	}
	if _, ok := b.scopeUnsubs[scope]; ok {
		return nil
	}
	subject := natsSubjectForScope(scope)
	unsubscribe, err := b.conn.Subscribe(subject, b.onRemote)
	if err != nil {
		return err
	}
	b.scopeUnsubs[scope] = unsubscribe
	return nil
}

func (b *NATSBus) unsubscribeAllLocked() []func() error {
	out := make([]func() error, 0, len(b.scopeUnsubs))
	for scope, unsubscribe := range b.scopeUnsubs {
		delete(b.scopeUnsubs, scope)
		if unsubscribe != nil {
			out = append(out, unsubscribe)
		}
	}
	return out
}

func (b *NATSBus) rememberRemote(key string) bool {
	if key == "" {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.seenRemote[key]; ok {
		return false
	}
	b.seenRemote[key] = struct{}{}
	b.seenOrder = append(b.seenOrder, key)
	for len(b.seenOrder) > natsRemoteDedupeLimit {
		oldest := b.seenOrder[0]
		b.seenOrder = b.seenOrder[1:]
		delete(b.seenRemote, oldest)
	}
	return true
}

func normalizedScopes(scopes []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}

func natsSubjectsForScopes(scopes []string) []string {
	normalized := normalizedScopes(scopes)
	if len(normalized) == 0 {
		return []string{natsSubjectForScope("_all")}
	}
	out := make([]string, 0, len(normalized))
	for _, scope := range normalized {
		out = append(out, natsSubjectForScope(scope))
	}
	return out
}

func natsSubjectForScope(scope string) string {
	if scope == "" {
		scope = "_all"
	}
	token := logmodel.EncodeSubjectToken(scope)
	if token == "" {
		token = "_"
	}
	return natsEventScopeSubjectPrefix + token
}

func remoteEventDedupeKey(env natsEnvelope, wire natsWireEvent) string {
	switch {
	case wire.Seq > 0:
		return fmt.Sprintf("%s:seq:%d", env.Node, wire.Seq)
	case wire.ESeq > 0:
		return fmt.Sprintf("%s:eseq:%d", env.Node, wire.ESeq)
	default:
		h := fnv.New64a()
		_, _ = h.Write(env.Body)
		for _, scope := range env.Scopes {
			_, _ = h.Write([]byte{0})
			_, _ = h.Write([]byte(scope))
		}
		return fmt.Sprintf("%s:event:%s:%d:%x", env.Node, wire.Event, wire.TS, h.Sum64())
	}
}
