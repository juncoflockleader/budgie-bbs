package core

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/captcha"
)

var (
	// ErrCaptchaRequired means captcha is enabled but no challenge was supplied.
	ErrCaptchaRequired = errors.New("captcha required")
	// ErrCaptchaFailed means the supplied captcha did not verify.
	ErrCaptchaFailed = errors.New("captcha verification failed")
)

const (
	CaptchaModeOff      = "off"
	CaptchaModeNative   = "native"
	CaptchaModeProvider = "provider"

	defaultCaptchaTTL = 5 * time.Minute
)

// CaptchaConfig configures signup captcha. Secret is server-side only (a
// provider secret, or the HMAC key for native challenges); it is never exposed.
type CaptchaConfig struct {
	Mode      string // off | native | provider
	Provider  string // provider mode: recaptcha | hcaptcha | turnstile
	SiteKey   string // public site key, exposed via the auth policy endpoint
	Secret    string // provider secret OR native HMAC key
	VerifyURL string // provider verify URL (optional; defaults per provider)
	TTL       time.Duration
}

type captchaRuntime struct {
	cfg      CaptchaConfig
	provider *captcha.ProviderVerifier
}

// SetCaptcha configures signup captcha. Call once at startup. Mode "off" (the
// default) disables it. Safe to call on a nil-captcha Core.
func (c *Core) SetCaptcha(cfg CaptchaConfig) {
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.Mode == "" {
		cfg.Mode = CaptchaModeOff
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaultCaptchaTTL
	}
	rt := &captchaRuntime{cfg: cfg}
	if cfg.Mode == CaptchaModeProvider {
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
		return CaptchaModeOff
	}
	return c.captcha.cfg.Mode
}

// CaptchaEnabled reports whether signup captcha is active.
func (c *Core) CaptchaEnabled() bool {
	m := c.captchaMode()
	return m == CaptchaModeNative || m == CaptchaModeProvider
}

// CaptchaPolicy is the public (unauthenticated) view of captcha configuration —
// never includes the secret.
type CaptchaPolicy struct {
	Enabled  bool   `json:"enabled"`
	Mode     string `json:"mode"`
	Provider string `json:"provider,omitempty"`
	SiteKey  string `json:"siteKey,omitempty"`
}

// CaptchaPolicy returns the public captcha policy for the auth policy endpoint.
func (c *Core) CaptchaPolicy() CaptchaPolicy {
	if !c.CaptchaEnabled() {
		return CaptchaPolicy{Enabled: false, Mode: CaptchaModeOff}
	}
	cfg := c.captcha.cfg
	return CaptchaPolicy{Enabled: true, Mode: cfg.Mode, Provider: cfg.Provider, SiteKey: cfg.SiteKey}
}

// CaptchaChallenge is a freshly issued native challenge.
type CaptchaChallenge struct {
	ID        string `json:"id"`
	SVG       string `json:"svg"`
	ExpiresAt int64  `json:"expiresAt"`
}

// IssueCaptchaChallenge mints a native distorted-text challenge, persists its
// hashed answer with an expiry, and returns the challenge id + SVG. Only valid
// in native mode.
func (c *Core) IssueCaptchaChallenge() (*CaptchaChallenge, error) {
	if c.captchaMode() != CaptchaModeNative {
		return nil, errors.New("native captcha not enabled")
	}
	now := nowMS()
	// Opportunistically prune expired challenges to bound table growth.
	_, _ = qExec(c.DB, `DELETE FROM captcha_challenges WHERE expires_at < ?`, now)

	code := captcha.RandomCode(5)
	id := newID("cap_")
	hash := captcha.HashAnswer(c.captcha.cfg.Secret, id, code)
	exp := now + c.captcha.cfg.TTL.Milliseconds()
	if _, err := qExec(c.DB,
		`INSERT INTO captcha_challenges (id, answer_hash, created_at, expires_at) VALUES (?,?,?,?)`,
		id, hash, now, exp,
	); err != nil {
		return nil, err
	}
	return &CaptchaChallenge{ID: id, SVG: captcha.RenderSVG(code), ExpiresAt: exp}, nil
}

// CaptchaSubmission carries what a signup form sends for captcha verification.
type CaptchaSubmission struct {
	ChallengeID string // native
	Answer      string // native
	Token       string // provider
	RemoteIP    string
}

// VerifyCaptcha enforces the configured captcha. It is a no-op when captcha is
// disabled, returns ErrCaptchaRequired when enabled but unanswered, and
// ErrCaptchaFailed on a bad/expired/unverifiable answer. Native challenges are
// single-use. Provider verification fails closed on transport errors.
func (c *Core) VerifyCaptcha(ctx context.Context, sub CaptchaSubmission) error {
	switch c.captchaMode() {
	case CaptchaModeOff:
		return nil
	case CaptchaModeNative:
		return c.verifyNativeCaptcha(sub)
	case CaptchaModeProvider:
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

func (c *Core) verifyNativeCaptcha(sub CaptchaSubmission) error {
	if strings.TrimSpace(sub.ChallengeID) == "" {
		return ErrCaptchaRequired
	}
	var hash string
	var exp int64
	err := qQueryRow(c.DB,
		`SELECT answer_hash, expires_at FROM captcha_challenges WHERE id=?`,
		sub.ChallengeID,
	).Scan(&hash, &exp)
	if err == sql.ErrNoRows {
		return ErrCaptchaFailed
	}
	if err != nil {
		return err
	}
	// Single-use: consume the challenge regardless of outcome.
	_, _ = qExec(c.DB, `DELETE FROM captcha_challenges WHERE id=?`, sub.ChallengeID)
	if exp < nowMS() {
		return ErrCaptchaFailed
	}
	if !captcha.AnswerMatches(c.captcha.cfg.Secret, sub.ChallengeID, sub.Answer, hash) {
		return ErrCaptchaFailed
	}
	return nil
}
