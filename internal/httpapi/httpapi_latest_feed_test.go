package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

func TestHTTPLatestFeedCarriesProjectionMetaAndVisibility(t *testing.T) {
	c, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":   "secret",
		"name": "Secret",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create secret board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/secret/settings", adminToken, map[string]bool{
		"memberReadMode": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set secret board member-read status: %d error=%+v", status, ack.Error)
	}

	publicThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", bobToken, map[string]string{
		"title": "Public latest",
		"body":  "visible global feed post",
	}, &publicThread); status != http.StatusCreated {
		t.Fatalf("create public thread status: %d error=%+v", status, publicThread.Error)
	}
	secretThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/secret/threads", adminToken, map[string]string{
		"title": "Secret latest",
		"body":  "hidden member-read feed post",
	}, &secretThread); status != http.StatusCreated {
		t.Fatalf("create secret thread status: %d error=%+v", status, secretThread.Error)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	staleApplied := head - 1
	if staleApplied < 0 {
		staleApplied = 0
	}
	if err := c.RecordDerivedViewApplied(core.DerivedViewLatestFeed, staleApplied); err != nil {
		t.Fatalf("record stale latest feed watermark: %v", err)
	}

	feedRec := getWithToken(t, handler, "/api/v1/feed/latest", aliceToken)
	if feedRec.Code != http.StatusOK {
		t.Fatalf("latest feed status: %d body=%s", feedRec.Code, feedRec.Body.String())
	}
	assertProjectionHeaders(t, feedRec, core.DerivedViewLatestFeed)
	feed := postsResponse{}
	if err := json.Unmarshal(feedRec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode latest feed: %v", err)
	}
	assertProjectionMeta(t, feed.Meta)
	if feed.Meta.AppliedSeq != staleApplied {
		t.Fatalf("latest feed applied seq = %d, want %d", feed.Meta.AppliedSeq, staleApplied)
	}
	if feed.Meta.LagEvents != head-staleApplied {
		t.Fatalf("latest feed lag = %d, want %d", feed.Meta.LagEvents, head-staleApplied)
	}
	assertHTTPFeedThreads(t, feed, []string{publicThread.Result.ID}, []string{secretThread.Result.ID})

	adminFeed := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/feed/latest", adminToken, nil, &adminFeed); status != http.StatusOK {
		t.Fatalf("admin latest feed status: %d", status)
	}
	assertHTTPFeedThreads(t, adminFeed, []string{publicThread.Result.ID, secretThread.Result.ID}, nil)
}

func assertHTTPFeedThreads(t *testing.T, feed postsResponse, wantThreads, absentThreads []string) {
	t.Helper()
	got := map[string]bool{}
	for _, post := range feed.Posts {
		got[post.Thread] = true
	}
	for _, threadID := range wantThreads {
		if !got[threadID] {
			t.Fatalf("feed threads = %v, want %s present; posts=%+v", got, threadID, feed.Posts)
		}
	}
	for _, threadID := range absentThreads {
		if got[threadID] {
			t.Fatalf("feed threads = %v, want %s absent; posts=%+v", got, threadID, feed.Posts)
		}
	}
}
