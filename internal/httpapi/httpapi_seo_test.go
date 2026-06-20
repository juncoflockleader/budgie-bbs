package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getText(t *testing.T, handler http.Handler, path string) (int, string, string) {
	t.Helper()
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr.Code, rr.Body.String(), rr.Header().Get("Content-Type")
}

// TestRobotsTxt checks robots.txt advertises the sitemap and keeps the API out
// of the index.
func TestRobotsTxt(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	status, body, ctype := getText(t, handler, "/robots.txt")
	if status != http.StatusOK {
		t.Fatalf("robots status=%d", status)
	}
	if !strings.HasPrefix(ctype, "text/plain") {
		t.Errorf("robots content-type=%q", ctype)
	}
	for _, want := range []string{"User-agent: *", "Disallow: /api/", "Sitemap:", "/sitemap.xml"} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt missing %q\n---\n%s", want, body)
		}
	}
}

// TestSitemap checks the sitemap lists guest-readable boards and threads while
// excluding member-only and admin-hidden boards.
func TestSitemap(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	adminToken := registerUser(t, handler, "admin")

	// Public thread on the default "general" board (guest-readable).
	pub := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Hello world", "body": "public",
	}, &pub); status != http.StatusCreated || pub.Result == nil {
		t.Fatalf("create public thread status=%d err=%+v", status, pub.Error)
	}

	// Member-only board "club" — excluded from the sitemap.
	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{"id": "club", "name": "Club"}, &ack); status != http.StatusCreated {
		t.Fatalf("create club status=%d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/club/settings", adminToken, map[string]bool{"memberReadMode": true}, &ack); status != http.StatusCreated {
		t.Fatalf("set club member-read status=%d", status)
	}
	clubThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/threads", adminToken, map[string]string{"title": "Roster", "body": "secret"}, &clubThread); status != http.StatusCreated {
		t.Fatalf("create club thread status=%d", status)
	}

	// Public board "lounge" but admin-overridden guestAccess=hidden — excluded.
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{"id": "lounge", "name": "Lounge"}, &ack); status != http.StatusCreated {
		t.Fatalf("create lounge status=%d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/lounge/settings", adminToken, map[string]any{"guestAccess": "hidden"}, &ack); status != http.StatusCreated {
		t.Fatalf("set lounge hidden status=%d", status)
	}
	hiddenThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/lounge/threads", adminToken, map[string]string{"title": "Lounge talk", "body": "x"}, &hiddenThread); status != http.StatusCreated {
		t.Fatalf("create lounge thread status=%d", status)
	}

	status, body, ctype := getText(t, handler, "/sitemap.xml")
	if status != http.StatusOK {
		t.Fatalf("sitemap status=%d", status)
	}
	if !strings.Contains(ctype, "xml") {
		t.Errorf("sitemap content-type=%q", ctype)
	}
	if !strings.Contains(body, "<urlset") || !strings.Contains(body, "sitemaps.org/schemas/sitemap/0.9") {
		t.Errorf("sitemap not a valid urlset:\n%s", body)
	}
	// Present: homepage, public board, public thread.
	for _, want := range []string{"/b/general", "/t/" + pub.Result.ID} {
		if !strings.Contains(body, want) {
			t.Errorf("sitemap missing %q", want)
		}
	}
	// Absent: member-only board + its thread, hidden board + its thread.
	for _, bad := range []string{"/b/club", "/t/" + clubThread.Result.ID, "/b/lounge", "/t/" + hiddenThread.Result.ID} {
		if strings.Contains(body, bad) {
			t.Errorf("sitemap should not contain %q\n---\n%s", bad, body)
		}
	}
}
