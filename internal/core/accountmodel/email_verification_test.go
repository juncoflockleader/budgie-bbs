package accountmodel

import (
	"strings"
	"testing"
)

func TestVerificationEmail(t *testing.T) {
	msg := VerificationEmail(" https://bbs.example/ ", "everi_abc123")
	if msg.Subject != "Confirm your email" {
		t.Fatalf("subject = %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, "https://bbs.example/api/v1/auth/verify-email?token=everi_abc123") {
		t.Fatalf("body missing absolute verification link:\n%s", msg.Body)
	}
	if !strings.Contains(msg.Body, "expires in 24 hours") {
		t.Fatalf("body missing expiry text:\n%s", msg.Body)
	}
}

func TestVerificationEmailWithoutBaseURL(t *testing.T) {
	msg := VerificationEmail("", "everi_abc123")
	if !strings.Contains(msg.Body, "/api/v1/auth/verify-email?token=everi_abc123") {
		t.Fatalf("body missing relative verification link:\n%s", msg.Body)
	}
}
