package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestEventsResponseCarriesVectorCursorAndAcceptsCursorParam(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	token := registerUser(t, handler, "alice")

	create := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "cursor thread",
		"body":  "hello cursor",
	}, &create); status != http.StatusCreated {
		t.Fatalf("create thread status: %d error=%+v", status, create.Error)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/v1/events?after=0&scope=board:general", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("events status: %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Log-Cursor") == "" {
		t.Fatal("missing X-Log-Cursor header")
	}
	if rec.Header().Get("X-Log-Delivered-Cursor") == "" {
		t.Fatal("missing X-Log-Delivered-Cursor header")
	}

	var first struct {
		Events          []proto.OutboundMessage `json:"events"`
		Head            int64                   `json:"head"`
		Cursor          proto.Cursor            `json:"cursor"`
		DeliveredCursor proto.Cursor            `json:"deliveredCursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first events: %v", err)
	}
	if first.Head == 0 || first.Cursor.Seq != first.Head {
		t.Fatalf("head/cursor mismatch: head=%d cursor=%+v", first.Head, first.Cursor)
	}
	if len(first.Cursor.Partitions) == 0 {
		t.Fatalf("cursor = %+v, want partition offsets", first.Cursor)
	}
	if offset, ok := first.DeliveredCursor.PartitionOffset("board", "general"); !ok || offset <= 0 {
		t.Fatalf("delivered cursor general offset = %d ok=%v cursor=%+v", offset, ok, first.DeliveredCursor)
	}

	cursorRaw, err := json.Marshal(first.Cursor)
	if err != nil {
		t.Fatalf("marshal cursor: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "http://example.com/api/v1/events?cursor="+url.QueryEscape(string(cursorRaw))+"&scope=board:general", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("events with cursor status: %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "thread.new") {
		t.Fatalf("cursor replay returned already-seen event: %s", rec.Body.String())
	}
}

func TestEventsAcceptsPartitionOnlyCursor(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	token := registerUser(t, handler, "alice")

	firstThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "partition cursor first",
		"body":  "already seen",
	}, &firstThread); status != http.StatusCreated {
		t.Fatalf("create first thread status: %d error=%+v", status, firstThread.Error)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/v1/events?after=0&scope=board:general", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("events status: %d body=%s", rec.Code, rec.Body.String())
	}
	var first struct {
		Cursor          proto.Cursor `json:"cursor"`
		DeliveredCursor proto.Cursor `json:"deliveredCursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first cursor: %v", err)
	}
	if len(first.Cursor.Partitions) == 0 {
		t.Fatalf("first cursor = %+v, want partition offsets", first.Cursor)
	}
	if first.DeliveredCursor.Seq != 0 {
		t.Fatalf("scoped delivered cursor seq = %d, want partition-only", first.DeliveredCursor.Seq)
	}
	if offset, ok := first.DeliveredCursor.PartitionOffset("board", "general"); !ok || offset <= 0 {
		t.Fatalf("first delivered cursor general offset = %d ok=%v cursor=%+v", offset, ok, first.DeliveredCursor)
	}

	secondThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "partition cursor second",
		"body":  "replay me",
	}, &secondThread); status != http.StatusCreated {
		t.Fatalf("create second thread status: %d error=%+v", status, secondThread.Error)
	}

	cursor := first.Cursor
	cursor.Seq = 0
	cursorRaw, err := json.Marshal(cursor)
	if err != nil {
		t.Fatalf("marshal partition cursor: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "http://example.com/api/v1/events?cursor="+url.QueryEscape(string(cursorRaw))+"&scope=board:general", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("events with partition cursor status: %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "partition cursor first") {
		t.Fatalf("partition cursor replay returned already-seen event: %s", body)
	}
	if !strings.Contains(body, "partition cursor second") {
		t.Fatalf("partition cursor replay body = %s, want second event", body)
	}
	var second struct {
		DeliveredCursor proto.Cursor `json:"deliveredCursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second cursor: %v", err)
	}
	if second.DeliveredCursor.Seq != 0 {
		t.Fatalf("partition replay delivered cursor seq = %d, want partition-only", second.DeliveredCursor.Seq)
	}
	if offset, ok := second.DeliveredCursor.PartitionOffset("board", "general"); !ok || offset <= 0 {
		t.Fatalf("second delivered cursor general offset = %d ok=%v cursor=%+v", offset, ok, second.DeliveredCursor)
	}
}

func TestEventsDeliveredCursorDoesNotMarkUnrequestedPartitionsSeen(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	adminToken := registerUser(t, handler, "admin")

	createLife := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "life",
		"name":        "Life",
		"description": "Life board",
	}, &createLife); status != http.StatusCreated {
		t.Fatalf("create life board status: %d error=%+v", status, createLife.Error)
	}

	generalThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "general delivered",
		"body":  "visible in scoped poll",
	}, &generalThread); status != http.StatusCreated {
		t.Fatalf("create general thread status: %d error=%+v", status, generalThread.Error)
	}

	lifeThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/life/threads", adminToken, map[string]string{
		"title": "life not delivered",
		"body":  "not in general scope",
	}, &lifeThread); status != http.StatusCreated {
		t.Fatalf("create life thread status: %d error=%+v", status, lifeThread.Error)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/v1/events?after=0&scope=board:general", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("events status: %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "life not delivered") {
		t.Fatalf("general-scoped poll returned life event: %s", rec.Body.String())
	}

	var got struct {
		Cursor          proto.Cursor `json:"cursor"`
		DeliveredCursor proto.Cursor `json:"deliveredCursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode events response: %v", err)
	}
	if _, ok := got.Cursor.PartitionOffset("board", "life"); !ok {
		t.Fatalf("head cursor = %+v, want life partition from global head", got.Cursor)
	}
	if got.DeliveredCursor.Seq != 0 {
		t.Fatalf("delivered cursor seq = %d, want partition-only for scoped delivery", got.DeliveredCursor.Seq)
	}
	if offset, ok := got.DeliveredCursor.PartitionOffset("board", "general"); !ok || offset <= 0 {
		t.Fatalf("delivered cursor general offset = %d ok=%v cursor=%+v", offset, ok, got.DeliveredCursor)
	}
	if _, ok := got.DeliveredCursor.PartitionOffset("board", "life"); ok {
		t.Fatalf("delivered cursor = %+v, should not mark life partition seen", got.DeliveredCursor)
	}

	headerCursor := proto.Cursor{}
	if err := json.Unmarshal([]byte(rec.Header().Get("X-Log-Delivered-Cursor")), &headerCursor); err != nil {
		t.Fatalf("decode X-Log-Delivered-Cursor: %v", err)
	}
	if headerCursor.Seq != got.DeliveredCursor.Seq {
		t.Fatalf("header delivered seq = %d, body seq = %d", headerCursor.Seq, got.DeliveredCursor.Seq)
	}
	if offset, ok := headerCursor.PartitionOffset("board", "general"); !ok || offset <= 0 {
		t.Fatalf("header delivered general offset = %d ok=%v cursor=%+v", offset, ok, headerCursor)
	}
	if _, ok := headerCursor.PartitionOffset("board", "life"); ok {
		t.Fatalf("header delivered cursor = %+v, should not mark life partition seen", headerCursor)
	}
}
