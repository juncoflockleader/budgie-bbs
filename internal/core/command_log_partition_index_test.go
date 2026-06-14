package core

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestSQLCommandLogPartitionIndexTracksProducedAndCommittedOffsets(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/partition-index.db")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	index := NewSQLCommandLogPartitionIndex(c.DB)
	log := NewIndexedCommandLog(NewMemoryCommandLog(), index)
	board := LogPartition{Kind: "board", Key: "general"}.Normalize()
	thread := LogPartition{Kind: "thread", Key: "thr_1"}.Normalize()

	produceIndexedPartitionRecord(t, ctx, log, board, "cid-board-1")
	produceIndexedPartitionRecord(t, ctx, log, board, "cid-board-2")
	produceIndexedPartitionRecord(t, ctx, log, thread, "cid-thread-1")
	if err := log.CommitPartition(ctx, board, 1); err != nil {
		t.Fatalf("CommitPartition: %v", err)
	}

	partitions, err := log.ListCommandPartitions(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommandPartitions: %v", err)
	}
	if !reflect.DeepEqual(partitions, []LogPartition{board, thread}) {
		t.Fatalf("partitions = %+v, want board then thread", partitions)
	}
	offsets, err := log.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommandPartitionOffsets: %v", err)
	}
	want := []CommandPartitionOffset{
		{Partition: board, TailOffset: 2, CommittedOffset: 1},
		{Partition: thread, TailOffset: 1, CommittedOffset: 0},
	}
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("offsets = %+v, want %+v", offsets, want)
	}
}

func TestIndexedCommandLogRegistersPartitionBeforeProducing(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/partition-index-candidate.db")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	index := NewSQLCommandLogPartitionIndex(c.DB)
	partition := LogPartition{Kind: "board", Key: "general"}.Normalize()

	_, err = NewIndexedCommandLog(failingProduceCommandLog{err: errors.New("synthetic produce failure")}, index).Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    "usr_alice",
		CID:        "cid-fails-after-candidate",
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"general","title":"General"}`),
		EnqueuedAt: 1000,
	})
	if err == nil {
		t.Fatalf("Produce succeeded, want inner produce failure")
	}
	partitions, err := index.ListCommandPartitions(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommandPartitions: %v", err)
	}
	if !reflect.DeepEqual(partitions, []LogPartition{partition}) {
		t.Fatalf("partitions after failed produce = %+v, want registered candidate", partitions)
	}
	offsets, err := index.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommandPartitionOffsets: %v", err)
	}
	want := []CommandPartitionOffset{{Partition: partition, TailOffset: 0, CommittedOffset: 0}}
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("offsets after failed produce = %+v, want %+v", offsets, want)
	}
}

func TestSQLCommandLogPartitionIndexTracksFetchedExternalTailsAndClampsCommit(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/partition-index-fetch.db")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	base := NewMemoryCommandLog()
	index := NewSQLCommandLogPartitionIndex(c.DB)
	partition := LogPartition{Kind: "board", Key: "general"}.Normalize()
	produceIndexedPartitionRecord(t, ctx, base, partition, "cid-external-1")
	produceIndexedPartitionRecord(t, ctx, base, partition, "cid-external-2")
	log := NewIndexedCommandLog(base, index)

	if err := log.CommitPartition(ctx, partition, 10); err != nil {
		t.Fatalf("CommitPartition: %v", err)
	}
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
	want := []CommandPartitionOffset{{Partition: partition, TailOffset: 2, CommittedOffset: 2}}
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("offsets = %+v, want committed offset clamped to indexed tail %+v", offsets, want)
	}
}

func TestIndexedCommandLogCommittedOffsetUsesPartitionIndex(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/partition-index-committed.db")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	base := NewMemoryCommandLog()
	index := NewSQLCommandLogPartitionIndex(c.DB)
	partition := LogPartition{Kind: "board", Key: "general"}.Normalize()
	produceIndexedPartitionRecord(t, ctx, base, partition, "cid-external-1")
	produceIndexedPartitionRecord(t, ctx, base, partition, "cid-external-2")
	log := NewIndexedCommandLog(base, index)

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
		t.Fatalf("committed offset = %d, want partition index offset 0 despite inner commit", offset)
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

func produceIndexedPartitionRecord(t *testing.T, ctx context.Context, log CommandLog, partition LogPartition, cid string) CommandLogRecord {
	t.Helper()
	record, err := log.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    "usr_alice",
		CID:        cid,
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"general","title":"General"}`),
		EnqueuedAt: 1000,
	})
	if err != nil {
		t.Fatalf("Produce %s: %v", cid, err)
	}
	return record
}
