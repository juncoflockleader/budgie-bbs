package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMeiliPostSearchIndexSendsDocumentsWaitsAndSearches(t *testing.T) {
	transport := &meiliTestTransport{}
	client := &http.Client{Transport: transport}
	transport.handler = func(r *http.Request, data []byte) (int, any) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want bearer key", got)
		}
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/indexes/posts/documents":
			var docs []PostSearchDocument
			if err := json.Unmarshal(data, &docs); err != nil {
				t.Fatalf("decode upsert: %v", err)
			}
			if len(docs) != 1 || docs[0].ID != "post1" || docs[0].BoardID != "general" {
				t.Fatalf("upsert docs = %+v", docs)
			}
			return http.StatusOK, map[string]any{"taskUid": 1}
		case r.Method == http.MethodDelete && r.URL.Path == "/indexes/posts/documents/post1":
			return http.StatusOK, map[string]any{"taskUid": 2}
		case r.Method == http.MethodDelete && r.URL.Path == "/indexes/posts/documents":
			return http.StatusOK, map[string]any{"taskUid": 3}
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
			return http.StatusOK, map[string]any{"status": "succeeded"}
		case r.Method == http.MethodPost && r.URL.Path == "/indexes/posts/search":
			var req meiliSearchRequest
			if err := json.Unmarshal(data, &req); err != nil {
				t.Fatalf("decode search: %v", err)
			}
			if req.Query != "needle" || req.Limit != 7 || req.Filter != `board_id = "general"` {
				t.Fatalf("search request = %+v", req)
			}
			return http.StatusOK, map[string]any{"hits": []map[string]string{{"id": "post1"}}}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return http.StatusInternalServerError, map[string]string{"message": "unreachable"}
	}

	index, err := NewMeiliPostSearchIndex(MeiliPostSearchIndexOptions{
		Endpoint:     "http://meili.test",
		APIKey:       "test-key",
		Index:        "posts",
		Client:       client,
		TaskTimeout:  time.Second,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMeiliPostSearchIndex: %v", err)
	}
	ctx := context.Background()
	if err := index.UpsertPost(ctx, PostSearchDocument{ID: "post1", PostID: "post1", BoardID: "general", Body: "needle"}); err != nil {
		t.Fatalf("UpsertPost: %v", err)
	}
	ids, err := index.Search(ctx, "needle", "general", 7)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(ids) != 1 || ids[0] != "post1" {
		t.Fatalf("search ids = %v", ids)
	}
	if err := index.DeletePost(ctx, "post1"); err != nil {
		t.Fatalf("DeletePost: %v", err)
	}
	if err := index.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	want := []string{
		"PUT /indexes/posts/documents",
		"GET /tasks/1",
		"POST /indexes/posts/search",
		"DELETE /indexes/posts/documents/post1",
		"GET /tasks/2",
		"DELETE /indexes/posts/documents",
		"GET /tasks/3",
	}
	if len(transport.requests) != len(want) {
		t.Fatalf("requests = %v, want %v", transport.requests, want)
	}
	for i := range want {
		if transport.requests[i] != want[i] {
			t.Fatalf("requests = %v, want %v", transport.requests, want)
		}
	}
}

type meiliTestTransport struct {
	mu       sync.Mutex
	requests []string
	handler  func(*http.Request, []byte) (int, any)
}

func (t *meiliTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var data []byte
	if r.Body != nil {
		var err error
		data, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
	}
	t.mu.Lock()
	t.requests = append(t.requests, r.Method+" "+r.URL.Path)
	t.mu.Unlock()
	status, body := t.handler(r, data)
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(encoded)),
		Request:    r,
	}, nil
}
