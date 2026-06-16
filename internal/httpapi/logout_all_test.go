package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func TestLogoutAllRevokesExistingSessions(t *testing.T) {
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

	reg := do(http.MethodPost, "/api/v1/auth/register", "", map[string]string{"name": "carol", "password": "password123"})
	if reg.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", reg.Code, reg.Body.String())
	}
	token := field(reg, "token")

	// The token works on a protected endpoint.
	if got := do(http.MethodGet, "/api/v1/account/2fa", token, nil); got.Code != http.StatusOK {
		t.Fatalf("token should be valid before logout-all, got %d %s", got.Code, got.Body.String())
	}

	// Revocation uses unix-second granularity (token iat vs the cutoff), so cross
	// a second boundary to make the assertion deterministic: the existing token's
	// iat is then strictly before the revoke cutoff.
	time.Sleep(1100 * time.Millisecond)

	// Sign out everywhere.
	if out := do(http.MethodPost, "/api/v1/auth/logout-all", token, nil); out.Code != http.StatusOK {
		t.Fatalf("logout-all: %d %s", out.Code, out.Body.String())
	}

	// The same token is now revoked.
	if got := do(http.MethodGet, "/api/v1/account/2fa", token, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("token must be rejected after logout-all, got %d %s", got.Code, got.Body.String())
	}

	// A fresh login issues a working token again.
	login := do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"name": "carol", "password": "password123"})
	newToken := field(login, "token")
	if newToken == "" {
		t.Fatalf("re-login should issue a token: %d %s", login.Code, login.Body.String())
	}
	if got := do(http.MethodGet, "/api/v1/account/2fa", newToken, nil); got.Code != http.StatusOK {
		t.Fatalf("a token minted after logout-all should work, got %d %s", got.Code, got.Body.String())
	}
}
