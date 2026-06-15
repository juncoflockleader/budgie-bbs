package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

var svgCharRe = regexp.MustCompile(`>([A-Z2-9])</text>`)

func TestSignupCaptchaFlow(t *testing.T) {
	c := newHTTPTestCore(t)
	c.SetCaptcha(core.CaptchaConfig{Mode: core.CaptchaModeNative, Secret: "http-test-hmac"})
	h := httpapi.New(c, []byte("test-secret")).Handler()

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		var rdr *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rdr)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Public policy advertises native captcha (no secret leaked).
	pol := do(http.MethodGet, "/api/v1/auth/policy", nil)
	if pol.Code != 200 {
		t.Fatalf("policy: %d %s", pol.Code, pol.Body.String())
	}
	if !bytesContains(pol.Body.Bytes(), `"mode":"native"`) || bytesContains(pol.Body.Bytes(), "http-test-hmac") {
		t.Fatalf("unexpected policy body: %s", pol.Body.String())
	}

	// Issue a native challenge.
	chRec := do(http.MethodGet, "/api/v1/auth/captcha", nil)
	if chRec.Code != 200 {
		t.Fatalf("captcha: %d %s", chRec.Code, chRec.Body.String())
	}
	var ch struct {
		ID  string `json:"id"`
		SVG string `json:"svg"`
	}
	if err := json.Unmarshal(chRec.Body.Bytes(), &ch); err != nil {
		t.Fatal(err)
	}
	code := ""
	for _, g := range svgCharRe.FindAllStringSubmatch(ch.SVG, -1) {
		code += g[1]
	}
	if ch.ID == "" || code == "" {
		t.Fatalf("bad challenge: id=%q code=%q", ch.ID, code)
	}

	// Register without captcha → 400 captcha_required.
	r1 := do(http.MethodPost, "/api/v1/auth/register", map[string]string{"name": "alice", "password": "pw123456"})
	if r1.Code != http.StatusBadRequest || !bytesContains(r1.Body.Bytes(), "captcha_required") {
		t.Fatalf("expected captcha_required, got %d %s", r1.Code, r1.Body.String())
	}

	// Register with wrong answer → 400 captcha_failed.
	r2 := do(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"name": "alice", "password": "pw123456",
		"captchaChallengeId": ch.ID, "captchaAnswer": "ZZZZZ",
	})
	if r2.Code != http.StatusBadRequest || !bytesContains(r2.Body.Bytes(), "captcha_failed") {
		t.Fatalf("expected captcha_failed, got %d %s", r2.Code, r2.Body.String())
	}

	// A fresh challenge + correct answer → account created.
	chRec2 := do(http.MethodGet, "/api/v1/auth/captcha", nil)
	var ch2 struct {
		ID  string `json:"id"`
		SVG string `json:"svg"`
	}
	_ = json.Unmarshal(chRec2.Body.Bytes(), &ch2)
	code2 := ""
	for _, g := range svgCharRe.FindAllStringSubmatch(ch2.SVG, -1) {
		code2 += g[1]
	}
	r3 := do(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"name": "alice", "password": "pw123456",
		"captchaChallengeId": ch2.ID, "captchaAnswer": code2,
	})
	if r3.Code != http.StatusCreated {
		t.Fatalf("expected 201 created, got %d %s", r3.Code, r3.Body.String())
	}
}

func bytesContains(b []byte, sub string) bool {
	return bytes.Contains(b, []byte(sub))
}
