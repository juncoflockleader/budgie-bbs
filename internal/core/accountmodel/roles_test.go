package accountmodel

import "testing"

func TestRoleAllowed(t *testing.T) {
	if !AdminRoleAllowed(true) || AdminRoleAllowed(false) {
		t.Fatalf("AdminRoleAllowed should mirror admin role")
	}
	if !ModeratorRoleAllowed(true) || ModeratorRoleAllowed(false) {
		t.Fatalf("ModeratorRoleAllowed should mirror moderator role")
	}
}

func TestSanctionTargetFailureFor(t *testing.T) {
	if got := SanctionTargetFailureFor(true, true, false); got != SanctionTargetAdmin {
		t.Fatalf("admin target failure = %q, want %q", got, SanctionTargetAdmin)
	}
	if got := SanctionTargetFailureFor(false, false, true); got != SanctionTargetModerator {
		t.Fatalf("moderator target failure = %q, want %q", got, SanctionTargetModerator)
	}
	if got := SanctionTargetFailureFor(true, false, true); got != SanctionTargetOK {
		t.Fatalf("admin sanctioning moderator failure = %q, want OK", got)
	}
	if got := SanctionTargetFailureFor(false, false, false); got != SanctionTargetOK {
		t.Fatalf("regular target failure = %q, want OK", got)
	}
}

func TestClearSanctionTargetFailureFor(t *testing.T) {
	if got := ClearSanctionTargetFailureFor(false, true); got != SanctionTargetClearModerator {
		t.Fatalf("clear moderator sanction failure = %q, want %q", got, SanctionTargetClearModerator)
	}
	if got := ClearSanctionTargetFailureFor(true, true); got != SanctionTargetOK {
		t.Fatalf("admin clearing moderator sanction failure = %q, want OK", got)
	}
	if got := ClearSanctionTargetFailureFor(false, false); got != SanctionTargetOK {
		t.Fatalf("clear regular sanction failure = %q, want OK", got)
	}
}
