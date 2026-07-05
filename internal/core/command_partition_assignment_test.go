package core

import (
	"context"
	"reflect"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
)

func TestHashCommandPartitionAssignerNormalizesMembersAndBumpsGeneration(t *testing.T) {
	ctx := context.Background()
	assigner := NewHashCommandPartitionAssigner([]string{"writer-b", " writer-a ", "writer-a", ""}, 4)
	if got := assigner.Members(); !reflect.DeepEqual(got, []string{"writer-a", "writer-b"}) {
		t.Fatalf("members = %v, want sorted unique writer ids", got)
	}
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", partition)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if assignment.Generation != 4 {
		t.Fatalf("assignment generation = %d, want 4", assignment.Generation)
	}
	if assignment.OwnerID != "writer-a" && assignment.OwnerID != "writer-b" {
		t.Fatalf("assignment owner = %q, want one configured writer", assignment.OwnerID)
	}
	if assigned != (assignment.OwnerID == "writer-a") {
		t.Fatalf("assigned = %v for owner %q, want ownership to match assignment", assigned, assignment.OwnerID)
	}

	if got := assigner.SetMembers([]string{"writer-c"}); got != 5 {
		t.Fatalf("generation after rebalance = %d, want 5", got)
	}
	rebalanced, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", partition)
	if err != nil {
		t.Fatalf("assign after rebalance: %v", err)
	}
	if rebalanced.OwnerID != "writer-c" || rebalanced.Generation != 5 || assigned {
		t.Fatalf("rebalanced assignment = %+v assigned=%v, want writer-c generation 5 not assigned to writer-a", rebalanced, assigned)
	}
}

func TestHashCommandPartitionAssignerIsOrderIndependent(t *testing.T) {
	ctx := context.Background()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	a := NewHashCommandPartitionAssigner([]string{"writer-a", "writer-b", "writer-c"}, 1)
	b := NewHashCommandPartitionAssigner([]string{"writer-c", "writer-b", "writer-a"}, 1)
	first, _, err := a.AssignCommandPartition(ctx, "writer-a", partition)
	if err != nil {
		t.Fatalf("assign first: %v", err)
	}
	second, _, err := b.AssignCommandPartition(ctx, "writer-a", partition)
	if err != nil {
		t.Fatalf("assign second: %v", err)
	}
	if first.OwnerID != second.OwnerID {
		t.Fatalf("assignment differs by member input order: %q vs %q", first.OwnerID, second.OwnerID)
	}
}

func TestHashCommandPartitionAssignerSupportsPartitionOverrides(t *testing.T) {
	ctx := context.Background()
	partition := LogPartition{Kind: partitionThread, Key: "thr_hot#reply-0"}
	assigner := NewHashCommandPartitionAssigner([]string{"writer-a", "writer-b"}, 3)
	if got := assigner.SetOverrides(map[LogPartition]string{partition: "writer-b"}); got != 4 {
		t.Fatalf("generation after override = %d, want 4", got)
	}
	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-b", partition)
	if err != nil {
		t.Fatalf("assign override owner: %v", err)
	}
	if !assigned || assignment.OwnerID != "writer-b" || assignment.Generation != 4 {
		t.Fatalf("override assignment = %+v assigned=%v, want writer-b generation 4", assignment, assigned)
	}
	assignment, assigned, err = assigner.AssignCommandPartition(ctx, "writer-a", partition)
	if err != nil {
		t.Fatalf("assign non-owner: %v", err)
	}
	if assigned || assignment.OwnerID != "writer-b" {
		t.Fatalf("non-owner assignment = %+v assigned=%v, want partition pinned to writer-b", assignment, assigned)
	}

	if got := assigner.SetMembers([]string{"writer-a"}); got != 5 {
		t.Fatalf("generation after removing override owner = %d, want 5", got)
	}
	if overrides := assigner.Overrides(); len(overrides) != 0 {
		t.Fatalf("overrides after removing owner = %+v, want dropped", overrides)
	}
	rebalanced, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", partition)
	if err != nil {
		t.Fatalf("assign after dropping override: %v", err)
	}
	if !assigned || rebalanced.OwnerID != "writer-a" || rebalanced.Generation != 5 {
		t.Fatalf("assignment after dropping override = %+v assigned=%v, want writer-a generation 5", rebalanced, assigned)
	}
}

func TestSnapshotCommandPartitionAssignerListsOwnedPartitionsAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	general := LogPartition{Kind: partitionBoard, Key: "general"}
	life := LogPartition{Kind: partitionBoard, Key: "life"}
	hotReply := LogPartition{Kind: partitionThread, Key: "thr_hot#reply-0"}
	assigner := NewSnapshotCommandPartitionAssigner(CommandPartitionAssignmentSnapshot{
		Generation: 11,
		Owners: map[LogPartition]string{
			general:  "writer-a",
			life:     "writer-b",
			hotReply: "writer-a",
		},
	})

	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", general)
	if err != nil {
		t.Fatalf("assign general: %v", err)
	}
	if !assigned || assignment.OwnerID != "writer-a" || assignment.Generation != 11 {
		t.Fatalf("general assignment = %+v assigned=%v, want writer-a generation 11", assignment, assigned)
	}
	assignment, assigned, err = assigner.AssignCommandPartition(ctx, "writer-a", life)
	if err != nil {
		t.Fatalf("assign life: %v", err)
	}
	if assigned || assignment.OwnerID != "writer-b" || assignment.Generation != 11 {
		t.Fatalf("life assignment = %+v assigned=%v, want writer-b generation 11 and not owned by writer-a", assignment, assigned)
	}
	missing := LogPartition{Kind: partitionBoard, Key: "missing"}
	assignment, assigned, err = assigner.AssignCommandPartition(ctx, "writer-a", missing)
	if err != nil {
		t.Fatalf("assign missing: %v", err)
	}
	if assigned || assignment.OwnerID != "" || assignment.Generation != 11 {
		t.Fatalf("missing assignment = %+v assigned=%v, want fail-closed unassigned generation 11", assignment, assigned)
	}

	owned, err := assigner.ListAssignedCommandPartitions(ctx, "writer-a", 0)
	if err != nil {
		t.Fatalf("list owned: %v", err)
	}
	wantOwned := []CommandPartitionAssignment{
		{Partition: general.Normalize(), OwnerID: "writer-a", Generation: 11},
		{Partition: hotReply.Normalize(), OwnerID: "writer-a", Generation: 11},
	}
	if !reflect.DeepEqual(owned, wantOwned) {
		t.Fatalf("owned assignments = %+v, want %+v", owned, wantOwned)
	}
	limited, err := assigner.ListAssignedCommandPartitions(ctx, "writer-a", 1)
	if err != nil {
		t.Fatalf("list limited: %v", err)
	}
	if !reflect.DeepEqual(limited, wantOwned[:1]) {
		t.Fatalf("limited assignments = %+v, want %+v", limited, wantOwned[:1])
	}

	if got := assigner.ApplySnapshot(CommandPartitionAssignmentSnapshot{
		Generation: 12,
		Owners: map[LogPartition]string{
			general: "writer-b",
		},
	}); got != 12 {
		t.Fatalf("apply snapshot generation = %d, want 12", got)
	}
	assignment, assigned, err = assigner.AssignCommandPartition(ctx, "writer-a", general)
	if err != nil {
		t.Fatalf("assign after rebalance: %v", err)
	}
	if assigned || assignment.OwnerID != "writer-b" || assignment.Generation != 12 {
		t.Fatalf("post-rebalance assignment = %+v assigned=%v, want writer-b generation 12", assignment, assigned)
	}
	owned, err = assigner.ListAssignedCommandPartitions(ctx, "writer-a", 0)
	if err != nil {
		t.Fatalf("list owned after rebalance: %v", err)
	}
	if len(owned) != 0 {
		t.Fatalf("owned after rebalance = %+v, want none", owned)
	}
}

func TestCommandPartitionAssignmentPartitionsNormalizesAndDedupes(t *testing.T) {
	general := LogPartition{Kind: partitionBoard, Key: "general"}.Normalize()
	global := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}.Normalize()
	got := logmodel.CommandPartitionAssignmentPartitions([]CommandPartitionAssignment{
		{Partition: general, OwnerID: "writer-a"},
		{Partition: general, OwnerID: "writer-a"},
		{Partition: LogPartition{}, OwnerID: "writer-a"},
	})
	want := []LogPartition{general, global}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commandPartitionAssignmentPartitions = %+v, want %+v", got, want)
	}
}

func TestCommandPartitionAssignmentsForOwnerNormalizesOwnerAndPartitions(t *testing.T) {
	general := LogPartition{Kind: partitionBoard, Key: "general"}.Normalize()
	global := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}.Normalize()
	single := logmodel.NewCommandPartitionAssignment(LogPartition{}, " writer-b ", 43)
	if want := (CommandPartitionAssignment{Partition: global, OwnerID: "writer-b", Generation: 43}); single != want {
		t.Fatalf("commandPartitionAssignmentForOwner = %+v, want %+v", single, want)
	}
	got := logmodel.CommandPartitionAssignmentsForOwner([]LogPartition{general, LogPartition{}}, " writer-a ", 42)
	want := []CommandPartitionAssignment{
		{Partition: general, OwnerID: "writer-a", Generation: 42},
		{Partition: global, OwnerID: "writer-a", Generation: 42},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commandPartitionAssignmentsForOwner = %+v, want %+v", got, want)
	}
}

func TestSnapshotCommandPartitionAssignerIgnoresStaleGenerations(t *testing.T) {
	ctx := context.Background()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	assigner := NewSnapshotCommandPartitionAssigner(CommandPartitionAssignmentSnapshot{
		Generation: 10,
		Owners: map[LogPartition]string{
			partition: "writer-a",
		},
	})

	if got := assigner.ApplySnapshot(CommandPartitionAssignmentSnapshot{
		Generation: 11,
		Owners: map[LogPartition]string{
			partition: "writer-b",
		},
	}); got != 11 {
		t.Fatalf("apply fresh generation = %d, want 11", got)
	}
	if got := assigner.ApplySnapshot(CommandPartitionAssignmentSnapshot{
		Generation: 11,
		Owners: map[LogPartition]string{
			partition: "writer-a",
		},
	}); got != 11 {
		t.Fatalf("apply repeated generation = %d, want current generation 11", got)
	}
	if got := assigner.ApplySnapshot(CommandPartitionAssignmentSnapshot{
		Generation: 9,
		Owners: map[LogPartition]string{
			partition: "writer-a",
		},
	}); got != 11 {
		t.Fatalf("apply stale generation = %d, want current generation 11", got)
	}

	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-b", partition)
	if err != nil {
		t.Fatalf("assign current owner: %v", err)
	}
	if !assigned || assignment.OwnerID != "writer-b" || assignment.Generation != 11 {
		t.Fatalf("current assignment = %+v assigned=%v, want writer-b generation 11", assignment, assigned)
	}
	assignment, assigned, err = assigner.AssignCommandPartition(ctx, "writer-a", partition)
	if err != nil {
		t.Fatalf("assign stale owner: %v", err)
	}
	if assigned || assignment.OwnerID != "writer-b" || assignment.Generation != 11 {
		t.Fatalf("stale-owner assignment = %+v assigned=%v, want writer-b generation 11", assignment, assigned)
	}
}
