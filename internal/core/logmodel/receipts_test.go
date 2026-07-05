package logmodel

import (
	"encoding/json"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

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

func TestDeterministicCommandReceiptEnqueuedAtIsStableAndScoped(t *testing.T) {
	partition := Partition{Kind: "board", Key: "general"}
	payload := json.RawMessage(`{"board":"general","title":"Hello"}`)
	first := DeterministicCommandReceiptEnqueuedAt(partition, "usr_alice", "cid-1", proto.CmdCreateThread, payload)
	if got := DeterministicCommandReceiptEnqueuedAt(partition, "usr_alice", "cid-1", proto.CmdCreateThread, payload); got != first {
		t.Fatalf("deterministic enqueue time changed from %d to %d", first, got)
	}
	if first <= deterministicCommandReceiptBaseUnixMS {
		t.Fatalf("deterministic enqueue time = %d, want after base %d", first, deterministicCommandReceiptBaseUnixMS)
	}
	if max := deterministicCommandReceiptBaseUnixMS + int64(deterministicCommandReceiptWindowMS); first > max {
		t.Fatalf("deterministic enqueue time = %d, want <= %d", first, max)
	}
	if got := DeterministicCommandReceiptEnqueuedAt(partition, "usr_bob", "cid-1", proto.CmdCreateThread, payload); got == first {
		t.Fatal("different actor produced the same deterministic enqueue time")
	}
	if got := DeterministicCommandReceiptEnqueuedAt(partition, "usr_alice", "cid-2", proto.CmdCreateThread, payload); got == first {
		t.Fatal("different cid produced the same deterministic enqueue time")
	}
}

func TestDeterministicCommandReceiptEnqueuedAtNormalizesPartition(t *testing.T) {
	fallback := DeterministicCommandReceiptEnqueuedAt(Partition{}, "usr_alice", "cid-1", proto.CmdCreateThread, nil)
	global := DeterministicCommandReceiptEnqueuedAt(Partition{Kind: PartitionGlobal, Key: PartitionGlobal}, "usr_alice", "cid-1", proto.CmdCreateThread, nil)
	if fallback != global {
		t.Fatalf("empty partition enqueue time = %d, want global %d", fallback, global)
	}
}
