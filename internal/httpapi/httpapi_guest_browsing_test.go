package httpapi_test

import (
	"net/http"
	"testing"
)

// TestGuestBrowsing verifies that an unauthenticated visitor (no token) can read
// the public browsing surface — non-member boards, their threads and posts, and
// the category list — while member-only boards stay gated and personal/member
// endpoints still require authentication.
func TestGuestBrowsing(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	// First registered user becomes admin.
	adminToken := registerUser(t, handler, "admin")

	// A thread on the default, non-member "general" board.
	publicThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Welcome",
		"body":  "hello world",
	}, &publicThread); status != http.StatusCreated || publicThread.Result == nil {
		t.Fatalf("create general thread status=%d err=%+v", status, publicThread.Error)
	}
	publicThreadID := publicThread.Result.ID

	// A member-only board with a thread that must stay private to guests.
	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":   "club",
		"name": "Club",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create club board status=%d err=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/club/settings", adminToken, map[string]bool{
		"memberReadMode": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set club member-read status=%d err=%+v", status, ack.Error)
	}
	clubThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/threads", adminToken, map[string]string{
		"title": "Roster",
		"body":  "members only",
	}, &clubThread); status != http.StatusCreated || clubThread.Result == nil {
		t.Fatalf("create club thread status=%d err=%+v", status, clubThread.Error)
	}
	clubThreadID := clubThread.Result.ID

	const guest = "" // no token

	// --- Guest CAN read the public browsing surface ---
	t.Run("guest reads public surface", func(t *testing.T) {
		cases := []struct{ name, path string }{
			{"categories", "/api/v1/categories"},
			{"boards", "/api/v1/boards"},
			{"board summaries", "/api/v1/boards/summary"},
			{"board detail", "/api/v1/boards/general"},
			{"thread detail", "/api/v1/threads/" + publicThreadID},
			{"thread posts", "/api/v1/threads/" + publicThreadID + "/posts"},
			{"thread polls", "/api/v1/threads/" + publicThreadID + "/polls"},
		}
		for _, tc := range cases {
			if status := doJSONRequest(t, handler, http.MethodGet, tc.path, guest, nil, nil); status != http.StatusOK {
				t.Errorf("guest GET %s (%s): status=%d, want 200", tc.path, tc.name, status)
			}
		}
	})

	// Guest sees the public thread when listing the board.
	t.Run("guest lists public board threads", func(t *testing.T) {
		threads := threadSummariesResponse{}
		if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/threads", guest, nil, &threads); status != http.StatusOK {
			t.Fatalf("guest list general threads status=%d", status)
		}
		if len(threads.Threads) == 0 {
			t.Fatalf("guest should see the public thread; got none")
		}
	})

	t.Run("guest reads public thread posts", func(t *testing.T) {
		posts := listPostsResponse{}
		if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+publicThreadID+"/posts", guest, nil, &posts); status != http.StatusOK {
			t.Fatalf("guest list public posts status=%d", status)
		}
		if len(posts.Posts) == 0 {
			t.Fatalf("guest should see at least the opening post")
		}
	})

	// --- Guest CANNOT read a member-only board ---
	t.Run("guest blocked from member board", func(t *testing.T) {
		cases := []struct{ name, path string }{
			{"board detail", "/api/v1/boards/club"},
			{"board threads", "/api/v1/boards/club/threads"},
			{"thread detail", "/api/v1/threads/" + clubThreadID},
			{"thread posts", "/api/v1/threads/" + clubThreadID + "/posts"},
			{"thread polls", "/api/v1/threads/" + clubThreadID + "/polls"},
		}
		for _, tc := range cases {
			if status := doJSONRequest(t, handler, http.MethodGet, tc.path, guest, nil, nil); status != http.StatusForbidden {
				t.Errorf("guest GET %s (%s): status=%d, want 403", tc.path, tc.name, status)
			}
		}
	})

	// --- Personal/member endpoints still require auth (401, not guest-served) ---
	t.Run("guest denied on personal endpoints", func(t *testing.T) {
		cases := []struct{ name, path string }{
			{"notifications", "/api/v1/notifications"},
			{"unread boards", "/api/v1/boards/unread"},
		}
		for _, tc := range cases {
			if status := doJSONRequest(t, handler, http.MethodGet, tc.path, guest, nil, nil); status != http.StatusUnauthorized {
				t.Errorf("guest GET %s (%s): status=%d, want 401", tc.path, tc.name, status)
			}
		}
	})
}

// TestGuestAccessOverride checks the per-board GuestAccess admin override:
// "hidden" blocks guests on an otherwise public board (without affecting
// logged-in users), and "public" exposes a member-only board to guests.
func TestGuestAccessOverride(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	adminToken := registerUser(t, handler, "admin")
	const guest = ""

	ack := ackResponse{}

	// "lounge": a normal public board. By default a guest can read it.
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id": "lounge", "name": "Lounge",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create lounge status=%d err=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/lounge", guest, nil, nil); status != http.StatusOK {
		t.Fatalf("guest default-read lounge status=%d, want 200", status)
	}

	// Override to "hidden": guests are blocked, logged-in users are not.
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/lounge/settings", adminToken, map[string]any{
		"guestAccess": "hidden",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set lounge guestAccess=hidden status=%d err=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/lounge", guest, nil, nil); status != http.StatusForbidden {
		t.Errorf("guest read hidden lounge status=%d, want 403", status)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/lounge", adminToken, nil, nil); status != http.StatusOK {
		t.Errorf("logged-in read hidden lounge status=%d, want 200 (override is guest-only)", status)
	}

	// "vip": a member-only board (guests blocked by default)...
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id": "vip", "name": "VIP",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create vip status=%d err=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/vip/settings", adminToken, map[string]bool{
		"memberReadMode": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set vip member-read status=%d err=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/vip", guest, nil, nil); status != http.StatusForbidden {
		t.Fatalf("guest read member-only vip status=%d, want 403", status)
	}
	// ...until the admin overrides it to "public".
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/vip/settings", adminToken, map[string]any{
		"guestAccess": "public",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set vip guestAccess=public status=%d err=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/vip", guest, nil, nil); status != http.StatusOK {
		t.Errorf("guest read public-override vip status=%d, want 200", status)
	}
}
