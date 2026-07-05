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
