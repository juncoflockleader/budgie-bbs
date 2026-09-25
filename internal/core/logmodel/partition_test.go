package logmodel

import (
	"reflect"
	"testing"
)

func TestNormalizePartitionFieldsTrimsAndDefaults(t *testing.T) {
	kind, key := NormalizePartitionFields(" board ", "\tgeneral\n")
	if kind != PartitionBoard || key != "general" {
		t.Fatalf("NormalizePartitionFields = %q/%q, want board/general", kind, key)
	}

	kind, key = NormalizePartitionFields(" ", "")
	if kind != PartitionGlobal || key != PartitionGlobal {
		t.Fatalf("NormalizePartitionFields empty = %q/%q, want global/global", kind, key)
	}
}

func TestLaggingCommandPartitionOffsetsNormalizesAndDeduplicates(t *testing.T) {
	global := Partition{Kind: PartitionGlobal, Key: PartitionGlobal}.Normalize()
	board := Partition{Kind: PartitionBoard, Key: "board-a"}.Normalize()
	got := LaggingCommandPartitionOffsets([]CommandPartitionOffset{
		{Partition: Partition{}, TailOffset: 3, CommittedOffset: 1},
		{Partition: Partition{Kind: PartitionBoard, Key: "board-a"}, TailOffset: 5, CommittedOffset: 5},
		{Partition: Partition{Kind: PartitionBoard, Key: "board-a"}, TailOffset: 7, CommittedOffset: 4},
		{Partition: Partition{Kind: PartitionBoard, Key: "board-a"}, TailOffset: 9, CommittedOffset: 2},
		{Partition: Partition{Kind: PartitionThread, Key: "thread-a"}, TailOffset: -1, CommittedOffset: -2},
	})
	want := []CommandPartitionOffset{
		{Partition: global, TailOffset: 3, CommittedOffset: 1},
		{Partition: board, TailOffset: 7, CommittedOffset: 4},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lagging offsets = %+v, want %+v", got, want)
	}
}

func TestCommandPartitionOffsetOrderingHelpers(t *testing.T) {
	board := Partition{Kind: PartitionBoard, Key: "general"}.Normalize()
	meta := Partition{Kind: PartitionBoard, Key: "meta"}.Normalize()
	thread := Partition{Kind: PartitionThread, Key: "thr_1"}.Normalize()
	normalized := (CommandPartitionOffset{
		Partition:       Partition{},
		TailOffset:      -1,
		CommittedOffset: 3,
	}).Normalize()
	if want := (CommandPartitionOffset{Partition: Partition{Kind: PartitionGlobal, Key: PartitionGlobal}}); normalized != want {
		t.Fatalf("CommandPartitionOffset.Normalize = %+v, want %+v", normalized, want)
	}
	if got := (CommandPartitionOffset{
		Partition:       board,
		TailOffset:      2,
		CommittedOffset: 5,
	}).Lag(); got != 0 {
		t.Fatalf("CommandPartitionOffset.Lag over-committed = %d, want 0", got)
	}
	offsets := []CommandPartitionOffset{
		{Partition: meta, TailOffset: 7, CommittedOffset: 7},
		{Partition: thread, TailOffset: 3, CommittedOffset: 0},
		{Partition: board, TailOffset: 5, CommittedOffset: 4},
	}

	partitions := CommandPartitionsByTailOffset(offsets, 2)
	if want := []Partition{meta, board}; !reflect.DeepEqual(partitions, want) {
		t.Fatalf("CommandPartitionsByTailOffset = %+v, want %+v", partitions, want)
	}

	SortCommandPartitionOffsetsByLag(offsets)
	want := []CommandPartitionOffset{
		{Partition: thread, TailOffset: 3, CommittedOffset: 0},
		{Partition: board, TailOffset: 5, CommittedOffset: 4},
		{Partition: meta, TailOffset: 7, CommittedOffset: 7},
	}
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("SortCommandPartitionOffsetsByLag = %+v, want %+v", offsets, want)
	}
}

func TestEventPartitionOffsetOrderingHelpers(t *testing.T) {
	general := Partition{Kind: PartitionBoard, Key: "general"}.Normalize()
	life := Partition{Kind: PartitionBoard, Key: "life"}.Normalize()
	user := Partition{Kind: PartitionUser, Key: "usr_1"}.Normalize()
	normalized := (EventPartitionOffset{
		Partition:  Partition{},
		LastOffset: -2,
	}).Normalize()
	if want := (EventPartitionOffset{Partition: Partition{Kind: PartitionGlobal, Key: PartitionGlobal}}); normalized != want {
		t.Fatalf("EventPartitionOffset.Normalize = %+v, want %+v", normalized, want)
	}

	offsets := []EventPartitionOffset{
		{Partition: life, LastOffset: 2},
		{Partition: user, LastOffset: 7},
		{Partition: general, LastOffset: 7},
	}
	partitions := EventPartitionsByLastOffset(offsets, 2)
	if want := []Partition{general, user}; !reflect.DeepEqual(partitions, want) {
		t.Fatalf("EventPartitionsByLastOffset = %+v, want %+v", partitions, want)
	}

	SortEventPartitionOffsetsByLastOffset(offsets)
	want := []EventPartitionOffset{
		{Partition: general, LastOffset: 7},
		{Partition: user, LastOffset: 7},
		{Partition: life, LastOffset: 2},
	}
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("SortEventPartitionOffsetsByLastOffset = %+v, want %+v", offsets, want)
	}
}

func TestEventProjectionTargetOffsets(t *testing.T) {
	global := Partition{Kind: PartitionGlobal, Key: PartitionGlobal}.Normalize()
	board := Partition{Kind: PartitionBoard, Key: "board-a"}.Normalize()
	thread := Partition{Kind: PartitionThread, Key: "thread-a"}.Normalize()

	got := EventProjectionTargetOffsets([]Partition{
		{},
		board,
		thread,
	}, []EventPartitionOffset{
		{Partition: board, LastOffset: 3},
		{Partition: board, LastOffset: 9},
		{Partition: Partition{Kind: PartitionUser, Key: "ignored"}, LastOffset: 7},
		{Partition: Partition{}, LastOffset: -4},
	})
	want := map[Partition]int64{
		global: 0,
		board:  9,
		thread: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EventProjectionTargetOffsets = %+v, want %+v", got, want)
	}
}

func TestPartitionOffsetsReached(t *testing.T) {
	board := Partition{Kind: PartitionBoard, Key: "board-a"}.Normalize()
	thread := Partition{Kind: PartitionThread, Key: "thread-a"}.Normalize()

	if !PartitionOffsetsReached(map[Partition]int64{board: 9, Partition{}: 1}, map[Partition]int64{board: 9, thread: 0, Partition{}: 1}) {
		t.Fatal("PartitionOffsetsReached returned false for reached positive targets and zero target")
	}
	if PartitionOffsetsReached(map[Partition]int64{board: 8}, map[Partition]int64{board: 9}) {
		t.Fatal("PartitionOffsetsReached returned true before target offset")
	}
	if PartitionOffsetsReached(map[Partition]int64{}, map[Partition]int64{board: 1}) {
		t.Fatal("PartitionOffsetsReached returned true with missing positive target")
	}
}
