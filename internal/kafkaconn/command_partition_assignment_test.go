package kafkaconn

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

func TestKafkaPartitionForLogicalPartitionIsDeterministic(t *testing.T) {
	partition := core.LogPartition{Kind: "board", Key: "general"}
	first, err := KafkaPartitionForLogicalPartition(partition, 32)
	if err != nil {
		t.Fatalf("KafkaPartitionForLogicalPartition: %v", err)
	}
	second, err := KafkaPartitionForLogicalPartition(partition, 32)
	if err != nil {
		t.Fatalf("KafkaPartitionForLogicalPartition second: %v", err)
	}
	if first != second {
		t.Fatalf("physical partition = %d then %d, want deterministic", first, second)
	}
	if first < 0 || first >= 32 {
		t.Fatalf("physical partition = %d, want in [0,32)", first)
	}
	if _, err := KafkaPartitionForLogicalPartition(partition, 0); err == nil {
		t.Fatalf("KafkaPartitionForLogicalPartition with zero partitions succeeded, want error")
	}
}

func TestCommandPartitionAssignmentSnapshotForOwnedKafkaPartitions(t *testing.T) {
	ctx := context.Background()
	commandTopic := "budgie.commands"
	partitionCount := int32(32)
	ownedLogical := core.LogPartition{Kind: "board", Key: "general"}
	ownedPhysical, err := KafkaPartitionForLogicalPartition(ownedLogical, partitionCount)
	if err != nil {
		t.Fatalf("owned physical partition: %v", err)
	}
	unownedLogical := firstDifferentPhysicalPartition(t, ownedLogical, ownedPhysical, partitionCount)

	snapshot, err := CommandPartitionAssignmentSnapshotForOwnedKafkaPartitions(CommandPartitionAssignmentOptions{
		CommandTopic:   commandTopic,
		OwnerID:        "writer-a",
		Generation:     17,
		PartitionCount: partitionCount,
	}, []TopicPartitionAssignment{
		{Topic: "other.commands", Partition: ownedPhysical},
		{Topic: commandTopic, Partition: ownedPhysical},
	}, []core.LogPartition{ownedLogical, unownedLogical})
	if err != nil {
		t.Fatalf("CommandPartitionAssignmentSnapshotForOwnedKafkaPartitions: %v", err)
	}
	assigner := core.NewSnapshotCommandPartitionAssigner(snapshot)
	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", ownedLogical)
	if err != nil {
		t.Fatalf("assign owned: %v", err)
	}
	if !assigned || assignment.OwnerID != "writer-a" || assignment.Generation != 17 {
		t.Fatalf("owned assignment = %+v assigned=%v, want writer-a generation 17", assignment, assigned)
	}
	assignment, assigned, err = assigner.AssignCommandPartition(ctx, "writer-a", unownedLogical)
	if err != nil {
		t.Fatalf("assign unowned: %v", err)
	}
	if assigned || assignment.OwnerID != "" || assignment.Generation != 17 {
		t.Fatalf("unowned assignment = %+v assigned=%v, want fail-closed unassigned", assignment, assigned)
	}
	owned, err := assigner.ListAssignedCommandPartitions(ctx, "writer-a", 0)
	if err != nil {
		t.Fatalf("list owned: %v", err)
	}
	if len(owned) != 1 || owned[0].Partition != ownedLogical.Normalize() || owned[0].Generation != 17 {
		t.Fatalf("owned list = %+v, want only %s/%s generation 17", owned, ownedLogical.Kind, ownedLogical.Key)
	}
}

func TestCommandPartitionRebalanceAdapterAppliesAssignmentAndRevoke(t *testing.T) {
	ctx := context.Background()
	commandTopic := "budgie.commands"
	partitionCount := int32(32)
	ownedLogical := core.LogPartition{Kind: "board", Key: "general"}
	ownedPhysical, err := KafkaPartitionForLogicalPartition(ownedLogical, partitionCount)
	if err != nil {
		t.Fatalf("owned physical partition: %v", err)
	}
	assigner := core.NewSnapshotCommandPartitionAssigner(core.CommandPartitionAssignmentSnapshot{})
	adapter := NewCommandPartitionRebalanceAdapter(assigner, CommandPartitionAssignmentOptions{
		CommandTopic:   commandTopic,
		OwnerID:        "writer-a",
		PartitionCount: partitionCount,
	})

	generation, err := adapter.ApplyConsumerGroupAssignment(ctx, 21, []TopicPartitionAssignment{
		{Topic: commandTopic, Partition: ownedPhysical},
	}, []core.LogPartition{ownedLogical})
	if err != nil {
		t.Fatalf("ApplyConsumerGroupAssignment: %v", err)
	}
	if generation != 21 {
		t.Fatalf("applied generation = %d, want 21", generation)
	}
	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", ownedLogical)
	if err != nil {
		t.Fatalf("assign after callback: %v", err)
	}
	if !assigned || assignment.OwnerID != "writer-a" || assignment.Generation != 21 {
		t.Fatalf("assignment after callback = %+v assigned=%v, want writer-a generation 21", assignment, assigned)
	}

	generation, err = adapter.RevokeConsumerGroupAssignment(ctx, 22)
	if err != nil {
		t.Fatalf("RevokeConsumerGroupAssignment: %v", err)
	}
	if generation != 22 {
		t.Fatalf("revoke generation = %d, want 22", generation)
	}
	assignment, assigned, err = assigner.AssignCommandPartition(ctx, "writer-a", ownedLogical)
	if err != nil {
		t.Fatalf("assign after revoke: %v", err)
	}
	if assigned || assignment.OwnerID != "" || assignment.Generation != 22 {
		t.Fatalf("assignment after revoke = %+v assigned=%v, want fail-closed generation 22", assignment, assigned)
	}
}

func TestCommandPartitionRebalanceAdapterIgnoresStaleAssignmentGeneration(t *testing.T) {
	ctx := context.Background()
	commandTopic := "budgie.commands"
	partitionCount := int32(32)
	logical := core.LogPartition{Kind: "board", Key: "general"}
	physical, err := KafkaPartitionForLogicalPartition(logical, partitionCount)
	if err != nil {
		t.Fatalf("physical partition: %v", err)
	}
	assigner := core.NewSnapshotCommandPartitionAssigner(core.CommandPartitionAssignmentSnapshot{})
	adapter := NewCommandPartitionRebalanceAdapter(assigner, CommandPartitionAssignmentOptions{
		CommandTopic:   commandTopic,
		OwnerID:        "writer-a",
		PartitionCount: partitionCount,
	})

	if _, err := adapter.RevokeConsumerGroupAssignment(ctx, 30); err != nil {
		t.Fatalf("revoke generation 30: %v", err)
	}
	generation, err := adapter.ApplyConsumerGroupAssignment(ctx, 29, []TopicPartitionAssignment{
		{Topic: commandTopic, Partition: physical},
	}, []core.LogPartition{logical})
	if err != nil {
		t.Fatalf("stale apply: %v", err)
	}
	if generation != 30 {
		t.Fatalf("stale apply returned generation = %d, want current 30", generation)
	}
	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", logical)
	if err != nil {
		t.Fatalf("assign after stale apply: %v", err)
	}
	if assigned || assignment.OwnerID != "" || assignment.Generation != 30 {
		t.Fatalf("assignment after stale apply = %+v assigned=%v, want revoked generation 30", assignment, assigned)
	}
}

func TestCommandPartitionAssignmentSnapshotRejectsInvalidPhysicalPartition(t *testing.T) {
	_, err := CommandPartitionAssignmentSnapshotForOwnedKafkaPartitions(CommandPartitionAssignmentOptions{
		CommandTopic:   "budgie.commands",
		OwnerID:        "writer-a",
		Generation:     1,
		PartitionCount: 4,
	}, []TopicPartitionAssignment{{Topic: "budgie.commands", Partition: 4}}, []core.LogPartition{{Kind: "board", Key: "general"}})
	if err == nil {
		t.Fatalf("CommandPartitionAssignmentSnapshotForOwnedKafkaPartitions succeeded, want invalid physical partition error")
	}
}

func TestCommandPartitionRebalanceAdapterRejectsInvalidCallbacks(t *testing.T) {
	ctx := context.Background()
	assigner := core.NewSnapshotCommandPartitionAssigner(core.CommandPartitionAssignmentSnapshot{})
	adapter := NewCommandPartitionRebalanceAdapter(assigner, CommandPartitionAssignmentOptions{
		CommandTopic:   "budgie.commands",
		OwnerID:        "writer-a",
		PartitionCount: 4,
	})
	if _, err := adapter.ApplyConsumerGroupAssignment(ctx, 0, nil, nil); err == nil {
		t.Fatalf("ApplyConsumerGroupAssignment without generation succeeded, want error")
	}
	if _, err := adapter.RevokeConsumerGroupAssignment(ctx, 0); err == nil {
		t.Fatalf("RevokeConsumerGroupAssignment without generation succeeded, want error")
	}
	nilAdapter := NewCommandPartitionRebalanceAdapter(nil, CommandPartitionAssignmentOptions{})
	if _, err := nilAdapter.ApplyConsumerGroupAssignment(ctx, 1, nil, nil); err == nil {
		t.Fatalf("ApplyConsumerGroupAssignment with nil target succeeded, want error")
	}
	if _, err := nilAdapter.RevokeConsumerGroupAssignment(ctx, 1); err == nil {
		t.Fatalf("RevokeConsumerGroupAssignment with nil target succeeded, want error")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := adapter.ApplyConsumerGroupAssignment(cancelled, 1, nil, nil); err == nil {
		t.Fatalf("ApplyConsumerGroupAssignment with cancelled context succeeded, want error")
	}
}

func TestCommandPartitionAssignmentSnapshotRequiresOwner(t *testing.T) {
	_, err := CommandPartitionAssignmentSnapshotForOwnedKafkaPartitions(CommandPartitionAssignmentOptions{
		CommandTopic:   "budgie.commands",
		PartitionCount: 4,
	}, nil, nil)
	if err == nil {
		t.Fatalf("CommandPartitionAssignmentSnapshotForOwnedKafkaPartitions without owner succeeded, want error")
	}
}

func firstDifferentPhysicalPartition(t *testing.T, first core.LogPartition, firstPhysical int32, partitionCount int32) core.LogPartition {
	t.Helper()
	for i := 0; i < 256; i++ {
		candidate := core.LogPartition{Kind: "board", Key: "candidate-" + string(rune('a'+i%26)) + "-" + string(rune('a'+(i/26)%26))}
		if candidate.Normalize() == first.Normalize() {
			continue
		}
		physical, err := KafkaPartitionForLogicalPartition(candidate, partitionCount)
		if err != nil {
			t.Fatalf("candidate physical partition: %v", err)
		}
		if physical != firstPhysical {
			return candidate
		}
	}
	t.Fatalf("could not find a candidate with a different physical partition from %d", firstPhysical)
	return core.LogPartition{}
}
