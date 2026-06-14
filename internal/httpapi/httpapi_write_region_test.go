package httpapi_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestWriteRegionProxyRoutesMutatingAPIRequests(t *testing.T) {
	c := newHTTPTestCore(t)
	srv := httpapi.New(c, []byte("test-secret"))
	localHandler := srv.Handler()
	token := registerUser(t, localHandler, "alice")

	proxied := proxyCapture{}
	if err := srv.SetWriteRegionURL("https://write.example.com/write-base"); err != nil {
		t.Fatalf("set write region: %v", err)
	}
	srv.SetWriteRegionTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		proxied.capture(t, r)
		return jsonProxyResponse(http.StatusAccepted, `{"ok":true,"region":"write"}`, r), nil
	}))
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/boards/general/threads?draft=1", strings.NewReader(`{"title":"remote","body":"body"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Command-Id", "cmd-regional")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("proxied write status = %d body=%s", rec.Code, rec.Body.String())
	}
	if proxied.Method != http.MethodPost {
		t.Fatalf("proxied method = %q, want POST", proxied.Method)
	}
	if proxied.Path != "/write-base/api/v1/boards/general/threads?draft=1" {
		t.Fatalf("proxied path = %q", proxied.Path)
	}
	if proxied.Host != "write.example.com" {
		t.Fatalf("proxied host = %q, want write.example.com", proxied.Host)
	}
	if proxied.Authorization != "Bearer "+token {
		t.Fatalf("proxied authorization = %q", proxied.Authorization)
	}
	if proxied.CommandID != "cmd-regional" {
		t.Fatalf("proxied command id = %q", proxied.CommandID)
	}
	if proxied.Routed != "1" {
		t.Fatalf("proxied routed header = %q, want 1", proxied.Routed)
	}
	if proxied.ForwardedHost != "example.com" {
		t.Fatalf("proxied forwarded host = %q, want example.com", proxied.ForwardedHost)
	}
	if proxied.Body != `{"title":"remote","body":"body"}` {
		t.Fatalf("proxied body = %q", proxied.Body)
	}
	if got := metrics.WriteRegionRoutedRequests.Value(map[string]string{"method": http.MethodPost}); got < 1 {
		t.Fatalf("write region routed POST counter = %d, want at least 1", got)
	}

	reads := getWithToken(t, handler, "/api/v1/boards", token)
	if reads.Code != http.StatusOK {
		t.Fatalf("local read status = %d body=%s", reads.Code, reads.Body.String())
	}
	if proxied.Count != 1 {
		t.Fatalf("proxy count after local read = %d, want 1", proxied.Count)
	}
}

func TestGatewayWriteRegionProxyRoutesMutatingAPIRequests(t *testing.T) {
	c := newHTTPTestCore(t)
	srv := httpapi.New(c, []byte("test-secret"))
	token := registerUser(t, srv.Handler(), "alice")

	proxied := proxyCapture{}
	if err := srv.SetWriteRegionURL("https://write.example.com"); err != nil {
		t.Fatalf("set write region: %v", err)
	}
	srv.SetWriteRegionTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		proxied.capture(t, r)
		return jsonProxyResponse(http.StatusAccepted, `{"ok":true}`, r), nil
	}))
	handler := srv.GatewayHandler()

	status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", token, map[string]any{
		"cid":     "cmd-gateway",
		"command": "setPresence",
		"payload": map[string]string{"status": "online"},
	}, nil)
	if status != http.StatusAccepted {
		t.Fatalf("gateway proxied command status = %d", status)
	}
	if proxied.Count != 1 || proxied.Method != http.MethodPost || proxied.Path != "/api/v1/commands" {
		t.Fatalf("gateway proxied request = %+v", proxied)
	}
	if proxied.Authorization != "Bearer "+token {
		t.Fatalf("gateway proxied authorization = %q", proxied.Authorization)
	}

	readRec := getWithToken(t, handler, "/api/v1/boards", token)
	if readRec.Code != http.StatusNotFound {
		t.Fatalf("gateway local read status = %d, want 404", readRec.Code)
	}
	if proxied.Count != 1 {
		t.Fatalf("proxy count after gateway read = %d, want 1", proxied.Count)
	}
}

func TestWriteRegionProxyFailureIsRetryable(t *testing.T) {
	c := newHTTPTestCore(t)
	srv := httpapi.New(c, []byte("test-secret"))
	token := registerUser(t, srv.Handler(), "alice")
	if err := srv.SetWriteRegionURL("https://write.example.com"); err != nil {
		t.Fatalf("set write region: %v", err)
	}
	srv.SetWriteRegionTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	}))
	handler := srv.Handler()
	before := metrics.WriteRegionProxyFailures.Value()

	var out ackResponse
	status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/commands", token, map[string]any{
		"cid":     "cmd-failover",
		"command": "setPresence",
		"payload": map[string]string{"status": "online"},
	}, &out)
	if status != http.StatusBadGateway {
		t.Fatalf("write-region failure status = %d, want 502", status)
	}
	if out.Error == nil || out.Error.Code != proto.ErrWriteRegionUnavailable || !out.Error.Retryable {
		t.Fatalf("write-region failure error = %+v", out.Error)
	}
	if got := metrics.WriteRegionProxyFailures.Value() - before; got != 1 {
		t.Fatalf("write region proxy failure counter delta = %d, want 1", got)
	}
}

func TestWriteRegionProxyRejectsInvalidURL(t *testing.T) {
	srv := httpapi.New(newHTTPTestCore(t), []byte("test-secret"))
	for _, raw := range []string{"write.example.com", "ftp://write.example.com/api"} {
		if err := srv.SetWriteRegionURL(raw); err == nil {
			t.Fatalf("SetWriteRegionURL(%q) succeeded, want error", raw)
		}
	}
}

type proxyCapture struct {
	Count         int
	Method        string
	Path          string
	Host          string
	Authorization string
	CommandID     string
	Routed        string
	ForwardedHost string
	Body          string
}

func (c *proxyCapture) capture(t *testing.T, r *http.Request) {
	t.Helper()
	c.Count++
	c.Method = r.Method
	c.Path = r.URL.RequestURI()
	c.Host = r.Host
	c.Authorization = r.Header.Get("Authorization")
	c.CommandID = r.Header.Get("X-Command-Id")
	c.Routed = r.Header.Get("X-Budgie-Write-Region-Routed")
	c.ForwardedHost = r.Header.Get("X-Forwarded-Host")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read proxied body: %v", err)
	}
	c.Body = string(body)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonProxyResponse(status int, body string, req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: req,
	}
}
