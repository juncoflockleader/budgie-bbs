package core_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/mailer"
)

type captureMailer struct{ ch chan mailer.Message }

func (m *captureMailer) Send(_ context.Context, msg mailer.Message) error {
	m.ch <- msg
	return nil
}

var verifyTokenRe = regexp.MustCompile(`token=(everi_[a-f0-9]+)`)

func waitMail(t *testing.T, ch chan mailer.Message) mailer.Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no verification email delivered within timeout")
		return mailer.Message{}
	}
}

func TestEmailVerificationLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	fm := &captureMailer{ch: make(chan mailer.Message, 4)}
	c.SetMailer(fm, "no-reply@budgie.test", true, "https://bbs.example")
	if !c.EmailVerificationEnabled() {
		t.Fatal("verification should be enabled with a mailer")
	}

	alice := registerAndGetUser(t, c, "alice", "pw12345678")
	if err := c.StartEmailVerification(alice.ID, "alice@dest.test"); err != nil {
		t.Fatalf("start verification: %v", err)
	}

	// The outbox worker should deliver the verification email.
	msg := waitMail(t, fm.ch)
	if msg.To != "alice@dest.test" {
		t.Fatalf("email To = %q", msg.To)
	}
	m := verifyTokenRe.FindStringSubmatch(msg.Body)
	if m == nil {
		t.Fatalf("no token link in email body:\n%s", msg.Body)
	}
	token := m[1]

	// Login is blocked until verified.
	if _, err := c.AuthenticateUser("alice", "pw12345678"); err != core.ErrEmailNotVerified {
		t.Fatalf("expected ErrEmailNotVerified, got %v", err)
	}

	// Verify the token, then login works.
	if _, err := c.VerifyEmailToken(token); err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if _, err := c.AuthenticateUser("alice", "pw12345678"); err != nil {
		t.Fatalf("login after verify: %v", err)
	}

	// Token is single-use.
	if _, err := c.VerifyEmailToken(token); err != core.ErrVerificationTokenInvalid {
		t.Fatalf("expected token reuse to fail, got %v", err)
	}
}

func TestEmailVerificationDisabledByDefault(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	if c.EmailVerificationEnabled() {
		t.Fatal("verification should be off without a mailer")
	}
	// A normal account (verified by default) can log in.
	registerAndGetUser(t, c, "bob", "pw12345678")
	if _, err := c.AuthenticateUser("bob", "pw12345678"); err != nil {
		t.Fatalf("login should work with verification off: %v", err)
	}
}

func TestResendEmailVerification(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	fm := &captureMailer{ch: make(chan mailer.Message, 4)}
	c.SetMailer(fm, "no-reply@budgie.test", true, "")

	carol := registerAndGetUser(t, c, "carol", "pw12345678")
	if err := c.StartEmailVerification(carol.ID, "carol@dest.test"); err != nil {
		t.Fatal(err)
	}
	waitMail(t, fm.ch) // initial

	if err := c.ResendEmailVerification("carol"); err != nil {
		t.Fatalf("resend: %v", err)
	}
	resent := waitMail(t, fm.ch)
	if resent.To != "carol@dest.test" {
		t.Fatalf("resent To = %q", resent.To)
	}
}
