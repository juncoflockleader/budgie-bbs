package proto

import (
	"strings"
	"testing"
)

func TestNormalizeAutomodRulePayload(t *testing.T) {
	got, actions := NormalizeAutomodRulePayload(SetBoardAutomodRulePayload{
		ID:        " rule_1 ",
		Board:     " general ",
		MatchType: " regex ",
		Pattern:   " spam ",
		Action:    " redact, board_mute, , manual_review ",
		Reason:    " cleanup ",
		Note:      " private ",
	})
	if got.ID != "rule_1" || got.Board != "general" || got.MatchType != "regex" ||
		got.Pattern != "spam" || got.Action != "redact,board_mute,manual_review" ||
		got.Reason != "cleanup" || got.Note != "private" {
		t.Fatalf("normalized automod payload = %+v", got)
	}
	wantActions := []string{"redact", "board_mute", "manual_review"}
	if len(actions) != len(wantActions) {
		t.Fatalf("actions = %#v, want %#v", actions, wantActions)
	}
	for i := range wantActions {
		if actions[i] != wantActions[i] {
			t.Fatalf("actions = %#v, want %#v", actions, wantActions)
		}
	}
}

func TestNormalizeSetBoardAutomodRulePayload(t *testing.T) {
	got, actions, msg := NormalizeSetBoardAutomodRulePayload(SetBoardAutomodRulePayload{
		ID:        " rule_1 ",
		Board:     " general ",
		MatchType: " keyword ",
		Pattern:   " spam ",
		Action:    " redact, lock_thread ",
	})
	if msg != "" || got.ID != "rule_1" || got.Board != "general" || got.MatchType != "keyword" ||
		got.Pattern != "spam" || got.Action != "redact,lock_thread" {
		t.Fatalf("normalized set automod payload = %+v, %#v, %q", got, actions, msg)
	}
	wantActions := []string{"redact", "lock_thread"}
	if len(actions) != len(wantActions) {
		t.Fatalf("set automod actions = %#v, want %#v", actions, wantActions)
	}
	for i := range wantActions {
		if actions[i] != wantActions[i] {
			t.Fatalf("set automod actions = %#v, want %#v", actions, wantActions)
		}
	}
	if _, _, msg := NormalizeSetBoardAutomodRulePayload(SetBoardAutomodRulePayload{Board: " ", MatchType: "keyword", Action: "redact"}); msg != boardRequiredValidationMessage {
		t.Fatalf("blank set automod board msg = %q, want %q", msg, boardRequiredValidationMessage)
	}
}

func TestNormalizeDeleteBoardAutomodRulePayload(t *testing.T) {
	got, msg := NormalizeDeleteBoardAutomodRulePayload(DeleteBoardAutomodRulePayload{
		ID:    " rule_1 ",
		Board: " general ",
	})
	if msg != "" || got.ID != "rule_1" || got.Board != "general" {
		t.Fatalf("normalized delete automod payload = %+v, %q", got, msg)
	}
	if _, msg := NormalizeDeleteBoardAutomodRulePayload(DeleteBoardAutomodRulePayload{ID: " ", Board: "general"}); msg != boardAndIDRequiredValidationMessage {
		t.Fatalf("blank automod id msg = %q, want %q", msg, boardAndIDRequiredValidationMessage)
	}
	if _, msg := NormalizeDeleteBoardAutomodRulePayload(DeleteBoardAutomodRulePayload{ID: "rule_1", Board: " "}); msg != boardAndIDRequiredValidationMessage {
		t.Fatalf("blank automod board msg = %q, want %q", msg, boardAndIDRequiredValidationMessage)
	}
}

func TestAutomodActionPermissionRequirements(t *testing.T) {
	req := AutomodActionPermissionRequirements([]string{"manual_review", "lock_thread", "global_mute"})
	if !req.Admin || !req.ThreadModeration || !req.PostModeration {
		t.Fatalf("AutomodActionPermissionRequirements = %+v; want all requirements", req)
	}
	if failure := CheckAutomodActionPermissions(req, false, true, true); failure == nil || failure.Message != AutomodGlobalSanctionPermission {
		t.Fatalf("global sanction permission failure = %#v", failure)
	}
	if failure := CheckAutomodActionPermissions(req, true, false, true); failure == nil || failure.Message != AutomodThreadModerationPermission {
		t.Fatalf("thread permission failure = %#v", failure)
	}
	if failure := CheckAutomodActionPermissions(req, true, true, false); failure == nil || failure.Message != AutomodPostModerationPermission {
		t.Fatalf("post permission failure = %#v", failure)
	}
	if failure := CheckAutomodActionPermissions(req, true, true, true); failure != nil {
		t.Fatalf("automod permissions with all capabilities = %#v; want nil", failure)
	}
}

func TestValidateAutomodRuleRejectsComplexRegex(t *testing.T) {
	base := SetBoardAutomodRulePayload{Board: "b", MatchType: "regex", Action: "redact"}

	// A normal regex is accepted.
	ok := base
	ok.Pattern = `(?i)\bspam(my)?\b`
	if msg := ValidateAutomodRule(ok); msg != "" {
		t.Fatalf("expected a simple regex to validate, got %q", msg)
	}

	// A large-bounded-repetition pattern (the ReDoS-style CPU bomb) is rejected
	// even though it is under the 500-char cap and compiles under RE2.
	bomb := base
	bomb.Pattern = strings.Repeat("a{1000}", 60) // ~420 chars, ~60k NFA instructions
	if len(bomb.Pattern) > 500 {
		t.Fatalf("test pattern unexpectedly exceeds the length cap (%d)", len(bomb.Pattern))
	}
	if msg := ValidateAutomodRule(bomb); msg == "" {
		t.Fatal("expected an over-complex regex to be rejected")
	}
}

func TestAutomodRegexWithinComplexityLimit(t *testing.T) {
	if !AutomodRegexWithinComplexityLimit(`https?://\S+`) {
		t.Fatal("a normal pattern should be within the limit")
	}
	if AutomodRegexWithinComplexityLimit(strings.Repeat("a{1000}", 60)) {
		t.Fatal("a large bounded-repetition pattern should exceed the limit")
	}
	if AutomodRegexWithinComplexityLimit("(") {
		t.Fatal("an invalid pattern should not be reported as within the limit")
	}
}
