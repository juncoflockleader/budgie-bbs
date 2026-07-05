package core_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/captchamodel"
)

var captchaCharRe = regexp.MustCompile(`>([A-Z2-9])</text>`)

func codeFromSVG(t *testing.T, svg string) string {
	t.Helper()
	m := captchaCharRe.FindAllStringSubmatch(svg, -1)
	if len(m) == 0 {
		t.Fatalf("no characters found in captcha svg")
	}
	out := ""
	for _, g := range m {
		out += g[1]
	}
	return out
}

func TestCaptchaOffIsNoOp(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	// Default (never configured) and explicit off both no-op.
	if c.CaptchaEnabled() {
		t.Fatal("captcha should be disabled by default")
	}
	if err := c.VerifyCaptcha(context.Background(), captchamodel.Submission{}); err != nil {
		t.Fatalf("disabled captcha should verify as no-op: %v", err)
	}
}

func TestNativeCaptchaIssueAndVerify(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	c.SetCaptcha(captchamodel.Config{Mode: captchamodel.ModeNative, Secret: "test-hmac"})

	ch, err := c.IssueCaptchaChallenge()
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if ch.ID == "" || ch.SVG == "" || ch.ExpiresAt == 0 {
		t.Fatalf("incomplete challenge: %+v", ch)
	}
	code := codeFromSVG(t, ch.SVG)

	// Wrong answer fails.
	if err := c.VerifyCaptcha(context.Background(), captchamodel.Submission{ChallengeID: ch.ID, Answer: "WRONG"}); err == nil {
		t.Fatal("wrong answer should fail")
	}
	// Wrong answer consumed the challenge (single-use): a retry — even correct — fails.
	if err := c.VerifyCaptcha(context.Background(), captchamodel.Submission{ChallengeID: ch.ID, Answer: code}); err == nil {
		t.Fatal("challenge should be single-use after a failed attempt")
	}

	// Fresh challenge, correct answer (case-insensitive) succeeds once.
	ch2, _ := c.IssueCaptchaChallenge()
	code2 := codeFromSVG(t, ch2.SVG)
	if err := c.VerifyCaptcha(context.Background(), captchamodel.Submission{ChallengeID: ch2.ID, Answer: "  " + code2 + "  "}); err != nil {
		t.Fatalf("correct answer should verify: %v", err)
	}
	// Reuse fails.
	if err := c.VerifyCaptcha(context.Background(), captchamodel.Submission{ChallengeID: ch2.ID, Answer: code2}); err == nil {
		t.Fatal("verified challenge should not be reusable")
	}
}

func TestNativeCaptchaRequiresChallengeID(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	c.SetCaptcha(captchamodel.Config{Mode: captchamodel.ModeNative, Secret: "k"})
	if err := c.VerifyCaptcha(context.Background(), captchamodel.Submission{}); err != core.ErrCaptchaRequired {
		t.Fatalf("expected ErrCaptchaRequired, got %v", err)
	}
}

func TestCaptchaPolicyHidesSecret(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	c.SetCaptcha(captchamodel.Config{Mode: captchamodel.ModeProvider, Provider: "turnstile", SiteKey: "site-123", Secret: "super-secret"})
	p := c.CaptchaPolicy()
	if !p.Enabled || p.Mode != captchamodel.ModeProvider || p.Provider != "turnstile" || p.SiteKey != "site-123" {
		t.Fatalf("unexpected policy: %+v", p)
	}
	// Provider mode with an empty token is "required", not a silent pass.
	if err := c.VerifyCaptcha(context.Background(), captchamodel.Submission{}); err != core.ErrCaptchaRequired {
		t.Fatalf("expected ErrCaptchaRequired for empty provider token, got %v", err)
	}
}
