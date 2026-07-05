package commandevents

import "testing"

func TestReviewResolved(t *testing.T) {
	scopes, payload := ReviewResolved("rev_1", "approved", "usr_mod", 1234)
	if len(scopes) != 1 || scopes[0] != "moderation:global" {
		t.Fatalf("ReviewResolved scopes = %#v, want moderation:global", scopes)
	}
	if payload.ReviewID != "rev_1" || payload.Resolution != "approved" || payload.By != "usr_mod" || payload.TS != 1234 {
		t.Fatalf("ReviewResolved payload = %+v", payload)
	}
}
