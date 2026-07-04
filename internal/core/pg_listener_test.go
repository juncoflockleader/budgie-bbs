package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lib/pq"

	"github.com/juncoflockleader/budgie-bbs/internal/core/eventwakeup"
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
	raw := marshalCoreTestJSON(t, "marshal", original)
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
	raw := marshalCoreTestJSON(t, "marshal wakeup", payload)

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
	raw := marshalCoreTestJSON(t, "marshal", p)
	var dec pgWakeupPayload
	if err := json.Unmarshal(raw, &dec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if dec != p {
		t.Errorf("round-trip mismatch: got %+v, want %+v", dec, p)
	}
	// EID must appear in JSON when set.
	if string(raw) == "" || !strings.Contains(string(raw), "eid") {
		t.Errorf("expected 'eid' in JSON, got %s", raw)
	}

	// Without EID — omitempty should suppress the field.
	p2 := pgWakeupPayload{Seq: 1, Event: "post.appended", NodeID: "node_x", Scopes: "board:g"}
	raw2 := marshalCoreTestJSON(t, "marshal without eid", p2)
	if strings.Contains(string(raw2), "eid") {
		t.Errorf("expected 'eid' to be absent when empty, got %s", raw2)
	}
}

// TestHandlePGNotificationEphemeralRoutingSkipsSelf verifies that an ephemeral
// notification from this node is silently ignored.
func TestHandlePGNotificationEphemeralRoutingSkipsSelf(t *testing.T) {
	const myNodeID = "node_self"
	published := 0
	bus := &mockBus{publishFn: func() { published++ }}

	payload := pgWakeupPayload{Seq: 0, Event: "chat.line", NodeID: myNodeID, Scopes: "chat:lobby", EID: "chat_xyz"}
	raw := marshalCoreTestJSON(t, "marshal ephemeral wakeup", payload)
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

func TestHandlePGEphemeralUnorderedTrafficWakeups(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	threadAck := execPGListenerTestCmd(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Listener unordered traffic",
		Body:  "[poll]\nPick?\nA\nB\n[/poll]",
	})
	posts, err := c.ListPosts(threadAck.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(posts))
	}
	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	fullPoll, err := c.GetPoll(poll.ID, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fullPoll.Options) == 0 {
		t.Fatal("expected poll options")
	}

	execPGListenerTestCmd(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{Post: posts[0].ID, Emoji: "heart"})
	reactionWakeup := marshalCoreTestJSON(t, "marshal reaction wakeup", eventwakeup.PostReaction{Post: posts[0].ID, User: bob.ID, Emoji: "heart", TS: 1234})
	reactionBus := &mockBus{}
	handlePGEphemeralNotification(pgWakeupPayload{
		Event:  string(proto.EvtPostReacted),
		Scopes: "thread:" + threadAck.ID + ",board:general",
		EID:    string(reactionWakeup),
	}, c.DB, reactionBus)
	if reactionBus.event == nil || reactionBus.event.Kind != proto.EvtPostReacted || reactionBus.event.Seq != 0 {
		t.Fatalf("reaction wakeup event = %+v, want non-durable post.reacted", reactionBus.event)
	}
	reactionPayload, ok := reactionBus.event.Payload.(*proto.PostReactedPayload)
	if !ok || reactionPayload.PostID != posts[0].ID || reactionPayload.User != bob.Name || reactionPayload.ReactionCount != 1 {
		t.Fatalf("reaction wakeup payload = %#v", reactionBus.event.Payload)
	}

	execPGListenerTestCmd(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: fullPoll.Options[0].ID})
	voteWakeup := marshalCoreTestJSON(t, "marshal vote wakeup", eventwakeup.PollVote{Poll: poll.ID, User: bob.ID, TS: 2345})
	voteBus := &mockBus{}
	handlePGEphemeralNotification(pgWakeupPayload{
		Event:  string(proto.EvtPollVoted),
		Scopes: "thread:" + threadAck.ID + ",board:general",
		EID:    string(voteWakeup),
	}, c.DB, voteBus)
	if voteBus.event == nil || voteBus.event.Kind != proto.EvtPollVoted || voteBus.event.Seq != 0 {
		t.Fatalf("poll vote wakeup event = %+v, want non-durable poll.voted", voteBus.event)
	}
	votePayload, ok := voteBus.event.Payload.(*proto.PollVotedPayload)
	if !ok || votePayload.Poll != poll.ID || votePayload.Option != fullPoll.Options[0].ID || votePayload.User != bob.Name {
		t.Fatalf("poll vote wakeup payload = %#v", voteBus.event.Payload)
	}
}

func execPGListenerTestCmd(t *testing.T, c *Core, actor *User, cmd proto.CommandName, payload any) *proto.AckResult {
	t.Helper()
	raw := marshalCoreTestJSON(t, "marshal "+string(cmd), payload)
	reply := c.ExecCmd(context.Background(), actor, cmd, raw, "")
	if reply.Err != nil {
		t.Fatalf("command %s failed: %+v", cmd, reply.Err)
	}
	return reply.Result
}

// mockBus is a minimal Bus that counts Publish calls.
type mockBus struct {
	publishFn func()
	event     *proto.Event
}

func (b *mockBus) Subscribe(scopes []string) *Subscription       { return nil }
func (b *mockBus) Unsubscribe(s *Subscription)                   {}
func (b *mockBus) AddScopes(s *Subscription, scopes []string)    {}
func (b *mockBus) RemoveScopes(s *Subscription, scopes []string) {}
func (b *mockBus) Publish(evt *proto.Event) {
	b.event = evt
	if b.publishFn != nil {
		b.publishFn()
	}
}
func (b *mockBus) PublishLocal(evt *proto.Event) {
	b.event = evt
	if b.publishFn != nil {
		b.publishFn()
	}
}
