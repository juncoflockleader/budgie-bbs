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
