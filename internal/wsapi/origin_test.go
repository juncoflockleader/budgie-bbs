package wsapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWSTokenSource(t *testing.T) {
	// Query param and Authorization header are not "via cookie".
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ws?token=abc", nil)
	if tok, viaCookie := wsToken(r); tok != "abc" || viaCookie {
		t.Fatalf("query token: got (%q,%v)", tok, viaCookie)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	r.Header.Set("Authorization", "Bearer hdr")
	if tok, viaCookie := wsToken(r); tok != "hdr" || viaCookie {
		t.Fatalf("header token: got (%q,%v)", tok, viaCookie)
	}
	// Cookie is flagged so the CSWSH origin check applies.
	r = httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	r.AddCookie(&http.Cookie{Name: "budgie_session", Value: "ck"})
	if tok, viaCookie := wsToken(r); tok != "ck" || !viaCookie {
		t.Fatalf("cookie token: got (%q,%v)", tok, viaCookie)
	}
}

func TestWSSameOriginGuard(t *testing.T) {
	mk := func(set func(*http.Request)) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
		r.Host = "budgie.example"
		set(r)
		return r
	}
	cases := []struct {
		name string
		set  func(*http.Request)
		want bool
	}{
		{"sec-fetch same-origin", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") }, true},
		{"sec-fetch cross-site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, false},
		{"origin matches host", func(r *http.Request) { r.Header.Set("Origin", "https://budgie.example") }, true},
		{"origin cross", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, false},
		{"no origin header", func(r *http.Request) {}, false}, // browsers always send Origin on WS
	}
	for _, tc := range cases {
		if got := wsSameOrigin(mk(tc.set)); got != tc.want {
			t.Fatalf("%s: wsSameOrigin = %v, want %v", tc.name, got, tc.want)
		}
	}
}
