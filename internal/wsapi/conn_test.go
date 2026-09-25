package wsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestGatewayCommandPolicy(t *testing.T) {
	if !New(nil, nil).allowCommands {
		t.Fatalf("default API websocket should allow command frames")
	}
	if NewGateway(nil, nil, false).allowCommands {
		t.Fatalf("live gateway websocket without authoritative command log should reject command frames")
	}
	if !NewGateway(nil, nil, true).allowCommands {
		t.Fatalf("gateway websocket with authoritative command log should allow command frames")
	}
}

func TestWSDeliverEventSkipsDuplicateDurableEvent(t *testing.T) {
	c := newWSReplayTestCore(t, "ws-duplicate.db")
	store := core.NewSQLEventStore(c.DB)
	ctx := context.Background()
	evt, err := appendWSReplayTestEvent(ctx, store, "thr_ws_duplicate", "general", "already delivered")
	if err != nil {
		t.Fatalf("append event: %v", err)
	}

	writes := []proto.OutboundMessage{}
	conn := &wsConn{
		core:   c,
		scopes: []string{"board:general"},
		cursor: proto.CursorFromEvent(evt),
		writeJSON: func(v any) error {
			msg, ok := v.(proto.OutboundMessage)
			if !ok {
				return fmt.Errorf("unexpected websocket write type %T", v)
			}
			writes = append(writes, msg)
			return nil
		},
	}
	if err := conn.deliverEvent(evt); err != nil {
		t.Fatalf("deliver duplicate event: %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("duplicate event wrote %d messages: %+v", len(writes), writes)
	}
	if conn.cursor.Seq != evt.Seq {
		t.Fatalf("cursor seq = %d, want %d", conn.cursor.Seq, evt.Seq)
	}
	if offset, ok := conn.cursor.PartitionOffset("board", "general"); !ok || offset != evt.PartitionOffset {
		t.Fatalf("cursor partition offset = %d ok=%v, want %d", offset, ok, evt.PartitionOffset)
	}
}

func TestWSDeliverEventRepairsPartitionGapBeforeCurrent(t *testing.T) {
	c := newWSReplayTestCore(t, "ws-partition-gap.db")
	store := core.NewSQLEventStore(c.DB)
	ctx := context.Background()
	evt1, err := appendWSReplayTestEvent(ctx, store, "thr_ws_gap_1", "general", "first general")
	if err != nil {
		t.Fatalf("append first event: %v", err)
	}
	evtMissing, err := appendWSReplayTestEvent(ctx, store, "thr_ws_gap_2", "general", "missing general")
	if err != nil {
		t.Fatalf("append missing event: %v", err)
	}
	evtOther, err := appendWSReplayTestEvent(ctx, store, "thr_ws_gap_life", "life", "other partition")
	if err != nil {
		t.Fatalf("append other event: %v", err)
	}
	evtCurrent, err := appendWSReplayTestEvent(ctx, store, "thr_ws_gap_3", "general", "current general")
	if err != nil {
		t.Fatalf("append current event: %v", err)
	}

	cursor := proto.CursorFromEvent(evt1)
	cursor.ObserveEvent(evtOther)
	if cursor.ScalarGapBeforeEvent(evtCurrent) {
		t.Fatal("test setup expected scalar seq to be contiguous")
	}
	if !cursor.PartitionGapBeforeEvent(evtCurrent) {
		t.Fatal("test setup expected partition offset gap")
	}

	writes := []proto.OutboundMessage{}
	conn := &wsConn{
		core:   c,
		scopes: []string{"board:general"},
		cursor: cursor,
		writeJSON: func(v any) error {
			msg, ok := v.(proto.OutboundMessage)
			if !ok {
				return fmt.Errorf("unexpected websocket write type %T", v)
			}
			writes = append(writes, msg)
			return nil
		},
	}
	repairsBefore := metrics.GatewayReplayRepairs.Value()
	if err := conn.deliverEvent(evtCurrent); err != nil {
		t.Fatalf("deliver event with gap: %v", err)
	}
	if got := metrics.GatewayReplayRepairs.Value() - repairsBefore; got != 1 {
		t.Fatalf("gateway replay repairs delta = %d, want 1", got)
	}
	if len(writes) != 2 {
		t.Fatalf("writes = %+v, want repaired and current events", writes)
	}
	if writes[0].Seq != evtMissing.Seq || writes[1].Seq != evtCurrent.Seq {
		t.Fatalf("write seqs = %d,%d want %d,%d", writes[0].Seq, writes[1].Seq, evtMissing.Seq, evtCurrent.Seq)
	}
	for _, msg := range writes {
		if msg.Seq == evt1.Seq || msg.Seq == evtOther.Seq {
			t.Fatalf("websocket re-delivered already seen or other-scope event: %+v", writes)
		}
	}
	if conn.cursor.Seq != evtCurrent.Seq {
		t.Fatalf("cursor seq = %d, want %d", conn.cursor.Seq, evtCurrent.Seq)
	}
	if offset, ok := conn.cursor.PartitionOffset("board", "general"); !ok || offset != evtCurrent.PartitionOffset {
		t.Fatalf("cursor general offset = %d ok=%v, want %d", offset, ok, evtCurrent.PartitionOffset)
	}
}

func TestWSMidSessionResumeReplaysNewScopeFromPartitionCursor(t *testing.T) {
	c := newWSReplayTestCore(t, "ws-mid-resume-partition.db")
	store := core.NewSQLEventStore(c.DB)
	ctx := context.Background()
	_, err := appendWSReplayTestEvent(ctx, store, "thr_ws_mid_general", "general", "general event")
	if err != nil {
		t.Fatalf("append general event: %v", err)
	}
	lifeSeen, err := appendWSReplayTestEvent(ctx, store, "thr_ws_mid_life_seen", "life", "already seen life")
	if err != nil {
		t.Fatalf("append seen life event: %v", err)
	}
	lifeReplay, err := appendWSReplayTestEvent(ctx, store, "thr_ws_mid_life_replay", "life", "replay life")
	if err != nil {
		t.Fatalf("append replay life event: %v", err)
	}

	oldSub := c.Subscribe([]string{"board:general"})
	writes := []proto.OutboundMessage{}
	cursor := proto.CursorFromEvent(lifeSeen)
	cursor.Seq = 0
	conn := &wsConn{
		core:   c,
		sub:    oldSub,
		scopes: []string{"board:general"},
		cursor: proto.CursorFromEvent(lifeSeen),
		writeJSON: func(v any) error {
			msg, ok := v.(proto.OutboundMessage)
			if !ok {
				return fmt.Errorf("unexpected websocket write type %T", v)
			}
			writes = append(writes, msg)
			return nil
		},
	}
	payload, err := json.Marshal(proto.ResumePayload{
		Cursor:        &cursor,
		Subscriptions: []string{"board:life"},
	})
	if err != nil {
		t.Fatalf("marshal resume payload: %v", err)
	}
	conn.handleInbound(ctx, proto.InboundMessage{
		Kind:    "control",
		Control: "resume",
		Payload: payload,
	})
	if len(writes) != 1 || writes[0].Seq != lifeReplay.Seq {
		t.Fatalf("mid-session resume writes = %+v, want only life replay seq %d", writes, lifeReplay.Seq)
	}
	if conn.sub == nil || conn.sub == oldSub {
		t.Fatalf("resume did not replace subscription")
	}
	select {
	case _, ok := <-oldSub.Ch:
		if ok {
			t.Fatal("old subscription channel still open after resume")
		}
	default:
		t.Fatal("old subscription channel was not closed by resume")
	}
	if len(conn.scopes) != 1 || conn.scopes[0] != "board:life" {
		t.Fatalf("scopes = %+v, want board:life", conn.scopes)
	}
	if offset, ok := conn.cursor.PartitionOffset("board", "life"); !ok || offset != lifeReplay.PartitionOffset {
		t.Fatalf("cursor life offset = %d ok=%v, want %d", offset, ok, lifeReplay.PartitionOffset)
	}
}

func TestWSMidSessionScopeUpdateWithoutCursorKeepsCurrentCursor(t *testing.T) {
	c := newWSReplayTestCore(t, "ws-mid-resume-keep-cursor.db")
	store := core.NewSQLEventStore(c.DB)
	ctx := context.Background()
	evt, err := appendWSReplayTestEvent(ctx, store, "thr_ws_mid_keep", "general", "already delivered")
	if err != nil {
		t.Fatalf("append event: %v", err)
	}

	writes := []proto.OutboundMessage{}
	conn := &wsConn{
		core:   c,
		sub:    c.Subscribe([]string{"board:general"}),
		scopes: []string{"board:general"},
		cursor: proto.CursorFromEvent(evt),
		writeJSON: func(v any) error {
			msg, ok := v.(proto.OutboundMessage)
			if !ok {
				return fmt.Errorf("unexpected websocket write type %T", v)
			}
			writes = append(writes, msg)
			return nil
		},
	}
	payload, err := json.Marshal(proto.ResumePayload{
		Subscriptions: []string{"board:general"},
	})
	if err != nil {
		t.Fatalf("marshal resume payload: %v", err)
	}
	conn.handleInbound(ctx, proto.InboundMessage{
		Kind:    "control",
		Control: "resume",
		Payload: payload,
	})
	if len(writes) != 0 {
		t.Fatalf("scope-only resume replayed already delivered events: %+v", writes)
	}
	if conn.cursor.Seq != evt.Seq {
		t.Fatalf("cursor seq = %d, want %d", conn.cursor.Seq, evt.Seq)
	}
}

func newWSReplayTestCore(t *testing.T, name string) *core.Core {
	t.Helper()
	c, err := core.New(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	t.Cleanup(func() { _ = c.DB.Close() })
	// Seed the public boards these replay tests scope events to, so the
	// gateway's scope-authorization (board read access) permits subscribing.
	for _, b := range []string{"general", "life"} {
		if _, err := c.DB.Exec(`INSERT OR IGNORE INTO boards (id, name) VALUES (?, ?)`, b, b); err != nil {
			t.Fatalf("seed board %q: %v", b, err)
		}
	}
	return c
}

func appendWSReplayTestEvent(ctx context.Context, store core.EventStore, id, board, title string) (*proto.Event, error) {
	return store.Append(ctx, core.EventAppend{
		ID:     "evt_" + id,
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:" + board},
		Payload: &proto.ThreadNewPayload{
			ID:     id,
			Board:  board,
			Author: "alice",
			Title:  title,
			TS:     1000,
		},
	})
}
