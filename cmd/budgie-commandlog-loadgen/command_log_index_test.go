package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestIndexedCommandLogTracksProducedPartitionsAndOffsets(t *testing.T) {
	ctx := context.Background()
	log := newIndexedCommandLog(core.NewMemoryCommandLog())
	board := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	thread := core.LogPartition{Kind: "thread", Key: "thr_1"}.Normalize()

	produceIndexedCommandLogRecord(t, ctx, log, board, "cid-board-1")
	produceIndexedCommandLogRecord(t, ctx, log, board, "cid-board-2")
	produceIndexedCommandLogRecord(t, ctx, log, thread, "cid-thread-1")
	if err := log.CommitPartition(ctx, board, 1); err != nil {
		t.Fatalf("commit board: %v", err)
	}

	partitions, err := log.ListCommandPartitions(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommandPartitions: %v", err)
	}
	if !reflect.DeepEqual(partitions, []core.LogPartition{board, thread}) {
		t.Fatalf("partitions = %+v, want board then thread by tail offset", partitions)
	}

	offsets, err := log.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommandPartitionOffsets: %v", err)
	}
	want := []core.CommandPartitionOffset{
		{Partition: board, TailOffset: 2, CommittedOffset: 1},
		{Partition: thread, TailOffset: 1, CommittedOffset: 0},
	}
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("offsets = %+v, want %+v", offsets, want)
	}
}

func TestIndexedCommandLogTracksFetchedTailsForExternalProducers(t *testing.T) {
	ctx := context.Background()
	base := core.NewMemoryCommandLog()
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	produceIndexedCommandLogRecord(t, ctx, base, partition, "cid-external-1")
	produceIndexedCommandLogRecord(t, ctx, base, partition, "cid-external-2")
	log := newIndexedCommandLog(base)

	records, err := log.FetchPartition(ctx, partition, 0, 10)
	if err != nil {
		t.Fatalf("FetchPartition: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	offsets, err := log.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommandPartitionOffsets: %v", err)
	}
	want := []core.CommandPartitionOffset{{Partition: partition, TailOffset: 2, CommittedOffset: 0}}
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("offsets = %+v, want %+v", offsets, want)
	}
}

func TestIndexedCommandLogClampsCommittedOffsetToIndexedTail(t *testing.T) {
	ctx := context.Background()
	log := newIndexedCommandLog(core.NewMemoryCommandLog())
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()

	produceIndexedCommandLogRecord(t, ctx, log, partition, "cid-board-1")
	if err := log.CommitPartition(ctx, partition, 10); err != nil {
		t.Fatalf("commit board: %v", err)
	}
	offsets, err := log.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommandPartitionOffsets: %v", err)
	}
	want := []core.CommandPartitionOffset{{Partition: partition, TailOffset: 1, CommittedOffset: 1}}
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("offsets = %+v, want committed offset clamped to tail %+v", offsets, want)
	}
}

func TestIndexedCommandLogCommittedOffsetUsesLogicalIndex(t *testing.T) {
	ctx := context.Background()
	base := core.NewMemoryCommandLog()
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	produceIndexedCommandLogRecord(t, ctx, base, partition, "cid-external-1")
	produceIndexedCommandLogRecord(t, ctx, base, partition, "cid-external-2")
	log := newIndexedCommandLog(base)

	if _, err := log.FetchPartition(ctx, partition, 0, 10); err != nil {
		t.Fatalf("FetchPartition: %v", err)
	}
	if err := base.CommitPartition(ctx, partition, 10); err != nil {
		t.Fatalf("inner commit: %v", err)
	}
	offset, err := log.CommittedOffset(ctx, partition)
	if err != nil {
		t.Fatalf("CommittedOffset: %v", err)
	}
	if offset != 0 {
		t.Fatalf("committed offset = %d, want logical index offset 0 despite inner commit", offset)
	}
	if err := log.CommitPartition(ctx, partition, 1); err != nil {
		t.Fatalf("indexed commit: %v", err)
	}
	offset, err = log.CommittedOffset(ctx, partition)
	if err != nil {
		t.Fatalf("CommittedOffset after indexed commit: %v", err)
	}
	if offset != 1 {
		t.Fatalf("committed offset after indexed commit = %d, want 1", offset)
	}
}

func produceIndexedCommandLogRecord(t *testing.T, ctx context.Context, log core.CommandLog, partition core.LogPartition, cid string) core.CommandLogRecord {
	t.Helper()
	record, err := log.Produce(ctx, core.CommandLogRecord{
		Partition:  partition,
		ActorID:    "usr_alice",
		CID:        cid,
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"general","title":"General"}`),
		EnqueuedAt: 1000,
	})
	if err != nil {
		t.Fatalf("produce %s: %v", cid, err)
	}
	return record
}
