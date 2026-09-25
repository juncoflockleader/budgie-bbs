package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func newTestServer(t *testing.T) (*httpapi.Server, func()) {
	t.Helper()
	c := newHTTPTestCore(t)
	srv := httpapi.New(c, []byte("test-secret"))
	return srv, func() {}
}

func TestHealthzAlwaysOK(t *testing.T) {
	srv, cancel := newTestServer(t)
	defer cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "ok\n" {
		t.Errorf("expected body 'ok\\n', got %q", body)
	}
}

func TestReadyzWithLiveDB(t *testing.T) {
	srv, cancel := newTestServer(t)
	defer cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "ok\n" {
		t.Errorf("expected body 'ok\\n', got %q", body)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv, cancel := newTestServer(t)
	defer cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain content type, got %q", ct)
	}
	body := rec.Body.String()
	// A few known metric families should always be present in the output.
	for _, want := range []string{
		"# TYPE budgie_ws_connections gauge",
		"# TYPE budgie_command_latency_ms histogram",
		"budgie_events_published_local_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q:\n%s", want, body)
		}
	}
}
