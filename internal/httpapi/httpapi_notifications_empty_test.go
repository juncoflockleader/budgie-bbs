package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A user with no notifications must get an empty JSON array, not null. A nil Go
// slice marshals to `null`, which crashed the web notifications page
// (notifs.some/.map on null). Guard the wire shape so it can't regress.
func TestHTTPNotificationsEmptyIsArrayNotNull(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	token := registerUser(t, handler, "loner")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `"notifications":null`) {
		t.Fatalf("notifications serialized as null (crashes the web client): %s", body)
	}
	if !strings.Contains(body, `"notifications":[]`) {
		t.Fatalf("expected an empty array for no notifications, got: %s", body)
	}
}
