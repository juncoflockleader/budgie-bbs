package captchamodel

import (
	"strings"
	"time"
)

const (
	ModeOff      = "off"
	ModeNative   = "native"
	ModeProvider = "provider"

	DefaultTTL = 5 * time.Minute
)

// Config configures signup captcha. Secret is server-side only (a provider
// secret, or the HMAC key for native challenges); it is never exposed.
type Config struct {
	Mode      string // off | native | provider
	Provider  string // provider mode: recaptcha | hcaptcha | turnstile
	SiteKey   string // public site key, exposed via the auth policy endpoint
	Secret    string // provider secret OR native HMAC key
	VerifyURL string // provider verify URL (optional; defaults per provider)
	TTL       time.Duration
}

// Policy is the public (unauthenticated) view of captcha configuration.
type Policy struct {
	Enabled  bool   `json:"enabled"`
	Mode     string `json:"mode"`
	Provider string `json:"provider,omitempty"`
	SiteKey  string `json:"siteKey,omitempty"`
}

// Submission carries what a signup form sends for captcha verification.
type Submission struct {
	ChallengeID string // native
	Answer      string // native
	Token       string // provider
	RemoteIP    string
}

// Challenge is a freshly issued native captcha challenge.
type Challenge struct {
	ID        string `json:"id"`
	SVG       string `json:"svg"`
	ExpiresAt int64  `json:"expiresAt"`
}

func NormalizeConfig(cfg Config) Config {
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.Mode == "" {
		cfg.Mode = ModeOff
	}
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultTTL
	}
	return cfg
}

func EnabledMode(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	return mode == ModeNative || mode == ModeProvider
}

func PolicyForConfig(cfg Config) Policy {
	cfg = NormalizeConfig(cfg)
	if !EnabledMode(cfg.Mode) {
		return Policy{Enabled: false, Mode: ModeOff}
	}
	return Policy{Enabled: true, Mode: cfg.Mode, Provider: cfg.Provider, SiteKey: cfg.SiteKey}
}
