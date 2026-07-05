package logmodel

import "testing"

func TestEffectiveCommandLogCIDUsesExplicitOrSyntheticID(t *testing.T) {
	partition := Partition{Kind: "thread", Key: "thr_1"}

	explicit, err := EffectiveCommandLogCID(CommandLogRecord{
		Partition: partition,
		Offset:    7,
		CID:       "cid_explicit",
	})
	if err != nil {
		t.Fatalf("EffectiveCommandLogCID explicit: %v", err)
	}
	if explicit != "cid_explicit" {
		t.Fatalf("EffectiveCommandLogCID explicit = %q, want cid_explicit", explicit)
	}

	synthetic, err := EffectiveCommandLogCID(CommandLogRecord{
		Partition: partition,
		Offset:    7,
	})
	if err != nil {
		t.Fatalf("EffectiveCommandLogCID synthetic: %v", err)
	}
	if want := SyntheticCommandLogCID(partition, 7); synthetic != want {
		t.Fatalf("EffectiveCommandLogCID synthetic = %q, want %q", synthetic, want)
	}
}

func TestEffectiveCommandLogCIDRejectsMissingOffset(t *testing.T) {
	_, err := EffectiveCommandLogCID(CommandLogRecord{Partition: Partition{Kind: "thread", Key: "thr_1"}})
	if err == nil {
		t.Fatal("EffectiveCommandLogCID missing offset succeeded")
	}
}
