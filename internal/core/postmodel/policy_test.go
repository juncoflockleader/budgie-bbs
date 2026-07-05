package postmodel

import "testing"

func boolPtr(v bool) *bool {
	return &v
}

func TestAuthoredBy(t *testing.T) {
	actor := &Actor{ID: "usr_alice", Name: "alice"}
	if !AuthoredBy(actor, "usr_alice", "someone") {
		t.Fatalf("author ID should take precedence")
	}
	if AuthoredBy(actor, "usr_bob", "alice") {
		t.Fatalf("mismatched author ID should not fall back to name")
	}
	if !AuthoredBy(actor, "", "alice") {
		t.Fatalf("blank author ID should fall back to author name")
	}
	if AuthoredBy(nil, "usr_alice", "alice") {
		t.Fatalf("nil actor should not author posts")
	}
	if !AuthoredByID(actor, "usr_alice") {
		t.Fatalf("AuthoredByID should match actor ID")
	}
	if AuthoredByID(actor, "") {
		t.Fatalf("blank author ID should not match")
	}
}

func TestAuthorEditAllowed(t *testing.T) {
	if !AuthorEditAllowed(true, false, false) {
		t.Fatalf("bypass should allow edits")
	}
	if !AuthorEditAllowed(false, true, true) {
		t.Fatalf("author inside window should allow edits")
	}
	if AuthorEditAllowed(false, true, false) {
		t.Fatalf("author outside window should not allow edits")
	}
	if AuthorEditAllowed(false, false, true) {
		t.Fatalf("non-author inside window should not allow edits")
	}
	if !WithinAuthorEditWindow(1099, 1000, 100) {
		t.Fatalf("window should remain open before the deadline")
	}
	if WithinAuthorEditWindow(1100, 1000, 100) {
		t.Fatalf("window should close at the deadline")
	}
}

func TestPlanFlagUpdateTracksPermissionGroups(t *testing.T) {
	plan := PlanFlagUpdate(Flags{
		Marked:      false,
		Recommended: true,
		NoReply:     false,
		TeX:         false,
		MailBack:    true,
	}, FlagPatch{
		Marked:      boolPtr(true),
		Recommended: boolPtr(true),
		NoReply:     boolPtr(true),
		TeX:         boolPtr(true),
		MailBack:    boolPtr(false),
	})

	if !plan.Marked || !plan.Recommended || !plan.NoReply || !plan.TeX || plan.MailBack {
		t.Fatalf("PlanFlagUpdate flags = %+v, want applied patch", plan.Flags)
	}
	if !plan.CuratorChange {
		t.Fatalf("marked change should require curator permission")
	}
	if !plan.ThreadModerationChange {
		t.Fatalf("no-reply change should require thread moderation permission")
	}
	if !plan.AuthorMetadataChange {
		t.Fatalf("tex/mailback changes should require author metadata permission")
	}
	if !plan.HasChanges() {
		t.Fatalf("changed patch should report changes")
	}
}

func TestPlanFlagUpdateIgnoresNoopPatch(t *testing.T) {
	current := Flags{Marked: true, TeX: true}
	plan := PlanFlagUpdate(current, FlagPatch{Marked: boolPtr(true), TeX: boolPtr(true)})
	if plan.Flags != current {
		t.Fatalf("PlanFlagUpdate flags = %+v, want %+v", plan.Flags, current)
	}
	if plan.HasChanges() {
		t.Fatalf("noop patch should not report changes")
	}
}

func TestFlagPlanPermissionFailure(t *testing.T) {
	actor := &Actor{ID: "usr_author"}
	authorMetadata := FlagPlan{AuthorMetadataChange: true}
	if got := authorMetadata.PermissionFailure(actor, "usr_author", false, false); got != FlagPermissionOK {
		t.Fatalf("author metadata permission failure = %q, want OK", got)
	}
	if got := authorMetadata.PermissionFailure(&Actor{ID: "usr_mod"}, "usr_author", false, true); got != FlagPermissionOK {
		t.Fatalf("thread moderator permission failure = %q, want OK", got)
	}
	if got := authorMetadata.PermissionFailure(&Actor{ID: "usr_other"}, "usr_author", false, false); got != FlagPermissionAuthorMetadata {
		t.Fatalf("author metadata failure = %q, want %q", got, FlagPermissionAuthorMetadata)
	}

	if got := (FlagPlan{CuratorChange: true}).PermissionFailure(actor, "usr_author", false, true); got != FlagPermissionCurator {
		t.Fatalf("curator failure = %q, want %q", got, FlagPermissionCurator)
	}
	if got := (FlagPlan{ThreadModerationChange: true}).PermissionFailure(actor, "usr_author", true, false); got != FlagPermissionThreadModeration {
		t.Fatalf("thread moderation failure = %q, want %q", got, FlagPermissionThreadModeration)
	}
}

func TestRedactionKind(t *testing.T) {
	if kind, ok := RedactionKind(false, true, true); !ok || kind != "junk" {
		t.Fatalf("author redaction = %q/%v, want junk/true", kind, ok)
	}
	if kind, ok := RedactionKind(true, false, false); !ok || kind != "recycle" {
		t.Fatalf("moderator redaction = %q/%v, want recycle/true", kind, ok)
	}
	if _, ok := RedactionKind(false, true, false); ok {
		t.Fatalf("author outside window should not redact")
	}
	if _, ok := RedactionKind(false, false, true); ok {
		t.Fatalf("non-author should not redact without moderation permission")
	}
}
