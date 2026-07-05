package commandevents

import (
	"reflect"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func requireScopes(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scopes = %#v, want %#v", got, want)
	}
}

func TestReviewResolved(t *testing.T) {
	scopes, payload := ReviewResolved("rev_1", "approved", "usr_mod", 1234)
	requireScopes(t, scopes, "moderation:global")
	if payload.ReviewID != "rev_1" || payload.Resolution != "approved" || payload.By != "usr_mod" || payload.TS != 1234 {
		t.Fatalf("ReviewResolved payload = %+v", payload)
	}
}

func TestBoardAutomodRuleEvents(t *testing.T) {
	scopes, payload := BoardAutomodRuleSet(
		"rule_1", "general", true, 7,
		"keyword", "spam", 3, 60,
		"manual_review", 120, "reason", "note", "usr_mod", 1234,
	)
	requireScopes(t, scopes, "board:general")
	if payload.ID != "rule_1" || payload.Board != "general" || !payload.Enabled || payload.Priority != 7 ||
		payload.MatchType != "keyword" || payload.Pattern != "spam" || payload.Threshold != 3 ||
		payload.WindowSec != 60 || payload.Action != "manual_review" || payload.DurationSec != 120 ||
		payload.Reason != "reason" || payload.Note != "note" || payload.By != "usr_mod" || payload.TS != 1234 {
		t.Fatalf("BoardAutomodRuleSet payload = %+v", payload)
	}

	scopes, deleted := BoardAutomodRuleDeleted("rule_1", "general", "usr_mod", 1235)
	requireScopes(t, scopes, "board:general")
	if deleted.ID != "rule_1" || deleted.Board != "general" || deleted.By != "usr_mod" || deleted.TS != 1235 {
		t.Fatalf("BoardAutomodRuleDeleted payload = %+v", deleted)
	}
}

func TestBoardCreated(t *testing.T) {
	scopes, payload := BoardCreated("general", "General", "Talk", "root", 2, "usr_admin", 1234)
	requireScopes(t, scopes, "board:general")
	if payload.ID != "general" || payload.Name != "General" || payload.Description != "Talk" ||
		payload.ParentID != "root" || payload.Position != 2 || payload.By != "usr_admin" || payload.TS != 1234 {
		t.Fatalf("BoardCreated payload = %+v", payload)
	}
}

func TestBoardConfigEvents(t *testing.T) {
	scopes, settings := BoardSettingsSet(BoardSettingsSetSpec{
		Board:              "general",
		ReadOnly:           true,
		AttachmentsAllowed: true,
		MemberReadMode:     true,
		GuestAccess:        "read",
		By:                 "usr_admin",
		TS:                 1234,
	})
	requireScopes(t, scopes, "board:general")
	if settings.Board != "general" || !settings.ReadOnly || !settings.AttachmentsAllowed ||
		!settings.MemberReadMode || settings.GuestAccess != "read" || settings.By != "usr_admin" || settings.TS != 1234 {
		t.Fatalf("BoardSettingsSet payload = %+v", settings)
	}

	scopes, requirements := BoardMemberRequirementsSet(BoardMemberRequirementsSetSpec{
		Board:                     "general",
		MinLoginCount:             1,
		MinPostCount:              2,
		MinTrustLevel:             3,
		MinBoardOriginalPostCount: 4,
		MaxMembers:                5,
		ApprovalMode:              "auto",
		By:                        "usr_admin",
		TS:                        1235,
	})
	requireScopes(t, scopes, "board:general")
	if requirements.Board != "general" || requirements.MinLoginCount != 1 || requirements.MinPostCount != 2 ||
		requirements.MinTrustLevel != 3 || requirements.MinBoardOriginalPostCount != 4 ||
		requirements.MaxMembers != 5 || requirements.ApprovalMode != "auto" ||
		requirements.By != "usr_admin" || requirements.TS != 1235 {
		t.Fatalf("BoardMemberRequirementsSet payload = %+v", requirements)
	}

	scopes, recommended := BoardRecommendedSet("general", true, "start here", 6, "usr_admin", 1236)
	requireScopes(t, scopes, "board:general")
	if recommended.Board != "general" || !recommended.Recommended || recommended.Note != "start here" ||
		recommended.Position != 6 || recommended.CuratedBy != "usr_admin" || recommended.TS != 1236 {
		t.Fatalf("BoardRecommendedSet payload = %+v", recommended)
	}
}

func TestBoardRoleMembershipEvents(t *testing.T) {
	scopes, moderator := BoardModeratorSet("general", "usr_alice", true, 4, "usr_admin", 1234)
	requireScopes(t, scopes, "board:general", "user:usr_alice")
	if moderator.Board != "general" || moderator.User != "usr_alice" || !moderator.Moderator ||
		moderator.Position != 4 || moderator.By != "usr_admin" || moderator.TS != 1234 {
		t.Fatalf("BoardModeratorSet payload = %+v", moderator)
	}

	scopes, member := BoardMemberSet(BoardMemberSetSpec{
		Board:               "general",
		User:                "usr_alice",
		Member:              true,
		Title:               "Regular",
		Position:            5,
		CanManageMembers:    true,
		CanCurate:           true,
		CanModeratePosts:    true,
		CanModerateThreads:  true,
		CanAnnounce:         true,
		CanManagePolls:      true,
		CanSetBoardSettings: true,
		By:                  "usr_admin",
		TS:                  1235,
	})
	requireScopes(t, scopes, "board:general", "user:usr_alice")
	if member.Board != "general" || member.User != "usr_alice" || !member.Member ||
		member.Title != "Regular" || member.Position != 5 || !member.CanManageMembers || !member.CanCurate ||
		!member.CanModeratePosts || !member.CanModerateThreads || !member.CanAnnounce ||
		!member.CanManagePolls || !member.CanSetBoardSettings || member.By != "usr_admin" || member.TS != 1235 {
		t.Fatalf("BoardMemberSet payload = %+v", member)
	}
}

func TestBoardMemberApplicationEvents(t *testing.T) {
	scopes, submitted := BoardMemberApplicationSubmitted("app_1", "general", "usr_alice", "please", 1234)
	requireScopes(t, scopes, "board:general", "user:usr_alice")
	if submitted.ID != "app_1" || submitted.Board != "general" || submitted.User != "usr_alice" ||
		submitted.Note != "please" || submitted.TS != 1234 {
		t.Fatalf("BoardMemberApplicationSubmitted payload = %+v", submitted)
	}

	scopes, reviewed := BoardMemberApplicationReviewed("app_1", "general", "usr_alice", "approved", "Regular", "usr_mod", "ok", 1235)
	requireScopes(t, scopes, "board:general", "user:usr_alice")
	if reviewed.Application != "app_1" || reviewed.Board != "general" || reviewed.User != "usr_alice" ||
		reviewed.Status != "approved" || reviewed.Title != "Regular" || reviewed.Reviewer != "usr_mod" ||
		reviewed.ReviewNote != "ok" || reviewed.TS != 1235 {
		t.Fatalf("BoardMemberApplicationReviewed payload = %+v", reviewed)
	}
}

func TestPostFlagged(t *testing.T) {
	scopes, payload := PostFlagged("rev_1", "post_flag", "post_1", "thread_1", "usr_reporter", "reason", 1234)
	requireScopes(t, scopes, "moderation:global")
	if payload.ReviewID != "rev_1" || payload.Kind != "post_flag" || payload.PostID != "post_1" ||
		payload.Thread != "thread_1" || payload.Reporter != "usr_reporter" || payload.Reason != "reason" || payload.TS != 1234 {
		t.Fatalf("PostFlagged payload = %+v", payload)
	}
}

func TestContentFilterSet(t *testing.T) {
	scopes, payload := ContentFilterSet("filter_1", "spam", proto.DefaultContentFilterScope, true, "usr_mod", 1234)
	requireScopes(t, scopes, "moderation:global")
	if payload.ID != "filter_1" || payload.Pattern != "spam" || payload.Scope != proto.DefaultContentFilterScope ||
		!payload.Active || payload.By != "usr_mod" || payload.TS != 1234 {
		t.Fatalf("ContentFilterSet default payload = %+v", payload)
	}

	scopes, payload = ContentFilterSet("filter_2", "eggs", "general", false, "usr_mod", 1235)
	requireScopes(t, scopes, "moderation:global", "board:general")
	if payload.ID != "filter_2" || payload.Pattern != "eggs" || payload.Scope != "general" ||
		payload.Active || payload.By != "usr_mod" || payload.TS != 1235 {
		t.Fatalf("ContentFilterSet board payload = %+v", payload)
	}
}

func TestUserSanctionEvents(t *testing.T) {
	scopes, payload := UserSanctioned("usr_target", "target-name", "mute", "global", 60, "usr_mod", "reason", 1234)
	requireScopes(t, scopes, "account:usr_target")
	if payload.User != "target-name" || payload.Kind != "mute" || payload.Scope != "global" ||
		payload.DurationSec != 60 || payload.By != "usr_mod" || payload.Reason != "reason" || payload.TS != 1234 {
		t.Fatalf("UserSanctioned payload = %+v", payload)
	}

	scopes, cleared := UserSanctionCleared("usr_target", "target-name", "mute", "general", "usr_mod", "reason", 1235)
	requireScopes(t, scopes, "account:usr_target")
	if cleared.User != "target-name" || cleared.Kind != "mute" || cleared.Scope != "general" ||
		cleared.By != "usr_mod" || cleared.Reason != "reason" || cleared.TS != 1235 {
		t.Fatalf("UserSanctionCleared payload = %+v", cleared)
	}
}

func TestThreadModerationEvents(t *testing.T) {
	scopes, title := ThreadTitleSet("thread_1", "general", "New title", "usr_mod", 1234)
	requireScopes(t, scopes, "board:general", "thread:thread_1")
	if title.Thread != "thread_1" || title.Title != "New title" || title.By != "usr_mod" || title.TS != 1234 {
		t.Fatalf("ThreadTitleSet payload = %+v", title)
	}

	scopes, locked := ThreadLocked("thread_1", "general", true, "usr_mod", 1235)
	requireScopes(t, scopes, "board:general", "thread:thread_1")
	if locked.Thread != "thread_1" || !locked.Locked || locked.By != "usr_mod" || locked.TS != 1235 {
		t.Fatalf("ThreadLocked payload = %+v", locked)
	}

	scopes, moved := ThreadMoved("thread_1", "general", "announcements", "usr_mod", 1236)
	requireScopes(t, scopes, "board:general", "board:announcements")
	if moved.Thread != "thread_1" || moved.FromBoard != "general" || moved.ToBoard != "announcements" ||
		moved.By != "usr_mod" || moved.TS != 1236 {
		t.Fatalf("ThreadMoved payload = %+v", moved)
	}
}

func TestThreadPostCreationEvents(t *testing.T) {
	scopes, thread := ThreadNew("thread_1", "general", "alice", "usr_alice", "Welcome", 1234)
	requireScopes(t, scopes, "board:general")
	if thread.ID != "thread_1" || thread.Board != "general" || thread.Author != "alice" ||
		thread.AuthorID != "usr_alice" || thread.Title != "Welcome" || thread.TS != 1234 {
		t.Fatalf("ThreadNew payload = %+v", thread)
	}

	notificationBody := "notification body"
	scopes, post := PostAppended("general", PostAppendedSpec{
		ID:                  "post_1",
		Thread:              "thread_1",
		Author:              "anon",
		Body:                "body",
		RawBody:             "raw",
		PostCommitBody:      &notificationBody,
		PostCommitActorID:   "usr_alice",
		PostCommitActorName: "alice",
		Signature:           "sig",
		ContentType:         "markup",
		ReplyTo:             "post_0",
		Attachments:         []proto.AttachmentPayload{{ID: "att_1"}},
		TS:                  1235,
	})
	requireScopes(t, scopes, "board:general", "thread:thread_1")
	if post.ID != "post_1" || post.Thread != "thread_1" || post.Author != "anon" ||
		post.Body != "body" || post.RawBody != "raw" || post.PostCommitBody == nil || *post.PostCommitBody != notificationBody ||
		post.PostCommitActorID != "usr_alice" || post.PostCommitActorName != "alice" || post.Signature != "sig" ||
		post.ContentType != "markup" || post.ReplyTo != "post_0" || len(post.Attachments) != 1 || post.Attachments[0].ID != "att_1" ||
		post.TS != 1235 {
		t.Fatalf("PostAppended payload = %+v", post)
	}
}

func TestPostDeletionEvents(t *testing.T) {
	scopes, redacted := PostRedacted("post_1", "thread_1", "general", "usr_mod", "reason", "recycle", 1234)
	requireScopes(t, scopes, "thread:thread_1", "board:general")
	if redacted.ID != "post_1" || redacted.Thread != "thread_1" || redacted.By != "usr_mod" ||
		redacted.Reason != "reason" || redacted.DeletionKind != "recycle" || redacted.TS != 1234 {
		t.Fatalf("PostRedacted payload = %+v", redacted)
	}

	scopes, restored := PostRestored("post_1", "thread_1", "general", "usr_mod", 1235)
	requireScopes(t, scopes, "thread:thread_1", "board:general")
	if restored.ID != "post_1" || restored.Thread != "thread_1" || restored.By != "usr_mod" || restored.TS != 1235 {
		t.Fatalf("PostRestored payload = %+v", restored)
	}

	scopes, cleared := PostDeletionCleared("post_1", "thread_1", "general", "junk", "usr_mod", 1236)
	requireScopes(t, scopes, "thread:thread_1", "board:general")
	if cleared.ID != "post_1" || cleared.Thread != "thread_1" || cleared.Board != "general" ||
		cleared.Kind != "junk" || cleared.By != "usr_mod" || cleared.TS != 1236 {
		t.Fatalf("PostDeletionCleared payload = %+v", cleared)
	}

	scopes, purged := PostPurged("post_1", "thread_1", "general", "usr_admin", "reason", 1237)
	requireScopes(t, scopes, "thread:thread_1", "board:general")
	if purged.ID != "post_1" || purged.Thread != "thread_1" || purged.By != "usr_admin" ||
		purged.Reason != "reason" || purged.TS != 1237 {
		t.Fatalf("PostPurged payload = %+v", purged)
	}
}

func TestPostUpdateEvents(t *testing.T) {
	scopes, attachment := PostAttachmentAdded("att_1", "post_1", "thread_1", "general", "file.txt", "text/plain", 42, "usr_author", "blob_1", 1234)
	requireScopes(t, scopes, "board:general", "thread:thread_1")
	if attachment.ID != "att_1" || attachment.Post != "post_1" || attachment.Thread != "thread_1" ||
		attachment.Filename != "file.txt" || attachment.ContentType != "text/plain" || attachment.SizeBytes != 42 ||
		attachment.AuthorID != "usr_author" || attachment.StagedBlobID != "blob_1" || attachment.TS != 1234 {
		t.Fatalf("PostAttachmentAdded payload = %+v", attachment)
	}

	scopes, edited := PostEdited("post_1", "thread_1", "general", "new body", 3, 1235)
	requireScopes(t, scopes, "thread:thread_1", "board:general")
	if edited.ID != "post_1" || edited.Thread != "thread_1" || edited.NewBody != "new body" ||
		edited.Version != 3 || edited.TS != 1235 {
		t.Fatalf("PostEdited payload = %+v", edited)
	}

	scopes, flags := PostFlagsSet("post_1", "thread_1", "general", true, true, false, true, false, "usr_mod", 1236)
	requireScopes(t, scopes, "thread:thread_1", "board:general")
	if flags.ID != "post_1" || flags.Thread != "thread_1" || !flags.Marked || !flags.Recommended ||
		flags.NoReply || !flags.TeX || flags.MailBack || flags.By != "usr_mod" || flags.TS != 1236 {
		t.Fatalf("PostFlagsSet payload = %+v", flags)
	}
}
