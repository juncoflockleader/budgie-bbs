package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func TestPasswordChangeRevokesOldSessions(t *testing.T) {
	c := newHTTPTestCore(t)
	secret := []byte("test-secret")
	h := httpapi.New(c, secret).Handler()

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
		var out string
		if u, ok := m["user"].(map[string]any); ok {
			out, _ = u[key].(string)
		}
		return out
	}
	mintToken := func(uid string, iat time.Time) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": uid, "typ": "session",
			"iat": iat.Unix(),
			"exp": iat.Add(24 * time.Hour).Unix(),
		})
		s, _ := tok.SignedString(secret)
		return s
	}

	reg := do(http.MethodPost, "/api/v1/auth/register", "", map[string]string{"name": "alice", "password": "password123"})
	if reg.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", reg.Code, reg.Body.String())
	}
	uid := field(reg, "id")
	if uid == "" {
		t.Fatal("no user id in register response")
	}

	// A token issued an hour ago works before any password change.
	old := mintToken(uid, time.Now().Add(-time.Hour))
	if rec := do(http.MethodGet, "/api/v1/account/2fa", old, nil); rec.Code != http.StatusOK {
		t.Fatalf("token should be valid before password change, got %d %s", rec.Code, rec.Body.String())
	}

	// Change the password (using a freshly-issued token).
	fresh := mintToken(uid, time.Now())
	if rec := do(http.MethodPatch, "/api/v1/users/me/password", fresh, map[string]string{"currentPassword": "password123", "newPassword": "newpassword456"}); rec.Code != http.StatusOK {
		t.Fatalf("change password: %d %s", rec.Code, rec.Body.String())
	}

	// The old token (issued before the change) is now revoked...
	if rec := do(http.MethodGet, "/api/v1/account/2fa", old, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old token should be revoked after password change, got %d", rec.Code)
	}
	// ...while a token issued after the change still works.
	after := mintToken(uid, time.Now().Add(time.Minute))
	if rec := do(http.MethodGet, "/api/v1/account/2fa", after, nil); rec.Code != http.StatusOK {
		t.Fatalf("token issued after the change should work, got %d %s", rec.Code, rec.Body.String())
	}
}
