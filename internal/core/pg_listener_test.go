package core

import (
	"encoding/json"
	"testing"

	"github.com/lib/pq"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// TestPGWakeupPayloadRoundTrip verifies the JSON marshaling used in pg_notify
// payloads matches what handlePGNotification expects.
func TestPGWakeupPayloadRoundTrip(t *testing.T) {
	original := pgWakeupPayload{
		Seq:    42,
		Event:  "post.appended",
		NodeID: "node_abc123",
		Scopes: "board:general,thread:thr_xyz",
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded pgWakeupPayload
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded != original {
		t.Errorf("round-trip mismatch: got %+v, want %+v", decoded, original)
	}
}

// TestHandlePGNotificationSkipsSelf verifies that handlePGNotification ignores
// events that originated from this node (same node_id).
func TestHandlePGNotificationSkipsSelf(t *testing.T) {
	const myNodeID = "node_self"

	published := 0
	bus := &mockBus{publishFn: func() { published++ }}

	payload := pgWakeupPayload{Seq: 1, Event: "post.appended", NodeID: myNodeID, Scopes: "board:general"}
	raw, _ := json.Marshal(payload)

	n := &pq.Notification{Channel: pgNotifyChannel, Extra: string(raw)}
	// Pass nil db — handlePGNotification should return early before reaching replayEvents.
	handlePGNotification(n, myNodeID, nil, bus)

	if published != 0 {
		t.Errorf("expected self-notification to be skipped, but bus.Publish was called %d time(s)", published)
	}
}

// TestHandlePGNotificationBadPayload verifies malformed payloads are safely ignored.
func TestHandlePGNotificationBadPayload(t *testing.T) {
	bus := &mockBus{publishFn: func() {}}
	n := &pq.Notification{Channel: pgNotifyChannel, Extra: "not valid json{{{"}
	// Should not panic.
	handlePGNotification(n, "other_node", nil, bus)
}

// TestPGWakeupPayloadEIDRoundTrip verifies that the EID field survives
// marshal/unmarshal and that omitempty suppresses it when empty.
func TestPGWakeupPayloadEIDRoundTrip(t *testing.T) {
	// With EID set.
	p := pgWakeupPayload{Seq: 0, Event: "chat.line", NodeID: "node_x", Scopes: "chat:lobby", EID: "chat_abc"}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var dec pgWakeupPayload
	if err := json.Unmarshal(raw, &dec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if dec != p {
		t.Errorf("round-trip mismatch: got %+v, want %+v", dec, p)
	}
	// EID must appear in JSON when set.
	if string(raw) == "" || !containsString(string(raw), "eid") {
		t.Errorf("expected 'eid' in JSON, got %s", raw)
	}

	// Without EID — omitempty should suppress the field.
	p2 := pgWakeupPayload{Seq: 1, Event: "post.appended", NodeID: "node_x", Scopes: "board:g"}
	raw2, _ := json.Marshal(p2)
	if containsString(string(raw2), "eid") {
		t.Errorf("expected 'eid' to be absent when empty, got %s", raw2)
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

// TestHandlePGNotificationEphemeralRoutingSkipsSelf verifies that an ephemeral
// notification from this node is silently ignored.
func TestHandlePGNotificationEphemeralRoutingSkipsSelf(t *testing.T) {
	const myNodeID = "node_self"
	published := 0
	bus := &mockBus{publishFn: func() { published++ }}

	payload := pgWakeupPayload{Seq: 0, Event: "chat.line", NodeID: myNodeID, Scopes: "chat:lobby", EID: "chat_xyz"}
	raw, _ := json.Marshal(payload)
	n := &pq.Notification{Channel: pgNotifyChannel, Extra: string(raw)}

	handlePGNotification(n, myNodeID, nil, bus)

	if published != 0 {
		t.Errorf("expected self ephemeral notification to be skipped, got %d publish calls", published)
	}
}

// TestHandlePGEphemeralUnknownKindNoOp verifies that an unrecognised ephemeral
// event kind is silently ignored (logged at debug level) without panicking.
func TestHandlePGEphemeralUnknownKindNoOp(t *testing.T) {
	published := 0
	bus := &mockBus{publishFn: func() { published++ }}

	p := pgWakeupPayload{Seq: 0, Event: "unknown.ephemeral.kind", NodeID: "node_other", Scopes: "chat:lobby", EID: "eph_123"}
	// Should not panic even with nil db, because the default branch just logs.
	handlePGEphemeralNotification(p, nil, bus)

	if published != 0 {
		t.Errorf("unknown ephemeral kind should not publish; got %d calls", published)
	}
}

// mockBus is a minimal Bus that counts Publish calls.
type mockBus struct {
	publishFn func()
}

func (b *mockBus) Subscribe(scopes []string) *Subscription      { return nil }
func (b *mockBus) Unsubscribe(s *Subscription)                  {}
func (b *mockBus) AddScopes(s *Subscription, scopes []string)   {}
func (b *mockBus) RemoveScopes(s *Subscription, scopes []string) {}
func (b *mockBus) Publish(evt *proto.Event) {
	if b.publishFn != nil {
		b.publishFn()
	}
}
