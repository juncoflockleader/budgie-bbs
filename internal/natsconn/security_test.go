package natsconn

import "testing"

func TestSecurityOptionsFromEnv(t *testing.T) {
	// No auth env → no extra options (backward compatible).
	if got := len(securityOptionsFromEnv()); got != 0 {
		t.Fatalf("expected no options with a clean env, got %d", got)
	}

	t.Setenv("BUDGIE_NATS_TOKEN", "s3cret-token")
	t.Setenv("BUDGIE_NATS_USER", "budgie")
	t.Setenv("BUDGIE_NATS_PASSWORD", "pw")
	t.Setenv("BUDGIE_NATS_TLS", "1")
	if got := len(securityOptionsFromEnv()); got != 3 {
		t.Fatalf("expected token+userinfo+tls = 3 options, got %d", got)
	}
}
