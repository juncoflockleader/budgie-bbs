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
	c := newCoreTestCore(t)
	index := NewSQLCommandLogPartitionIndex(c.DB)
	log := NewIndexedCommandLog(NewMemoryCommandLog(), index)
	board := LogPartition{Kind: "board", Key: "general"}.Normalize()
	thread := LogPartition{Kind: "thread", Key: "thr_1"}.Normalize()

	produceCommandLogWorkerRecord(t, ctx, log, board, "cid-board-1")
	produceCommandLogWorkerRecord(t, ctx, log, board, "cid-board-2")
	produceCommandLogWorkerRecord(t, ctx, log, thread, "cid-thread-1")
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

func TestListCommandPartitionOffsetsWithLimitReportsOverflowAndNormalizes(t *testing.T) {
	ctx := context.Background()
	board := LogPartition{Kind: partitionBoard, Key: "general"}.Normalize()
	thread := LogPartition{Kind: partitionThread, Key: "thr_1"}.Normalize()
	offsets := commandPartitionOffsetSliceLister{
		{Partition: board, TailOffset: 2, CommittedOffset: 5},
		{Partition: LogPartition{}, TailOffset: -1, CommittedOffset: -3},
		{Partition: thread, TailOffset: 4, CommittedOffset: 1},
	}

	got, limited, err := listCommandPartitionOffsetsWithLimit(ctx, offsets, 2)
	if err != nil {
		t.Fatalf("listCommandPartitionOffsetsWithLimit: %v", err)
	}
	if !limited {
		t.Fatal("limited = false, want true")
	}
	want := []CommandPartitionOffset{
		{Partition: board, TailOffset: 2, CommittedOffset: 2},
		{Partition: LogPartition{Kind: partitionGlobal, Key: partitionGlobal}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("offsets = %+v, want %+v", got, want)
	}

	got, limited, err = listCommandPartitionOffsetsWithLimit(ctx, offsets, 0)
	if err != nil {
		t.Fatalf("listCommandPartitionOffsetsWithLimit unlimited: %v", err)
	}
	if limited {
		t.Fatal("unlimited limited = true, want false")
	}
	want = append(want, CommandPartitionOffset{Partition: thread, TailOffset: 4, CommittedOffset: 1})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unlimited offsets = %+v, want %+v", got, want)
	}
}

func TestListCommandLogPartitionOffsetsWithLimitRequiresOffsetLister(t *testing.T) {
	commandLog := struct{ CommandLog }{CommandLog: NewMemoryCommandLog()}
	_, _, err := listCommandLogPartitionOffsetsWithLimit(context.Background(), commandLog, 10, "partition offsets required")
	if err == nil || err.Error() != "partition offsets required" {
		t.Fatalf("error = %v, want partition offsets required", err)
	}
}

type commandPartitionOffsetSliceLister []CommandPartitionOffset

func (l commandPartitionOffsetSliceLister) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := append([]CommandPartitionOffset(nil), l...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func TestIndexedCommandLogRegistersPartitionBeforeProducing(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	index := NewSQLCommandLogPartitionIndex(c.DB)
	partition := LogPartition{Kind: "board", Key: "general"}.Normalize()

	_, err := NewIndexedCommandLog(failingProduceCommandLog{err: errors.New("synthetic produce failure")}, index).Produce(ctx, CommandLogRecord{
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
	c := newCoreTestCore(t)
	base := NewMemoryCommandLog()
	index := NewSQLCommandLogPartitionIndex(c.DB)
	partition := LogPartition{Kind: "board", Key: "general"}.Normalize()
	produceCommandLogWorkerRecord(t, ctx, base, partition, "cid-external-1")
	produceCommandLogWorkerRecord(t, ctx, base, partition, "cid-external-2")
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
	c := newCoreTestCore(t)
	base := NewMemoryCommandLog()
	index := NewSQLCommandLogPartitionIndex(c.DB)
	partition := LogPartition{Kind: "board", Key: "general"}.Normalize()
	produceCommandLogWorkerRecord(t, ctx, base, partition, "cid-external-1")
	produceCommandLogWorkerRecord(t, ctx, base, partition, "cid-external-2")
	log := NewIndexedCommandLog(base, index)

	if _, err := log.FetchPartition(ctx, partition, 0, 10); err != nil {
		t.Fatalf("FetchPartition: %v", err)
	}
	if err := base.CommitPartition(ctx, partition, 10); err != nil {
		t.Fatalf("inner commit: %v", err)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "partition index offset after inner commit")
	if err := log.CommitPartition(ctx, partition, 1); err != nil {
		t.Fatalf("indexed commit: %v", err)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 1, "partition index offset after indexed commit")
}
