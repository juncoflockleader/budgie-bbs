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
