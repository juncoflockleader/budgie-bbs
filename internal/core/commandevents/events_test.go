package commandevents

import "testing"

func TestReviewResolved(t *testing.T) {
	scopes, payload := ReviewResolved("rev_1", "approved", "usr_mod", 1234)
	if len(scopes) != 1 || scopes[0] != "moderation:global" {
		t.Fatalf("ReviewResolved scopes = %#v, want moderation:global", scopes)
	}
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
	if len(scopes) != 1 || scopes[0] != "board:general" {
		t.Fatalf("BoardAutomodRuleSet scopes = %#v, want board:general", scopes)
	}
	if payload.ID != "rule_1" || payload.Board != "general" || !payload.Enabled || payload.Priority != 7 ||
		payload.MatchType != "keyword" || payload.Pattern != "spam" || payload.Threshold != 3 ||
		payload.WindowSec != 60 || payload.Action != "manual_review" || payload.DurationSec != 120 ||
		payload.Reason != "reason" || payload.Note != "note" || payload.By != "usr_mod" || payload.TS != 1234 {
		t.Fatalf("BoardAutomodRuleSet payload = %+v", payload)
	}

	scopes, deleted := BoardAutomodRuleDeleted("rule_1", "general", "usr_mod", 1235)
	if len(scopes) != 1 || scopes[0] != "board:general" {
		t.Fatalf("BoardAutomodRuleDeleted scopes = %#v, want board:general", scopes)
	}
	if deleted.ID != "rule_1" || deleted.Board != "general" || deleted.By != "usr_mod" || deleted.TS != 1235 {
		t.Fatalf("BoardAutomodRuleDeleted payload = %+v", deleted)
	}
}

func TestPostFlagged(t *testing.T) {
	scopes, payload := PostFlagged("rev_1", "post_flag", "post_1", "thread_1", "usr_reporter", "reason", 1234)
	if len(scopes) != 1 || scopes[0] != "moderation:global" {
		t.Fatalf("PostFlagged scopes = %#v, want moderation:global", scopes)
	}
	if payload.ReviewID != "rev_1" || payload.Kind != "post_flag" || payload.PostID != "post_1" ||
		payload.Thread != "thread_1" || payload.Reporter != "usr_reporter" || payload.Reason != "reason" || payload.TS != 1234 {
		t.Fatalf("PostFlagged payload = %+v", payload)
	}
}
