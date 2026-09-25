package captchamodel

import "testing"

func TestNormalizeConfigDefaults(t *testing.T) {
	cfg := NormalizeConfig(Config{Mode: " Native "})
	if cfg.Mode != ModeNative {
		t.Fatalf("mode = %q, want %q", cfg.Mode, ModeNative)
	}
	if cfg.TTL != DefaultTTL {
		t.Fatalf("TTL = %v, want default %v", cfg.TTL, DefaultTTL)
	}
}

func TestNormalizeConfigEmptyModeDisablesCaptcha(t *testing.T) {
	cfg := NormalizeConfig(Config{})
	if cfg.Mode != ModeOff {
		t.Fatalf("empty mode = %q, want off", cfg.Mode)
	}
	if !EnabledMode(ModeNative) || !EnabledMode(ModeProvider) || EnabledMode(ModeOff) {
		t.Fatalf("EnabledMode returned unexpected values")
	}
}

func TestPolicyForConfig(t *testing.T) {
	disabled := PolicyForConfig(Config{})
	if disabled.Enabled || disabled.Mode != ModeOff {
		t.Fatalf("disabled policy = %+v", disabled)
	}

	enabled := PolicyForConfig(Config{
		Mode:     " Provider ",
		Provider: "turnstile",
		SiteKey:  "site-123",
		Secret:   "secret",
	})
	if !enabled.Enabled || enabled.Mode != ModeProvider || enabled.Provider != "turnstile" || enabled.SiteKey != "site-123" {
		t.Fatalf("enabled policy = %+v", enabled)
	}
}
