package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func TestSiteAppearance(t *testing.T) {
	c := newHTTPTestCore(t)
	h := httpapi.New(c, []byte("test-secret")).Handler()

	do := func(method, path, token string, body any) *httptest.ResponseRecorder {
		var rdr *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rdr)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	field := func(rec *httptest.ResponseRecorder, key string) string {
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		s, _ := m[key].(string)
		return s
	}

	boss := do(http.MethodPost, "/api/v1/auth/register", "", map[string]string{"name": "boss", "password": "password123"})
	bossTok := field(boss, "token")
	bob := do(http.MethodPost, "/api/v1/auth/register", "", map[string]string{"name": "bob", "password": "password123"})
	bobTok := field(bob, "token")

	// Public GET works without a token and returns defaults.
	pub := do(http.MethodGet, "/api/v1/site/appearance", "", nil)
	if pub.Code != http.StatusOK || field(pub, "siteTitle") != "Budgie BBS" {
		t.Fatalf("public appearance: %d %s", pub.Code, pub.Body.String())
	}

	// Admin can update.
	upd := do(http.MethodPatch, "/api/v1/admin/site-appearance", bossTok, map[string]any{
		"siteTitle": "Campus Hub", "tagline": "for students", "bannerMessage": "Welcome!",
		"accentColor": "#FF8800", "defaultTheme": "warm",
	})
	if upd.Code != http.StatusOK || field(upd, "siteTitle") != "Campus Hub" || field(upd, "accentColor") != "#ff8800" {
		t.Fatalf("admin update: %d %s", upd.Code, upd.Body.String())
	}

	// Public GET reflects the update.
	pub2 := do(http.MethodGet, "/api/v1/site/appearance", "", nil)
	if field(pub2, "siteTitle") != "Campus Hub" || field(pub2, "bannerMessage") != "Welcome!" {
		t.Fatalf("public appearance not updated: %s", pub2.Body.String())
	}

	// A bad accent color is rejected.
	if bad := do(http.MethodPatch, "/api/v1/admin/site-appearance", bossTok, map[string]any{"accentColor": "blue"}); bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for bad accent, got %d %s", bad.Code, bad.Body.String())
	}
	// Non-admins cannot update.
	if forb := do(http.MethodPatch, "/api/v1/admin/site-appearance", bobTok, map[string]any{"siteTitle": "Hax"}); forb.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin, got %d", forb.Code)
	}
}
