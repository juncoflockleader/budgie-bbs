package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

type userProfileResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Title       string `json:"title"`
	Bio         string `json:"bio"`
	Avatar      string `json:"avatar"`
	Signature   string `json:"signature"`
	Plan        string `json:"plan"`
	Homepage    string `json:"homepage"`
}

type userPrivateProfileResponse struct {
	UserID            string `json:"userId"`
	RealName          string `json:"realName"`
	RealEmail         string `json:"realEmail"`
	RegistrationEmail string `json:"registrationEmail"`
	Address           string `json:"address"`
	Phone             string `json:"phone"`
	Mobile            string `json:"mobile"`
	Birthday          string `json:"birthday"`
	School            string `json:"school"`
	ContactNote       string `json:"contactNote"`
}

type userPersonalFilePayload struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Body   string `json:"body"`
	Public bool   `json:"public"`
}

type userPersonalFilesResponse struct {
	Files []userPersonalFilePayload `json:"files"`
}

type userPersonalFileResponse struct {
	File userPersonalFilePayload `json:"file"`
}

type postPayload struct {
	ID          string `json:"id"`
	Author      string `json:"author"`
	AuthorID    string `json:"authorId"`
	Body        string `json:"body"`
	Signature   string `json:"signature"`
	Attachments []struct {
		ID          string `json:"id"`
		Filename    string `json:"filename"`
		ContentType string `json:"contentType"`
		SizeBytes   int64  `json:"sizeBytes"`
		URL         string `json:"url"`
		Stored      bool   `json:"stored"`
	} `json:"attachments"`
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

	c := newHTTPTestCore(t)
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

func loginUser(t *testing.T, handler http.Handler, name string) string {
	t.Helper()
	out := registerResponse{}
	status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     name,
		"password": "pw",
	}, &out)
	if status != http.StatusOK {
		t.Fatalf("login %s status: %d", name, status)
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

func TestHTTPThreadPollWithMultipleBlocksUsesFirst(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")

	threadBody := "intro\n[poll]\nFirst question?\nOption A\nOption B\n[/poll]\nbetween\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]\nafter"
	createAck := ackResponse{}
	createStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "Duplicate poll blocks",
		"body":  threadBody,
	}, &createAck)
	if createStatus != http.StatusCreated || !createAck.OK || createAck.Result == nil {
		t.Fatalf("create thread status: %d err=%+v", createStatus, createAck.Error)
	}

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/posts", token, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected 1 post in created thread, got %d", len(posts.Posts))
	}
	expectedBody := "intro\nbetween\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]\nafter"
	if posts.Posts[0].Body != expectedBody {
		t.Fatalf("expected first poll to be stripped and second preserved, got %q", posts.Posts[0].Body)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/polls", token, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 1 {
		t.Fatalf("expected exactly one poll projection, got %d", len(threadPolls.Polls))
	}
	poll := threadPolls.Polls[posts.Posts[0].ID]
	if poll == nil {
		t.Fatalf("expected poll attached to first post %s", posts.Posts[0].ID)
	}
	if poll.Question != "First question?" {
		t.Fatalf("expected first poll question %q, got %q", "First question?", poll.Question)
	}
}

func TestHTTPThreadPollWithMalformedFirstBlockSkipsLaterPolls(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")

	threadBody := "[poll expires=badtime]\nFirst question?\nOption A\nOption B\n[/poll]\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]"
	createAck := ackResponse{}
	createStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "Malformed first block",
		"body":  threadBody,
	}, &createAck)
	if createStatus != http.StatusCreated || !createAck.OK || createAck.Result == nil {
		t.Fatalf("create thread status: %d err=%+v", createStatus, createAck.Error)
	}

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/posts", token, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected 1 post in malformed thread, got %d", len(posts.Posts))
	}
	if posts.Posts[0].Body != threadBody {
		t.Fatalf("expected malformed first block to remain in body, got %q", posts.Posts[0].Body)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/polls", token, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected no poll projection after malformed first block")
	}
}

func TestHTTPPollCreationMissingCloseTagDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")

	body := "before\n[poll]\nQuestion?\nOne\nTwo\nafter"
	createAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "Unclosed poll",
		"body":  body,
	}, &createAck); status != http.StatusCreated {
		t.Fatalf("create thread status: %d", status)
	}
	if createAck.Result == nil {
		t.Fatalf("thread create missing ack result: %+v", createAck)
	}

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/posts", token, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected 1 post after unclosed poll create, got %d", len(posts.Posts))
	}
	if posts.Posts[0].Body != body {
		t.Fatalf("expected unclosed poll markup to remain in body, got %q", posts.Posts[0].Body)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/polls", token, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected no poll projection for missing close tag")
	}

	pollStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/posts/"+posts.Posts[0].ID+"/poll", token, nil, &pollPayload{})
	if pollStatus != http.StatusNotFound {
		t.Fatalf("expected 404 for posts/%s/poll, got %d", posts.Posts[0].ID, pollStatus)
	}
}

func TestHTTPCommandEndpointPollMissingCloseTagDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	body := "before\n[poll]\nQuestion?\nOne\nTwo\nafter"
	commandBody := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Unclosed poll command",
			"body":  body,
		},
	}

	commandAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandBody, &commandAck); status != http.StatusCreated || !commandAck.OK || commandAck.Result == nil {
		t.Fatalf("expected command create thread status for unclosed poll, got %d ack=%+v", status, commandAck)
	}

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/posts", adminToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected one post, got %d", len(posts.Posts))
	}
	if posts.Posts[0].Body != body {
		t.Fatalf("expected unclosed command poll markup to remain in post body, got %q", posts.Posts[0].Body)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected no poll projection for command post with missing close tag")
	}
}

func TestHTTPPollCreationRequiresQuestionText(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")

	body := "[poll]\n- Option A\n- Option B\n[/poll]"
	createAck := ackResponse{}
	createStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "Missing question poll",
		"body":  body,
	}, &createAck)
	if createStatus != http.StatusCreated {
		t.Fatalf("create thread status: %d", createStatus)
	}
	if !createAck.OK || createAck.Result == nil {
		t.Fatalf("thread create failed: %+v", createAck.Error)
	}

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/posts", token, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected one post in malformed body thread, got %d", len(posts.Posts))
	}
	if posts.Posts[0].Body != body {
		t.Fatalf("expected malformed poll body to remain intact, got %q", posts.Posts[0].Body)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/polls", token, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed poll body to produce no poll projection")
	}
}

func TestHTTPPollCreationMalformedExpiryDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")

	threadBody := "[poll expires=badtime]\nQuestion?\nOne\nTwo\n[/poll]"
	createAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "Malformed expiry poll",
		"body":  threadBody,
	}, &createAck); status != http.StatusCreated {
		t.Fatalf("create thread status: %d", status)
	}
	if createAck.Result == nil {
		t.Fatalf("thread create failed: %+v", createAck.Error)
	}

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/posts", token, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected one post after malformed expiry thread create, got %d", len(posts.Posts))
	}
	if posts.Posts[0].Body != threadBody {
		t.Fatalf("expected malformed expiry poll body to remain intact, got %q", posts.Posts[0].Body)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/polls", token, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed expiry poll to produce no poll projection")
	}
}

func TestHTTPCommandEndpointPollRequiresQuestionText(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	commandBody := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Missing question command poll",
			"body":  "[poll]\n- Option A\n- Option B\n[/poll]",
		},
	}

	commandAck := ackResponse{}
	commandStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandBody, &commandAck)
	if commandStatus != http.StatusCreated || !commandAck.OK || commandAck.Result == nil {
		t.Fatalf("create command thread status: %d ok=%v err=%+v", commandStatus, commandAck.OK, commandAck.Error)
	}

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected one post in malformed command thread, got %d", len(posts.Posts))
	}
	if posts.Posts[0].Body != "[poll]\n- Option A\n- Option B\n[/poll]" {
		t.Fatalf("expected malformed command poll body to remain intact, got %q", posts.Posts[0].Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+commandAck.Result.ID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed command poll to produce no poll projection")
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

func TestHTTPCommandEndpointReplyPollUppercaseTags(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Uppercase reply poll command",
			"body":  "base body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("command create base thread failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	replyCommand := map[string]any{
		"command": "appendPost",
		"payload": map[string]any{
			"thread": threadID,
			"body":   "[POLL]\nQuestion?\nOption A\nOption B\n[/POLL]",
		},
	}
	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, replyCommand, &replyAck)
	if replyStatus != http.StatusCreated || !replyAck.OK || replyAck.Result == nil {
		t.Fatalf("command reply uppercase poll failed: status=%d ok=%v err=%+v", replyStatus, replyAck.OK, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	if len(posts.Posts) != 2 {
		t.Fatalf("expected 2 posts after command reply, got %d", len(posts.Posts))
	}
	var replyPost *postPayload
	for i := range posts.Posts {
		if posts.Posts[i].ID == replyPostID {
			replyPost = &posts.Posts[i]
			break
		}
	}
	if replyPost == nil {
		t.Fatalf("expected command reply post %s in thread", replyPostID)
	}
	if replyPost.Body != "" {
		t.Fatalf("expected command reply poll body to be stripped, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	poll := threadPolls.Polls[replyPostID]
	if poll == nil {
		t.Fatalf("expected poll attached to command reply post %s", replyPostID)
	}
	if poll.Question != "Question?" {
		t.Fatalf("expected question %q, got %q", "Question?", poll.Question)
	}
	if len(poll.Options) != 2 {
		t.Fatalf("expected 2 options for command reply uppercase poll, got %d", len(poll.Options))
	}
}

func TestHTTPRestEndpointReplyPollUppercaseTags(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Uppercase reply poll base",
			"body":  "base body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("command create base thread failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}

	threadID := threadAck.Result.ID
	replyBody := "[POLL]\nQuestion?\nOption A\nOption B\n[/POLL]"
	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": replyBody,
	}, &replyAck)
	if replyStatus != http.StatusCreated || replyAck.Result == nil {
		t.Fatalf("REST reply uppercase poll failed: status=%d err=%+v", replyStatus, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 2 {
		t.Fatalf("expected 2 posts after REST reply, got %d", len(posts.Posts))
	}
	var replyPost *postPayload
	for i := range posts.Posts {
		if posts.Posts[i].ID == replyPostID {
			replyPost = &posts.Posts[i]
			break
		}
	}
	if replyPost == nil {
		t.Fatalf("expected REST reply post %s in thread", replyPostID)
	}
	if replyPost.Body != "" {
		t.Fatalf("expected REST reply poll body to be stripped, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	poll := threadPolls.Polls[replyPostID]
	if poll == nil {
		t.Fatalf("expected poll attached to REST reply post %s", replyPostID)
	}
	if poll.Question != "Question?" {
		t.Fatalf("expected question %q, got %q", "Question?", poll.Question)
	}
	if len(poll.Options) != 2 {
		t.Fatalf("expected 2 options for REST reply uppercase poll, got %d", len(poll.Options))
	}
}

func TestHTTPCommandEndpointReplyPollWithSurroundingText(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Reply poll with surrounding text",
			"body":  "base body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("command create base thread failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	replyCommand := map[string]any{
		"command": "appendPost",
		"payload": map[string]any{
			"thread": threadID,
			"body":   "before line\n[poll]\nQuestion?\nOption A\nOption B\n[/poll]\nafter line",
		},
	}
	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, replyCommand, &replyAck)
	if replyStatus != http.StatusCreated || !replyAck.OK || replyAck.Result == nil {
		t.Fatalf("command reply poll with surrounding text failed: status=%d ok=%v err=%+v", replyStatus, replyAck.OK, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	var replyPost *postPayload
	for i := range posts.Posts {
		if posts.Posts[i].ID == replyPostID {
			replyPost = &posts.Posts[i]
			break
		}
	}
	if replyPost == nil {
		t.Fatalf("expected command reply post %s in thread", replyPostID)
	}
	if strings.TrimSpace(replyPost.Body) != "before line\nafter line" {
		t.Fatalf("expected surrounding text to remain, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	poll := threadPolls.Polls[replyPostID]
	if poll == nil {
		t.Fatalf("expected poll attached to command reply post %s", replyPostID)
	}
	if poll.Question != "Question?" {
		t.Fatalf("expected question %q, got %q", "Question?", poll.Question)
	}
	if len(poll.Options) != 2 {
		t.Fatalf("expected 2 options for command reply poll with surrounding text, got %d", len(poll.Options))
	}
}

func TestHTTPRestEndpointReplyPollWithSurroundingText(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Reply poll with surrounding text",
			"body":  "base body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("command create base thread failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": "before line\n[poll]\nQuestion?\nOption A\nOption B\n[/poll]\nafter line",
	}, &replyAck)
	if replyStatus != http.StatusCreated || replyAck.Result == nil {
		t.Fatalf("REST reply poll with surrounding text failed: status=%d err=%+v", replyStatus, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
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
		t.Fatalf("expected REST reply post %s in thread", replyPostID)
	}
	if strings.TrimSpace(replyPost.Body) != "before line\nafter line" {
		t.Fatalf("expected surrounding text to remain, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	poll := threadPolls.Polls[replyPostID]
	if poll == nil {
		t.Fatalf("expected poll attached to REST reply post %s", replyPostID)
	}
	if poll.Question != "Question?" {
		t.Fatalf("expected question %q, got %q", "Question?", poll.Question)
	}
	if len(poll.Options) != 2 {
		t.Fatalf("expected 2 options for REST reply poll with surrounding text, got %d", len(poll.Options))
	}
}

func TestHTTPCommandEndpointRejectsUnknownCommand(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "nonExistent",
		"payload": map[string]any{},
	}, &ack); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for unknown command, got %d", status)
	}
	if ack.Error == nil || ack.Error.Code != "validation_failed" {
		t.Fatalf("expected validation_failed for unknown command, got %+v", ack.Error)
	}
}

func TestHTTPCommandEndpointRejectsMissingPayload(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
	}, &ack); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing command payload, got %d", status)
	}
	if ack.Error == nil || ack.Error.Code != "validation_failed" {
		t.Fatalf("expected validation_failed error, got %+v", ack.Error)
	}
}

func TestHTTPCommandEndpointIdempotentPayloadMismatch(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	commandID := "poll-conflict-replay-1"
	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Idempotent conflict poll",
			"body":  "[poll]\nFirst?\nA\nB\n[/poll]",
		},
	}, &first, map[string]string{
		"X-Command-Id": commandID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first command failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}

	conflict := ackResponse{}
	conflictStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Different title",
			"body":  "[poll]\nSecond?\nA\nB\n[/poll]",
		},
	}, &conflict, map[string]string{
		"X-Command-Id": commandID,
	})
	if conflictStatus != http.StatusConflict {
		t.Fatalf("expected 409 when replaying same command-id with different payload, got %d", conflictStatus)
	}
	if conflict.Error == nil || conflict.Error.Code != "conflict" {
		t.Fatalf("expected conflict error payload, got %+v", conflict.Error)
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
	if _, err := c.DB.Exec(testRebind(`UPDATE polls SET expires_at=? WHERE id=?`), time.Now().Add(-time.Minute).UnixMilli(), poll.ID); err != nil {
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
	if _, err := c.DB.Exec(testRebind(`UPDATE polls SET expires_at=? WHERE id=?`), 0, poll.ID); err != nil {
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

func TestHTTPPollVoteEndpointIsIdempotent(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	createThreadAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Vote idempotency base",
			"body":  "[poll]\nChoose?\nOne\nTwo\n[/poll]",
		},
	}, &createThreadAck); status != http.StatusCreated || !createThreadAck.OK || createThreadAck.Result == nil {
		t.Fatalf("create thread for vote idempotency failed: status=%d ok=%v err=%+v", status, createThreadAck.OK, createThreadAck.Error)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createThreadAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 1 {
		t.Fatalf("expected one poll for vote idempotency, got %d", len(threadPolls.Polls))
	}

	var targetPostID string
	for postID := range threadPolls.Polls {
		targetPostID = postID
		break
	}
	poll := threadPolls.Polls[targetPostID]
	if poll == nil {
		t.Fatal("expected poll in thread polls map")
	}

	cmdID := "poll-vote-idempotent-1"
	votePayload := map[string]string{"option": poll.Options[0].ID}

	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", adminToken, votePayload, &first, map[string]string{
		"X-Command-Id": cmdID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first vote failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}

	second := ackResponse{}
	secondStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", adminToken, votePayload, &second, map[string]string{
		"X-Command-Id": cmdID,
	})
	if secondStatus != http.StatusCreated {
		t.Fatalf("expected idempotent vote replay status 201, got %d", secondStatus)
	}
	if second.Result == nil || second.Result.ID != first.Result.ID {
		t.Fatalf("expected same ack id from replay, got %s and %s", first.Result.ID, valueOrEmpty(second.Result))
	}
	if second.Result.Seq != first.Result.Seq {
		t.Fatalf("expected replayed vote to return same seq %d, got %d", first.Result.Seq, second.Result.Seq)
	}

	voted := pollPayload{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/polls/"+poll.ID, adminToken, nil, &voted); status != http.StatusOK {
		t.Fatalf("get poll after idempotent vote failed: %d", status)
	}
	if len(voted.Options) == 0 || voted.Options[0].VoteCount != 1 {
		t.Fatalf("expected one vote on first option, got %+v", voted.Options)
	}

	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/posts/"+targetPostID+"/poll", adminToken, nil, &voted); status != http.StatusOK {
		t.Fatalf("get poll by post after idempotent vote failed: %d", status)
	}
}

func TestHTTPPublishPollResultCreatesVoteBoardRecord(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	createThreadAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Vote result base",
			"body":  "[poll]\nBest option?\nOption A\nOption B\n[/poll]",
		},
	}, &createThreadAck); status != http.StatusCreated || !createThreadAck.OK || createThreadAck.Result == nil {
		t.Fatalf("create thread for poll result failed: status=%d ok=%v err=%+v", status, createThreadAck.OK, createThreadAck.Error)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createThreadAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	var poll *pollPayload
	for _, candidate := range threadPolls.Polls {
		poll = candidate
		break
	}
	if poll == nil || len(poll.Options) != 2 {
		t.Fatalf("expected poll with options, got %+v", threadPolls.Polls)
	}
	vote := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", bobToken, map[string]string{
		"option": poll.Options[0].ID,
	}, &vote); status != http.StatusCreated {
		t.Fatalf("vote poll status: %d error=%+v", status, vote.Error)
	}

	forbidden := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/publish-result", bobToken, nil, &forbidden); status != http.StatusForbidden {
		t.Fatalf("expected non-author poll result publish forbidden, got %d error=%+v", status, forbidden.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/general/members/carol", adminToken, map[string]bool{
		"canManagePolls": true,
	}, &vote); status != http.StatusCreated {
		t.Fatalf("grant carol poll manager permission status: %d error=%+v", status, vote.Error)
	}
	result := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/publish-result", carolToken, nil, &result); status != http.StatusCreated {
		t.Fatalf("publish poll result status: %d error=%+v", status, result.Error)
	}
	if result.Result == nil || result.Result.ID == "" {
		t.Fatalf("expected generated vote result thread id, got %+v", result)
	}
	threads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/vote/threads", bobToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list vote threads status: %d", status)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].ID != result.Result.ID || !strings.Contains(threads.Threads[0].Title, "Best option?") {
		t.Fatalf("expected generated vote thread, got %+v", threads.Threads)
	}
	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+result.Result.ID+"/posts", bobToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list vote posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected one generated vote post, got %+v", posts.Posts)
	}
	for _, want := range []string{"# Poll result: Best option?", "Source thread: Vote result base", "Total votes: 1", "Option A: 1 vote", "Option B: 0 vote", "Generated public poll result"} {
		if !strings.Contains(posts.Posts[0].Body, want) {
			t.Fatalf("expected vote result post to contain %q, got:\n%s", want, posts.Posts[0].Body)
		}
	}
	again := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/publish-result", carolToken, nil, &again); status != http.StatusCreated {
		t.Fatalf("repeat poll result publish status: %d error=%+v", status, again.Error)
	}
	if again.Result == nil || again.Result.ID != result.Result.ID {
		t.Fatalf("expected repeated poll result publish to reuse %q, got %+v", result.Result.ID, again)
	}
}

func TestHTTPSanctionClearCreatesDenyPostRecords(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	createBoard := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":   "policy",
		"name": "Policy",
	}, &createBoard); status != http.StatusCreated || !createBoard.OK {
		t.Fatalf("create policy board failed: status=%d err=%+v", status, createBoard.Error)
	}
	createThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/policy/threads", adminToken, map[string]string{
		"title": "Board rules",
		"body":  "Please keep this board tidy.",
	}, &createThread); status != http.StatusCreated || !createThread.OK || createThread.Result == nil {
		t.Fatalf("create policy thread failed: status=%d err=%+v", status, createThread.Error)
	}

	denied := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/users/alice/sanctions", adminToken, map[string]string{
		"kind":   "mute",
		"scope":  "policy",
		"reason": "cooldown",
	}, &denied); status != http.StatusCreated || !denied.OK {
		t.Fatalf("sanction alice failed: status=%d err=%+v", status, denied.Error)
	}
	blocked := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+createThread.Result.ID+"/posts", aliceToken, map[string]string{
		"body": "I should be muted",
	}, &blocked); status != http.StatusConflict {
		t.Fatalf("expected muted append conflict, got status=%d err=%+v", status, blocked.Error)
	}

	denyThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/denypost/threads", bobToken, nil, &denyThreads); status != http.StatusOK {
		t.Fatalf("list denypost threads status: %d", status)
	}
	if len(denyThreads.Threads) != 1 || !strings.Contains(denyThreads.Threads[0].Title, "Board posting denied: alice on policy") {
		t.Fatalf("expected denypost generated thread, got %+v", denyThreads.Threads)
	}
	denyPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+denyThreads.Threads[0].ID+"/posts", bobToken, nil, &denyPosts); status != http.StatusOK {
		t.Fatalf("list denypost posts status: %d", status)
	}
	if len(denyPosts.Posts) != 1 || !strings.Contains(denyPosts.Posts[0].Body, "- Reason: cooldown") {
		t.Fatalf("expected denypost body with reason, got %+v", denyPosts.Posts)
	}

	cleared := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/users/alice/sanctions?kind=mute&scope=policy&reason=served", adminToken, nil, &cleared); status != http.StatusCreated || !cleared.OK {
		t.Fatalf("clear sanction failed: status=%d err=%+v", status, cleared.Error)
	}
	appended := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+createThread.Result.ID+"/posts", aliceToken, map[string]string{
		"body": "I can post again",
	}, &appended); status != http.StatusCreated || !appended.OK {
		t.Fatalf("append after clear failed: status=%d err=%+v", status, appended.Error)
	}

	undenyThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/undenypost/threads", bobToken, nil, &undenyThreads); status != http.StatusOK {
		t.Fatalf("list undenypost threads status: %d", status)
	}
	if len(undenyThreads.Threads) != 1 || !strings.Contains(undenyThreads.Threads[0].Title, "Board posting restored: alice on policy") {
		t.Fatalf("expected undenypost generated thread, got %+v", undenyThreads.Threads)
	}
	undenyPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+undenyThreads.Threads[0].ID+"/posts", bobToken, nil, &undenyPosts); status != http.StatusOK {
		t.Fatalf("list undenypost posts status: %d", status)
	}
	if len(undenyPosts.Posts) != 1 || !strings.Contains(undenyPosts.Posts[0].Body, "- Action: board posting restored") || !strings.Contains(undenyPosts.Posts[0].Body, "- Reason: served") {
		t.Fatalf("expected undenypost restoration body, got %+v", undenyPosts.Posts)
	}
}

func TestHTTPPollVoteEndpointPayloadMismatch(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	createThreadAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Vote mismatch base",
			"body":  "[poll]\nChoose?\nYes\nNo\n[/poll]",
		},
	}, &createThreadAck); status != http.StatusCreated || !createThreadAck.OK || createThreadAck.Result == nil {
		t.Fatalf("create thread for vote mismatch failed: status=%d ok=%v err=%+v", status, createThreadAck.OK, createThreadAck.Error)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createThreadAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 1 {
		t.Fatalf("expected one poll for vote mismatch, got %d", len(threadPolls.Polls))
	}
	var postID string
	for p := range threadPolls.Polls {
		postID = p
		break
	}
	poll := threadPolls.Polls[postID]
	if poll == nil {
		t.Fatal("expected poll projection")
	}

	cmdID := "poll-vote-mismatch-1"
	firstPayload := map[string]string{"option": poll.Options[0].ID}
	secondPayload := map[string]string{"option": poll.Options[1].ID}

	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", adminToken, firstPayload, &first, map[string]string{
		"X-Command-Id": cmdID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first mismatched vote failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}

	conflict := ackResponse{}
	conflictStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", adminToken, secondPayload, &conflict, map[string]string{
		"X-Command-Id": cmdID,
	})
	if conflictStatus != http.StatusConflict {
		t.Fatalf("expected 409 for mismatched vote payload replay, got %d", conflictStatus)
	}
	if conflict.Error == nil || conflict.Error.Code != "conflict" {
		t.Fatalf("expected conflict code for mismatched vote payload, got %+v", conflict.Error)
	}
}

func TestHTTPPollVoteEndpointReplacesExistingVote(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	createThreadAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Vote replacement base",
			"body":  "[poll]\nReplace?\nYes\nNo\n[/poll]",
		},
	}, &createThreadAck); status != http.StatusCreated || !createThreadAck.OK || createThreadAck.Result == nil {
		t.Fatalf("create thread for replacement test failed: status=%d ok=%v err=%+v", status, createThreadAck.OK, createThreadAck.Error)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createThreadAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 1 {
		t.Fatalf("expected one poll for vote replacement, got %d", len(threadPolls.Polls))
	}
	var pollID string
	for _, poll := range threadPolls.Polls {
		if poll != nil {
			pollID = poll.ID
			break
		}
	}
	if pollID == "" {
		t.Fatal("expected poll id for replacement test")
	}

	var optionIDs []string
	for _, poll := range threadPolls.Polls {
		if poll == nil {
			continue
		}
		for _, opt := range poll.Options {
			optionIDs = append(optionIDs, opt.ID)
		}
		break
	}
	if len(optionIDs) != 2 {
		t.Fatalf("expected 2 poll options for replacement test, got %d", len(optionIDs))
	}

	first := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+pollID+"/vote", adminToken, map[string]string{
		"option": optionIDs[0],
	}, &first); status != http.StatusCreated || !first.OK {
		t.Fatalf("expected first vote success, got status=%d ok=%v err=%+v", status, first.OK, first.Error)
	}

	second := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+pollID+"/vote", adminToken, map[string]string{
		"option": optionIDs[1],
	}, &second); status != http.StatusCreated || !second.OK {
		t.Fatalf("expected replacement vote success, got status=%d ok=%v err=%+v", status, second.OK, second.Error)
	}
	if second.Result == nil {
		t.Fatalf("replacement vote should return ack result")
	}

	poll := pollPayload{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/polls/"+pollID, adminToken, nil, &poll); status != http.StatusOK {
		t.Fatalf("get poll after replacement vote failed: %d", status)
	}
	if poll.Voted != optionIDs[1] {
		t.Fatalf("expected voted option %q after replacement, got %q", optionIDs[1], poll.Voted)
	}
	if len(poll.Options) != 2 {
		t.Fatalf("expected two options in poll projection, got %d", len(poll.Options))
	}
	for _, option := range poll.Options {
		switch option.ID {
		case optionIDs[0]:
			if option.VoteCount != 0 {
				t.Fatalf("expected first option vote count to move off first option, got %d", option.VoteCount)
			}
		case optionIDs[1]:
			if option.VoteCount != 1 {
				t.Fatalf("expected second option vote count to receive replacement vote, got %d", option.VoteCount)
			}
		}
	}
}

func TestHTTPCommandEndpointPollVoteIdempotencyAndMismatch(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	commandCreate := map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Poll vote command idempotency base",
			"body":  "[poll]\nVote?\nYes\nNo\n[/poll]",
		},
	}

	createAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, commandCreate, &createAck); status != http.StatusCreated || !createAck.OK || createAck.Result == nil {
		t.Fatalf("create thread for command vote idempotency failed: status=%d ok=%v err=%+v", status, createAck.OK, createAck.Error)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 1 {
		t.Fatalf("expected one poll for command vote idempotency, got %d", len(threadPolls.Polls))
	}
	var pollID string
	var optionID string
	for _, poll := range threadPolls.Polls {
		if poll == nil || len(poll.Options) == 0 {
			continue
		}
		pollID = poll.ID
		optionID = poll.Options[0].ID
		break
	}
	if pollID == "" || optionID == "" {
		t.Fatal("expected poll and option for command vote idempotency")
	}

	cmdID := "poll-command-vote-idempotent-1"
	votePayload := map[string]any{
		"command": "votePoll",
		"payload": map[string]any{
			"poll":   pollID,
			"option": optionID,
		},
	}

	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, votePayload, &first, map[string]string{
		"X-Command-Id": cmdID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first command vote failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}

	second := ackResponse{}
	secondStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, votePayload, &second, map[string]string{
		"X-Command-Id": cmdID,
	})
	if secondStatus != http.StatusCreated {
		t.Fatalf("expected idempotent command vote replay status 201, got %d", secondStatus)
	}
	if second.Result == nil || second.Result.ID != first.Result.ID {
		t.Fatalf("expected same id from idempotent command vote replay, got %s and %s", valueOrEmpty(first.Result), valueOrEmpty(second.Result))
	}
	if second.Result.Seq != first.Result.Seq {
		t.Fatalf("expected same seq from idempotent command vote replay, got %d and %d", first.Result.Seq, second.Result.Seq)
	}

	if !second.OK {
		t.Fatalf("expected replayed command vote to be ok: %+v", second.Error)
	}

	conflictPayload := map[string]any{
		"command": "votePoll",
		"payload": map[string]any{
			"poll":   pollID,
			"option": "opt-mismatch",
		},
	}
	conflict := ackResponse{}
	conflictStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, conflictPayload, &conflict, map[string]string{
		"X-Command-Id": cmdID,
	})
	if conflictStatus != http.StatusConflict {
		t.Fatalf("expected 409 for mismatched command vote payload replay, got %d", conflictStatus)
	}
	if conflict.Error == nil || conflict.Error.Code != "conflict" {
		t.Fatalf("expected conflict code for mismatched command vote payload replay, got %+v", conflict.Error)
	}
}

func TestHTTPPollVoteEndpointValidationAndErrors(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	createThreadAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Vote error base",
			"body":  "[poll]\nChoice?\nA\nB\n[/poll]",
		},
	}, &createThreadAck); status != http.StatusCreated || !createThreadAck.OK || createThreadAck.Result == nil {
		t.Fatalf("create thread for vote errors failed: status=%d ok=%v err=%+v", status, createThreadAck.OK, createThreadAck.Error)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createThreadAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 1 {
		t.Fatalf("expected one poll for validation test, got %d", len(threadPolls.Polls))
	}
	var pollID string
	for _, poll := range threadPolls.Polls {
		if poll != nil {
			pollID = poll.ID
			break
		}
	}
	if pollID == "" {
		t.Fatal("expected poll projection with id")
	}

	missingOption := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+pollID+"/vote", adminToken, map[string]string{}, &missingOption); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing option, got %d", status)
	}

	unknown := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/does-not-exist/vote", adminToken, map[string]string{"option": "opt_x"}, &unknown); status != http.StatusNotFound {
		t.Fatalf("expected 404 for missing poll, got %d", status)
	}

	// Build endpoint-specific invalid payload (malformed JSON) for direct validation path.
	errReq, err := http.NewRequest(http.MethodPost, "http://example.com/api/v1/polls/"+pollID+"/vote", strings.NewReader("{this is not json"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	errReq.Header.Set("Authorization", "Bearer "+adminToken)
	errReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, errReq)
	if rec.Result().StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for malformed json body, got %d", rec.Result().StatusCode)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+pollID+"/vote", "", map[string]string{"option": "any"}, &ackResponse{}); status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated vote, got %d", status)
	}

	// Missing endpoint-auth with valid payload but wrong option.
	unknownOption := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+pollID+"/vote", adminToken, map[string]string{"option": "opt_missing"}, &unknownOption); status != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown option, got %d", status)
	}
}

func TestHTTPPollVoteEndpointRejectsExpiredPoll(t *testing.T) {
	c, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	createThreadAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Expired vote base",
			"body":  "[poll]\nExpire now?\nA\nB\n[/poll]",
		},
	}, &createThreadAck); status != http.StatusCreated || !createThreadAck.OK || createThreadAck.Result == nil {
		t.Fatalf("create thread for expired poll test failed: status=%d ok=%v err=%+v", status, createThreadAck.OK, createThreadAck.Error)
	}

	threadPolls := threadPollsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createThreadAck.Result.ID+"/polls", adminToken, nil, &threadPolls); status != http.StatusOK {
		t.Fatalf("list thread polls status: %d", status)
	}
	if len(threadPolls.Polls) != 1 {
		t.Fatalf("expected one poll for expired poll test, got %d", len(threadPolls.Polls))
	}
	var pollID string
	for _, poll := range threadPolls.Polls {
		if poll != nil {
			pollID = poll.ID
			break
		}
	}
	if pollID == "" {
		t.Fatal("expected poll id for expiry test")
	}

	if _, err := c.DB.Exec(testRebind(`UPDATE polls SET expires_at=? WHERE id=?`), time.Now().Add(-time.Minute).UnixMilli(), pollID); err != nil {
		t.Fatalf("expire poll in DB: %v", err)
	}

	var optionID string
	for _, poll := range threadPolls.Polls {
		if poll == nil || len(poll.Options) == 0 {
			continue
		}
		optionID = poll.Options[0].ID
		break
	}
	if optionID == "" {
		t.Fatalf("expected option ID for expired poll test")
	}

	expired := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+pollID+"/vote", adminToken, map[string]string{"option": optionID}, &expired); status != http.StatusConflict {
		t.Fatalf("expected 409 for expired poll, got %d", status)
	}
	if expired.Error == nil || expired.Error.Code != "conflict" {
		t.Fatalf("expected conflict code for expired poll, got %+v", expired.Error)
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

func TestHTTPCommandEndpointReplyPollCreateIsIdempotent(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Base for reply poll",
			"body":  "base body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("command create base thread failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	cmdID := "poll-reply-idempotent-1"
	replyPollBody := "[poll]\nPick one\nOption A\nOption B\n[/poll]"
	replyPayload := map[string]any{
		"command": "appendPost",
		"payload": map[string]any{
			"thread": threadID,
			"body":   replyPollBody,
		},
	}

	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, replyPayload, &first, map[string]string{
		"X-Command-Id": cmdID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first command reply poll failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}
	if countThreadPosts(t, handler, adminToken, threadID) != 2 {
		t.Fatalf("expected one reply post after first command")
	}

	second := ackResponse{}
	secondStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, replyPayload, &second, map[string]string{
		"X-Command-Id": cmdID,
	})
	if secondStatus != http.StatusCreated {
		t.Fatalf("expected idempotent replay status 201, got %d", secondStatus)
	}
	if second.Result == nil || second.Result.ID != first.Result.ID {
		t.Fatalf("expected same reply id from idempotent replay, got %s and %s", valueOrEmpty(first.Result), valueOrEmpty(second.Result))
	}
	if !second.OK {
		t.Fatalf("expected replayed command to be ok: %+v", second.Error)
	}
	if countThreadPosts(t, handler, adminToken, threadID) != 2 {
		t.Fatalf("expected no duplicate posts from reply idempotent replay")
	}
}

func TestHTTPCommandEndpointReplyPollCreatePayloadMismatch(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Base for reply mismatch",
			"body":  "base body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("command create base thread failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	cmdID := "poll-reply-mismatch-1"
	firstPayload := map[string]any{
		"command": "appendPost",
		"payload": map[string]any{
			"thread": threadID,
			"body":   "[poll]\nChoose first\nA\nB\n[/poll]",
		},
	}
	secondPayload := map[string]any{
		"command": "appendPost",
		"payload": map[string]any{
			"thread": threadID,
			"body":   "[poll]\nChoose second\nX\nY\n[/poll]",
		},
	}

	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, firstPayload, &first, map[string]string{
		"X-Command-Id": cmdID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first command reply poll failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}

	second := ackResponse{}
	secondStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/commands", adminToken, secondPayload, &second, map[string]string{
		"X-Command-Id": cmdID,
	})
	if secondStatus != http.StatusConflict {
		t.Fatalf("expected 409 for mismatched command payload replay, got %d", secondStatus)
	}
	if second.Error == nil || second.Error.Code != "conflict" {
		t.Fatalf("expected conflict code for mismatched reply command payload, got %+v", second.Error)
	}
}

func TestHTTPRestEndpointPollCreateIsIdempotent(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	cmdID := "rest-poll-create-idempotent-1"
	body := map[string]string{
		"title": "Retry-safe poll",
		"body":  "before\n[poll]\nYes?\nA\nB\n[/poll]\nafter",
	}

	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, body, &first, map[string]string{
		"X-Command-Id": cmdID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first REST thread poll create failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}
	threadID := first.Result.ID
	if countThreadPosts(t, handler, adminToken, threadID) != 1 {
		t.Fatalf("expected 1 post after first REST thread create")
	}

	second := ackResponse{}
	secondStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, body, &second, map[string]string{
		"X-Command-Id": cmdID,
	})
	if secondStatus != http.StatusCreated {
		t.Fatalf("expected idempotent REST replay status 201, got %d", secondStatus)
	}
	if second.Result == nil || second.Result.ID != first.Result.ID {
		t.Fatalf("expected same thread id from idempotent replay, got %s and %s", first.Result.ID, valueOrEmpty(second.Result))
	}
	if !second.OK {
		t.Fatalf("expected replayed REST command to be ok: %+v", second.Error)
	}

	if countThreadPosts(t, handler, adminToken, threadID) != 1 {
		t.Fatalf("expected no duplicate posts from idempotent REST replay")
	}
}

func TestHTTPRestEndpointPollCreatePayloadMismatch(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	cmdID := "rest-poll-create-mismatch-1"
	firstBody := map[string]string{
		"title": "Idempotent poll baseline",
		"body":  "[poll]\nChoose first\nA\nB\n[/poll]",
	}
	secondBody := map[string]string{
		"title": "Idempotent poll baseline",
		"body":  "[poll]\nChoose second\nX\nY\n[/poll]",
	}

	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, firstBody, &first, map[string]string{
		"X-Command-Id": cmdID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first REST poll create failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}

	conflict := ackResponse{}
	conflictStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, secondBody, &conflict, map[string]string{
		"X-Command-Id": cmdID,
	})
	if conflictStatus != http.StatusConflict {
		t.Fatalf("expected 409 for mismatched REST payload replay, got %d", conflictStatus)
	}
	if conflict.Error == nil || conflict.Error.Code != "conflict" {
		t.Fatalf("expected conflict code for mismatched REST payload, got %+v", conflict.Error)
	}
}

func TestHTTPRestEndpointReplyPollCreateIsIdempotent(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Base for REST reply poll",
			"body":  "base body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("REST base thread create failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	cmdID := "rest-reply-poll-idempotent-1"
	replyBody := map[string]string{
		"body": "before\n[poll]\nPick one\nOption A\nOption B\n[/poll]\nafter",
	}

	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, replyBody, &first, map[string]string{
		"X-Command-Id": cmdID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first REST reply poll failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}
	if countThreadPosts(t, handler, adminToken, threadID) != 2 {
		t.Fatalf("expected one reply post after first REST reply poll")
	}

	second := ackResponse{}
	secondStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, replyBody, &second, map[string]string{
		"X-Command-Id": cmdID,
	})
	if secondStatus != http.StatusCreated {
		t.Fatalf("expected idempotent REST reply status 201, got %d", secondStatus)
	}
	if second.Result == nil || second.Result.ID != first.Result.ID {
		t.Fatalf("expected same reply id from REST idempotent replay, got %s and %s", valueOrEmpty(first.Result), valueOrEmpty(second.Result))
	}
	if !second.OK {
		t.Fatalf("expected replayed REST reply to be ok: %+v", second.Error)
	}
	if countThreadPosts(t, handler, adminToken, threadID) != 2 {
		t.Fatalf("expected no duplicate posts from REST reply idempotent replay")
	}
}

func TestHTTPRestEndpointReplyPollCreatePayloadMismatch(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Base for REST reply mismatch",
			"body":  "base body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("REST base thread create failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	cmdID := "rest-reply-poll-mismatch-1"
	firstPayload := map[string]string{
		"body": "[poll]\nChoose first\nA\nB\n[/poll]",
	}
	secondPayload := map[string]string{
		"body": "[poll]\nChoose second\nX\nY\n[/poll]",
	}

	first := ackResponse{}
	firstStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, firstPayload, &first, map[string]string{
		"X-Command-Id": cmdID,
	})
	if firstStatus != http.StatusCreated || !first.OK || first.Result == nil {
		t.Fatalf("first REST reply poll failed: status=%d ok=%v err=%+v", firstStatus, first.OK, first.Error)
	}

	second := ackResponse{}
	secondStatus := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, secondPayload, &second, map[string]string{
		"X-Command-Id": cmdID,
	})
	if secondStatus != http.StatusConflict {
		t.Fatalf("expected 409 for mismatched REST reply payload replay, got %d", secondStatus)
	}
	if second.Error == nil || second.Error.Code != "conflict" {
		t.Fatalf("expected conflict code for mismatched REST reply payload, got %+v", second.Error)
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

func TestHTTPReplyPollWithMultipleBlocksUsesFirst(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Multiple reply poll blocks",
		"body":  "starter body",
	}, &threadAck)
	if threadStatus != http.StatusCreated || threadAck.Result == nil {
		t.Fatalf("create thread status: %d err=%+v", threadStatus, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	replyBody := "reply intro\n[poll]\nFirst question?\nOption A\nOption B\n[/poll]\nafter poll\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]"
	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": replyBody,
	}, &replyAck)
	if replyStatus != http.StatusCreated || replyAck.Result == nil {
		t.Fatalf("expected reply with multiple polls to be accepted: status=%d err=%+v", replyStatus, replyAck.Error)
	}

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 2 {
		t.Fatalf("expected 2 posts after reply, got %d", len(posts.Posts))
	}

	var replyPost *postPayload
	for i := range posts.Posts {
		if posts.Posts[i].ID == replyAck.Result.ID {
			replyPost = &posts.Posts[i]
			break
		}
	}
	if replyPost == nil {
		t.Fatalf("reply post %s should be in thread list", replyAck.Result.ID)
	}

	expectedBody := "reply intro\nafter poll\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]"
	if replyPost.Body != expectedBody {
		t.Fatalf("expected first reply poll to be stripped, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	poll := threadPolls.Polls[replyPost.ID]
	if poll == nil {
		t.Fatalf("expected poll attached to reply post %s", replyPost.ID)
	}
	if poll.Question != "First question?" {
		t.Fatalf("expected first poll question %q, got %q", "First question?", poll.Question)
	}
}

func TestHTTPReplyPollWithMalformedFirstBlockSkipsLaterPolls(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Malformed first reply poll block",
		"body":  "starter body",
	}, &threadAck)
	if threadStatus != http.StatusCreated || threadAck.Result == nil {
		t.Fatalf("create thread status: %d err=%+v", threadStatus, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	replyBody := "[poll expires=badtime]\nFirst question?\nOption A\nOption B\n[/poll]\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]"
	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": replyBody,
	}, &replyAck)
	if replyStatus != http.StatusCreated || replyAck.Result == nil {
		t.Fatalf("expected reply post create status: %d err=%+v", replyStatus, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
	if postsStatus != http.StatusOK {
		t.Fatalf("list posts status: %d", postsStatus)
	}
	if len(posts.Posts) != 2 {
		t.Fatalf("expected 2 posts after reply, got %d", len(posts.Posts))
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
	if replyPost.Body != replyBody {
		t.Fatalf("expected malformed first reply poll block to keep body intact, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if got, ok := threadPolls.Polls[replyPostID]; ok && got != nil {
		t.Fatalf("expected no poll projection for malformed reply first block, got %+v", got)
	}
}

func TestHTTPReplyPollWithMissingCloseTagDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Missing close tag reply poll",
		"body":  "starter body",
	}, &threadAck)
	if threadStatus != http.StatusCreated {
		t.Fatalf("create thread status: %d", threadStatus)
	}

	threadID := threadAck.Result.ID
	replyBody := "before\n[poll]\nQuestion?\nOne\nTwo\nafter"
	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": replyBody,
	}, &replyAck)
	if replyStatus != http.StatusCreated || replyAck.Result == nil {
		t.Fatalf("expected malformed close-tag reply to be accepted as post create: status=%d err=%+v", replyStatus, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
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
	if replyPost.Body != replyBody {
		t.Fatalf("expected malformed close-tag reply poll to remain intact, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed close-tag reply poll to produce no poll projection")
	}
	if got, ok := threadPolls.Polls[replyPostID]; ok && got != nil {
		t.Fatalf("expected malformed close-tag reply poll to produce no poll projection, got %+v", got)
	}
}

func TestHTTPMalformedQuestionReplyPollDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Malformed question reply poll",
		"body":  "starter body",
	}, &threadAck)
	if threadStatus != http.StatusCreated {
		t.Fatalf("create thread status: %d", threadStatus)
	}

	threadID := threadAck.Result.ID
	malformedReply := "[poll]\n- Option A\n- Option B\n[/poll]"
	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": malformedReply,
	}, &replyAck)
	if replyStatus != http.StatusCreated || replyAck.Result == nil {
		t.Fatalf("expected malformed question reply to be accepted as post create: status=%d err=%+v", replyStatus, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
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
	if replyPost.Body != malformedReply {
		t.Fatalf("malformed question reply poll should remain in stored body, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed question reply poll to produce no poll projection")
	}
	if got, ok := threadPolls.Polls[replyPostID]; ok && got != nil {
		t.Fatalf("expected malformed question reply poll to produce no poll projection, got %+v", got)
	}
}

func TestHTTPCommandEndpointReplyPollWithMissingCloseTagDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Missing close tag reply poll base",
			"body":  "starter body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("command base thread create failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}

	threadID := threadAck.Result.ID
	replyCommand := map[string]any{
		"command": "appendPost",
		"payload": map[string]any{
			"thread": threadID,
			"body":   "before\n[poll]\nQuestion?\nOne\nTwo\nafter",
		},
	}

	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, replyCommand, &replyAck)
	if replyStatus != http.StatusCreated || !replyAck.OK || replyAck.Result == nil {
		t.Fatalf("expected malformed close-tag reply command to be accepted: status=%d ok=%v err=%+v", replyStatus, replyAck.OK, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
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
	if replyPost.Body != "before\n[poll]\nQuestion?\nOne\nTwo\nafter" {
		t.Fatalf("malformed close-tag reply poll should remain in stored body, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed close-tag reply command to produce no poll projection")
	}
	if got, ok := threadPolls.Polls[replyPostID]; ok && got != nil {
		t.Fatalf("expected malformed close-tag reply command to produce no poll projection, got %+v", got)
	}
}

func TestHTTPMalformedExpiryReplyPollDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Malformed expiry reply poll",
		"body":  "starter body",
	}, &threadAck)
	if threadStatus != http.StatusCreated {
		t.Fatalf("create thread status: %d", threadStatus)
	}

	threadID := threadAck.Result.ID
	malformedReply := "[poll expires=badtime]\nQuestion only\nOne\nTwo\n[/poll]"
	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": malformedReply,
	}, &replyAck)
	if replyStatus != http.StatusCreated || replyAck.Result == nil {
		t.Fatalf("expected malformed expiry reply to be accepted as post create: status=%d err=%+v", replyStatus, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
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
		t.Fatalf("malformed expiry reply poll should remain in stored body, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed expiry reply poll to produce no poll projection")
	}
	if got, ok := threadPolls.Polls[replyPostID]; ok && got != nil {
		t.Fatalf("expected malformed expiry reply poll to produce no poll projection, got %+v", got)
	}
}

func TestHTTPCommandEndpointMalformedQuestionReplyPollDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Malformed question reply poll base",
			"body":  "starter body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("command base thread create failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}

	threadID := threadAck.Result.ID
	replyCommand := map[string]any{
		"command": "appendPost",
		"payload": map[string]any{
			"thread": threadID,
			"body":   "[poll]\n- Option A\n- Option B\n[/poll]",
		},
	}

	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, replyCommand, &replyAck)
	if replyStatus != http.StatusCreated || !replyAck.OK || replyAck.Result == nil {
		t.Fatalf("expected malformed question reply command to be accepted: status=%d ok=%v err=%+v", replyStatus, replyAck.OK, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
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
	if replyPost.Body != "[poll]\n- Option A\n- Option B\n[/poll]" {
		t.Fatalf("malformed question reply poll should remain in stored body, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed question reply command to produce no poll projection")
	}
	if got, ok := threadPolls.Polls[replyPostID]; ok && got != nil {
		t.Fatalf("expected malformed question reply command to produce no poll projection, got %+v", got)
	}
}

func TestHTTPCommandEndpointMalformedExpiryReplyPollDoesNotCreatePoll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	threadAck := ackResponse{}
	threadStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, map[string]any{
		"command": "createThread",
		"payload": map[string]any{
			"board": "general",
			"title": "Malformed expiry reply poll base",
			"body":  "starter body",
		},
	}, &threadAck)
	if threadStatus != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("command base thread create failed: status=%d ok=%v err=%+v", threadStatus, threadAck.OK, threadAck.Error)
	}

	threadID := threadAck.Result.ID
	replyCommand := map[string]any{
		"command": "appendPost",
		"payload": map[string]any{
			"thread": threadID,
			"body":   "[poll expires=badtime]\nQuestion only\nOne\nTwo\n[/poll]",
		},
	}

	replyAck := ackResponse{}
	replyStatus := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", adminToken, replyCommand, &replyAck)
	if replyStatus != http.StatusCreated || !replyAck.OK || replyAck.Result == nil {
		t.Fatalf("expected malformed expiry reply command to be accepted: status=%d ok=%v err=%+v", replyStatus, replyAck.OK, replyAck.Error)
	}
	replyPostID := replyAck.Result.ID

	posts := listPostsResponse{}
	postsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts)
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
		t.Fatalf("malformed expiry reply poll should remain in stored body, got %q", replyPost.Body)
	}

	threadPolls := threadPollsResponse{}
	pollsStatus := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/polls", adminToken, nil, &threadPolls)
	if pollsStatus != http.StatusOK {
		t.Fatalf("list thread polls status: %d", pollsStatus)
	}
	if len(threadPolls.Polls) != 0 {
		t.Fatalf("expected malformed expiry reply command to produce no poll projection")
	}
	if got, ok := threadPolls.Polls[replyPostID]; ok && got != nil {
		t.Fatalf("expected malformed expiry reply command to produce no poll projection, got %+v", got)
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
	if _, err := core.DB.Exec(testRebind(`UPDATE polls SET expires_at=? WHERE id=?`), time.Now().Add(-time.Minute).UnixMilli(), poll.ID); err != nil {
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
	if _, err := core.DB.Exec(testRebind(`UPDATE polls SET expires_at=? WHERE id=?`), 0, poll.ID); err != nil {
		t.Fatalf("restore poll expiry: %v", err)
	}
	vote := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/polls/"+poll.ID+"/vote", adminToken, map[string]string{
		"option": poll.Options[0].ID,
	}, &vote); status != http.StatusCreated || !vote.OK {
		t.Fatalf("expected success vote after restore: status=%d ok=%v err=%+v", status, vote.OK, vote.Error)
	}
}

func TestHTTPProfileSignatureSnapshotsOnPosts(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")
	profileUpdate := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/users/me", token, map[string]string{
		"displayName": "Alice",
		"title":       "Campus Guide",
		"bio":         "bio",
		"avatar":      "A",
		"signature":   "first signature",
		"plan":        "KBS campus plan",
		"homepage":    "example.edu/~alice",
	}, &profileUpdate); status != http.StatusOK {
		t.Fatalf("update profile status: %d", status)
	}
	profile := userProfileResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice", "", nil, &profile); status != http.StatusOK {
		t.Fatalf("get profile status: %d", status)
	}
	if profile.Title != "Campus Guide" || profile.Signature != "first signature" || profile.Plan != "KBS campus plan" || profile.Homepage != "example.edu/~alice" {
		t.Fatalf("expected profile signature, got %+v", profile)
	}

	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "Signature thread",
		"body":  "first body",
	}, &thread); status != http.StatusCreated {
		t.Fatalf("create signed thread status: %d error=%+v", status, thread.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/users/me", token, map[string]string{
		"displayName": "Alice",
		"title":       "Campus Guide",
		"bio":         "bio",
		"avatar":      "A",
		"signature":   "second signature",
		"plan":        "KBS campus plan",
		"homepage":    "example.edu/~alice",
	}, &profileUpdate); status != http.StatusOK {
		t.Fatalf("update second profile status: %d", status)
	}
	reply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/posts", token, map[string]string{
		"body": "second body",
	}, &reply); status != http.StatusCreated {
		t.Fatalf("append signed reply status: %d error=%+v", status, reply.Error)
	}

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", token, nil, &posts); status != http.StatusOK {
		t.Fatalf("list signed posts status: %d", status)
	}
	if len(posts.Posts) != 2 {
		t.Fatalf("expected signed starter and reply, got %+v", posts.Posts)
	}
	if posts.Posts[0].Signature != "first signature" {
		t.Fatalf("expected starter post to keep first signature, got %+v", posts.Posts[0])
	}
	if posts.Posts[1].ID != reply.Result.ID || posts.Posts[1].Signature != "second signature" {
		t.Fatalf("expected reply to snapshot second signature, got %+v", posts.Posts[1])
	}
}

func TestHTTPUserPrivateProfileFieldsAndVisibility(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	ack := map[string]any{}
	payload := map[string]string{
		"realName":          "Alice Zhang",
		"realEmail":         "alice@real.example",
		"registrationEmail": "alice@register.example",
		"address":           "Dorm 7",
		"phone":             "010-123456",
		"mobile":            "13900000000",
		"birthday":          "1984-05-04",
		"school":            "Computer Science",
		"contactNote":       "class of 2006",
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/users/me/private-profile", aliceToken, payload, &ack); status != http.StatusOK {
		t.Fatalf("update private profile status: %d", status)
	}

	own := userPrivateProfileResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/me/private-profile", aliceToken, nil, &own); status != http.StatusOK {
		t.Fatalf("get own private profile status: %d", status)
	}
	if own.RealName != "Alice Zhang" ||
		own.RealEmail != "alice@real.example" ||
		own.RegistrationEmail != "alice@register.example" ||
		own.Address != "Dorm 7" ||
		own.Phone != "010-123456" ||
		own.Mobile != "13900000000" ||
		own.Birthday != "1984-05-04" ||
		own.School != "Computer Science" ||
		own.ContactNote != "class of 2006" {
		t.Fatalf("expected private profile fields, got %+v", own)
	}

	forbidden := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice/private-profile", bobToken, nil, &forbidden); status != http.StatusForbidden {
		t.Fatalf("expected non-admin private profile lookup forbidden, got %d", status)
	}

	adminView := userPrivateProfileResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice/private-profile", adminToken, nil, &adminView); status != http.StatusOK {
		t.Fatalf("expected admin private profile lookup ok, got %d", status)
	}
	if adminView.RealName != own.RealName || adminView.RealEmail != own.RealEmail {
		t.Fatalf("expected admin to see private profile, got %+v", adminView)
	}

	publicProfile := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice", "", nil, &publicProfile); status != http.StatusOK {
		t.Fatalf("get public profile status: %d", status)
	}
	for _, key := range []string{"realName", "realEmail", "registrationEmail", "address", "phone", "mobile", "birthday", "school", "contactNote"} {
		if _, ok := publicProfile[key]; ok {
			t.Fatalf("public profile leaked private key %q in %+v", key, publicProfile)
		}
	}
}

func TestHTTPUserPersonalFilesVisibility(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	_ = registerUser(t, handler, "bob")

	saved := userPersonalFileResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/me/files/resume.txt", aliceToken, map[string]any{
		"body":   "public body",
		"public": true,
	}, &saved); status != http.StatusOK {
		t.Fatalf("save public personal file status: %d", status)
	}
	if saved.File.Name != "resume.txt" || saved.File.Body != "public body" || !saved.File.Public {
		t.Fatalf("expected public personal file response, got %+v", saved)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/me/files/secret.txt", aliceToken, map[string]any{
		"body":   "private body",
		"public": false,
	}, &saved); status != http.StatusOK {
		t.Fatalf("save private personal file status: %d", status)
	}

	publicList := userPersonalFilesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice/files", "", nil, &publicList); status != http.StatusOK {
		t.Fatalf("list public personal files status: %d", status)
	}
	if len(publicList.Files) != 1 || publicList.Files[0].Name != "resume.txt" {
		t.Fatalf("expected only public file, got %+v", publicList)
	}
	publicFile := userPersonalFilePayload{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice/files/resume.txt", "", nil, &publicFile); status != http.StatusOK {
		t.Fatalf("get public personal file status: %d", status)
	}
	if publicFile.Body != "public body" {
		t.Fatalf("expected public file body, got %+v", publicFile)
	}
	hidden := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice/files/secret.txt", "", nil, &hidden); status != http.StatusNotFound {
		t.Fatalf("expected private personal file hidden, got %d", status)
	}

	ownList := userPersonalFilesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/me/files", aliceToken, nil, &ownList); status != http.StatusOK {
		t.Fatalf("list own personal files status: %d", status)
	}
	if len(ownList.Files) != 2 {
		t.Fatalf("expected owner to see public and private files, got %+v", ownList)
	}
	ownPrivate := userPersonalFilePayload{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/me/files/secret.txt", aliceToken, nil, &ownPrivate); status != http.StatusOK {
		t.Fatalf("get own private personal file status: %d", status)
	}
	if ownPrivate.Body != "private body" || ownPrivate.Public {
		t.Fatalf("expected owner private file body, got %+v", ownPrivate)
	}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/users/me/files/resume.txt", aliceToken, nil, &hidden); status != http.StatusOK {
		t.Fatalf("delete personal file status: %d", status)
	}
	publicList = userPersonalFilesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice/files", "", nil, &publicList); status != http.StatusOK {
		t.Fatalf("list public personal files after delete status: %d", status)
	}
	if len(publicList.Files) != 0 {
		t.Fatalf("expected deleted public file to disappear, got %+v", publicList)
	}
}

func TestHTTPUserSignatureBankSelection(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")
	type signatureResponse struct {
		Signature struct {
			ID     string `json:"id"`
			Label  string `json:"label"`
			Body   string `json:"body"`
			Active bool   `json:"active"`
		} `json:"signature"`
	}
	type signatureBundleResponse struct {
		Signatures []struct {
			ID     string `json:"id"`
			Label  string `json:"label"`
			Body   string `json:"body"`
			Active bool   `json:"active"`
		} `json:"signatures"`
		Settings struct {
			SelectedSignatureID string `json:"selectedSignatureId"`
			RandomEnabled       bool   `json:"randomEnabled"`
		} `json:"settings"`
		MaxCount int `json:"maxCount"`
	}
	type signatureRecountResponse struct {
		Recount struct {
			Count               int    `json:"count"`
			ActiveCount         int    `json:"activeCount"`
			SelectedSignatureID string `json:"selectedSignatureId"`
			RandomEnabled       bool   `json:"randomEnabled"`
			CurrentSignature    string `json:"currentSignature"`
		} `json:"recount"`
	}

	first := signatureResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/users/me/signatures", token, map[string]any{
		"label": "First",
		"body":  "http signature one",
	}, &first); status != http.StatusCreated {
		t.Fatalf("create first signature status: %d", status)
	}
	second := signatureResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/users/me/signatures", token, map[string]any{
		"label": "Second",
		"body":  "http signature two",
	}, &second); status != http.StatusCreated {
		t.Fatalf("create second signature status: %d", status)
	}
	settings := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/users/me/signatures/settings", token, map[string]any{
		"selectedSignatureId": second.Signature.ID,
		"randomEnabled":       false,
	}, &settings); status != http.StatusOK {
		t.Fatalf("select signature status: %d", status)
	}

	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "Signature bank",
		"body":  "first post",
	}, &thread); status != http.StatusCreated {
		t.Fatalf("create signed thread status: %d error=%+v", status, thread.Error)
	}
	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", token, nil, &posts); status != http.StatusOK {
		t.Fatalf("list signed posts status: %d", status)
	}
	if len(posts.Posts) != 1 || posts.Posts[0].Signature != "http signature two" {
		t.Fatalf("expected selected signature snapshot, got %+v", posts.Posts)
	}

	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/users/me/signatures/settings", token, map[string]any{
		"selectedSignatureId": first.Signature.ID,
		"randomEnabled":       true,
	}, &settings); status != http.StatusOK {
		t.Fatalf("enable random signatures status: %d", status)
	}
	reply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/posts", token, map[string]string{
		"body": "reply",
	}, &reply); status != http.StatusCreated {
		t.Fatalf("create signed reply status: %d error=%+v", status, reply.Error)
	}
	posts = listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", token, nil, &posts); status != http.StatusOK {
		t.Fatalf("list signed reply posts status: %d", status)
	}
	if len(posts.Posts) != 2 || (posts.Posts[1].Signature != "http signature one" && posts.Posts[1].Signature != "http signature two") {
		t.Fatalf("expected random active signature snapshot, got %+v", posts.Posts)
	}

	bundle := signatureBundleResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/me/signatures", token, nil, &bundle); status != http.StatusOK {
		t.Fatalf("list signatures status: %d", status)
	}
	if len(bundle.Signatures) != 2 || !bundle.Settings.RandomEnabled || bundle.Settings.SelectedSignatureID != first.Signature.ID || bundle.MaxCount == 0 {
		t.Fatalf("expected signature bundle with random settings, got %+v", bundle)
	}
	recount := signatureRecountResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/users/me/signatures/recount", token, nil, &recount); status != http.StatusOK {
		t.Fatalf("recount signatures status: %d", status)
	}
	if recount.Recount.Count != 2 || recount.Recount.ActiveCount != 2 || !recount.Recount.RandomEnabled || recount.Recount.SelectedSignatureID != first.Signature.ID || recount.Recount.CurrentSignature != "http signature one" {
		t.Fatalf("expected signature recount totals and refreshed preview, got %+v", recount)
	}
}

func TestHTTPChangePasswordLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")
	out := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/users/me/password", token, map[string]string{
		"currentPassword": "bad",
		"newPassword":     "newpw",
	}, &out); status != http.StatusUnauthorized {
		t.Fatalf("expected wrong current password to be unauthorized, got %d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/users/me/password", token, map[string]string{
		"currentPassword": "pw",
		"newPassword":     "newpw",
	}, &out); status != http.StatusOK {
		t.Fatalf("change password status: %d", status)
	}

	oldLogin := registerResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "alice",
		"password": "pw",
	}, &oldLogin); status != http.StatusUnauthorized {
		t.Fatalf("expected old password login to fail, got %d", status)
	}
	newLogin := registerResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "alice",
		"password": "newpw",
	}, &newLogin); status != http.StatusOK || newLogin.Token == "" {
		t.Fatalf("expected new password login to succeed, got status=%d response=%+v", status, newLogin)
	}
}

func TestHTTPPasswordRecoveryAdminReset(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	registerUser(t, handler, "alice")
	out := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/password-recovery", "", map[string]string{
		"name":          "alice",
		"submittedName": "Alice Zhang",
		"email":         "alice@example.edu",
		"note":          "lost password",
	}, &out); status != http.StatusAccepted {
		t.Fatalf("password recovery request status: %d", status)
	}
	type recoveryListResponse struct {
		Requests []struct {
			ID             string `json:"id"`
			UserName       string `json:"userName"`
			Status         string `json:"status"`
			SubmittedName  string `json:"submittedName"`
			SubmittedEmail string `json:"submittedEmail"`
		} `json:"requests"`
	}
	pending := recoveryListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/admin/password-recovery?status=pending", adminToken, nil, &pending); status != http.StatusOK {
		t.Fatalf("list password recovery status: %d", status)
	}
	if len(pending.Requests) != 1 || pending.Requests[0].UserName != "alice" || pending.Requests[0].SubmittedName != "Alice Zhang" {
		t.Fatalf("expected alice recovery request, got %+v", pending)
	}
	review := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/admin/password-recovery/"+pending.Requests[0].ID+"/review", adminToken, map[string]string{
		"decision":    "reset",
		"newPassword": "newpw",
		"note":        "verified",
	}, &review); status != http.StatusOK {
		t.Fatalf("review password recovery status: %d body=%+v", status, review)
	}
	oldLogin := registerResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "alice",
		"password": "pw",
	}, &oldLogin); status != http.StatusUnauthorized {
		t.Fatalf("expected old password login to fail after recovery, got %d", status)
	}
	newLogin := registerResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "alice",
		"password": "newpw",
	}, &newLogin); status != http.StatusOK || newLogin.Token == "" {
		t.Fatalf("expected recovery password login to succeed, got status=%d response=%+v", status, newLogin)
	}
}

func TestHTTPTransferUserID(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", aliceToken, map[string]string{
		"title": "Transfer thread",
		"body":  "before transfer",
	}, &thread); status != http.StatusCreated {
		t.Fatalf("create transfer thread status: %d error=%+v", status, thread.Error)
	}

	out := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/admin/users/alice/transfer-id", adminToken, map[string]string{
		"newName": "alice2",
	}, &out); status != http.StatusOK {
		t.Fatalf("transfer user id status: %d body=%+v", status, out)
	}
	oldProfile := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice", "", nil, &oldProfile); status != http.StatusNotFound {
		t.Fatalf("expected old profile not found, got %d", status)
	}
	newProfile := userProfileResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice2", "", nil, &newProfile); status != http.StatusOK {
		t.Fatalf("expected new profile status ok, got %d", status)
	}
	if newProfile.Name != "alice2" {
		t.Fatalf("expected new profile name, got %+v", newProfile)
	}
	oldLogin := registerResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "alice",
		"password": "pw",
	}, &oldLogin); status != http.StatusUnauthorized {
		t.Fatalf("expected old login name unauthorized, got %d", status)
	}
	newLogin := registerResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "alice2",
		"password": "pw",
	}, &newLogin); status != http.StatusOK || newLogin.Token == "" {
		t.Fatalf("expected new login name ok, got status=%d response=%+v", status, newLogin)
	}
	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", adminToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list transferred posts status: %d", status)
	}
	if len(posts.Posts) != 1 || posts.Posts[0].Author != "alice2" {
		t.Fatalf("expected transferred post author, got %+v", posts.Posts)
	}
}

func TestHTTPDeleteUserHardPurgesAccount(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", aliceToken, map[string]string{
		"title": "Delete thread",
		"body":  "before deletion",
	}, &thread); status != http.StatusCreated {
		t.Fatalf("create delete thread status: %d error=%+v", status, thread.Error)
	}

	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/admin/users/admin", adminToken, map[string]string{
		"reason": "self",
	}, nil); status != http.StatusForbidden {
		t.Fatalf("expected self delete forbidden, got %d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/admin/users/alice", adminToken, map[string]string{
		"reason": "operator purge",
	}, nil); status != http.StatusOK {
		t.Fatalf("delete user status: %d", status)
	}

	oldProfile := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/alice", "", nil, &oldProfile); status != http.StatusNotFound {
		t.Fatalf("expected deleted profile not found, got %d", status)
	}
	oldLogin := registerResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "alice",
		"password": "pw",
	}, &oldLogin); status != http.StatusUnauthorized {
		t.Fatalf("expected deleted login unauthorized, got %d", status)
	}
	authRead := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &authRead); status != http.StatusUnauthorized {
		t.Fatalf("expected old token unauthorized, got %d", status)
	}
	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", adminToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list deleted-user posts status: %d", status)
	}
	if len(posts.Posts) != 1 || posts.Posts[0].Author != "[deleted]" || posts.Posts[0].AuthorID == "" {
		t.Fatalf("expected tombstoned post author, got %+v", posts.Posts)
	}
}

func TestHTTPUserLoginACL(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")
	type aclBundleResponse struct {
		Rules []struct {
			ID      string `json:"id"`
			Pattern string `json:"pattern"`
			Active  bool   `json:"active"`
		} `json:"rules"`
		Settings struct {
			Enabled bool `json:"enabled"`
		} `json:"settings"`
		Host    string `json:"host"`
		Allowed bool   `json:"allowed"`
	}
	type aclRuleResponse struct {
		Rule struct {
			ID      string `json:"id"`
			Pattern string `json:"pattern"`
		} `json:"rule"`
	}

	emptyEnable := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/users/me/login-acl/settings", token, map[string]bool{
		"enabled": true,
	}, &emptyEnable); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected enabling empty ACL to fail, got %d", status)
	}
	rule := aclRuleResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/users/me/login-acl/rules", token, map[string]any{
		"pattern": "203.0.113.0/24",
		"note":    "campus vpn",
	}, &rule); status != http.StatusCreated {
		t.Fatalf("create login ACL rule status: %d", status)
	}
	if rule.Rule.ID == "" || rule.Rule.Pattern != "203.0.113.0/24" {
		t.Fatalf("unexpected login ACL rule: %+v", rule)
	}
	settings := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/users/me/login-acl/settings", token, map[string]bool{
		"enabled": true,
	}, &settings); status != http.StatusOK {
		t.Fatalf("enable login ACL status: %d", status)
	}

	deniedLogin := registerResponse{}
	if status := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "alice",
		"password": "pw",
	}, &deniedLogin, map[string]string{"X-Forwarded-For": "198.51.100.9"}); status != http.StatusUnauthorized {
		t.Fatalf("expected disallowed login host to fail, got %d", status)
	}
	allowedLogin := registerResponse{}
	if status := doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "alice",
		"password": "pw",
	}, &allowedLogin, map[string]string{"X-Forwarded-For": "203.0.113.9"}); status != http.StatusOK {
		t.Fatalf("expected allowed login host to succeed, got %d", status)
	}

	bundle := aclBundleResponse{}
	if status := doJSONRequestWithHeaders(t, handler, http.MethodGet, "/api/v1/users/me/login-acl", token, nil, &bundle, map[string]string{"X-Forwarded-For": "203.0.113.9"}); status != http.StatusOK {
		t.Fatalf("list login ACL status: %d", status)
	}
	if !bundle.Settings.Enabled || !bundle.Allowed || bundle.Host != "203.0.113.9" || len(bundle.Rules) != 1 {
		t.Fatalf("expected enabled allowed login ACL bundle, got %+v", bundle)
	}
}

func TestHTTPRegisterUserCreatesNewcomerRecord(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	registerUser(t, handler, "bob")

	newcomerThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/newcomers/threads", aliceToken, nil, &newcomerThreads); status != http.StatusOK {
		t.Fatalf("list newcomers threads status: %d", status)
	}
	if len(newcomerThreads.Threads) != 2 || newcomerThreads.Threads[0].Title != "New user: bob" {
		t.Fatalf("expected generated newcomer thread, got %+v", newcomerThreads.Threads)
	}

	newcomerPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+newcomerThreads.Threads[0].ID+"/posts", aliceToken, nil, &newcomerPosts); status != http.StatusOK {
		t.Fatalf("list newcomer posts status: %d", status)
	}
	if len(newcomerPosts.Posts) != 1 || !strings.Contains(newcomerPosts.Posts[0].Body, "Status: registered") || !strings.Contains(newcomerPosts.Posts[0].Body, "Role: user") {
		t.Fatalf("expected generated newcomer post, got %+v", newcomerPosts.Posts)
	}
	if strings.Contains(newcomerPosts.Posts[0].Body, "pw") {
		t.Fatalf("newcomer post leaked private password data: %q", newcomerPosts.Posts[0].Body)
	}
}

func TestHTTPAccountRegistrationApprovalQueue(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	settings := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/admin/registration-settings", adminToken, map[string]bool{
		"requireApproval": true,
	}, &settings); status != http.StatusOK {
		t.Fatalf("enable registration approval status: %d", status)
	}

	pendingRegister := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"name":     "bob",
		"password": "pw",
	}, &pendingRegister); status != http.StatusAccepted {
		t.Fatalf("expected pending registration status 202, got %d body=%+v", status, pendingRegister)
	}
	if pendingRegister["token"] != nil || pendingRegister["status"] != "pending" {
		t.Fatalf("pending registration should not return token, got %+v", pendingRegister)
	}
	login := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "bob",
		"password": "pw",
	}, &login); status != http.StatusUnauthorized {
		t.Fatalf("expected pending login unauthorized, got %d", status)
	}

	type registrationListResponse struct {
		Registrations []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"registrations"`
	}
	pending := registrationListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/admin/registrations?status=pending", adminToken, nil, &pending); status != http.StatusOK {
		t.Fatalf("list pending registrations status: %d", status)
	}
	if len(pending.Registrations) != 1 || pending.Registrations[0].Name != "bob" || pending.Registrations[0].Status != "pending" {
		t.Fatalf("expected bob pending registration, got %+v", pending)
	}

	review := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/admin/registrations/bob/review", adminToken, map[string]string{
		"decision": "approved",
		"reason":   "welcome",
	}, &review); status != http.StatusOK {
		t.Fatalf("approve registration status: %d body=%+v", status, review)
	}
	bobLogin := registerResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "bob",
		"password": "pw",
	}, &bobLogin); status != http.StatusOK {
		t.Fatalf("expected approved login ok, got %d", status)
	}
	if bobLogin.Token == "" {
		t.Fatalf("expected approved login token, got %+v", bobLogin)
	}

	newcomerThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/newcomers/threads", adminToken, nil, &newcomerThreads); status != http.StatusOK {
		t.Fatalf("list newcomers after approval status: %d", status)
	}
	if len(newcomerThreads.Threads) != 2 || newcomerThreads.Threads[0].Title != "New user: bob" {
		t.Fatalf("expected bob newcomer on approval, got %+v", newcomerThreads.Threads)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"name":     "carol",
		"password": "pw",
	}, &pendingRegister); status != http.StatusAccepted {
		t.Fatalf("expected carol pending registration, got %d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/admin/registrations/carol/review", adminToken, map[string]string{
		"decision": "rejected",
		"reason":   "not this time",
	}, &review); status != http.StatusOK {
		t.Fatalf("reject registration status: %d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "carol",
		"password": "pw",
	}, &login); status != http.StatusUnauthorized {
		t.Fatalf("expected rejected login unauthorized, got %d", status)
	}
}

func TestHTTPDeactivateAccountCreatesGoodbyeRecord(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	out := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/users/me/deactivate", aliceToken, map[string]string{
		"password": "bad",
		"reason":   "private farewell note",
	}, &out); status != http.StatusUnauthorized {
		t.Fatalf("expected wrong password deactivation to be unauthorized, got %d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/users/me/deactivate", aliceToken, map[string]string{
		"password": "pw",
		"reason":   "private farewell note",
	}, &out); status != http.StatusOK {
		t.Fatalf("deactivate account status: %d", status)
	}

	oldLogin := registerResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"name":     "alice",
		"password": "pw",
	}, &oldLogin); status != http.StatusUnauthorized {
		t.Fatalf("expected deactivated login to fail, got %d", status)
	}
	boards := boardsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards", aliceToken, nil, &boards); status != http.StatusUnauthorized {
		t.Fatalf("expected old token to be rejected after deactivation, got %d", status)
	}

	goodbyeThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/Goodbye/threads", bobToken, nil, &goodbyeThreads); status != http.StatusOK {
		t.Fatalf("list Goodbye threads status: %d", status)
	}
	if len(goodbyeThreads.Threads) != 1 || goodbyeThreads.Threads[0].Title != "Goodbye: alice" {
		t.Fatalf("expected generated Goodbye thread, got %+v", goodbyeThreads.Threads)
	}
	goodbyePosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+goodbyeThreads.Threads[0].ID+"/posts", bobToken, nil, &goodbyePosts); status != http.StatusOK {
		t.Fatalf("list Goodbye posts status: %d", status)
	}
	if len(goodbyePosts.Posts) != 1 || !strings.Contains(goodbyePosts.Posts[0].Body, "Status: deactivated") {
		t.Fatalf("expected generated Goodbye post, got %+v", goodbyePosts.Posts)
	}
	if strings.Contains(goodbyePosts.Posts[0].Body, "private farewell note") {
		t.Fatalf("Goodbye post leaked private deactivation note: %q", goodbyePosts.Posts[0].Body)
	}
}
