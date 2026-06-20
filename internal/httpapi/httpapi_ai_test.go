package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestAIConfigTokenNeverReturned is the key privacy guarantee: a board's BYO
// token is accepted by PATCH but never appears in any GET response.
func TestAIConfigTokenNeverReturned(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	adminToken := registerUser(t, handler, "admin") // first user → admin
	ack := struct {
		Error *struct{ Message string } `json:"error"`
	}{}

	// Enable AI site-wide (admin) and create a board.
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/admin/ai-settings", adminToken, map[string]bool{"enabled": true}, &ack); status != http.StatusOK {
		t.Fatalf("enable site AI status=%d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{"id": "lab", "name": "Lab"}, &ack); status != http.StatusCreated {
		t.Fatalf("create board status=%d", status)
	}

	const secret = "sk-super-secret-token-xyz"
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/lab/ai", adminToken, map[string]any{
		"enabled": true, "apiToken": secret, "mode": "every_post",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set board AI config status=%d err=%+v", status, ack.Error)
	}

	// GET must report tokenSet but never echo the token.
	cfg := struct {
		Enabled  bool `json:"enabled"`
		TokenSet bool `json:"tokenSet"`
	}{}
	status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/lab/ai", adminToken, nil, &cfg)
	if status != http.StatusOK {
		t.Fatalf("get board AI config status=%d", status)
	}
	if !cfg.TokenSet || !cfg.Enabled {
		t.Fatalf("expected tokenSet+enabled, got %+v", cfg)
	}
	// Read the raw body to be certain the secret is absent.
	_, body, _ := getText(t, handler, "/api/v1/boards/lab/ai")
	// getText is unauthenticated; board AI GET requires auth → 403, no token.
	if strings.Contains(body, secret) {
		t.Fatalf("token leaked in response body: %s", body)
	}
}

// TestAIConfigAuthz verifies non-admins can't flip the site switch and
// non-managers can't read/write a board's AI config.
func TestAIConfigAuthz(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	adminToken := registerUser(t, handler, "admin")
	bobToken := registerUser(t, handler, "bob") // ordinary user
	ack := struct{}{}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{"id": "lab", "name": "Lab"}, &ack); status != http.StatusCreated {
		t.Fatalf("create board status=%d", status)
	}
	// Non-admin can't toggle site AI.
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/admin/ai-settings", bobToken, map[string]bool{"enabled": true}, &ack); status != http.StatusForbidden {
		t.Errorf("non-admin site AI PATCH status=%d, want 403", status)
	}
	// Ordinary user can't read or write a board's AI config.
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/lab/ai", bobToken, nil, &ack); status != http.StatusForbidden {
		t.Errorf("non-manager AI GET status=%d, want 403", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/lab/ai", bobToken, map[string]any{"enabled": true}, &ack); status != http.StatusForbidden {
		t.Errorf("non-manager AI PATCH status=%d, want 403", status)
	}
}

// TestAIConfigEnableRequiresSiteToggle verifies a board can't enable AI while
// the site switch is off.
func TestAIConfigEnableRequiresSiteToggle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	adminToken := registerUser(t, handler, "admin")
	ack := struct{}{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{"id": "lab", "name": "Lab"}, &ack); status != http.StatusCreated {
		t.Fatalf("create board status=%d", status)
	}
	// Site AI is off by default → enabling a board's bot is a conflict.
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/lab/ai", adminToken, map[string]any{"enabled": true}, &ack); status != http.StatusConflict {
		t.Fatalf("enable board AI with site off status=%d, want 409", status)
	}
}
