package proto

import (
	"encoding/json"
	"testing"
)

func TestEventToOutboundIncludesVectorCursor(t *testing.T) {
	evt := &Event{
		Kind:            EvtThreadNew,
		Seq:             42,
		TS:              1000,
		PartitionKind:   "board",
		PartitionKey:    "general",
		PartitionOffset: 7,
		Payload:         &ThreadNewPayload{ID: "thr_1", Board: "general", Author: "alice", Title: "hello", TS: 1000},
	}

	msg := EventToOutbound(evt)
	if msg.Seq != 42 {
		t.Fatalf("seq = %d, want 42", msg.Seq)
	}
	if msg.Cursor == nil {
		t.Fatal("cursor missing")
	}
	if msg.Cursor.Seq != 42 {
		t.Fatalf("cursor seq = %d, want 42", msg.Cursor.Seq)
	}
	if len(msg.Cursor.Partitions) != 1 {
		t.Fatalf("partition cursor count = %d, want 1", len(msg.Cursor.Partitions))
	}
	part := msg.Cursor.Partitions[0]
	if part.Kind != "board" || part.Key != "general" || part.Offset != 7 {
		t.Fatalf("partition cursor = %+v", part)
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(raw) || string(raw) == "" {
		t.Fatalf("invalid json: %s", raw)
	}
}

func TestUnorderedTrafficEventsAreNotDurable(t *testing.T) {
	events := []*Event{
		{
			Kind:            EvtPostReacted,
			Seq:             99,
			ESeq:            7,
			PartitionKind:   "board",
			PartitionKey:    "general",
			PartitionOffset: 11,
			Payload:         &PostReactedPayload{PostID: "pst_1", Thread: "thr_1", User: "alice", Emoji: "heart", ReactionCount: 1, TS: 1000},
			TS:              1000,
		},
		{
			Kind:            EvtPostUnreacted,
			Seq:             100,
			ESeq:            8,
			PartitionKind:   "board",
			PartitionKey:    "general",
			PartitionOffset: 12,
			Payload:         &PostUnreactedPayload{PostID: "pst_1", Thread: "thr_1", User: "alice", Emoji: "heart", ReactionCount: 0, TS: 1001},
			TS:              1001,
		},
		{
			Kind:            EvtPollVoted,
			Seq:             101,
			ESeq:            9,
			PartitionKind:   "board",
			PartitionKey:    "general",
			PartitionOffset: 13,
			Payload:         &PollVotedPayload{Poll: "pol_1", Option: "opt_1", User: "alice", TS: 1002},
			TS:              1002,
		},
	}
	for _, evt := range events {
		t.Run(string(evt.Kind), func(t *testing.T) {
			if evt.IsDurable() {
				t.Fatalf("%s classified as durable", evt.Kind)
			}
			msg := EventToOutbound(evt)
			if msg.Seq != 0 || msg.Cursor != nil || msg.PartitionKind != "" || msg.PartitionKey != "" || msg.PartitionOffset != 0 {
				t.Fatalf("outbound = %+v, want no durable cursor metadata", msg)
			}
			if msg.ESeq != evt.ESeq {
				t.Fatalf("eseq = %d, want %d", msg.ESeq, evt.ESeq)
			}
		})
	}
}

func TestCursorAfterSeqPreservesScalarCompatibility(t *testing.T) {
	cursor := Cursor{Seq: 10, Partitions: []PartitionCursor{{Kind: "board", Key: "general", Offset: 20}}}
	if got := cursor.AfterSeq(5); got != 10 {
		t.Fatalf("cursor with scalar seq after = %d, want 10", got)
	}

	cursor = Cursor{Partitions: []PartitionCursor{{Kind: "board", Key: "general", Offset: 20}}}
	if got := cursor.AfterSeq(5); got != 5 {
		t.Fatalf("cursor with partition-only after = %d, want fallback 5", got)
	}
}

func TestCursorObserveEventMergesPartitionsMonotonically(t *testing.T) {
	cursor := Cursor{Seq: 10, Partitions: []PartitionCursor{{Kind: "board", Key: "general", Offset: 4}}}
	cursor.ObserveEvent(&Event{
		Kind:            EvtThreadNew,
		Seq:             9,
		PartitionKind:   "board",
		PartitionKey:    "general",
		PartitionOffset: 3,
	})
	cursor.ObserveEvent(&Event{
		Kind:            EvtThreadNew,
		Seq:             12,
		PartitionKind:   "chat",
		PartitionKey:    "lobby",
		PartitionOffset: 1,
	})
	cursor.ObserveEvent(&Event{
		Kind:            EvtThreadNew,
		Seq:             11,
		PartitionKind:   "board",
		PartitionKey:    "general",
		PartitionOffset: 5,
	})

	if cursor.Seq != 12 {
		t.Fatalf("seq = %d, want 12", cursor.Seq)
	}
	if len(cursor.Partitions) != 2 {
		t.Fatalf("partitions = %+v, want two entries", cursor.Partitions)
	}
	if cursor.Partitions[0] != (PartitionCursor{Kind: "board", Key: "general", Offset: 5}) {
		t.Fatalf("first partition = %+v", cursor.Partitions[0])
	}
	if cursor.Partitions[1] != (PartitionCursor{Kind: "chat", Key: "lobby", Offset: 1}) {
		t.Fatalf("second partition = %+v", cursor.Partitions[1])
	}
}

func TestCursorSeenAndGapPreferPartitionOffsets(t *testing.T) {
	cursor := Cursor{
		Seq: 99,
		Partitions: []PartitionCursor{
			{Kind: "board", Key: "general", Offset: 1},
			{Kind: "chat", Key: "lobby", Offset: 7},
		},
	}

	seen := &Event{Kind: EvtThreadNew, Seq: 100, PartitionKind: "board", PartitionKey: "general", PartitionOffset: 1}
	if !cursor.SeenEvent(seen) {
		t.Fatal("expected event at known partition offset to be seen")
	}

	missing := &Event{Kind: EvtThreadNew, Seq: 50, PartitionKind: "board", PartitionKey: "general", PartitionOffset: 2}
	if cursor.SeenEvent(missing) {
		t.Fatal("partition offset 2 should not be seen even though scalar seq is behind cursor")
	}
	if cursor.PartitionGapBeforeEvent(missing) {
		t.Fatal("next partition offset should not be reported as a gap")
	}

	current := &Event{Kind: EvtThreadNew, Seq: 100, PartitionKind: "board", PartitionKey: "general", PartitionOffset: 3}
	if !cursor.PartitionGapBeforeEvent(current) {
		t.Fatal("partition offset jump should be reported as a gap")
	}
	if cursor.ScalarGapBeforeEvent(current) {
		t.Fatal("scalar gap should not be reported when seq advanced by one")
	}
}

func TestCursorPartitionOnlyDropsScalarSeq(t *testing.T) {
	cursor := Cursor{Seq: 20, Partitions: []PartitionCursor{{Kind: "board", Key: "general", Offset: 8}}}
	partitionOnly := cursor.PartitionOnly()
	if partitionOnly.Seq != 0 {
		t.Fatalf("partition-only seq = %d, want 0", partitionOnly.Seq)
	}
	if len(partitionOnly.Partitions) != 1 || partitionOnly.Partitions[0].Offset != 8 {
		t.Fatalf("partition-only cursor = %+v", partitionOnly)
	}
	if cursor.Seq != 20 {
		t.Fatalf("original cursor seq = %d, want unchanged 20", cursor.Seq)
	}
}

func TestDurableEventAtOrAfterPrefersSamePartitionOffset(t *testing.T) {
	current := &Event{Kind: EvtThreadNew, Seq: 100, PartitionKind: "board", PartitionKey: "general", PartitionOffset: 3}
	priorSamePartition := &Event{Kind: EvtThreadNew, Seq: 101, PartitionKind: "board", PartitionKey: "general", PartitionOffset: 2}
	if DurableEventAtOrAfter(priorSamePartition, current) {
		t.Fatal("same-partition lower offset should be before current even with a higher seq")
	}

	currentSamePartition := &Event{Kind: EvtThreadNew, Seq: 99, PartitionKind: "board", PartitionKey: "general", PartitionOffset: 3}
	if !DurableEventAtOrAfter(currentSamePartition, current) {
		t.Fatal("same-partition equal offset should be at current")
	}

	nextOtherPartition := &Event{Kind: EvtThreadNew, Seq: 101, PartitionKind: "board", PartitionKey: "life", PartitionOffset: 1}
	if !DurableEventAtOrAfter(nextOtherPartition, current) {
		t.Fatal("cross-partition comparison should fall back to seq")
	}
}
