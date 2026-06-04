package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

type ackResult struct {
	ID  string `json:"id"`
	Seq int64  `json:"seq"`
}

type ackResponse struct {
	Kind   string `json:"kind"`
	OK     bool   `json:"ok"`
	CID    string `json:"cid"`
	Result *ackResult
	Error  *struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

type registerResponse struct {
	Token string `json:"token"`
	User  struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"user"`
}

type postPayload struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

type listPostsResponse struct {
	Posts []postPayload `json:"posts"`
}

type pollOption struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	VoteCount int    `json:"voteCount"`
}

type pollPayload struct {
	ID        string       `json:"id"`
	PostID    string       `json:"postId"`
	Question  string       `json:"question"`
	ExpiresAt int64        `json:"expiresAt"`
	TS        int64        `json:"ts"`
	Options   []pollOption `json:"options"`
	Voted     string       `json:"voted"`
}

type threadPollsResponse struct {
	Polls map[string]*pollPayload `json:"polls"`
}

func setupHTTPTestServer(t *testing.T) (*core.Core, http.Handler) {
	t.Helper()

	f, err := os.CreateTemp("", "budgie-httpapi-poll-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	dbPath := f.Name()
	f.Close()
	t.Cleanup(func() {
		_ = os.Remove(dbPath)
	})

	c, err := core.New(dbPath)
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	t.Cleanup(func() {
		cancel()
		_ = c.DB.Close()
	})

	return c, httpapi.New(c, []byte("test-secret")).Handler()
}

func registerUser(t *testing.T, handler http.Handler, name string) string {
	t.Helper()
	out := registerResponse{}
	status := doJSONRequest(t, handler, "POST", "/api/v1/auth/register", "", map[string]string{
		"name":     name,
		"password": "pw",
	}, &out)
	if status != http.StatusCreated {
		t.Fatalf("register %s status: %d", name, status)
	}
	return out.Token
}

func doJSONRequest(t *testing.T, handler http.Handler, method, path, token string, body any, out any) int {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = http.NoBody
	}

	req, err := http.NewRequest(method, "http://example.com"+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	res := rec.Result()
	if err != nil {
		t.Fatalf("response missing: %v", err)
	}

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			t.Fatalf("decode response: %v body=%s", err, string(payload))
		}
	}
	return res.StatusCode
}

func TestHTTPPollLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")

	threadBody := "Topline\n[poll]\nWhat's for lunch?\nTacos\nBurritos\n[/poll]\nBottomline"
	createAck := ackResponse{}
	createStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "Lunch ideas",
		"body":  threadBody,
	}, &createAck)
	if createStatus != http.StatusCreated {
		t.Fatalf("create thread status: %d", createStatus)
	}
	if !createAck.OK || createAck.Result == nil {
		t.Fatalf("create thread failed: %+v", createAck.Error)
	}
	threadID := createAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", token, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected 1 post in new thread, got %d", len(posts.Posts))
	}
	post := posts.Posts[0]
	if strings.Contains(post.Body, "[poll") {
		t.Fatalf("poll markup should be stripped from stored post body, got %q", post.Body)
	}
	if got := strings.TrimSpace(post.Body); got != "Topline\nBottomline" {
		t.Fatalf("expected stripped post body %q, got %q", "Topline\nBottomline", got)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", token, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	pollByPost := threadPolls.Polls[post.ID]
	if pollByPost == nil {
		t.Fatalf("expected poll attached to post %s", post.ID)
	}
	if pollByPost.Question != "What's for lunch?" {
		t.Fatalf("unexpected poll question: %q", pollByPost.Question)
	}
	if len(pollByPost.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(pollByPost.Options))
	}
	if pollByPost.ExpiresAt != 0 {
		t.Fatalf("expected non-expiring poll")
	}
	if pollByPost.Voted != "" {
		t.Fatalf("expected fresh poll without vote, got voted=%q", pollByPost.Voted)
	}

	getPoll := pollPayload{}
	getPollStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/posts/"+post.ID+"/poll", token, nil, &getPoll)
	if getPollStatus != http.StatusOK {
		t.Fatalf("get poll by post status: %d", getPollStatus)
	}
	if getPoll.ID != pollByPost.ID {
		t.Fatalf("expected same poll ID from thread list and post lookup")
	}

	vote := ackResponse{}
	voteStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+pollByPost.ID+"/vote", token, map[string]string{
		"option": pollByPost.Options[0].ID,
	}, &vote)
	if voteStatus != http.StatusCreated || !vote.OK {
		t.Fatalf("vote failed: status=%d ok=%v err=%+v", voteStatus, vote.OK, vote.Error)
	}

	getVoted := pollPayload{}
	votedStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/polls/"+pollByPost.ID, token, nil, &getVoted)
	if votedStatus != http.StatusOK {
		t.Fatalf("get poll status: %d", votedStatus)
	}
	if got, want := getVoted.Voted, pollByPost.Options[0].ID; got != want {
		t.Fatalf("expected voted option %q, got %q", want, got)
	}
	if got, want := getVoted.Options[0].VoteCount, 1; got != want {
		t.Fatalf("expected voted option count %d, got %d", want, got)
	}
}

func TestHTTPPollCreationRequiresTrustLevel(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	_ = registerUser(t, handler, "admin") // first user is admin and can create polls
	userToken := registerUser(t, handler, "alice")

	createPoll := "[poll]\nShould fail?\nYes\nNo\n[/poll]\nOops"
	ack := ackResponse{}
	createStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", userToken, map[string]string{
		"title": "No Trust Poll",
		"body":  createPoll,
	}, &ack)
	if createStatus != http.StatusForbidden {
		t.Fatalf("expected 403 for non-trusted poll creation, got %d", createStatus)
	}
	if ack.Error == nil || ack.Error.Code != "forbidden" {
		t.Fatalf("expected forbidden error payload, got %+v", ack.Error)
	}
}

func TestHTTPMalformedPollDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")

	threadBody := "[poll]\nMissing options?\nJust one\n[/poll]"
	createAck := ackResponse{}
	createStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "Bad Poll",
		"body":  threadBody,
	}, &createAck)
	if createStatus != http.StatusCreated {
		t.Fatalf("create thread status: %d", createStatus)
	}
	if !createAck.OK || createAck.Result == nil {
		t.Fatalf("thread create failed: %+v", createAck.Error)
	}

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/posts", token, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	post := posts.Posts[0]
	if !strings.Contains(post.Body, "[poll") {
		t.Fatalf("malformed poll should be preserved in post body")
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/polls", token, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed poll to produce no poll projection")
	}

	getPollStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/posts/"+post.ID+"/poll", token, nil, &pollPayload{})
	if getPollStatus != http.StatusNotFound {
		t.Fatalf("expected 404 for posts/%s/poll, got %d", post.ID, getPollStatus)
	}
}
