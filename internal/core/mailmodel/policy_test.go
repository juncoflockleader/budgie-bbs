package mailmodel

import "testing"

func TestPolicyPredicates(t *testing.T) {
	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{name: "sender can mutate attachments", got: AttachmentMutationAllowed("alice", "alice"), want: true},
		{name: "non-sender cannot mutate attachments", got: AttachmentMutationAllowed("alice", "bob"), want: false},
		{name: "self mail skips ignore", got: RecipientIgnoreApplies("alice", "alice", false), want: false},
		{name: "mail all skips ignore", got: RecipientIgnoreApplies("alice", "bob", true), want: false},
		{name: "direct recipient checks ignore", got: RecipientIgnoreApplies("alice", "bob", false), want: true},
		{name: "non-admin mail-all rejected", got: MailAllAllowed(true, false), want: false},
		{name: "admin mail-all allowed", got: MailAllAllowed(true, true), want: true},
		{name: "normal mail does not require admin", got: MailAllAllowed(false, false), want: true},
		{name: "empty recipients", got: RecipientRefsEmpty(0), want: true},
		{name: "non-empty recipients", got: RecipientRefsEmpty(1), want: false},
		{name: "direct too many recipients", got: RecipientRefsTooMany(false, MaxRecipientsPerSend+1), want: true},
		{name: "mail-all bypasses direct cap", got: RecipientRefsTooMany(true, MaxRecipientsPerSend+1), want: false},
		{name: "mail group includes owner", got: MailGroupIncludesOwner(true), want: true},
		{name: "mail group excludes owner", got: MailGroupIncludesOwner(false), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}
