package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func TestUnreadSummaryReadsCarryConsistencyMeta(t *testing.T) {
	c, handler := setupHTTPTestServer(t)
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	create := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", bobToken, map[string]string{
		"title": "Unread summary freshness",
		"body":  "summary views can lag without hiding local state",
	}, &create); status != http.StatusCreated {
		t.Fatalf("create unread thread status: %d error=%+v", status, create.Error)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	staleApplied := head - 1
	if staleApplied < 0 {
		staleApplied = 0
	}
	for _, view := range []string{projections.DerivedViewBoardSummaries, projections.DerivedViewUnreadThreads} {
		if err := c.RecordDerivedViewApplied(view, staleApplied); err != nil {
			t.Fatalf("record stale %s watermark: %v", view, err)
		}
	}

	summaryRec := getWithToken(t, handler, "/api/v1/boards/summary", aliceToken)
	if summaryRec.Code != http.StatusOK {
		t.Fatalf("board summary status: %d body=%s", summaryRec.Code, summaryRec.Body.String())
	}
	assertProjectionHeaders(t, summaryRec, projections.DerivedViewBoardSummaries)
	var summary struct {
		Boards []json.RawMessage `json:"boards"`
		Meta   projectionMetaDTO `json:"meta"`
	}
	if err := json.Unmarshal(summaryRec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode board summary: %v", err)
	}
	assertStaleProjectionMeta(t, summary.Meta, staleApplied, head)
	if len(summary.Boards) == 0 {
		t.Fatalf("expected board summary rows, got %+v", summary)
	}

	unreadBoardsRec := getWithToken(t, handler, "/api/v1/boards/unread", aliceToken)
	if unreadBoardsRec.Code != http.StatusOK {
		t.Fatalf("unread boards status: %d body=%s", unreadBoardsRec.Code, unreadBoardsRec.Body.String())
	}
	assertProjectionHeaders(t, unreadBoardsRec, projections.DerivedViewBoardSummaries)
	var unreadBoards struct {
		Boards []json.RawMessage `json:"boards"`
		Meta   projectionMetaDTO `json:"meta"`
	}
	if err := json.Unmarshal(unreadBoardsRec.Body.Bytes(), &unreadBoards); err != nil {
		t.Fatalf("decode unread boards: %v", err)
	}
	assertStaleProjectionMeta(t, unreadBoards.Meta, staleApplied, head)
	if len(unreadBoards.Boards) == 0 {
		t.Fatalf("expected unread board rows, got %+v", unreadBoards)
	}

	unreadThreadsRec := getWithToken(t, handler, "/api/v1/threads/unread", aliceToken)
	if unreadThreadsRec.Code != http.StatusOK {
		t.Fatalf("unread threads status: %d body=%s", unreadThreadsRec.Code, unreadThreadsRec.Body.String())
	}
	assertProjectionHeaders(t, unreadThreadsRec, projections.DerivedViewUnreadThreads)
	var unreadThreads struct {
		Threads []json.RawMessage `json:"threads"`
		Meta    projectionMetaDTO `json:"meta"`
	}
	if err := json.Unmarshal(unreadThreadsRec.Body.Bytes(), &unreadThreads); err != nil {
		t.Fatalf("decode unread threads: %v", err)
	}
	assertStaleProjectionMeta(t, unreadThreads.Meta, staleApplied, head)
	if len(unreadThreads.Threads) == 0 {
		t.Fatalf("expected unread thread rows, got %+v", unreadThreads)
	}
}

func assertStaleProjectionMeta(t *testing.T, meta projectionMetaDTO, applied, head int64) {
	t.Helper()
	assertProjectionMeta(t, meta)
	if meta.AppliedSeq != applied {
		t.Fatalf("applied seq = %d, want %d", meta.AppliedSeq, applied)
	}
	if meta.LagEvents != head-applied {
		t.Fatalf("lag = %d, want %d", meta.LagEvents, head-applied)
	}
}
