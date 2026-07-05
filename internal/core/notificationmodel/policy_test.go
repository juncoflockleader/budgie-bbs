package notificationmodel

import "testing"

func TestPreferredPostKind(t *testing.T) {
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
			if got := PreferredPostKind(tt.existing, tt.candidate); got != tt.want {
				t.Fatalf("PreferredPostKind(%q, %q) = %q, want %q", tt.existing, tt.candidate, got, tt.want)
			}
		})
	}
}

func TestCanReceiveBoardPost(t *testing.T) {
	tests := []struct {
		name              string
		userPresent       bool
		memberReadMode    bool
		canUseMemberBoard bool
		want              bool
	}{
		{name: "missing user cannot receive", userPresent: false, memberReadMode: false, canUseMemberBoard: true, want: false},
		{name: "public board allows present user", userPresent: true, memberReadMode: false, canUseMemberBoard: false, want: true},
		{name: "member board requires access", userPresent: true, memberReadMode: true, canUseMemberBoard: true, want: true},
		{name: "member board rejects inaccessible user", userPresent: true, memberReadMode: true, canUseMemberBoard: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanReceiveBoardPost(tt.userPresent, tt.memberReadMode, tt.canUseMemberBoard); got != tt.want {
				t.Fatalf("CanReceiveBoardPost(%v, %v, %v) = %v, want %v", tt.userPresent, tt.memberReadMode, tt.canUseMemberBoard, got, tt.want)
			}
		})
	}
}
