package mailmodel

import "testing"

func TestCopyCounts(t *testing.T) {
	counts := CopyCounts([]Recipient{
		{ID: "usr_alice"},
		{ID: "usr_bob"},
		{ID: "usr_alice"},
	}, "usr_sender", true)

	want := map[string]int{
		"usr_alice":  2,
		"usr_bob":    1,
		"usr_sender": 1,
	}
	if len(counts) != len(want) {
		t.Fatalf("CopyCounts len = %d, want %d: %#v", len(counts), len(want), counts)
	}
	for userID, copies := range want {
		if counts[userID] != copies {
			t.Fatalf("CopyCounts[%s] = %d, want %d: %#v", userID, counts[userID], copies, counts)
		}
	}
}

func TestCopyCountsPreservesBlankIDs(t *testing.T) {
	counts := CopyCounts([]Recipient{{}}, "", true)
	if counts[""] != 2 {
		t.Fatalf("CopyCounts blank IDs = %#v, want 2 blank copies", counts)
	}
}
