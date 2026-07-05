package boardmodel

import "testing"

func TestPostPolicySettingsAllowsMailIn(t *testing.T) {
	if !(*PostPolicySettings)(nil).AllowsMailIn(false) {
		t.Fatalf("nil settings should not block mail-in")
	}
	if (&PostPolicySettings{}).AllowsMailIn(false) {
		t.Fatalf("disabled mail-in should block non-moderators")
	}
	if !(&PostPolicySettings{}).AllowsMailIn(true) {
		t.Fatalf("moderators should bypass disabled mail-in")
	}
	if !(&PostPolicySettings{MailInAllowed: true}).AllowsMailIn(false) {
		t.Fatalf("enabled mail-in should allow non-moderators")
	}
}

func TestPostPolicySettingsThreadCreationAndReplies(t *testing.T) {
	settings := &PostPolicySettings{ReadOnly: true}
	if !settings.BlocksThreadCreation(false) {
		t.Fatalf("read-only board should block thread creation")
	}
	if settings.BlocksThreadCreation(true) {
		t.Fatalf("moderators should bypass read-only thread creation block")
	}
	if !settings.BlocksReply(false) {
		t.Fatalf("read-only board should block replies")
	}
	if settings.BlocksReply(true) {
		t.Fatalf("moderators should bypass read-only reply block")
	}

	settings = &PostPolicySettings{NoReply: true}
	if settings.BlocksThreadCreation(false) {
		t.Fatalf("no-reply board should not block thread creation")
	}
	if !settings.BlocksReply(false) {
		t.Fatalf("no-reply board should block replies")
	}
}

func TestPostPolicySettingsMembershipRequirements(t *testing.T) {
	if (*PostPolicySettings)(nil).RequiresPostingMembership() || (*PostPolicySettings)(nil).RequiresReadMembership() {
		t.Fatalf("nil settings should not require membership")
	}
	if !(&PostPolicySettings{MemberReadMode: true}).RequiresPostingMembership() {
		t.Fatalf("member-read boards should require membership for posting")
	}
	if !(&PostPolicySettings{MemberPostMode: true}).RequiresPostingMembership() {
		t.Fatalf("member-post boards should require membership for posting")
	}
	if !(&PostPolicySettings{MemberReadMode: true}).RequiresReadMembership() {
		t.Fatalf("member-read boards should require membership for reading")
	}
	if (&PostPolicySettings{MemberPostMode: true}).RequiresReadMembership() {
		t.Fatalf("member-post-only boards should not require membership for reading")
	}
}
