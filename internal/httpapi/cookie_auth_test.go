package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func TestSessionCookieAuthAndCSRF(t *testing.T) {
	c := newHTTPTestCore(t)
	h := httpapi.New(c, []byte("test-secret")).Handler()

	// Register and capture the session cookie set by the server.
	reg := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
		bytes.NewReader([]byte(`{"name":"dora","password":"password123"}`)))
	regRec := httptest.NewRecorder()
	h.ServeHTTP(regRec, reg)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", regRec.Code, regRec.Body.String())
	}
	var bearer string
	if err := func() error {
		var m map[string]any
		_ = json.Unmarshal(regRec.Body.Bytes(), &m)
		bearer, _ = m["token"].(string)
		return nil
	}(); err != nil {
		t.Fatal(err)
	}
	var sessionCookie *http.Cookie
	for _, ck := range regRec.Result().Cookies() {
		if ck.Name == "budgie_session" {
			sessionCookie = ck
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("register must set a budgie_session cookie")
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}

	withCookie := func(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(sessionCookie)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Cookie alone (no Authorization header) authenticates a safe GET.
	if got := withCookie(http.MethodGet, "/api/v1/account/2fa", nil); got.Code != http.StatusOK {
		t.Fatalf("cookie auth should work on GET, got %d %s", got.Code, got.Body.String())
	}

	// /auth/me bootstraps the SPA session from the cookie alone.
	me := withCookie(http.MethodGet, "/api/v1/auth/me", nil)
	if me.Code != http.StatusOK {
		t.Fatalf("/auth/me via cookie should return the user, got %d %s", me.Code, me.Body.String())
	}
	var meBody map[string]any
	_ = json.Unmarshal(me.Body.Bytes(), &meBody)
	if meBody["name"] != "dora" {
		t.Fatalf("/auth/me should return the current user, got %s", me.Body.String())
	}
	// Unauthenticated /auth/me is rejected.
	if anon := httptest.NewRecorder(); true {
		h.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
		if anon.Code != http.StatusUnauthorized {
			t.Fatalf("/auth/me without auth should be 401, got %d", anon.Code)
		}
	}

	// A cookie-authenticated mutation from a cross-origin context is blocked (CSRF).
	if got := withCookie(http.MethodPost, "/api/v1/auth/logout", map[string]string{"Origin": "http://evil.example"}); got.Code != http.StatusForbidden {
		t.Fatalf("cross-origin cookie mutation should be blocked, got %d %s", got.Code, got.Body.String())
	}

	// A same-origin cookie mutation is allowed.
	if got := withCookie(http.MethodPost, "/api/v1/auth/logout", map[string]string{"Sec-Fetch-Site": "same-origin"}); got.Code != http.StatusOK {
		t.Fatalf("same-origin cookie mutation should be allowed, got %d %s", got.Code, got.Body.String())
	}

	// Header (Bearer) auth is exempt from CSRF (not forgeable cross-site).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Origin", "http://evil.example") // ignored for header auth
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer-auth mutation must not require CSRF, got %d %s", rec.Code, rec.Body.String())
	}
	// Logout cleared the cookie.
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "budgie_session" && ck.MaxAge >= 0 {
			t.Fatalf("logout should expire the session cookie, got MaxAge=%d", ck.MaxAge)
		}
	}
}
