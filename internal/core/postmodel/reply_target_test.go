package postmodel

import "testing"

func TestPlanReplyTargetRootReply(t *testing.T) {
	plan, failure := PlanReplyTarget("", nil, "thr_1", false, false)
	if failure != ReplyTargetOK {
		t.Fatalf("PlanReplyTarget root failure = %q, want OK", failure)
	}
	if !plan.NeedsRootMailBack {
		t.Fatalf("root reply should request root mail-back lookup")
	}
}

func TestPlanReplyTargetRejectsQuotedRootReply(t *testing.T) {
	if _, failure := PlanReplyTarget("", nil, "thr_1", true, false); failure != ReplyTargetMissingForQuote {
		t.Fatalf("PlanReplyTarget quoted root failure = %q, want %q", failure, ReplyTargetMissingForQuote)
	}
}

func TestPlanReplyTargetParentFailures(t *testing.T) {
	if _, failure := PlanReplyTarget("pst_missing", nil, "thr_1", false, false); failure != ReplyTargetMissing {
		t.Fatalf("missing parent failure = %q, want %q", failure, ReplyTargetMissing)
	}
	if _, failure := PlanReplyTarget("pst_1", &ReplyTarget{ID: "pst_1", ThreadID: "thr_2"}, "thr_1", false, false); failure != ReplyTargetWrongThread {
		t.Fatalf("wrong thread failure = %q, want %q", failure, ReplyTargetWrongThread)
	}
	if _, failure := PlanReplyTarget("pst_1", &ReplyTarget{ID: "pst_1", ThreadID: "thr_1", Redacted: true}, "thr_1", true, false); failure != ReplyTargetRedactedQuote {
		t.Fatalf("redacted quote failure = %q, want %q", failure, ReplyTargetRedactedQuote)
	}
	if _, failure := PlanReplyTarget("pst_1", &ReplyTarget{ID: "pst_1", ThreadID: "thr_1", NoReply: true}, "thr_1", false, false); failure != ReplyTargetNoReply {
		t.Fatalf("no-reply failure = %q, want %q", failure, ReplyTargetNoReply)
	}
}

func TestPlanReplyTargetParentPlan(t *testing.T) {
	parent := &ReplyTarget{
		ID:       "pst_parent",
		ThreadID: "thr_1",
		ReplyTo:  "pst_root",
		MailBack: true,
	}
	plan, failure := PlanReplyTarget("pst_parent", parent, "thr_1", true, false)
	if failure != ReplyTargetOK {
		t.Fatalf("PlanReplyTarget failure = %q, want OK", failure)
	}
	if plan.EffectiveReplyTo != "pst_root" {
		t.Fatalf("EffectiveReplyTo = %q, want parent reply target", plan.EffectiveReplyTo)
	}
	if !plan.NotifyParent {
		t.Fatalf("parent reply should notify parent")
	}
	if !plan.QuoteParent {
		t.Fatalf("quoted parent reply should quote parent")
	}
	if !plan.MailBackParent {
		t.Fatalf("mail-back parent should become mail-back target")
	}
}

func TestPlanReplyTargetModeratorBypassesNoReply(t *testing.T) {
	parent := &ReplyTarget{ID: "pst_parent", ThreadID: "thr_1", NoReply: true}
	plan, failure := PlanReplyTarget("pst_parent", parent, "thr_1", false, true)
	if failure != ReplyTargetOK {
		t.Fatalf("moderator no-reply failure = %q, want OK", failure)
	}
	if plan.EffectiveReplyTo != "pst_parent" {
		t.Fatalf("EffectiveReplyTo = %q, want parent", plan.EffectiveReplyTo)
	}
}
