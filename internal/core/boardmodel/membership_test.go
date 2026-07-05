package boardmodel

import "testing"

func TestMembershipApplicantFailure(t *testing.T) {
	if got := MembershipApplicantFailure(false); got != MembershipApplicationOK {
		t.Fatalf("non-member applicant failure = %q, want OK", got)
	}
	if got := MembershipApplicantFailure(true); got != MembershipApplicationAlreadyMember {
		t.Fatalf("member applicant failure = %q, want %q", got, MembershipApplicationAlreadyMember)
	}
}

func TestMembershipApplicationStartFailure(t *testing.T) {
	if got := MembershipApplicationStartFailure(""); got != MembershipApplicationOK {
		t.Fatalf("new application failure = %q, want OK", got)
	}
	if got := MembershipApplicationStartFailure("pending"); got != MembershipApplicationAlreadyPending {
		t.Fatalf("pending application failure = %q, want %q", got, MembershipApplicationAlreadyPending)
	}
	if got := MembershipApplicationStartFailure("blacklisted"); got != MembershipApplicationBlocked {
		t.Fatalf("blacklisted application failure = %q, want %q", got, MembershipApplicationBlocked)
	}
}

func TestMembershipApplicationReviewFailure(t *testing.T) {
	if got := MembershipApplicationReviewFailure("pending"); got != MembershipApplicationOK {
		t.Fatalf("pending review failure = %q, want OK", got)
	}
	if got := MembershipApplicationReviewFailure("approved"); got != MembershipApplicationAlreadyReviewed {
		t.Fatalf("reviewed application failure = %q, want %q", got, MembershipApplicationAlreadyReviewed)
	}
}
