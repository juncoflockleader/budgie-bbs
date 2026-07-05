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
