package core_test

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestBoardAutomodRuleCRUDAndAuthz(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw") // first user => admin
	carol := registerAndGetUser(t, c, "carol", "pw") // board moderator, no site role
	bob := registerAndGetUser(t, c, "bob", "pw")     // ordinary user

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "garden", Name: "Garden"})
	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{Board: "garden", User: carol.ID, Moderator: true})

	// Board moderator can create a board-scoped rule.
	ack := exec(t, c, carol, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "garden", MatchType: "keyword", Pattern: "spam", Action: "manual_review", Reason: "no spam",
	})
	ruleID := ack.ID
	if rules, _ := c.ListBoardAutomodRules("garden"); len(rules) != 1 || rules[0].Pattern != "spam" || !rules[0].Enabled {
		t.Fatalf("expected one enabled keyword rule, got %+v", rules)
	}

	// Ordinary user cannot manage rules.
	execExpectErr(t, c, bob, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "garden", MatchType: "keyword", Pattern: "x", Action: "manual_review",
	}, proto.ErrForbidden)

	// Board moderator cannot create a global-sanction rule (admin only).
	execExpectErr(t, c, carol, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "garden", MatchType: "keyword", Pattern: "y", Action: "global_mute",
	}, proto.ErrForbidden)

	// Invalid regex is rejected.
	execExpectErr(t, c, carol, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "garden", MatchType: "regex", Pattern: "(", Action: "redact",
	}, proto.ErrValidationFailed)

	// rate_threshold without a window is rejected.
	execExpectErr(t, c, carol, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "garden", MatchType: "rate_threshold", Threshold: 5, Action: "board_mute",
	}, proto.ErrValidationFailed)

	// Update the existing rule by id.
	exec(t, c, carol, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		ID: ruleID, Board: "garden", MatchType: "keyword", Pattern: "spammy", Action: "redact", Enabled: boolPtr(false),
	})
	rules, _ := c.ListBoardAutomodRules("garden")
	if len(rules) != 1 || rules[0].Pattern != "spammy" || rules[0].Action != "redact" || rules[0].Enabled {
		t.Fatalf("update not applied: %+v", rules)
	}

	// Rules survive a projection rebuild from the event log.
	clearProjectionTablesForTest(t, c)
	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	rules, _ = c.ListBoardAutomodRules("garden")
	if len(rules) != 1 || rules[0].Pattern != "spammy" || rules[0].Enabled {
		t.Fatalf("rule not preserved across rebuild: %+v", rules)
	}

	// Delete.
	exec(t, c, carol, proto.CmdDeleteBoardAutomodRule, proto.DeleteBoardAutomodRulePayload{Board: "garden", ID: ruleID})
	if rules, _ := c.ListBoardAutomodRules("garden"); len(rules) != 0 {
		t.Fatalf("expected no rules after delete, got %+v", rules)
	}
}
