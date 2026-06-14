package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

func TestHTTPResidentBoardFeed(t *testing.T) {
	c, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	ack := ackResponse{}
	for _, board := range []map[string]string{
		{"id": "club", "name": "Club"},
		{"id": "lab", "name": "Lab"},
	} {
		if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, board, &ack); status != http.StatusCreated {
			t.Fatalf("create board %s status: %d error=%+v", board["id"], status, ack.Error)
		}
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/club/settings", adminToken, map[string]bool{
		"memberReadMode": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set club member-read status: %d error=%+v", status, ack.Error)
	}
	for _, board := range []string{"club", "lab"} {
		if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/"+board+"/members/alice", adminToken, map[string]string{
			"title": "resident",
		}, &ack); status != http.StatusCreated {
			t.Fatalf("add alice to %s status: %d error=%+v", board, status, ack.Error)
		}
	}

	generalThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", bobToken, map[string]string{
		"title": "General public",
		"body":  "ordinary board post",
	}, &generalThread); status != http.StatusCreated {
		t.Fatalf("create general thread status: %d error=%+v", status, generalThread.Error)
	}
	clubThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/threads", adminToken, map[string]string{
		"title": "Resident club",
		"body":  "member-read board post",
	}, &clubThread); status != http.StatusCreated {
		t.Fatalf("create club thread status: %d error=%+v", status, clubThread.Error)
	}
	labThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/lab/threads", bobToken, map[string]string{
		"title": "Resident lab",
		"body":  "public resident board post",
	}, &labThread); status != http.StatusCreated {
		t.Fatalf("create lab thread status: %d error=%+v", status, labThread.Error)
	}
	redactedThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/lab/threads", bobToken, map[string]string{
		"title": "Hidden lab",
		"body":  "redacted resident board post",
	}, &redactedThread); status != http.StatusCreated {
		t.Fatalf("create redacted lab thread status: %d error=%+v", status, redactedThread.Error)
	}
	redactedPosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+redactedThread.Result.ID+"/posts", aliceToken, nil, &redactedPosts); status != http.StatusOK {
		t.Fatalf("list redacted thread posts status: %d", status)
	}
	if len(redactedPosts.Posts) != 1 {
		t.Fatalf("expected one redaction target post, got %+v", redactedPosts.Posts)
	}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/posts/"+redactedPosts.Posts[0].ID, adminToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("redact lab post status: %d error=%+v", status, ack.Error)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	staleApplied := head - 1
	if staleApplied < 0 {
		staleApplied = 0
	}
	if err := c.RecordDerivedViewApplied(core.DerivedViewResidentFeed, staleApplied); err != nil {
		t.Fatalf("record stale resident feed watermark: %v", err)
	}

	feedRec := getWithToken(t, handler, "/api/v1/boards/resident-feed", aliceToken)
	if feedRec.Code != http.StatusOK {
		t.Fatalf("resident feed status: %d body=%s", feedRec.Code, feedRec.Body.String())
	}
	assertProjectionHeaders(t, feedRec, core.DerivedViewResidentFeed)
	feed := postsResponse{}
	if err := json.Unmarshal(feedRec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode resident feed: %v", err)
	}
	assertProjectionMeta(t, feed.Meta)
	if feed.Meta.AppliedSeq != staleApplied {
		t.Fatalf("resident feed applied seq = %d, want %d", feed.Meta.AppliedSeq, staleApplied)
	}
	if feed.Meta.LagEvents != head-staleApplied {
		t.Fatalf("resident feed lag = %d, want %d", feed.Meta.LagEvents, head-staleApplied)
	}
	if len(feed.Posts) != 2 {
		t.Fatalf("expected two resident feed posts, got %+v", feed.Posts)
	}
	byThread := map[string]struct {
		board string
		title string
		body  string
	}{}
	for _, post := range feed.Posts {
		byThread[post.Thread] = struct {
			board string
			title string
			body  string
		}{post.Board, post.ThreadTitle, post.Body}
		if post.Thread == generalThread.Result.ID || post.Thread == redactedThread.Result.ID {
			t.Fatalf("resident feed included nonresident/redacted thread: %+v", post)
		}
	}
	if got := byThread[clubThread.Result.ID]; got.board != "club" || got.title != "Resident club" || got.body != "member-read board post" {
		t.Fatalf("expected club resident post with context, got %+v", got)
	}
	if got := byThread[labThread.Result.ID]; got.board != "lab" || got.title != "Resident lab" || got.body != "public resident board post" {
		t.Fatalf("expected lab resident post with context, got %+v", got)
	}

	empty := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/resident-feed", carolToken, nil, &empty); status != http.StatusOK {
		t.Fatalf("carol resident feed status: %d", status)
	}
	if len(empty.Posts) != 0 {
		t.Fatalf("expected nonresident feed to be empty, got %+v", empty.Posts)
	}
}
