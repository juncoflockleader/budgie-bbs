package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestHTTPPrivateMailThreadAndAuthorReads(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	root := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"to":      []string{"alice"},
		"subject": "Study thread",
		"body":    "Root note.",
	}, &root); status != http.StatusCreated {
		t.Fatalf("send root mail status: %d error=%+v", status, root.Error)
	}
	aliceReply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", aliceToken, map[string]any{
		"to":      []string{"bob"},
		"subject": "Re: Study thread",
		"body":    "First reply.",
		"replyTo": root.Result.ID,
	}, &aliceReply); status != http.StatusCreated {
		t.Fatalf("send first reply status: %d error=%+v", status, aliceReply.Error)
	}
	bobFollowup := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"to":      []string{"alice"},
		"subject": "Re: Study thread",
		"body":    "Nested followup.",
		"replyTo": aliceReply.Result.ID,
	}, &bobFollowup); status != http.StatusCreated {
		t.Fatalf("send nested reply status: %d error=%+v", status, bobFollowup.Error)
	}
	carolMail := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", carolToken, map[string]any{
		"to":      []string{"alice"},
		"subject": "Separate sender",
		"body":    "Different author.",
	}, &carolMail); status != http.StatusCreated {
		t.Fatalf("send carol mail status: %d error=%+v", status, carolMail.Error)
	}

	thread := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail/thread/"+bobFollowup.Result.ID, aliceToken, nil, &thread); status != http.StatusOK {
		t.Fatalf("list alice mail thread status: %d", status)
	}
	if len(thread.Mail) != 3 {
		t.Fatalf("expected full three-message thread, got %+v", thread.Mail)
	}
	wantThreadIDs := []string{root.Result.ID, aliceReply.Result.ID, bobFollowup.Result.ID}
	for i, want := range wantThreadIDs {
		if thread.Mail[i].ID != want {
			t.Fatalf("expected thread item %d to be %s, got %+v", i, want, thread.Mail)
		}
	}
	if thread.Mail[1].ParentID != root.Result.ID || thread.Mail[2].ParentID != aliceReply.Result.ID {
		t.Fatalf("expected recursive parent ids in thread, got %+v", thread.Mail)
	}

	author := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail/author/"+bobFollowup.Result.ID, aliceToken, nil, &author); status != http.StatusOK {
		t.Fatalf("list alice mail author status: %d", status)
	}
	if len(author.Mail) != 2 || author.Mail[0].ID != bobFollowup.Result.ID || author.Mail[1].ID != root.Result.ID {
		t.Fatalf("expected visible mail from bob only, got %+v", author.Mail)
	}
	for _, item := range author.Mail {
		if item.ID == aliceReply.Result.ID || item.ID == carolMail.Result.ID {
			t.Fatalf("author read leaked another sender's mail, got %+v", author.Mail)
		}
	}

	forbidden := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail/thread/"+root.Result.ID, carolToken, nil, &forbidden); status != http.StatusNotFound {
		t.Fatalf("expected hidden mail thread to be not found, got %d", status)
	}
}

func TestHTTPForwardPrivateMail(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")
	daveToken := registerUser(t, handler, "dave")

	source := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"to":      []string{"alice"},
		"subject": "Lab schedule",
		"body":    "The lab opens at seven.",
		"attachments": []map[string]any{{
			"filename":    "schedule.txt",
			"contentType": "text/plain",
			"sizeBytes":   42,
			"url":         "https://example.edu/schedule.txt",
		}},
	}, &source); status != http.StatusCreated {
		t.Fatalf("send source mail status: %d error=%+v", status, source.Error)
	}

	forward := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail/"+source.Result.ID+"/forward", aliceToken, map[string]any{
		"to":   []string{"carol"},
		"note": "FYI for tomorrow.",
	}, &forward); status != http.StatusCreated {
		t.Fatalf("forward mail status: %d error=%+v", status, forward.Error)
	}

	carolMail := mailItemResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail/"+forward.Result.ID, carolToken, nil, &carolMail); status != http.StatusOK {
		t.Fatalf("get forwarded mail status: %d", status)
	}
	if carolMail.FromName != "alice" || carolMail.Subject != "Fwd: Lab schedule" {
		t.Fatalf("expected forwarded mail from alice with default subject, got %+v", carolMail)
	}
	for _, want := range []string{"FYI for tomorrow.", "----- Forwarded mail -----", "From: bob", "To: alice", "Subject: Lab schedule", "Attachments: schedule.txt", "The lab opens at seven."} {
		if !strings.Contains(carolMail.Body, want) {
			t.Fatalf("expected forwarded body to contain %q, got %q", want, carolMail.Body)
		}
	}

	aliceSent := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail?mailbox=sent", aliceToken, nil, &aliceSent); status != http.StatusOK {
		t.Fatalf("list alice sent status: %d", status)
	}
	if len(aliceSent.Mail) != 1 || aliceSent.Mail[0].ID != forward.Result.ID {
		t.Fatalf("expected forwarded mail in alice sent box, got %+v", aliceSent.Mail)
	}

	hidden := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail/"+source.Result.ID+"/forward", daveToken, map[string]any{
		"to": []string{"carol"},
	}, &hidden); status != http.StatusNotFound {
		t.Fatalf("expected hidden mail forward to be not found, got %d error=%+v", status, hidden.Error)
	}
}

func TestHTTPDeletePrivateMailRange(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	registerUser(t, handler, "carol")

	first := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"to":      []string{"alice"},
		"subject": "Range one",
		"body":    "first",
	}, &first); status != http.StatusCreated {
		t.Fatalf("send first mail status: %d error=%+v", status, first.Error)
	}
	second := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"to":      []string{"alice"},
		"subject": "Range two",
		"body":    "second",
	}, &second); status != http.StatusCreated {
		t.Fatalf("send second mail status: %d error=%+v", status, second.Error)
	}
	third := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"to":      []string{"alice"},
		"subject": "Range three",
		"body":    "third",
	}, &third); status != http.StatusCreated {
		t.Fatalf("send third mail status: %d error=%+v", status, third.Error)
	}
	carolOnly := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"to":      []string{"carol"},
		"subject": "Hidden from alice",
		"body":    "private",
	}, &carolOnly); status != http.StatusCreated {
		t.Fatalf("send carol-only mail status: %d error=%+v", status, carolOnly.Error)
	}

	rangeAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail/range-delete", aliceToken, map[string]any{
		"mail": []string{first.Result.ID, second.Result.ID},
	}, &rangeAck); status != http.StatusCreated {
		t.Fatalf("range delete status: %d error=%+v", status, rangeAck.Error)
	}
	if rangeAck.Result == nil || rangeAck.Result.ID != "2" {
		t.Fatalf("expected range delete ack count, got %+v", rangeAck.Result)
	}

	inbox := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail", aliceToken, nil, &inbox); status != http.StatusOK {
		t.Fatalf("list alice inbox status: %d", status)
	}
	if len(inbox.Mail) != 1 || inbox.Mail[0].ID != third.Result.ID {
		t.Fatalf("expected only third mail in inbox, got %+v", inbox.Mail)
	}
	trash := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail?mailbox=trash", aliceToken, nil, &trash); status != http.StatusOK {
		t.Fatalf("list alice trash status: %d", status)
	}
	foundTrash := map[string]bool{}
	for _, item := range trash.Mail {
		foundTrash[item.ID] = true
	}
	if len(trash.Mail) != 2 || !foundTrash[first.Result.ID] || !foundTrash[second.Result.ID] {
		t.Fatalf("expected first and second mail in trash, got %+v", trash.Mail)
	}

	hidden := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail/range-delete", aliceToken, map[string]any{
		"mail": []string{third.Result.ID, carolOnly.Result.ID},
	}, &hidden); status != http.StatusNotFound {
		t.Fatalf("expected range delete with hidden mail to fail, got %d error=%+v", status, hidden.Error)
	}
	inbox = mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail", aliceToken, nil, &inbox); status != http.StatusOK {
		t.Fatalf("list alice inbox after failed range status: %d", status)
	}
	if len(inbox.Mail) != 1 || inbox.Mail[0].ID != third.Result.ID {
		t.Fatalf("expected failed range delete to roll back third mail, got %+v", inbox.Mail)
	}
}

func TestHTTPPostPrivateMailToBoard(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	source := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"to":      []string{"alice"},
		"subject": "Board-worthy mail",
		"body":    "Please share this with the board.",
		"attachments": []map[string]any{{
			"filename":    "context.txt",
			"contentType": "text/plain",
			"sizeBytes":   16,
			"url":         "https://example.edu/context.txt",
		}},
	}, &source); status != http.StatusCreated {
		t.Fatalf("send source mail status: %d error=%+v", status, source.Error)
	}

	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail/"+source.Result.ID+"/board", aliceToken, map[string]any{
		"board": "general",
		"note":  "Public follow-up.",
	}, &thread); status != http.StatusCreated {
		t.Fatalf("post mail to board status: %d error=%+v", status, thread.Error)
	}
	posts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", bobToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list mail-to-board posts status: %d", status)
	}
	if len(posts.Posts) != 1 || posts.Posts[0].Author != "alice" {
		t.Fatalf("expected mail-to-board root post, got %+v", posts.Posts)
	}
	for _, want := range []string{"Public follow-up.", "Posted from private mail.", "From: bob", "To: alice", "Subject: Board-worthy mail", "Attachments: context.txt", "Please share this with the board."} {
		if !strings.Contains(posts.Posts[0].Body, want) {
			t.Fatalf("expected mail-to-board post to contain %q, got %q", want, posts.Posts[0].Body)
		}
	}

	reply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail/"+source.Result.ID+"/board", aliceToken, map[string]any{
		"thread": thread.Result.ID,
		"note":   "Thread appendix.",
	}, &reply); status != http.StatusCreated {
		t.Fatalf("append mail to board thread status: %d error=%+v", status, reply.Error)
	}
	posts = postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", aliceToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list appended mail-to-board posts status: %d", status)
	}
	if len(posts.Posts) != 2 || !strings.Contains(posts.Posts[1].Body, "Thread appendix.") {
		t.Fatalf("expected appended mail-to-board post, got %+v", posts.Posts)
	}

	hidden := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail/"+source.Result.ID+"/board", carolToken, map[string]any{
		"board": "general",
	}, &hidden); status != http.StatusNotFound {
		t.Fatalf("expected hidden mail-to-board source to be not found, got %d error=%+v", status, hidden.Error)
	}
}
