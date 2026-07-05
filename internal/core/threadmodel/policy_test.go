package threadmodel

import "testing"

func TestReplyAllowed(t *testing.T) {
	if !ReplyAllowed(false, false) {
		t.Fatalf("unlocked thread should accept replies")
	}
	if ReplyAllowed(true, false) {
		t.Fatalf("locked thread should block non-moderators")
	}
	if !ReplyAllowed(true, true) {
		t.Fatalf("moderator should bypass locked thread")
	}
}

func TestStarterAcceptsReplies(t *testing.T) {
	if !StarterAcceptsReplies(false, false) {
		t.Fatalf("starter without no-reply should accept replies")
	}
	if StarterAcceptsReplies(true, false) {
		t.Fatalf("starter no-reply should block non-moderators")
	}
	if !StarterAcceptsReplies(true, true) {
		t.Fatalf("thread moderator should bypass starter no-reply")
	}
}

func TestModerationAllowed(t *testing.T) {
	if ModerationAllowed(false) {
		t.Fatalf("thread moderation should require permission")
	}
	if !ModerationAllowed(true) {
		t.Fatalf("thread moderation permission should allow moderation")
	}
}

func TestTitlePermissionFailureFor(t *testing.T) {
	if got := TitlePermissionFailureFor(true, false, false); got != TitlePermissionOK {
		t.Fatalf("moderator title permission failure = %q, want OK", got)
	}
	if got := TitlePermissionFailureFor(false, false, true); got != TitlePermissionAuthor {
		t.Fatalf("non-author title permission failure = %q, want %q", got, TitlePermissionAuthor)
	}
	if got := TitlePermissionFailureFor(false, true, false); got != TitlePermissionEditWindow {
		t.Fatalf("expired title permission failure = %q, want %q", got, TitlePermissionEditWindow)
	}
	if got := TitlePermissionFailureFor(false, true, true); got != TitlePermissionOK {
		t.Fatalf("author in window title permission failure = %q, want OK", got)
	}
}
