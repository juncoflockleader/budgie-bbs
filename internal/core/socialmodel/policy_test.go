package socialmodel

import "testing"

func TestOtherUserFailureFor(t *testing.T) {
	if got := OtherUserFailureFor(true); got != OtherUserSelf {
		t.Fatalf("OtherUserFailureFor(true) = %q, want %q", got, OtherUserSelf)
	}
	if got := OtherUserFailureFor(false); got != OtherUserOK {
		t.Fatalf("OtherUserFailureFor(false) = %q, want %q", got, OtherUserOK)
	}
}

func TestLoginWatchStartFailure(t *testing.T) {
	tests := []struct {
		name   string
		active bool
		friend bool
		want   LoginWatchFailure
	}{
		{name: "deactivate does not require friend", active: false, friend: false, want: LoginWatchOK},
		{name: "active friend allowed", active: true, friend: true, want: LoginWatchOK},
		{name: "active non-friend rejected", active: true, friend: false, want: LoginWatchFriendRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LoginWatchStartFailure(tt.active, tt.friend); got != tt.want {
				t.Fatalf("LoginWatchStartFailure(%v, %v) = %q, want %q", tt.active, tt.friend, got, tt.want)
			}
		})
	}
}

func TestBlessingFailureFor(t *testing.T) {
	tests := []struct {
		name           string
		ignored        bool
		alreadyBlessed bool
		want           BlessingFailure
	}{
		{name: "allowed", want: BlessingOK},
		{name: "ignored", ignored: true, want: BlessingTargetIgnores},
		{name: "already blessed", alreadyBlessed: true, want: BlessingAlreadyBlessed},
		{name: "ignored takes precedence", ignored: true, alreadyBlessed: true, want: BlessingTargetIgnores},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BlessingFailureFor(tt.ignored, tt.alreadyBlessed); got != tt.want {
				t.Fatalf("BlessingFailureFor(%v, %v) = %q, want %q", tt.ignored, tt.alreadyBlessed, got, tt.want)
			}
		})
	}
}

func TestDirectMessageRecipientFailureFor(t *testing.T) {
	tests := []struct {
		name    string
		ignored bool
		allowed bool
		want    DirectMessageRecipientFailure
	}{
		{name: "allowed", allowed: true, want: DirectMessageRecipientOK},
		{name: "ignored", ignored: true, allowed: true, want: DirectMessageRecipientIgnored},
		{name: "friends only", allowed: false, want: DirectMessageRecipientFriendsOnly},
		{name: "ignored takes precedence", ignored: true, allowed: false, want: DirectMessageRecipientIgnored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DirectMessageRecipientFailureFor(tt.ignored, tt.allowed); got != tt.want {
				t.Fatalf("DirectMessageRecipientFailureFor(%v, %v) = %q, want %q", tt.ignored, tt.allowed, got, tt.want)
			}
		})
	}
}
