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
