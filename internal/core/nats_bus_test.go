package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestNATSSubjectForScopeUsesLogmodelSubjectToken(t *testing.T) {
	scope := "board:general/with spaces"
	want := natsEventScopeSubjectPrefix + logmodel.EncodeSubjectToken(scope)
	if got := natsSubjectForScope(scope); got != want {
		t.Fatalf("natsSubjectForScope = %q, want %q", got, want)
	}
}

func marshalNATSBusEventEnvelope(t *testing.T, node string, scopes []string, event proto.EventKind, seq int64, payload any, ts int64) []byte {
	t.Helper()
	body, err := json.Marshal(proto.OutboundMessage{
		Kind:    "event",
		Event:   event,
		Seq:     seq,
		Payload: payload,
		TS:      ts,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	raw, err := json.Marshal(natsEnvelope{
		Node:   node,
		Scopes: scopes,
		Body:   body,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return raw
}

func TestNATSBusPublishSendsOneEnvelopeAndLocalEvent(t *testing.T) {
	conn := &fakeNATSConn{}
	bus := NewNATSBus(conn, "node_a")
	sub := bus.Subscribe([]string{"thread:thr_1"})
	defer bus.Unsubscribe(sub)

	evt := &proto.Event{
		Kind:   proto.EvtPostAppended,
		Seq:    42,
		Scopes: []string{"board:general", "thread:thr_1"},
		Payload: &proto.PostAppendedPayload{
			ID:          "pst_1",
			Thread:      "thr_1",
			Author:      "alice",
			Body:        "hello",
			ContentType: "markup",
			TS:          1000,
		},
		TS: 1000,
	}

	bus.Publish(evt)

	select {
	case got := <-sub.Ch:
		if got != evt {
			t.Fatalf("local publish got different event pointer")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for local event")
	}

	published := conn.publishedPayloads()
	if len(published) != 2 {
		t.Fatalf("published payload count = %d, want one per unique scope", len(published))
	}
	wantSubjects := []string{
		natsSubjectForScope("board:general"),
		natsSubjectForScope("thread:thr_1"),
	}
	if got := conn.publishedSubjects(); !slices.Equal(sortStrings(got), sortStrings(wantSubjects)) {
		t.Fatalf("subjects = %#v, want %#v", got, wantSubjects)
	}
	var env natsEnvelope
	if err := json.Unmarshal(published[0], &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Node != "node_a" {
		t.Fatalf("node = %q, want node_a", env.Node)
	}
	if len(env.Scopes) != 2 || env.Scopes[0] != "board:general" || env.Scopes[1] != "thread:thr_1" {
		t.Fatalf("scopes = %#v", env.Scopes)
	}
	var wire natsWireEvent
	if err := json.Unmarshal(env.Body, &wire); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if wire.Event != proto.EvtPostAppended || wire.Seq != 42 || wire.TS != 1000 {
		t.Fatalf("wire event = %+v", wire)
	}
}

func TestNATSBusOnRemotePublishesTypedLocalEvent(t *testing.T) {
	bus := NewNATSBus(nil, "node_b")
	sub := bus.Subscribe([]string{"thread:thr_1"})
	defer bus.Unsubscribe(sub)

	payload := &proto.PostAppendedPayload{
		ID:          "pst_1",
		Thread:      "thr_1",
		Author:      "alice",
		Body:        "hello from elsewhere",
		ContentType: "markup",
		TS:          1000,
	}
	raw := marshalNATSBusEventEnvelope(t, "node_a", []string{"board:general", "thread:thr_1"}, proto.EvtPostAppended, 42, payload, 1000)

	bus.onRemote(raw)

	select {
	case got := <-sub.Ch:
		if got.Kind != proto.EvtPostAppended || got.Seq != 42 || got.TS != 1000 {
			t.Fatalf("event metadata = %+v", got)
		}
		if len(got.Scopes) != 2 || got.Scopes[1] != "thread:thr_1" {
			t.Fatalf("scopes = %#v", got.Scopes)
		}
		p, ok := got.Payload.(*proto.PostAppendedPayload)
		if !ok {
			t.Fatalf("payload type = %T, want *proto.PostAppendedPayload", got.Payload)
		}
		if p.Body != "hello from elsewhere" {
			t.Fatalf("payload body = %q", p.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for remote event")
	}
}

func TestNATSBusOnRemoteSkipsSelfAndDoesNotRepublish(t *testing.T) {
	conn := &fakeNATSConn{}
	bus := NewNATSBus(conn, "node_a")
	sub := bus.Subscribe([]string{"thread:thr_1"})
	defer bus.Unsubscribe(sub)

	raw := marshalNATSBusEventEnvelope(t, "node_a", []string{"thread:thr_1"}, proto.EvtPostAppended, 42,
		&proto.PostAppendedPayload{ID: "pst_1", Thread: "thr_1", Author: "alice", Body: "hello", ContentType: "markup", TS: 1000}, 1000)

	bus.onRemote(raw)

	select {
	case got := <-sub.Ch:
		t.Fatalf("self-originated remote event was published locally: %+v", got)
	default:
	}
	if got := len(conn.publishedPayloads()); got != 0 {
		t.Fatalf("remote inbound republished to NATS %d time(s), want 0", got)
	}
}

func TestNATSBusOnRemoteDeduplicatesMultiScopeDeliveries(t *testing.T) {
	bus := NewNATSBus(nil, "node_b")
	sub := bus.Subscribe([]string{"board:general", "thread:thr_1"})
	defer bus.Unsubscribe(sub)

	raw := marshalNATSBusEventEnvelope(t, "node_a", []string{"board:general", "thread:thr_1"}, proto.EvtPostAppended, 42,
		&proto.PostAppendedPayload{ID: "pst_1", Thread: "thr_1", Author: "alice", Body: "hello", ContentType: "markup", TS: 1000}, 1000)

	bus.onRemote(raw)
	bus.onRemote(raw)

	select {
	case got := <-sub.Ch:
		if got.Seq != 42 {
			t.Fatalf("seq = %d, want 42", got.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first remote event")
	}
	select {
	case got := <-sub.Ch:
		t.Fatalf("duplicate remote event was published locally: %+v", got)
	default:
	}
}

func TestNATSBusPublishLocalDoesNotRepublish(t *testing.T) {
	conn := &fakeNATSConn{}
	bus := NewNATSBus(conn, "node_a")
	sub := bus.Subscribe([]string{"thread:thr_1"})
	defer bus.Unsubscribe(sub)

	bus.PublishLocal(&proto.Event{
		Kind:    proto.EvtPostAppended,
		Seq:     42,
		Scopes:  []string{"thread:thr_1"},
		Payload: &proto.PostAppendedPayload{ID: "pst_1", Thread: "thr_1", Author: "alice", Body: "hello", ContentType: "markup", TS: 1000},
		TS:      1000,
	})

	select {
	case got := <-sub.Ch:
		if got.Seq != 42 {
			t.Fatalf("seq = %d, want 42", got.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for local event")
	}
	if got := len(conn.publishedPayloads()); got != 0 {
		t.Fatalf("PublishLocal republished to NATS %d time(s), want 0", got)
	}
}

func TestNATSBusStartTracksLocalScopeUnion(t *testing.T) {
	conn := &fakeNATSConn{}
	bus := NewNATSBus(conn, "node_a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := conn.subscribedSubjects(); len(got) != 0 {
		t.Fatalf("subscribed subjects before local subscribers = %#v, want none", got)
	}

	subA := bus.Subscribe([]string{"thread:thr_1", "board:general"})
	want := []string{natsSubjectForScope("board:general"), natsSubjectForScope("thread:thr_1")}
	if got := conn.subscribedSubjects(); !slices.Equal(sortStrings(got), sortStrings(want)) {
		t.Fatalf("subscribed subjects after subA = %#v, want %#v", got, want)
	}

	subB := bus.Subscribe([]string{"thread:thr_1"})
	if got := conn.subscribedSubjects(); !slices.Equal(sortStrings(got), sortStrings(want)) {
		t.Fatalf("duplicate scope should not add upstream subscription: got %#v want %#v", got, want)
	}

	bus.AddScopes(subB, []string{"chat:lobby"})
	want = []string{natsSubjectForScope("board:general"), natsSubjectForScope("chat:lobby"), natsSubjectForScope("thread:thr_1")}
	if got := conn.subscribedSubjects(); !slices.Equal(sortStrings(got), sortStrings(want)) {
		t.Fatalf("subscribed subjects after chat add = %#v, want %#v", got, want)
	}

	bus.RemoveScopes(subA, []string{"board:general"})
	if !conn.wasUnsubscribed(natsSubjectForScope("board:general")) {
		t.Fatalf("board:general upstream subscription was not removed")
	}
	bus.Unsubscribe(subB)
	if !conn.wasUnsubscribed(natsSubjectForScope("chat:lobby")) {
		t.Fatalf("chat:lobby upstream subscription was not removed")
	}
	if conn.wasUnsubscribed(natsSubjectForScope("thread:thr_1")) {
		t.Fatalf("thread:thr_1 unsubscribed while subA still referenced it")
	}
	bus.Unsubscribe(subA)
	if !conn.wasUnsubscribed(natsSubjectForScope("thread:thr_1")) {
		t.Fatalf("thread:thr_1 upstream subscription was not removed after final subscriber")
	}

	conn.resetUnsubscribed()
	subC := bus.Subscribe([]string{"presence:global"})
	if got := conn.subscribedSubjects(); !slices.Contains(got, natsSubjectForScope("presence:global")) {
		t.Fatalf("presence scope was not subscribed: %#v", got)
	}
	bus.Unsubscribe(subC)

	cancel()
	deadline := time.After(time.Second)
	for {
		if conn.wasUnsubscribed(natsSubjectForScope("presence:global")) {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for unsubscribe")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestNATSOutageKeepsDurableReplayAuthoritative(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "nats-outage-replay.db")
	writerConn := &fakeNATSConn{publishErr: errors.New("nats unavailable")}
	writer, err := New(dbPath, WithBus(NewNATSBus(writerConn, "node_writer"), true))
	if err != nil {
		t.Fatalf("new writer core: %v", err)
	}
	defer writer.DB.Close()
	go writer.Run(ctx)

	readerConn := &fakeNATSConn{}
	reader, err := New(dbPath, WithBus(NewNATSBus(readerConn, "node_reader"), true))
	if err != nil {
		t.Fatalf("new reader core: %v", err)
	}
	defer reader.DB.Close()
	go reader.Run(ctx)

	readerLive := reader.Subscribe([]string{"board:general"})
	defer reader.Unsubscribe(readerLive)

	alice, err := writer.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	raw := marshalCoreTestJSON(t, "marshal create thread", proto.CreateThreadPayload{
		Board: "general",
		Title: "nats outage replay",
		Body:  "durable replay repairs missing live fanout",
	})

	failuresBefore := metrics.RemotePublishFailures.Value()
	reply := writer.ExecCmd(ctx, alice, proto.CmdCreateThread, raw, "cid-nats-outage")
	if reply.Err != nil {
		t.Fatalf("create thread through writer: %+v", reply.Err)
	}
	if reply.Result == nil || reply.Result.Seq == 0 {
		t.Fatalf("create thread result = %+v, want durable seq", reply.Result)
	}
	if got := metrics.RemotePublishFailures.Value() - failuresBefore; got == 0 {
		t.Fatal("expected failed NATS publish to increment remote publish failures")
	}

	select {
	case evt := <-readerLive.Ch:
		t.Fatalf("reader received live event despite failed NATS publish: %+v", evt)
	default:
	}

	replayed, err := reader.Replay(0, []string{"board:general"}, 10)
	if err != nil {
		t.Fatalf("reader replay: %v", err)
	}
	var found bool
	for _, evt := range replayed {
		if evt.Seq == reply.Result.Seq {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("reader replay events = %+v, want acknowledged seq %d after NATS outage", replayed, reply.Result.Seq)
	}
}

type fakeNATSConn struct {
	mu         sync.Mutex
	subjects   []string
	payloads   [][]byte
	handlers   map[string]func([]byte)
	unsubs     map[string]int
	publishErr error
}

func (c *fakeNATSConn) Publish(ctx context.Context, subject string, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.publishErr != nil {
		return c.publishErr
	}
	c.subjects = append(c.subjects, subject)
	c.payloads = append(c.payloads, append([]byte(nil), payload...))
	return nil
}

func (c *fakeNATSConn) Subscribe(subject string, handler func(data []byte)) (func() error, error) {
	c.mu.Lock()
	if c.handlers == nil {
		c.handlers = map[string]func([]byte){}
	}
	if c.unsubs == nil {
		c.unsubs = map[string]int{}
	}
	c.handlers[subject] = handler
	c.mu.Unlock()
	return func() error {
		c.mu.Lock()
		c.unsubs[subject]++
		delete(c.handlers, subject)
		c.mu.Unlock()
		return nil
	}, nil
}

func (c *fakeNATSConn) publishedSubjects() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.subjects...)
}

func (c *fakeNATSConn) publishedPayloads() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.payloads))
	for i := range c.payloads {
		out[i] = append([]byte(nil), c.payloads[i]...)
	}
	return out
}

func (c *fakeNATSConn) subscribedSubjects() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.handlers))
	for subject := range c.handlers {
		out = append(out, subject)
	}
	return sortStrings(out)
}

func (c *fakeNATSConn) wasUnsubscribed(subject string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unsubs[subject] > 0
}

func (c *fakeNATSConn) resetUnsubscribed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unsubs = map[string]int{}
}

func sortStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
