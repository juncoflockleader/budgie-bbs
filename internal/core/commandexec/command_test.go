package commandexec

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestHashCommandIsStableAndPayloadSensitive(t *testing.T) {
	payload := json.RawMessage(`{"body":"hello"}`)
	first := HashCommand(proto.CmdAppendPost, payload)
	if first == "" {
		t.Fatal("hash should not be empty")
	}
	if len(first) != 64 {
		t.Fatalf("hash length = %d, want 64", len(first))
	}
	if got := HashCommand(proto.CmdAppendPost, payload); got != first {
		t.Fatalf("hash changed from %q to %q", first, got)
	}
	if got := HashCommand(proto.CmdAppendPost, json.RawMessage(`{"body":"bye"}`)); got == first {
		t.Fatal("different payload produced the same hash")
	}
	if got := HashCommand(proto.CmdCreateThread, payload); got == first {
		t.Fatal("different command name produced the same hash")
	}
}

func TestNormalizePartitionDefaultsAndTrims(t *testing.T) {
	got := NormalizePartition(Partition{Kind: " board ", Key: " general "})
	if got != (Partition{Kind: "board", Key: "general"}) {
		t.Fatalf("normalized partition = %+v", got)
	}
	got = NormalizePartition(Partition{})
	if got != (Partition{Kind: GlobalPartition, Key: GlobalPartition}) {
		t.Fatalf("empty partition = %+v, want global", got)
	}
}

func TestPartitionLaneIndexIsDeterministic(t *testing.T) {
	partition := Partition{Kind: "board", Key: "general"}
	first := PartitionLaneIndex(partition, 16)
	for i := 0; i < 20; i++ {
		if got := PartitionLaneIndex(partition, 16); got != first {
			t.Fatalf("lane changed from %d to %d", first, got)
		}
	}
	if got := PartitionLaneIndex(partition, 0); got != 0 {
		t.Fatalf("lane for zero lanes = %d, want 0", got)
	}
	if got := PartitionLaneIndex(partition, 1); got != 0 {
		t.Fatalf("lane for one lane = %d, want 0", got)
	}
}

func TestPartitionLaneIndexNormalizesInput(t *testing.T) {
	a := Partition{Kind: "board", Key: "general"}
	b := Partition{Kind: " " + a.Kind + " ", Key: "\t" + a.Key + "\n"}
	if PartitionLaneIndex(a, 32) != PartitionLaneIndex(b, 32) {
		t.Fatalf("lane should ignore surrounding whitespace: %q", strings.TrimSpace(b.Key))
	}
}
