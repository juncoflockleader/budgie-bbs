package commandrules

import "testing"

func TestPreferredPostNotificationKind(t *testing.T) {
	tests := []struct {
		name      string
		existing  string
		candidate string
		want      string
	}{
		{name: "mention beats reply", existing: "reply", candidate: "mention", want: "mention"},
		{name: "reply beats watched", existing: "watched", candidate: "reply", want: "reply"},
		{name: "existing wins ties", existing: "mention", candidate: "mention", want: "mention"},
		{name: "existing higher priority stays", existing: "mention", candidate: "watched", want: "mention"},
		{name: "unknown loses to watched", existing: "unknown", candidate: "watched", want: "watched"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PreferredPostNotificationKind(tt.existing, tt.candidate); got != tt.want {
				t.Fatalf("PreferredPostNotificationKind(%q, %q) = %q, want %q", tt.existing, tt.candidate, got, tt.want)
			}
		})
	}
}
