package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/captcha"
	"github.com/juncoflockleader/budgie-bbs/internal/core/captchamodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/captchastore"
)

var (
	// ErrCaptchaRequired means captcha is enabled but no challenge was supplied.
	ErrCaptchaRequired = errors.New("captcha required")
	// ErrCaptchaFailed means the supplied captcha did not verify.
	ErrCaptchaFailed = errors.New("captcha verification failed")
)

type captchaRuntime struct {
	cfg      captchamodel.Config
	provider *captcha.ProviderVerifier
}

// SetCaptcha configures signup captcha. Call once at startup. Mode "off" (the
// default) disables it. Safe to call on a nil-captcha Core.
func (c *Core) SetCaptcha(cfg captchamodel.Config) {
	cfg = captchamodel.NormalizeConfig(cfg)
	rt := &captchaRuntime{cfg: cfg}
	if cfg.Mode == captchamodel.ModeProvider {
		verifyURL := cfg.VerifyURL
		if verifyURL == "" {
			verifyURL = captcha.DefaultVerifyURL(cfg.Provider)
		}
		rt.provider = captcha.NewProviderVerifier(
			captcha.ProviderConfig{VerifyURL: verifyURL, Secret: cfg.Secret},
			&http.Client{Timeout: 10 * time.Second},
		)
	}
	c.captcha = rt
}

func (c *Core) captchaMode() string {
	if c == nil || c.captcha == nil {
		return captchamodel.ModeOff
	}
	return c.captcha.cfg.Mode
}

// CaptchaEnabled reports whether signup captcha is active.
func (c *Core) CaptchaEnabled() bool {
	return captchamodel.EnabledMode(c.captchaMode())
}

// CaptchaPolicy returns the public captcha policy for the auth policy endpoint.
func (c *Core) CaptchaPolicy() captchamodel.Policy {
	if c == nil || c.captcha == nil {
		return captchamodel.PolicyForConfig(captchamodel.Config{})
	}
	return captchamodel.PolicyForConfig(c.captcha.cfg)
}

// IssueCaptchaChallenge mints a native distorted-text challenge, persists its
// hashed answer with an expiry, and returns the challenge id + SVG. Only valid
// in native mode.
func (c *Core) IssueCaptchaChallenge() (*captchamodel.Challenge, error) {
	if c.captchaMode() != captchamodel.ModeNative {
		return nil, errors.New("native captcha not enabled")
	}
	now := nowMS()
	// Opportunistically prune expired challenges to bound table growth.
	_ = captchastore.PruneExpired(c.DB, now)

	code := captcha.RandomCode(5)
	id := newID("cap_")
	hash := captcha.HashAnswer(c.captcha.cfg.Secret, id, code)
	exp := now + c.captcha.cfg.TTL.Milliseconds()
	if err := captchastore.InsertChallenge(c.DB, id, hash, now, exp); err != nil {
		return nil, err
	}
	return &captchamodel.Challenge{ID: id, SVG: captcha.RenderSVG(code), ExpiresAt: exp}, nil
}

// VerifyCaptcha enforces the configured captcha. It is a no-op when captcha is
// disabled, returns ErrCaptchaRequired when enabled but unanswered, and
// ErrCaptchaFailed on a bad/expired/unverifiable answer. Native challenges are
// single-use. Provider verification fails closed on transport errors.
func (c *Core) VerifyCaptcha(ctx context.Context, sub captchamodel.Submission) error {
	switch c.captchaMode() {
	case captchamodel.ModeOff:
		return nil
	case captchamodel.ModeNative:
		return c.verifyNativeCaptcha(sub)
	case captchamodel.ModeProvider:
		if strings.TrimSpace(sub.Token) == "" {
			return ErrCaptchaRequired
		}
		ok, err := c.captcha.provider.Verify(ctx, sub.Token, sub.RemoteIP)
		if err != nil || !ok {
			return ErrCaptchaFailed
		}
		return nil
	default:
		return nil
	}
}

func (c *Core) verifyNativeCaptcha(sub captchamodel.Submission) error {
	if strings.TrimSpace(sub.ChallengeID) == "" {
		return ErrCaptchaRequired
	}
	challenge, claimed, err := captchastore.ClaimChallenge(c.DB, sub.ChallengeID)
	if err != nil {
		return err
	}
	// Single-use: atomically claim the challenge. Exactly one concurrent request
	// deletes the row; any replay of the same solved challenge gets 0 rows
	// affected and fails, so one solved captcha authorizes only one signup.
	if !claimed {
		return ErrCaptchaFailed
	}
	if challenge.ExpiresAt < nowMS() {
		return ErrCaptchaFailed
	}
	if !captcha.AnswerMatches(c.captcha.cfg.Secret, sub.ChallengeID, sub.Answer, challenge.AnswerHash) {
		return ErrCaptchaFailed
	}
	return nil
}
