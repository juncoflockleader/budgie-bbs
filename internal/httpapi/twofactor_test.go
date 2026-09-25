package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
	"github.com/juncoflockleader/budgie-bbs/internal/totp"
)

func TestStaffTwoFactorLoginChallenge(t *testing.T) {
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

	// First user becomes admin and gets a session token immediately.
	reg := do(http.MethodPost, "/api/v1/auth/register", "", map[string]string{"name": "boss", "password": "password123"})
	if reg.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", reg.Code, reg.Body.String())
	}
	token := field(reg, "token")
	if token == "" {
		t.Fatal("expected a session token from first-user registration")
	}

	// Enroll TOTP for the admin.
	initRec := do(http.MethodPost, "/api/v1/account/2fa/totp", token, nil)
	if initRec.Code != http.StatusOK {
		t.Fatalf("totp init: %d %s", initRec.Code, initRec.Body.String())
	}
	secret := field(initRec, "secret")
	if secret == "" {
		t.Fatal("totp init returned no secret")
	}
	code, _ := totp.CodeAtTime(secret, time.Now().Unix())
	if conf := do(http.MethodPost, "/api/v1/account/2fa/totp/confirm", token, map[string]string{"code": code}); conf.Code != http.StatusOK {
		t.Fatalf("totp confirm: %d %s", conf.Code, conf.Body.String())
	}

	// Turn on enforcement.
	if sec := do(http.MethodPatch, "/api/v1/admin/security-settings", token, map[string]bool{"staff2faRequired": true}); sec.Code != http.StatusOK {
		t.Fatalf("security settings: %d %s", sec.Code, sec.Body.String())
	}

	// A fresh login now yields a challenge, not a token.
	login := do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"name": "boss", "password": "password123"})
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	if field(login, "status") != "2fa_required" {
		t.Fatalf("expected 2fa_required, got %s", login.Body.String())
	}
	if field(login, "token") != "" {
		t.Fatal("no session token should be issued before 2FA")
	}
	challenge := field(login, "challengeToken")
	if challenge == "" {
		t.Fatal("login challenge missing challengeToken")
	}

	// The pre-verification challenge token must NOT authenticate protected
	// endpoints — otherwise it bypasses the second factor entirely (C1).
	if leak := do(http.MethodGet, "/api/v1/account/2fa", challenge, nil); leak.Code != http.StatusUnauthorized {
		t.Fatalf("challenge token must not be accepted as a session token, got %d %s", leak.Code, leak.Body.String())
	}

	// Wrong code is rejected.
	if bad := do(http.MethodPost, "/api/v1/auth/2fa/verify", "", map[string]string{"challengeToken": challenge, "method": "totp", "code": "000000"}); bad.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a bad code, got %d %s", bad.Code, bad.Body.String())
	}

	// The right code completes login and issues the session token.
	code2, _ := totp.CodeAtTime(secret, time.Now().Unix())
	ok := do(http.MethodPost, "/api/v1/auth/2fa/verify", "", map[string]string{"challengeToken": challenge, "method": "totp", "code": code2})
	if ok.Code != http.StatusOK || field(ok, "token") == "" {
		t.Fatalf("2fa verify should issue a token: %d %s", ok.Code, ok.Body.String())
	}
}
