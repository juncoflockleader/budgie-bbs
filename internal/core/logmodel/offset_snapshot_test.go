package logmodel

import (
	"context"
	"reflect"
	"testing"
)

func TestCommandPartitionOffsetSnapshotClampsAndCopiesOffsets(t *testing.T) {
	ctx := context.Background()
	board := Partition{Kind: PartitionBoard, Key: "board-a"}.Normalize()
	thread := Partition{Kind: PartitionThread, Key: "thread-a"}.Normalize()
	source := NewCommandPartitionOffsetSnapshot([]CommandPartitionOffset{
		{Partition: board, TailOffset: 2, CommittedOffset: 5},
		{Partition: thread, TailOffset: -1, CommittedOffset: -2},
	}, 0)

	listed, err := source.ListCommandPartitionOffsets(ctx, 1)
	if err != nil {
		t.Fatalf("ListCommandPartitionOffsets: %v", err)
	}
	want := []CommandPartitionOffset{{Partition: board, TailOffset: 2, CommittedOffset: 2}}
	if !reflect.DeepEqual(listed, want) {
		t.Fatalf("listed = %+v, want %+v", listed, want)
	}
	listed[0].CommittedOffset = 0

	again, err := source.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		t.Fatalf("second ListCommandPartitionOffsets: %v", err)
	}
	wantAll := []CommandPartitionOffset{
		{Partition: board, TailOffset: 2, CommittedOffset: 2},
		{Partition: thread, TailOffset: 0, CommittedOffset: 0},
	}
	if !reflect.DeepEqual(again, wantAll) {
		t.Fatalf("second listed = %+v, want unclobbered snapshot %+v", again, wantAll)
	}
}
