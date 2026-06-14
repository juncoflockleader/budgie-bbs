package httpapi_test

import (
	"net/http"
	"testing"
)

func TestHTTPPostNotificationsAndCleanup(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	threadAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", aliceToken, map[string]string{
		"title": "Notification flow",
		"body":  "Root post",
	}, &threadAck); status != http.StatusCreated || !threadAck.OK || threadAck.Result == nil {
		t.Fatalf("create thread status: %d error=%+v", status, threadAck.Error)
	}
	threadID := threadAck.Result.ID

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", aliceToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected root post, got %+v", posts.Posts)
	}
	rootPostID := posts.Posts[0].ID

	prefAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/threads/"+threadID+"/prefs", carolToken, map[string]string{
		"level": "watch",
	}, &prefAck); status != http.StatusCreated || !prefAck.OK {
		t.Fatalf("set watch pref status: %d error=%+v", status, prefAck.Error)
	}

	replyAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", bobToken, map[string]string{
		"body":    "@alice mention ping",
		"replyTo": rootPostID,
	}, &replyAck); status != http.StatusCreated || !replyAck.OK {
		t.Fatalf("append mention reply status: %d error=%+v", status, replyAck.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", bobToken, map[string]string{
		"body":    "plain reply ping",
		"replyTo": rootPostID,
	}, &replyAck); status != http.StatusCreated || !replyAck.OK {
		t.Fatalf("append plain reply status: %d error=%+v", status, replyAck.Error)
	}

	aliceNotifs := notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &aliceNotifs); status != http.StatusOK {
		t.Fatalf("list alice notifications status: %d", status)
	}
	if aliceNotifs.UnreadCount != 2 || len(aliceNotifs.Notifications) != 2 {
		t.Fatalf("expected alice two unread notifications, got %+v", aliceNotifs)
	}
	kinds := map[string]bool{}
	for _, notif := range aliceNotifs.Notifications {
		if notif.Actor != "bob" || notif.ThreadID != threadID {
			t.Fatalf("expected bob/thread notification, got %+v", notif)
		}
		kinds[notif.Kind] = true
	}
	if !kinds["mention"] || !kinds["reply"] {
		t.Fatalf("expected mention and reply notifications, got %+v", aliceNotifs.Notifications)
	}

	carolNotifs := notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", carolToken, nil, &carolNotifs); status != http.StatusOK {
		t.Fatalf("list carol notifications status: %d", status)
	}
	if carolNotifs.UnreadCount != 2 || len(carolNotifs.Notifications) != 2 {
		t.Fatalf("expected carol two watched notifications, got %+v", carolNotifs)
	}
	for _, notif := range carolNotifs.Notifications {
		if notif.Kind != "watched" || notif.Actor != "bob" || notif.ThreadID != threadID {
			t.Fatalf("expected watched notification, got %+v", notif)
		}
	}

	markResult := map[string]any{}
	readID := aliceNotifs.Notifications[0].ID
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/notifications/"+readID+"/read", aliceToken, nil, &markResult); status != http.StatusOK {
		t.Fatalf("mark notification read status: %d", status)
	}
	clearResult := map[string]any{}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/notifications?read=1", aliceToken, nil, &clearResult); status != http.StatusOK {
		t.Fatalf("clear read notifications status: %d", status)
	}
	aliceNotifs = notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &aliceNotifs); status != http.StatusOK {
		t.Fatalf("list alice after clear-read status: %d", status)
	}
	if aliceNotifs.UnreadCount != 1 || len(aliceNotifs.Notifications) != 1 || aliceNotifs.Notifications[0].Read {
		t.Fatalf("expected clear-read to leave one unread notification, got %+v", aliceNotifs)
	}

	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/notifications/"+carolNotifs.Notifications[0].ID, aliceToken, nil, &clearResult); status != http.StatusOK {
		t.Fatalf("cross-owner delete status: %d", status)
	}
	carolAfterCrossDelete := notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", carolToken, nil, &carolAfterCrossDelete); status != http.StatusOK {
		t.Fatalf("list carol after cross-owner delete status: %d", status)
	}
	if len(carolAfterCrossDelete.Notifications) != 2 {
		t.Fatalf("expected alice delete not to affect carol, got %+v", carolAfterCrossDelete)
	}

	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/notifications/"+aliceNotifs.Notifications[0].ID, aliceToken, nil, &clearResult); status != http.StatusOK {
		t.Fatalf("delete alice notification status: %d", status)
	}
	aliceNotifs = notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &aliceNotifs); status != http.StatusOK {
		t.Fatalf("list alice after delete status: %d", status)
	}
	if aliceNotifs.UnreadCount != 0 || len(aliceNotifs.Notifications) != 0 {
		t.Fatalf("expected alice feed empty, got %+v", aliceNotifs)
	}

	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/notifications", carolToken, nil, &clearResult); status != http.StatusOK {
		t.Fatalf("truncate carol notifications status: %d", status)
	}
	carolNotifs = notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", carolToken, nil, &carolNotifs); status != http.StatusOK {
		t.Fatalf("list carol after truncate status: %d", status)
	}
	if carolNotifs.UnreadCount != 0 || len(carolNotifs.Notifications) != 0 {
		t.Fatalf("expected carol feed empty, got %+v", carolNotifs)
	}
}
