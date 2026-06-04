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
	"time"

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
	return doJSONRequestWithHeaders(t, handler, method, path, token, body, out, nil)
}

func doJSONRequestWithHeaders(t *testing.T, handler http.Handler, method, path, token string, body any, out any, headers map[string]string) int {
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
	if headers != nil {
		for key, value := range headers {
			req.Header.Set(key, value)
		}
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

func countThreadPosts(t *testing.T, handler http.Handler, token, threadID string) int {
	t.Helper()

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", token, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	return len(posts.Posts)
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

func TestHTTPCommandEndpointCreatesPoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	commandBody := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Command endpoint poll",
			"body":  "before\n[poll]\nFavorite color?\nBlue\nGreen\n[/poll]\nafter",
		},
	}

	commandAck := ackResponse{}
	commandStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandBody, &commandAck)
	if commandStatus != http.StatusCreated || !commandAck.OK || commandAck.Result == nil {
		t.Fatalf("command create thread with poll failed: status=%d ok=%v err=%+v", commandStatus, commandAck.OK, commandAck.Error)
	}
	threadID := commandAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected 1 post in created thread, got %d", len(posts.Posts))
	}
	post := posts.Posts[0]
	if got := strings.TrimSpace(post.Body); got != "before\nafter" {
		t.Fatalf("expected command-created poll body stripped, got %q", got)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	poll := threadPolls.Polls[post.ID]
	if poll == nil {
		t.Fatalf("expected poll attached to command-created post %s", post.ID)
	}
	if poll.Question != "Favorite color?" {
		t.Fatalf("unexpected poll question: %q", poll.Question)
	}
	if len(poll.Options) != 2 {
		t.Fatalf("expected 2 options from command payload, got %d", len(poll.Options))
	}
}

func TestHTTPCommandEndpointPollRequiresTrustLevel(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	_ = registerUser(t, handler, "admin")
	userToken := registerUser(t, handler, "alice")

	commandBody := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "No trust via command",
			"body":  "[poll]\nShould fail?\nYes\nNo\n[/poll]",
		},
	}

	commandAck := ackResponse{}
	commandStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", userToken, commandBody, &commandAck)
	if commandStatus != http.StatusForbidden {
		t.Fatalf("expected 403 for non-trusted poll creation via command, got %d", commandStatus)
	}
	if commandAck.Error == nil || commandAck.Error.Code != "forbidden" {
		t.Fatalf("expected forbidden error payload for command poll creation, got %+v", commandAck.Error)
	}
}

func TestHTTPCommandEndpointMalformedPollDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	commandBody := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Malformed command poll",
			"body":  "[poll]\nOnly one option?\nOne\n[/poll]",
		},
	}

	commandAck := ackResponse{}
	commandStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandBody, &commandAck)
	if commandStatus != http.StatusCreated || !commandAck.OK || commandAck.Result == nil {
		t.Fatalf("create malformed poll thread via command failed: status=%d ok=%v err=%+v", commandStatus, commandAck.OK, commandAck.Error)
	}

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected 1 post in malformed command thread, got %d", len(posts.Posts))
	}
	post := posts.Posts[0]
	if !strings.Contains(post.Body, "[poll") {
		t.Fatalf("malformed command poll should remain in body, got %q", post.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected no poll projection for malformed command thread poll")
	}
}

func TestHTTPCommandEndpointPollWithExpiry(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	expiresAt := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	rawExpires := expiresAt.Format(time.RFC3339)
	threadBody := "[poll expires=" + rawExpires + "]\nPick color\nBlue\nGreen\n[/poll]"
	commandBody := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Command expiry poll",
			"body":  threadBody,
		},
	}

	commandAck := ackResponse{}
	commandStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandBody, &commandAck)
	if commandStatus != http.StatusCreated || !commandAck.OK || commandAck.Result == nil {
		t.Fatalf("command create expiry poll failed: status=%d ok=%v err=%+v", commandStatus, commandAck.OK, commandAck.Error)
	}

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected 1 post in expiry poll thread, got %d", len(posts.Posts))
	}
	post := posts.Posts[0]

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	poll := threadPolls.Polls[post.ID]
	if poll == nil {
		t.Fatalf("expected poll projection for command expiry poll")
	}
	if poll.Question != "Pick color" {
		t.Fatalf("expected poll question %q, got %q", "Pick color", poll.Question)
	}
	if poll.ExpiresAt == 0 {
		t.Fatalf("expected expiry on command-created poll")
	}
	targetMin := expiresAt.Add(-15 * time.Second).UnixMilli()
	targetMax := expiresAt.Add(15 * time.Second).UnixMilli()
	if poll.ExpiresAt < targetMin || poll.ExpiresAt > targetMax {
		t.Fatalf("expected expiry close to %d, got %d", expiresAt.UnixMilli(), poll.ExpiresAt)
	}
}

func TestHTTPCommandEndpointMalformedExpiryPollDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	commandBody := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Command malformed expiry poll",
			"body":  "[poll expires=badtime]\nQuestion?\nOne\nTwo\n[/poll]",
		},
	}

	commandAck := ackResponse{}
	commandStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandBody, &commandAck)
	if commandStatus != http.StatusCreated || !commandAck.OK || commandAck.Result == nil {
		t.Fatalf("create malformed expiry poll via command failed: status=%d ok=%v err=%+v", commandStatus, commandAck.OK, commandAck.Error)
	}

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected 1 post in malformed expiry command thread, got %d", len(posts.Posts))
	}
	post := posts.Posts[0]
	if !strings.Contains(post.Body, "[poll") {
		t.Fatalf("malformed expiry poll should remain in body, got %q", post.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed expiry poll command to produce no poll projection")
	}
}

func TestHTTPCommandEndpointUppercasePollTags(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	commandBody := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Uppercase poll command",
			"body":  "[POLL]\nQuestion?\nOption A\nOption B\n[/POLL]",
		},
	}

	commandAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandBody, &commandAck); status != http.StatusCreated || !commandAck.OK || commandAck.Result == nil {
		t.Fatalf("expected command create uppercase poll to succeed: status=%d ok=%v err=%+v", status, commandAck.OK, commandAck.Error)
	}

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/posts", adminToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected 1 post in uppercase poll command thread, got %d", len(posts.Posts))
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	poll := threadPolls.Polls[posts.Posts[0].ID]
	if poll == nil {
		t.Fatalf("expected poll projection for uppercase command poll")
	}
	if poll.Question != "Question?" {
		t.Fatalf("expected question %q, got %q", "Question?", poll.Question)
	}
	if len(poll.Options) != 2 {
		t.Fatalf("expected 2 options for uppercase command poll, got %d", len(poll.Options))
	}
}

func TestHTTPCommandEndpointVotePollLifecycleAndErrors(t *testing.T) {
	c, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	commandBody := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Command vote poll",
			"body":  "[poll]\nChoose?\nNorth\nSouth\n[/poll]",
		},
	}

	createAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandBody, &createAck); status != http.StatusCreated || !createAck.OK || createAck.Result == nil {
		t.Fatalf("create command poll thread failed: status=%d ok=%v err=%+v", status, createAck.OK, createAck.Error)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 1 {
		t.Fatalf("expected 1 poll in thread, got %d", len(threadPolls.Polls))
	}

	var postID string
	for id := range threadPolls.Polls {
		postID = id
		break
	}
	poll := threadPolls.Polls[postID]
	if poll == nil {
		t.Fatal("expected poll projection for command-created thread")
	}

	// Unknown poll ID should return not_found via command endpoint.
	unknownAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "votePoll",
		"payload": map[string]any{
			"poll":   "pol_missing",
			"option": poll.Options[0].ID,
		},
	}, &unknownAck); status != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown poll command vote, got %d", status)
	}

	// Unknown option should return not_found.
	unknownOptionAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "votePoll",
		"payload": map[string]any{
			"poll":   poll.ID,
			"option": "opt_missing",
		},
	}, &unknownOptionAck); status != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown option command vote, got %d", status)
	}

	// Expired poll should reject via command vote endpoint.
	if _, err := c.DB.Exec(`UPDATE polls SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UnixMilli(), poll.ID); err != nil {
		t.Fatalf("expire poll in DB: %v", err)
	}

	expiredAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "votePoll",
		"payload": map[string]any{
			"poll":   poll.ID,
			"option": poll.Options[0].ID,
		},
	}, &expiredAck); status != http.StatusConflict {
		t.Fatalf("expected 409 for expired poll command vote, got %d", status)
	}
	if expiredAck.Error == nil || expiredAck.Error.Code != "conflict" {
		t.Fatalf("expected conflict error payload, got %+v", expiredAck.Error)
	}

	// Restore to open the poll, then command vote should succeed.
	if _, err := c.DB.Exec(`UPDATE polls SET expires_at=? WHERE id=?`, 0, poll.ID); err != nil {
		t.Fatalf("restore poll expiry: %v", err)
	}
	successAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "votePoll",
		"payload": map[string]any{
			"poll":   poll.ID,
			"option": poll.Options[0].ID,
		},
	}, &successAck); status != http.StatusCreated || !successAck.OK {
		t.Fatalf("expected successful command poll vote after restore, status=%d ok=%v err=%+v", status, successAck.OK, successAck.Error)
	}

	// Unauthenticated command vote should be rejected.
	unauthAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", "", map[string]any{
		"command": "votePoll",
		"payload": map[string]any{
			"poll":   poll.ID,
			"option": poll.Options[0].ID,
		},
	}, &unauthAck); status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated command vote, got %d", status)
	}
}

func TestHTTPCommandEndpointInvalidPayload(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": "not-an-object",
	}, &ack); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for non-object command payload, got %d", status)
	}
}

func TestHTTPCommandEndpointPollCreateIsIdempotent(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	cmdID := "poll-create-idempotent-1"
	commandBody := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Retry-safe poll",
			"body":  "[poll]\nYes?\nA\nB\n[/poll]",
		},
	}

	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandBody, &first, map[string]string{
		"X-Command-Id": cmdID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first command create failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}
	threadID := first.Result.ID
	if countThreadPosts(t, handler, adminToken, threadID) != 1 {
		t.Fatalf("expected 1 post after first command-created thread")
	}

	second := ackResponse{}
	secondStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandBody, &second, map[string]string{
		"X-Command-Id": cmdID,
	})
	if secondStatus != http.StatusCreated {
		t.Fatalf("expected idempotent replay status 201, got %d", secondStatus)
	}
	if second.Result == nil || second.Result.ID != first.Result.ID {
		t.Fatalf("expected same thread id from idempotent replay, got %s and %s", first.Result.ID, valueOrEmpty(second.Result))
	}
	if !second.OK {
		t.Fatalf("expected replayed command to be ok: %+v", second.Error)
	}

	if countThreadPosts(t, handler, adminToken, threadID) != 1 {
		t.Fatalf("expected no duplicate posts from idempotent replay")
	}
}

func valueOrEmpty(r *ackResult) string {
	if r == nil {
		return ""
	}
	return r.ID
}

func TestHTTPCommandEndpointReplyPollLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	userToken := registerUser(t, handler, "alice")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Command reply poll",
			"body":  "root post",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("command create base thread failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	replyCommandBody := map[string]any{
		"command": "appendPost",
		"payload": map[string]any{
			"thread": threadID,
			"body":   "before\n[poll]\nVote?\nYes\nNo\n[/poll]\nafter",
		},
	}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", userToken, replyCommandBody, &ackResponse{})
	if replyStatus != http.StatusForbidden {
		t.Fatalf("expected non-trusted reply poll via command to be forbidden, got %d", replyStatus)
	}

	replyAck := ackResponse{}
	replyStatus = doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, replyCommandBody, &replyAck)
	if replyStatus != http.StatusCreated || !replyAck.OK || replyAck.Result == nil {
		t.Fatalf("trusted command reply poll create failed: status=%d ok=%v err=%+v", replyStatus, replyAck.OK, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 2 {
		t.Fatalf("expected 2 posts in thread, got %d", len(posts.Posts))
	}
	var replyPost *postPayload
	for i := range posts.Posts {
		if posts.Posts[i].ID == replyPostID {
			replyPost = &posts.Posts[i]
			break
		}
	}
	if replyPost == nil {
		t.Fatalf("expected command-created reply post %s in thread", replyPostID)
	}
	if strings.TrimSpace(replyPost.Body) != "before\nafter" {
		t.Fatalf("expected command-created poll body stripped, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	replyPoll := threadPolls.Polls[replyPostID]
	if replyPoll == nil {
		t.Fatalf("expected poll attached to command reply post %s", replyPostID)
	}
	if replyPoll.Question != "Vote?" {
		t.Fatalf("unexpected poll question from reply command: %q", replyPoll.Question)
	}

	vote := ackResponse{}
	voteStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "votePoll",
		"payload": map[string]any{
			"poll":   replyPoll.ID,
			"option": replyPoll.Options[0].ID,
		},
	}, &vote)
	if voteStatus != http.StatusCreated || !vote.OK {
		t.Fatalf("command vote failed: status=%d ok=%v err=%+v", voteStatus, vote.OK, vote.Error)
	}
}

func TestHTTPReplyPollLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	userToken := registerUser(t, handler, "alice")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Reply poll check",
		"body":  "base body",
	}, &threadAck)
	if threadStatus != http.StatusCreated {
		t.Fatalf("create thread status: %d", threadStatus)
	}
	threadID := threadAck.Result.ID

	replyPollBody := "before\n[poll]\nLunch time?\nTaco\nBurrito\n[/poll]\nafter"
	replyAck := ackResponse{}
	replyCreateStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", userToken, map[string]string{
		"body": replyPollBody,
	}, &replyAck)
	if replyCreateStatus != http.StatusForbidden {
		t.Fatalf("expected non-trust reply poll to be forbidden, got %d", replyCreateStatus)
	}
	if replyAck.Error == nil || replyAck.Error.Code != "forbidden" {
		t.Fatalf("expected forbidden payload for non-trust reply poll, got %+v", replyAck.Error)
	}

	// Now create a valid poll reply as trusted user (admin).
	trustedReplyAck := ackResponse{}
	trustedReplyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": replyPollBody,
	}, &trustedReplyAck)
	if trustedReplyStatus != http.StatusCreated {
		t.Fatalf("trusted reply poll create status: %d", trustedReplyStatus)
	}
	if trustedReplyAck.Result == nil || trustedReplyAck.Result.ID == "" {
		t.Fatalf("trusted reply poll create missing result: %+v", trustedReplyAck)
	}
	replyPostID := trustedReplyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 2 {
		t.Fatalf("expected 2 posts after trusted poll reply, got %d", len(posts.Posts))
	}

	var replyPost *postPayload
	for i := range posts.Posts {
		if posts.Posts[i].ID == replyPostID {
			replyPost = &posts.Posts[i]
			break
		}
	}
	if replyPost == nil {
		t.Fatalf("expected newly created reply post %s in listing", replyPostID)
	}
	expectedBody := "before\nafter"
	if strings.TrimSpace(replyPost.Body) != expectedBody {
		t.Fatalf("expected poll stripped reply body %q, got %q", expectedBody, replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	poll := threadPolls.Polls[replyPostID]
	if poll == nil {
		t.Fatalf("expected poll attached to reply post %s", replyPostID)
	}
	if len(poll.Options) != 2 {
		t.Fatalf("expected 2 options for reply poll, got %d", len(poll.Options))
	}

	vote := ackResponse{}
	voteStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", adminToken, map[string]string{
		"option": poll.Options[0].ID,
	}, &vote)
	if voteStatus != http.StatusCreated || !vote.OK {
		t.Fatalf("vote on reply poll failed: status=%d ok=%v err=%+v", voteStatus, vote.OK, vote.Error)
	}

	getVoted := pollPayload{}
	getVotedStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/polls/"+poll.ID, adminToken, nil, &getVoted)
	if getVotedStatus != http.StatusOK {
		t.Fatalf("get voted poll status: %d", getVotedStatus)
	}
	if getVoted.Voted != poll.Options[0].ID {
		t.Fatalf("expected voted option %q after reply poll vote, got %q", poll.Options[0].ID, getVoted.Voted)
	}
}

func TestHTTPMalformedReplyPollDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Malformed reply poll",
		"body":  "starter body",
	}, &threadAck)
	if threadStatus != http.StatusCreated {
		t.Fatalf("create thread status: %d", threadStatus)
	}

	malformedReply := "[poll]\nQuestion only\nOnly one option\n[/poll]"
	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadAck.Result.ID+"/posts", adminToken, map[string]string{
		"body": malformedReply,
	}, &replyAck)
	if replyStatus != http.StatusCreated || replyAck.Result == nil {
		t.Fatalf("expected malformed reply to be accepted as post create: status=%d err=%+v", replyStatus, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadAck.Result.ID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	var replyPost *postPayload
	for i := range posts.Posts {
		if posts.Posts[i].ID == replyPostID {
			replyPost = &posts.Posts[i]
			break
		}
	}
	if replyPost == nil {
		t.Fatalf("reply post %s should be in thread list", replyPostID)
	}
	if !strings.Contains(replyPost.Body, "[poll") {
		t.Fatalf("malformed reply poll should remain in stored body, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadAck.Result.ID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed reply poll to produce no poll projection")
	}
	if got, ok := threadPolls.Polls[replyPostID]; ok && got != nil {
		t.Fatalf("expected malformed reply poll to produce no poll projection, got %+v", got)
	}
}

func TestHTTPPollVoteLifecycleAndErrors(t *testing.T) {
	core, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	createThreadAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Vote path",
		"body":  "[poll]\nChoose?\nLeft\nRight\n[/poll]",
	}, &createThreadAck); status != http.StatusCreated {
		t.Fatalf("create thread status: %d", status)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createThreadAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 1 {
		t.Fatalf("expected 1 poll for thread, got %d", len(threadPolls.Polls))
	}

	var postID string
	for id := range threadPolls.Polls {
		postID = id
		break
	}
	poll := threadPolls.Polls[postID]
	if poll == nil {
		t.Fatal("expected poll for created thread")
	}

	// Unknown poll ID should return not_found.
	missing := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/pol_missing/vote", adminToken, map[string]string{
		"option": poll.Options[0].ID,
	}, &missing); status != http.StatusNotFound {
		t.Fatalf("expected 404 when voting unknown poll, got %d", status)
	}

	// Unknown option should return not_found.
	missingOpt := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", adminToken, map[string]string{
		"option": "opt_missing",
	}, &missingOpt); status != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown option, got %d", status)
	}

	// Expired poll should reject voting with conflict.
	if _, err := core.DB.Exec(`UPDATE polls SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UnixMilli(), poll.ID); err != nil {
		t.Fatalf("expire poll in DB: %v", err)
	}

	expired := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", adminToken, map[string]string{
		"option": poll.Options[0].ID,
	}, &expired); status != http.StatusConflict {
		t.Fatalf("expected 409 for expired poll, got %d", status)
	}
	if expired.Error == nil || expired.Error.Code != "conflict" {
		t.Fatalf("expected conflict error payload, got %+v", expired.Error)
	}

	// Missing auth should return unauthenticated.
	unauth := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", "", map[string]string{
		"option": poll.Options[0].ID,
	}, &unauth); status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated vote, got %d", status)
	}

	// Restore poll to valid expiry to prove happy path still works for this poll after recovery.
	if _, err := core.DB.Exec(`UPDATE polls SET expires_at=? WHERE id=?`, 0, poll.ID); err != nil {
		t.Fatalf("restore poll expiry: %v", err)
	}
	vote := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", adminToken, map[string]string{
		"option": poll.Options[0].ID,
	}, &vote); status != http.StatusCreated || !vote.OK {
		t.Fatalf("expected success vote after restore: status=%d ok=%v err=%+v", status, vote.OK, vote.Error)
	}
}
