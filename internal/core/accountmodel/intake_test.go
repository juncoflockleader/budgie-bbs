package accountmodel

import "testing"

func TestNormalizeRegistrationIntake(t *testing.T) {
	got := NormalizeRegistrationIntake(RegistrationIntake{
		RealName:       " Alice Example ",
		Affiliation:    " School ",
		Note:           " Hello ",
		PolicyAccepted: true,
		PolicyVersion:  " 2026-07 ",
	}, 1234)
	if got.RealName != "Alice Example" || got.Affiliation != "School" || got.Note != "Hello" {
		t.Fatalf("free text not trimmed: %+v", got)
	}
	if got.PolicyAcceptedAt != 1234 || got.PolicyVersion != "2026-07" {
		t.Fatalf("policy acceptance not normalized: %+v", got)
	}
}

func TestNormalizeRegistrationIntakeWithoutPolicyAcceptance(t *testing.T) {
	got := NormalizeRegistrationIntake(RegistrationIntake{
		PolicyVersion: " ignored ",
	}, 1234)
	if got.PolicyAcceptedAt != 0 || got.PolicyVersion != "" {
		t.Fatalf("policy fields should be empty when not accepted: %+v", got)
	}
}
