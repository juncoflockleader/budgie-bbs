package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
	"github.com/juncoflockleader/budgie-bbs/internal/mailer"
)

type capMailer struct{ ch chan mailer.Message }

func (m *capMailer) Send(_ context.Context, msg mailer.Message) error { m.ch <- msg; return nil }

var tokenRe = regexp.MustCompile(`token=(everi_[a-f0-9]+)`)

func TestSignupEmailVerificationFlow(t *testing.T) {
	c := newHTTPTestCore(t)
	fm := &capMailer{ch: make(chan mailer.Message, 4)}
	c.SetMailer(fm, "no-reply@budgie.test", true, "")
	h := httpapi.New(c, []byte("test-secret")).Handler()

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		var rdr *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, rdr))
		return rec
	}

	// Register without an email → 422 email_required.
	noEmail := do(http.MethodPost, "/api/v1/auth/register", map[string]string{"name": "dave", "password": "pw12345678"})
	if noEmail.Code != http.StatusUnprocessableEntity || !bytes.Contains(noEmail.Body.Bytes(), []byte("email_required")) {
		t.Fatalf("expected email_required, got %d %s", noEmail.Code, noEmail.Body.String())
	}

	// Register with an email → 202 verification_required, no token issued.
	reg := do(http.MethodPost, "/api/v1/auth/register", map[string]string{"name": "dave", "password": "pw12345678", "email": "dave@dest.test"})
	if reg.Code != http.StatusAccepted || !bytes.Contains(reg.Body.Bytes(), []byte("verification_required")) {
		t.Fatalf("expected verification_required, got %d %s", reg.Code, reg.Body.String())
	}
	if bytes.Contains(reg.Body.Bytes(), []byte("\"token\"")) {
		t.Fatalf("no token should be issued before verification: %s", reg.Body.String())
	}

	// Capture the verification email + token.
	var msg mailer.Message
	select {
	case msg = <-fm.ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no verification email")
	}
	tm := tokenRe.FindStringSubmatch(msg.Body)
	if tm == nil {
		t.Fatalf("no token in email: %s", msg.Body)
	}
	token := tm[1]

	// Login blocked until verified.
	pre := do(http.MethodPost, "/api/v1/auth/login", map[string]string{"name": "dave", "password": "pw12345678"})
	if pre.Code != http.StatusUnauthorized || !bytes.Contains(pre.Body.Bytes(), []byte("email_not_verified")) {
		t.Fatalf("expected email_not_verified, got %d %s", pre.Code, pre.Body.String())
	}

	// Click the verification link.
	vr := do(http.MethodGet, "/api/v1/auth/verify-email?token="+token, nil)
	if vr.Code != http.StatusOK || !bytes.Contains(vr.Body.Bytes(), []byte("Email verified")) {
		t.Fatalf("verify failed: %d %s", vr.Code, vr.Body.String())
	}

	// Login now succeeds.
	post := do(http.MethodPost, "/api/v1/auth/login", map[string]string{"name": "dave", "password": "pw12345678"})
	if post.Code != http.StatusOK || !bytes.Contains(post.Body.Bytes(), []byte("\"token\"")) {
		t.Fatalf("login after verify failed: %d %s", post.Code, post.Body.String())
	}
}
