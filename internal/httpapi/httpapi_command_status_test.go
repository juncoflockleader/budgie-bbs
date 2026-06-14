package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type commandAckResponse struct {
	Kind   string           `json:"kind"`
	OK     bool             `json:"ok"`
	CID    string           `json:"cid"`
	Result *proto.AckResult `json:"result"`
	Error  *struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

type commandStatusResponse struct {
	CommandID            string           `json:"commandId"`
	Status               string           `json:"status"`
	CommandPartitionKind string           `json:"commandPartitionKind"`
	CommandPartitionKey  string           `json:"commandPartitionKey"`
	CommandOffset        int64            `json:"commandOffset"`
	CommittedOffset      int64            `json:"committedOffset"`
	Result               *proto.AckResult `json:"result"`
	Error                *struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

func TestHTTPCommandStatusTracksAuthoritativeCommandLogDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := core.NewMemoryCommandLog()
	c, err := core.New(t.TempDir()+"/budgie.db", core.WithAuthoritativeCommandLog(commandLog))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	go c.Run(ctx)

	srv := httpapi.New(c, []byte("test-secret"))
	fullHandler := srv.Handler()
	gatewayHandler := srv.GatewayHandler()
	token := registerUser(t, fullHandler, "alice")

	cid := "cid-http-command-status"
	queued := commandAckResponse{}
	status := doJSONRequestWithHeaders(t, fullHandler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "HTTP command status",
		"body":  "queued behind the command log",
	}, &queued, map[string]string{"X-Command-Id": cid})
	if status != http.StatusAccepted {
		t.Fatalf("enqueue command status = %d error=%+v", status, queued.Error)
	}
	if queued.Result == nil || queued.Result.Status != proto.AckStatusPending || queued.Result.CommandOffset != 1 {
		t.Fatalf("queued result = %+v, want pending command-log receipt at offset 1", queued.Result)
	}

	path := "/api/v1/commands/" + cid + "?commandPartitionKind=board&commandPartitionKey=general&commandOffset=1"
	before := commandStatusResponse{}
	if status := doJSONRequest(t, gatewayHandler, http.MethodGet, path, token, nil, &before); status != http.StatusOK {
		t.Fatalf("command status before drain status = %d error=%+v", status, before.Error)
	}
	if before.Status != core.CommandStatusPending || before.CommandID != cid || before.CommandOffset != 1 || before.Result != nil {
		t.Fatalf("status before drain = %+v, want pending receipt", before)
	}

	worker := core.NewCommandLogWorker(core.CommandLogWorkerConfig{
		Log:       commandLog,
		Executor:  c,
		BatchSize: 10,
	})
	results, err := worker.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("drain once: %v", err)
	}
	if len(results) != 1 || results[0].Processed != 1 {
		t.Fatalf("drain results = %+v, want one processed command", results)
	}

	after := commandStatusResponse{}
	if status := doJSONRequest(t, gatewayHandler, http.MethodGet, path, token, nil, &after); status != http.StatusOK {
		t.Fatalf("command status after drain status = %d error=%+v", status, after.Error)
	}
	if after.Status != core.CommandStatusApplied || after.Result == nil || after.Result.ID == "" || after.Result.Seq == 0 {
		t.Fatalf("status after drain = %+v, want applied command result", after)
	}

	wrongOffset := commandStatusResponse{}
	wrongOffsetPath := "/api/v1/commands/" + cid + "?commandPartitionKind=board&commandPartitionKey=general&commandOffset=2"
	if status := doJSONRequest(t, gatewayHandler, http.MethodGet, wrongOffsetPath, token, nil, &wrongOffset); status != http.StatusNotFound {
		t.Fatalf("wrong-offset command status = %d %+v, want 404", status, wrongOffset.Error)
	}

	bobToken := registerUser(t, fullHandler, "bob")
	hidden := commandStatusResponse{}
	if status := doJSONRequest(t, gatewayHandler, http.MethodGet, path, bobToken, nil, &hidden); status != http.StatusNotFound {
		t.Fatalf("other actor command status = %d %+v, want 404", status, hidden.Error)
	}
}

func TestHTTPAuthoritativeReadbackAliasesReturnPendingReceiptsAndStageBlobUploads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := core.NewMemoryCommandLog()
	c, err := core.New(t.TempDir()+"/budgie.db", core.WithAuthoritativeCommandLog(commandLog))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	go c.Run(ctx)

	handler := httpapi.New(c, []byte("test-secret")).Handler()
	token := registerUser(t, handler, "alice")
	worker := core.NewCommandLogWorker(core.CommandLogWorkerConfig{
		Log:       commandLog,
		Executor:  c,
		BatchSize: 10,
	})

	importAck := commandAckResponse{}
	status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/favorites/import", token, map[string]any{
		"replace":   true,
		"folders":   []any{},
		"favorites": []any{},
	}, &importAck)
	if status != http.StatusAccepted {
		t.Fatalf("favorite import status = %d body=%+v, want pending ack", status, importAck)
	}
	if importAck.Kind != "ack" || importAck.Result == nil || importAck.Result.Status != proto.AckStatusPending || importAck.Result.CommandOffset == 0 {
		t.Fatalf("favorite import ack = %+v, want pending authoritative receipt", importAck)
	}

	threadAck := commandAckResponse{}
	threadCID := "cid-stage-thread"
	status = doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "Staged blobs",
		"body":  "create a post before staging attachment bytes",
	}, &threadAck, map[string]string{"X-Command-Id": threadCID})
	if status != http.StatusAccepted {
		t.Fatalf("create staged thread status = %d error=%+v", status, threadAck.Error)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain thread command: %v", err)
	}
	threadStatus := commandStatusResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, commandStatusPath(threadCID, threadAck.Result), token, nil, &threadStatus); status != http.StatusOK {
		t.Fatalf("thread command status = %d error=%+v", status, threadStatus.Error)
	}
	if threadStatus.Status != core.CommandStatusApplied || threadStatus.Result == nil || threadStatus.Result.ID == "" {
		t.Fatalf("thread command status = %+v, want applied thread result", threadStatus)
	}
	posts, err := c.ListPosts(threadStatus.Result.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v, want first thread post", posts)
	}

	postUploadCID := "cid-stage-post-upload"
	postUpload := commandAckResponse{}
	postBytes := []byte("queued attachment")
	status = doMultipartFileRequestWithHeaders(t, handler, "/api/v1/posts/"+posts[0].ID+"/attachments", token, "queued.txt", postBytes, &postUpload, map[string]string{"X-Command-Id": postUploadCID})
	if status != http.StatusAccepted {
		t.Fatalf("post attachment upload status = %d body=%+v, want pending staged ack", status, postUpload)
	}
	if postUpload.Result == nil || postUpload.Result.Status != proto.AckStatusPending {
		t.Fatalf("post attachment ack = %+v, want pending receipt", postUpload.Result)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain post attachment command: %v", err)
	}
	postUploadStatus := commandStatusResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, commandStatusPath(postUploadCID, postUpload.Result), token, nil, &postUploadStatus); status != http.StatusOK {
		t.Fatalf("post attachment status = %d error=%+v", status, postUploadStatus.Error)
	}
	if postUploadStatus.Status != core.CommandStatusApplied || postUploadStatus.Result == nil || postUploadStatus.Result.ID == "" {
		t.Fatalf("post attachment status = %+v, want applied attachment result", postUploadStatus)
	}
	storedPostBytes, _, err := c.GetAttachmentBlob(postUploadStatus.Result.ID)
	if err != nil {
		t.Fatalf("get staged post attachment blob: %v", err)
	}
	if !bytes.Equal(storedPostBytes, postBytes) {
		t.Fatalf("stored post blob = %q, want %q", storedPostBytes, postBytes)
	}

	mailCID := "cid-stage-mail"
	mailAck := commandAckResponse{}
	status = doJSONRequestWithHeaders(t, handler, http.MethodPost, "/api/v1/mail", token, map[string]any{
		"to":      []string{"alice"},
		"subject": "Staged mail",
		"body":    "create mail before staging attachment bytes",
	}, &mailAck, map[string]string{"X-Command-Id": mailCID})
	if status != http.StatusAccepted {
		t.Fatalf("send mail status = %d error=%+v", status, mailAck.Error)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain mail command: %v", err)
	}
	mailStatus := commandStatusResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, commandStatusPath(mailCID, mailAck.Result), token, nil, &mailStatus); status != http.StatusOK {
		t.Fatalf("mail command status = %d error=%+v", status, mailStatus.Error)
	}
	if mailStatus.Status != core.CommandStatusApplied || mailStatus.Result == nil || mailStatus.Result.ID == "" {
		t.Fatalf("mail command status = %+v, want applied mail result", mailStatus)
	}

	mailUploadCID := "cid-stage-mail-upload"
	mailUpload := commandAckResponse{}
	mailBytes := []byte("queued mail attachment")
	status = doMultipartFileRequestWithHeaders(t, handler, "/api/v1/mail/"+mailStatus.Result.ID+"/attachments", token, "queued-mail.txt", mailBytes, &mailUpload, map[string]string{"X-Command-Id": mailUploadCID})
	if status != http.StatusAccepted {
		t.Fatalf("mail attachment upload status = %d body=%+v, want pending staged ack", status, mailUpload)
	}
	if mailUpload.Result == nil || mailUpload.Result.Status != proto.AckStatusPending {
		t.Fatalf("mail attachment ack = %+v, want pending receipt", mailUpload.Result)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain mail attachment command: %v", err)
	}
	mailUploadStatus := commandStatusResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, commandStatusPath(mailUploadCID, mailUpload.Result), token, nil, &mailUploadStatus); status != http.StatusOK {
		t.Fatalf("mail attachment status = %d error=%+v", status, mailUploadStatus.Error)
	}
	if mailUploadStatus.Status != core.CommandStatusApplied || mailUploadStatus.Result == nil || mailUploadStatus.Result.ID == "" {
		t.Fatalf("mail attachment status = %+v, want applied attachment result", mailUploadStatus)
	}
	storedMailBytes, _, err := c.GetMailAttachmentBlob(mailUploadStatus.Result.ID)
	if err != nil {
		t.Fatalf("get staged mail attachment blob: %v", err)
	}
	if !bytes.Equal(storedMailBytes, mailBytes) {
		t.Fatalf("stored mail blob = %q, want %q", storedMailBytes, mailBytes)
	}
}

func commandStatusPath(commandID string, ack *proto.AckResult) string {
	params := url.Values{}
	params.Set("commandPartitionKind", ack.CommandPartitionKind)
	params.Set("commandPartitionKey", ack.CommandPartitionKey)
	params.Set("commandOffset", strconv.FormatInt(ack.CommandOffset, 10))
	return "/api/v1/commands/" + url.PathEscape(commandID) + "?" + params.Encode()
}

func doMultipartFileRequestWithHeaders(t *testing.T, handler http.Handler, path, token, filename string, data []byte, out any, headers map[string]string) int {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode multipart response: %v body=%s", err, rec.Body.String())
		}
	}
	return rec.Code
}

func TestHTTPCommandStatusExplainsTerminalCommandLogFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := core.NewMemoryCommandLog()
	c, err := core.New(t.TempDir()+"/budgie.db", core.WithAuthoritativeCommandLog(commandLog))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	go c.Run(ctx)

	srv := httpapi.New(c, []byte("test-secret"))
	fullHandler := srv.Handler()
	gatewayHandler := srv.GatewayHandler()
	token := registerUser(t, fullHandler, "alice")

	cid := "cid-http-command-status-failed"
	queued := commandAckResponse{}
	status := doJSONRequestWithHeaders(t, fullHandler, http.MethodPost, "/api/v1/boards/missing/threads", token, map[string]string{
		"title": "HTTP failed command status",
		"body":  "missing board",
	}, &queued, map[string]string{"X-Command-Id": cid})
	if status != http.StatusAccepted {
		t.Fatalf("enqueue missing-board command status = %d error=%+v", status, queued.Error)
	}
	if queued.Result == nil || queued.Result.Status != proto.AckStatusPending || queued.Result.CommandPartitionKey != "missing" {
		t.Fatalf("queued result = %+v, want pending missing-board receipt", queued.Result)
	}

	worker := core.NewCommandLogWorker(core.CommandLogWorkerConfig{
		Log:       commandLog,
		Executor:  c,
		BatchSize: 10,
	})
	results, err := worker.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("drain once: %v", err)
	}
	if len(results) != 1 || results[0].TerminalFailures != 1 || results[0].Processed != 1 {
		t.Fatalf("drain results = %+v, want one committed terminal failure", results)
	}

	path := "/api/v1/commands/" + cid + "?commandPartitionKind=board&commandPartitionKey=missing&commandOffset=1"
	failed := commandStatusResponse{}
	if status := doJSONRequest(t, gatewayHandler, http.MethodGet, path, token, nil, &failed); status != http.StatusOK {
		t.Fatalf("command status after terminal failure status = %d error=%+v", status, failed.Error)
	}
	if failed.Status != core.CommandStatusFailed || failed.Error == nil || failed.Error.Code != proto.ErrNotFound || failed.Result != nil {
		t.Fatalf("failed status = %+v, want failed command receipt with not_found error", failed)
	}
}

func TestHTTPCommandStatusReportsRetryableCommandLogFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := core.NewMemoryCommandLog()
	c, err := core.New(t.TempDir()+"/budgie.db", core.WithAuthoritativeCommandLog(commandLog))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()

	srv := httpapi.New(c, []byte("test-secret"))
	fullHandler := srv.Handler()
	gatewayHandler := srv.GatewayHandler()
	token := registerUser(t, fullHandler, "alice")

	cid := "cid-http-command-status-retrying"
	queued := commandAckResponse{}
	status := doJSONRequestWithHeaders(t, fullHandler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "HTTP retrying command status",
		"body":  "writer dependency unavailable",
	}, &queued, map[string]string{"X-Command-Id": cid})
	if status != http.StatusAccepted {
		t.Fatalf("enqueue command status = %d error=%+v", status, queued.Error)
	}
	if queued.Result == nil || queued.Result.Status != proto.AckStatusPending || queued.Result.CommandPartitionKey != "general" {
		t.Fatalf("queued result = %+v, want pending general-board receipt", queued.Result)
	}

	retryableErr := &proto.ErrorDetail{Code: "dependency_unavailable", Message: "try again", Retryable: true}
	worker := core.NewCommandLogWorker(core.CommandLogWorkerConfig{
		Log:               commandLog,
		RetryableFailures: c,
		BatchSize:         10,
		Executor: core.CommandLogExecutorFunc(func(ctx context.Context, record core.CommandLogRecord) core.Reply {
			return core.Reply{Err: retryableErr}
		}),
	})
	results, err := worker.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("drain once: %v", err)
	}
	if len(results) != 1 || results[0].RetryableFailure == nil || results[0].Processed != 0 {
		t.Fatalf("drain results = %+v, want retryable stop before offset commit", results)
	}

	path := "/api/v1/commands/" + cid + "?commandPartitionKind=board&commandPartitionKey=general&commandOffset=1"
	retrying := commandStatusResponse{}
	if status := doJSONRequest(t, gatewayHandler, http.MethodGet, path, token, nil, &retrying); status != http.StatusOK {
		t.Fatalf("command status after retryable failure status = %d error=%+v", status, retrying.Error)
	}
	if retrying.Status != core.CommandStatusRetrying || retrying.Error == nil || retrying.Error.Code != retryableErr.Code || !retrying.Error.Retryable || retrying.CommittedOffset != 0 {
		t.Fatalf("retrying status = %+v, want retryable command receipt", retrying)
	}
}
