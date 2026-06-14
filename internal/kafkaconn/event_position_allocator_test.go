package kafkaconn

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestSQLEventPositionAllocatorAllocatesDurablePositions(t *testing.T) {
	ctx := context.Background()
	c, err := core.New(filepath.Join(t.TempDir(), "event-position-allocator.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.DB.Close() })
	allocator := NewSQLEventPositionAllocator(c.DB, SQLEventPositionAllocatorOptions{})

	first, err := allocator.AllocateEventPositions(ctx, []core.BrokerEventRecord{
		eventPositionAllocatorRecord("evt_position_general_1", "general", 0),
		eventPositionAllocatorRecord("evt_position_general_2", "general", 0),
		eventPositionAllocatorRecord("evt_position_life_1", "life", 0),
	})
	if err != nil {
		t.Fatalf("AllocateEventPositions first: %v", err)
	}
	wantFirst := []EventPositionAllocation{
		{Partition: core.LogPartition{Kind: "board", Key: "general"}, PartitionOffset: 1, CompatibilitySeq: 1},
		{Partition: core.LogPartition{Kind: "board", Key: "general"}, PartitionOffset: 2, CompatibilitySeq: 2},
		{Partition: core.LogPartition{Kind: "board", Key: "life"}, PartitionOffset: 1, CompatibilitySeq: 3},
	}
	if !sameEventPositionAllocations(first, wantFirst) {
		t.Fatalf("first allocations = %+v, want %+v", first, wantFirst)
	}

	next, err := allocator.AllocateEventPositions(ctx, []core.BrokerEventRecord{
		eventPositionAllocatorRecord("evt_position_general_3", "general", 0),
	})
	if err != nil {
		t.Fatalf("AllocateEventPositions next: %v", err)
	}
	wantNext := []EventPositionAllocation{
		{Partition: core.LogPartition{Kind: "board", Key: "general"}, PartitionOffset: 3, CompatibilitySeq: 4},
	}
	if !sameEventPositionAllocations(next, wantNext) {
		t.Fatalf("next allocations = %+v, want %+v", next, wantNext)
	}
}

func TestSQLEventPositionAllocatorSeedsScalarFromExistingSQLEvents(t *testing.T) {
	ctx := context.Background()
	c, err := core.New(filepath.Join(t.TempDir(), "event-position-seed.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.DB.Close() })
	if _, err := core.NewSQLEventStore(c.DB).Append(ctx, core.EventAppend{
		ID:     "evt_existing_sql_seed",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:       "thr_existing_sql_seed",
			Board:    "general",
			Author:   "alice",
			AuthorID: "usr_alice",
			Title:    "Existing SQL event",
			TS:       1234,
		},
		TS: 1234,
	}); err != nil {
		t.Fatalf("append SQL event: %v", err)
	}
	allocator := NewSQLEventPositionAllocator(c.DB, SQLEventPositionAllocatorOptions{})

	allocations, err := allocator.AllocateEventPositions(ctx, []core.BrokerEventRecord{
		eventPositionAllocatorRecord("evt_position_after_sql_seed", "general", 0),
	})
	if err != nil {
		t.Fatalf("AllocateEventPositions: %v", err)
	}
	if len(allocations) != 1 || allocations[0].CompatibilitySeq != 2 {
		t.Fatalf("allocations = %+v, want scalar sequence after existing SQL seq 1", allocations)
	}
	if allocations[0].PartitionOffset != 2 {
		t.Fatalf("partition offset = %d, want after existing SQL partition offset 1", allocations[0].PartitionOffset)
	}
}

func TestSQLEventPositionAllocatorRollsBackRequestedSequenceMismatch(t *testing.T) {
	ctx := context.Background()
	c, err := core.New(filepath.Join(t.TempDir(), "event-position-rollback.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.DB.Close() })
	allocator := NewSQLEventPositionAllocator(c.DB, SQLEventPositionAllocatorOptions{})

	_, err = allocator.AllocateEventPositions(ctx, []core.BrokerEventRecord{
		eventPositionAllocatorRecord("evt_position_bad_requested_seq", "general", 99),
	})
	if err == nil || !strings.Contains(err.Error(), "does not match next reserved sequence 1") {
		t.Fatalf("AllocateEventPositions err = %v, want requested sequence mismatch", err)
	}
	allocations, err := allocator.AllocateEventPositions(ctx, []core.BrokerEventRecord{
		eventPositionAllocatorRecord("evt_position_after_bad_request", "general", 0),
	})
	if err != nil {
		t.Fatalf("AllocateEventPositions after rollback: %v", err)
	}
	want := []EventPositionAllocation{
		{Partition: core.LogPartition{Kind: "board", Key: "general"}, PartitionOffset: 1, CompatibilitySeq: 1},
	}
	if !sameEventPositionAllocations(allocations, want) {
		t.Fatalf("allocations after rollback = %+v, want %+v", allocations, want)
	}
}

func TestSQLEventPositionAllocatorDisablesScalarCompatibility(t *testing.T) {
	ctx := context.Background()
	c, err := core.New(filepath.Join(t.TempDir(), "event-position-partition-only.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.DB.Close() })
	allocator := NewSQLEventPositionAllocator(c.DB, SQLEventPositionAllocatorOptions{DisableCompatibilitySeq: true})

	_, err = allocator.AllocateEventPositions(ctx, []core.BrokerEventRecord{
		eventPositionAllocatorRecord("evt_position_partition_only_requested_seq", "general", 7),
	})
	if err == nil || !strings.Contains(err.Error(), "scalar sequence 7 while scalar compatibility allocation is disabled") {
		t.Fatalf("AllocateEventPositions requested seq err = %v, want scalar-disabled error", err)
	}

	allocations, err := allocator.AllocateEventPositions(ctx, []core.BrokerEventRecord{
		eventPositionAllocatorRecord("evt_position_partition_only_general_1", "general", 0),
		eventPositionAllocatorRecord("evt_position_partition_only_general_2", "general", 0),
		eventPositionAllocatorRecord("evt_position_partition_only_life_1", "life", 0),
	})
	if err != nil {
		t.Fatalf("AllocateEventPositions partition only: %v", err)
	}
	want := []EventPositionAllocation{
		{Partition: core.LogPartition{Kind: "board", Key: "general"}, PartitionOffset: 1, CompatibilitySeq: 0},
		{Partition: core.LogPartition{Kind: "board", Key: "general"}, PartitionOffset: 2, CompatibilitySeq: 0},
		{Partition: core.LogPartition{Kind: "board", Key: "life"}, PartitionOffset: 1, CompatibilitySeq: 0},
	}
	if !sameEventPositionAllocations(allocations, want) {
		t.Fatalf("partition-only allocations = %+v, want %+v", allocations, want)
	}
	if _, err := allocator.Head(ctx); err == nil || !strings.Contains(err.Error(), "scalar head disabled") {
		t.Fatalf("Head err = %v, want scalar head disabled", err)
	}
}

func TestSQLEventPositionAllocatorListsPartitionsAndHead(t *testing.T) {
	ctx := context.Background()
	c, err := core.New(filepath.Join(t.TempDir(), "event-position-list.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.DB.Close() })
	allocator := NewSQLEventPositionAllocator(c.DB, SQLEventPositionAllocatorOptions{})

	if _, err := allocator.AllocateEventPositions(ctx, []core.BrokerEventRecord{
		eventPositionAllocatorRecord("evt_position_general_1", "general", 0),
		eventPositionAllocatorRecord("evt_position_general_2", "general", 0),
		eventPositionAllocatorRecord("evt_position_life_1", "life", 0),
	}); err != nil {
		t.Fatalf("AllocateEventPositions: %v", err)
	}

	partitions, err := allocator.ListEventPartitions(ctx, 0)
	if err != nil {
		t.Fatalf("ListEventPartitions: %v", err)
	}
	want := []core.LogPartition{
		{Kind: "board", Key: "general"},
		{Kind: "board", Key: "life"},
	}
	if !reflect.DeepEqual(partitions, want) {
		t.Fatalf("partitions = %+v, want %+v", partitions, want)
	}
	limited, err := allocator.ListEventPartitions(ctx, 1)
	if err != nil {
		t.Fatalf("ListEventPartitions limited: %v", err)
	}
	if !reflect.DeepEqual(limited, want[:1]) {
		t.Fatalf("limited partitions = %+v, want %+v", limited, want[:1])
	}
	head, err := allocator.Head(ctx)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != 3 {
		t.Fatalf("head = %d, want 3", head)
	}
}

func eventPositionAllocatorRecord(id, board string, compatibilitySeq int64) core.BrokerEventRecord {
	return core.BrokerEventRecord{
		Version:          1,
		ID:               id,
		Kind:             proto.EvtThreadNew,
		CompatibilitySeq: compatibilitySeq,
		Scopes:           []string{"board:" + board},
		Payload:          []byte(`{"id":"thr_` + id + `","board":"` + board + `","author":"alice","authorID":"usr_alice","title":"Positioned","ts":1234}`),
		TS:               1234,
		PartitionKind:    "board",
		PartitionKey:     board,
	}
}

func sameEventPositionAllocations(got, want []EventPositionAllocation) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Partition.Normalize() != want[i].Partition.Normalize() ||
			got[i].PartitionOffset != want[i].PartitionOffset ||
			got[i].CompatibilitySeq != want[i].CompatibilitySeq {
			return false
		}
	}
	return true
}
