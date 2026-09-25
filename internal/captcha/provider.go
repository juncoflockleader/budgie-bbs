package captcha

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultVerifyURL returns the standard siteverify endpoint for a known
// provider, or "" if unknown (the caller must then supply an explicit URL).
// reCAPTCHA, hCaptcha, and Cloudflare Turnstile share the same request/response
// contract, so a single verifier covers all three.
func DefaultVerifyURL(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "recaptcha":
		return "https://www.google.com/recaptcha/api/siteverify"
	case "hcaptcha":
		return "https://hcaptcha.com/siteverify"
	case "turnstile":
		return "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	default:
		return ""
	}
}

// ProviderConfig configures the third-party verifier.
type ProviderConfig struct {
	VerifyURL string
	Secret    string
}

// ProviderVerifier verifies provider tokens over HTTP. The zero value is not
// usable; build one with NewProviderVerifier.
type ProviderVerifier struct {
	cfg    ProviderConfig
	client *http.Client
}

// NewProviderVerifier returns a verifier. A nil client uses a default with a
// short timeout.
func NewProviderVerifier(cfg ProviderConfig, client *http.Client) *ProviderVerifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &ProviderVerifier{cfg: cfg, client: client}
}

type siteverifyResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

// Verify posts the token to the provider's siteverify endpoint and reports
// whether the challenge passed. remoteIP is optional.
func (v *ProviderVerifier) Verify(ctx context.Context, token, remoteIP string) (bool, error) {
	if v.cfg.Secret == "" || v.cfg.VerifyURL == "" {
		return false, fmt.Errorf("captcha provider: missing secret or verify URL")
	}
	if strings.TrimSpace(token) == "" {
		return false, nil
	}
	form := url.Values{}
	form.Set("secret", v.cfg.Secret)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.cfg.VerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("captcha provider: verify returned status %d", resp.StatusCode)
	}
	var out siteverifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("captcha provider: decode response: %w", err)
	}
	return out.Success, nil
}
