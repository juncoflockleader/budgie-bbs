package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func newTestServer(t *testing.T) (*httpapi.Server, func()) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "health-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	f.Close()

	c, err := core.New(f.Name())
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)

	srv := httpapi.New(c, []byte("test-secret"))
	return srv, cancel
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
