package logmodel

import "testing"

func TestNormalizeHotThreadSplitsTrimsAndDropsDisabledEntries(t *testing.T) {
	got := NormalizeHotThreadSplits(map[string]int{
		" thr_hot ": 4,
		"thr_off":   1,
		"":          8,
		"thr_zero":  0,
	})
	want := map[string]int{"thr_hot": 4}
	if len(got) != len(want) || got["thr_hot"] != want["thr_hot"] {
		t.Fatalf("NormalizeHotThreadSplits = %#v, want %#v", got, want)
	}
}

func TestMergeHotThreadSplitsLetsOverridesWin(t *testing.T) {
	got := MergeHotThreadSplits(
		map[string]int{"thr_hot": 4, "thr_persisted": 2},
		map[string]int{"thr_hot": 6, "thr_off": 1},
	)
	if got["thr_hot"] != 6 {
		t.Fatalf("override split = %d, want 6", got["thr_hot"])
	}
	if got["thr_persisted"] != 2 {
		t.Fatalf("persisted split = %d, want 2", got["thr_persisted"])
	}
	if _, ok := got["thr_off"]; ok {
		t.Fatalf("disabled override should be absent: %#v", got)
	}
}

func TestHotThreadSplitPartitionKeyRoundTrips(t *testing.T) {
	key := HotThreadSplitPartitionKey("thr_hot", 3)
	threadID, ok := HotThreadSplitPartitionThread(key)
	if !ok || threadID != "thr_hot" {
		t.Fatalf("partition key %q parsed as %q ok=%v, want thr_hot true", key, threadID, ok)
	}
	for _, invalid := range []string{"thr_hot", "thr_hot#reply-", "#reply-1", "thr_hot#reply--1", "thr_hot#reply-x"} {
		if threadID, ok := HotThreadSplitPartitionThread(invalid); ok {
			t.Fatalf("invalid key %q parsed as %q", invalid, threadID)
		}
	}
}

func TestHotThreadSplitPartitionSetIncludesBaseCurrentAndNextShards(t *testing.T) {
	got := HotThreadSplitPartitionSet("thr_hot", 2, 4)
	for _, partition := range []Partition{
		{Kind: PartitionThread, Key: "thr_hot"},
		{Kind: PartitionThread, Key: "thr_hot#reply-0"},
		{Kind: PartitionThread, Key: "thr_hot#reply-1"},
		{Kind: PartitionThread, Key: "thr_hot#reply-2"},
		{Kind: PartitionThread, Key: "thr_hot#reply-3"},
	} {
		if _, ok := got[partition]; !ok {
			t.Fatalf("missing partition %+v in %#v", partition, got)
		}
	}
	if len(got) != 5 {
		t.Fatalf("partition set size = %d, want 5: %#v", len(got), got)
	}
}

func TestHotThreadReplyShardIsStableAndBounded(t *testing.T) {
	payload := []byte(`{"thread":"thr_hot","body":"hello"}`)
	first := HotThreadReplyShard("usr_alice", payload, 4)
	if first < 0 || first >= 4 {
		t.Fatalf("reply shard = %d, want within [0,4)", first)
	}
	if got := HotThreadReplyShard("usr_alice", payload, 4); got != first {
		t.Fatalf("reply shard changed from %d to %d", first, got)
	}
	if got := HotThreadReplyShard("usr_alice", payload, 1); got != 0 {
		t.Fatalf("reply shard for one shard = %d, want 0", got)
	}
}
