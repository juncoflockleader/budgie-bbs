package pollmodel

import "testing"

func TestCreationTrustAllowed(t *testing.T) {
	if !CreationTrustAllowed(true, 0, 2) {
		t.Fatalf("moderator should bypass creation trust gate")
	}
	if !CreationTrustAllowed(false, 2, 2) {
		t.Fatalf("actor at minimum trust should pass")
	}
	if CreationTrustAllowed(false, 1, 2) {
		t.Fatalf("actor below minimum trust should fail")
	}
}

func TestResultPublisherAllowed(t *testing.T) {
	if !ResultPublisherAllowed(true, false, false) {
		t.Fatalf("poll manager should publish results")
	}
	if !ResultPublisherAllowed(false, true, false) {
		t.Fatalf("post author should publish results")
	}
	if !ResultPublisherAllowed(false, false, true) {
		t.Fatalf("thread author should publish results")
	}
	if ResultPublisherAllowed(false, false, false) {
		t.Fatalf("unprivileged actor should not publish results")
	}
}

func TestPublicResultAllowed(t *testing.T) {
	if !PublicResultAllowed(true) {
		t.Fatalf("public result post should be allowed")
	}
	if PublicResultAllowed(false) {
		t.Fatalf("non-public result post should be blocked")
	}
}
