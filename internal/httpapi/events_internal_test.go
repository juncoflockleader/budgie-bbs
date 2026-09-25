package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestDeliverSSEEventRepairsDurableGap(t *testing.T) {
	c, err := core.New(filepath.Join(t.TempDir(), "sse-gap.db"))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	t.Cleanup(func() { _ = c.DB.Close() })

	store := core.NewSQLEventStore(c.DB)
	ctx := context.Background()
	evt1, err := appendTestSSEEvent(ctx, store, "thr_sse_1", "first")
	if err != nil {
		t.Fatalf("append first event: %v", err)
	}
	evt2, err := appendTestSSEEvent(ctx, store, "thr_sse_2", "missing")
	if err != nil {
		t.Fatalf("append missing event: %v", err)
	}
	evt3, err := appendTestSSEEvent(ctx, store, "thr_sse_3", "current")
	if err != nil {
		t.Fatalf("append current event: %v", err)
	}

	srv := &Server{core: c}
	lastDurableCursor := proto.CursorFromEvent(evt1)
	repairsBefore := metrics.GatewayReplayRepairs.Value()
	rec := httptest.NewRecorder()
	if err := srv.deliverSSEEvent(rec, evt3, []string{"board:general"}, &lastDurableCursor); err != nil {
		t.Fatalf("deliver SSE event: %v", err)
	}
	if lastDurableCursor.Seq != evt3.Seq {
		t.Fatalf("last durable seq = %d, want %d", lastDurableCursor.Seq, evt3.Seq)
	}
	if offset, ok := lastDurableCursor.PartitionOffset("board", "general"); !ok || offset != evt3.PartitionOffset {
		t.Fatalf("last durable partition offset = %d ok=%v, want %d", offset, ok, evt3.PartitionOffset)
	}
	if got := metrics.GatewayReplayRepairs.Value() - repairsBefore; got != 1 {
		t.Fatalf("gateway replay repairs delta = %d, want 1", got)
	}

	body := rec.Body.String()
	missingID := fmt.Sprintf("id: %d", evt2.Seq)
	currentID := fmt.Sprintf("id: %d", evt3.Seq)
	if !strings.Contains(body, missingID) || !strings.Contains(body, currentID) {
		t.Fatalf("SSE body missing repaired/current events:\n%s", body)
	}
	if strings.Index(body, missingID) > strings.Index(body, currentID) {
		t.Fatalf("SSE body delivered current event before repaired event:\n%s", body)
	}
	if strings.Contains(body, fmt.Sprintf("id: %d", evt1.Seq)) {
		t.Fatalf("SSE body re-delivered already seen event:\n%s", body)
	}
}

func TestDeliverSSEEventRepairsPartitionGapAfterScalarAdvanced(t *testing.T) {
	c, err := core.New(filepath.Join(t.TempDir(), "sse-partition-gap.db"))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	t.Cleanup(func() { _ = c.DB.Close() })

	store := core.NewSQLEventStore(c.DB)
	ctx := context.Background()
	evt1, err := appendTestSSEBoardEvent(ctx, store, "thr_partition_gap_1", "general", "first general")
	if err != nil {
		t.Fatalf("append first event: %v", err)
	}
	evt2, err := appendTestSSEBoardEvent(ctx, store, "thr_partition_gap_2", "general", "missing general")
	if err != nil {
		t.Fatalf("append missing event: %v", err)
	}
	evtOther, err := appendTestSSEBoardEvent(ctx, store, "thr_partition_gap_life", "life", "other partition")
	if err != nil {
		t.Fatalf("append other partition event: %v", err)
	}
	evtCurrent, err := appendTestSSEBoardEvent(ctx, store, "thr_partition_gap_3", "general", "current general")
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

	srv := &Server{core: c}
	repairsBefore := metrics.GatewayReplayRepairs.Value()
	rec := httptest.NewRecorder()
	if err := srv.deliverSSEEvent(rec, evtCurrent, []string{"board:general"}, &cursor); err != nil {
		t.Fatalf("deliver SSE event: %v", err)
	}
	if got := metrics.GatewayReplayRepairs.Value() - repairsBefore; got != 1 {
		t.Fatalf("gateway replay repairs delta = %d, want 1", got)
	}

	body := rec.Body.String()
	missingID := fmt.Sprintf("id: %d", evt2.Seq)
	currentID := fmt.Sprintf("id: %d", evtCurrent.Seq)
	if !strings.Contains(body, missingID) || !strings.Contains(body, currentID) {
		t.Fatalf("SSE body missing partition repair/current events:\n%s", body)
	}
	if strings.Contains(body, fmt.Sprintf("id: %d", evt1.Seq)) {
		t.Fatalf("SSE body re-delivered already seen event:\n%s", body)
	}
	if strings.Contains(body, fmt.Sprintf("id: %d", evtOther.Seq)) {
		t.Fatalf("SSE body delivered other-scope event:\n%s", body)
	}
	if strings.Index(body, missingID) > strings.Index(body, currentID) {
		t.Fatalf("SSE body delivered current event before repaired event:\n%s", body)
	}
	if offset, ok := cursor.PartitionOffset("board", "general"); !ok || offset != evtCurrent.PartitionOffset {
		t.Fatalf("cursor general offset = %d ok=%v, want %d", offset, ok, evtCurrent.PartitionOffset)
	}
}

func TestSSEStreamAcceptsPartitionOnlyCursor(t *testing.T) {
	c, err := core.New(filepath.Join(t.TempDir(), "sse-partition-cursor.db"))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	t.Cleanup(func() { _ = c.DB.Close() })

	store := core.NewSQLEventStore(c.DB)
	ctx := context.Background()
	evt1, err := appendTestSSEEvent(ctx, store, "thr_sse_partition_cursor_1", "already delivered")
	if err != nil {
		t.Fatalf("append first event: %v", err)
	}
	evt2, err := appendTestSSEEvent(ctx, store, "thr_sse_partition_cursor_2", "replayed by partition cursor")
	if err != nil {
		t.Fatalf("append second event: %v", err)
	}

	cursor := proto.CursorFromEvent(evt1)
	cursor.Seq = 0
	rawCursor, err := json.Marshal(cursor)
	if err != nil {
		t.Fatalf("marshal cursor: %v", err)
	}

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream?scope=board:general&cursor="+url.QueryEscape(string(rawCursor)), nil).WithContext(reqCtx)
	rec := httptest.NewRecorder()

	(&Server{core: c}).handleEventsStream(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("SSE status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, fmt.Sprintf("id: %d", evt1.Seq)) {
		t.Fatalf("SSE body re-delivered acknowledged event:\n%s", body)
	}
	if !strings.Contains(body, fmt.Sprintf("id: %d", evt2.Seq)) {
		t.Fatalf("SSE body missing partition-cursor replay:\n%s", body)
	}
}

func TestSSEGatewayRestartStormReplaysFromLastEventID(t *testing.T) {
	c, err := core.New(filepath.Join(t.TempDir(), "sse-restart-storm.db"))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	t.Cleanup(func() { _ = c.DB.Close() })

	store := core.NewSQLEventStore(c.DB)
	ctx := context.Background()
	evt1, err := appendTestSSEEvent(ctx, store, "thr_restart_1", "already delivered")
	if err != nil {
		t.Fatalf("append already-delivered event: %v", err)
	}
	evt2, err := appendTestSSEEvent(ctx, store, "thr_restart_2", "replayed after restart")
	if err != nil {
		t.Fatalf("append replayed event: %v", err)
	}
	evt3, err := appendTestSSEEvent(ctx, store, "thr_restart_3", "also replayed")
	if err != nil {
		t.Fatalf("append second replayed event: %v", err)
	}

	srv := &Server{core: c}
	reconnectsBefore := metrics.GatewayReconnects.Value()
	for i := 0; i < 25; i++ {
		reqCtx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream?scope=board:general", nil).WithContext(reqCtx)
		req.Header.Set("Last-Event-ID", strconv.FormatInt(evt1.Seq, 10))
		rec := httptest.NewRecorder()

		srv.handleEventsStream(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("restart %d SSE status = %d body=%s", i, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		alreadyID := fmt.Sprintf("id: %d", evt1.Seq)
		replayedID := fmt.Sprintf("id: %d", evt2.Seq)
		currentID := fmt.Sprintf("id: %d", evt3.Seq)
		if strings.Contains(body, alreadyID) {
			t.Fatalf("restart %d re-delivered acknowledged event %s:\n%s", i, alreadyID, body)
		}
		if !strings.Contains(body, replayedID) || !strings.Contains(body, currentID) {
			t.Fatalf("restart %d missing replayed events %s/%s:\n%s", i, replayedID, currentID, body)
		}
		if strings.Index(body, replayedID) > strings.Index(body, currentID) {
			t.Fatalf("restart %d delivered replay out of order:\n%s", i, body)
		}
	}
	if got := metrics.GatewayReconnects.Value() - reconnectsBefore; got != 25 {
		t.Fatalf("gateway reconnect metric delta = %d, want 25", got)
	}
}

func appendTestSSEEvent(ctx context.Context, store core.EventStore, id, title string) (*proto.Event, error) {
	return appendTestSSEBoardEvent(ctx, store, id, "general", title)
}

func appendTestSSEBoardEvent(ctx context.Context, store core.EventStore, id, board, title string) (*proto.Event, error) {
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
