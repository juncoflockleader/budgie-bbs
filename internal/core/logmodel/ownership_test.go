package logmodel

import "testing"

func TestNewCommandPartitionClaimNormalizesOwnerAndPartition(t *testing.T) {
	global := Partition{Kind: PartitionGlobal, Key: PartitionGlobal}.Normalize()
	got := NewCommandPartitionClaim(Partition{}, " writer-a ", 42)
	want := CommandPartitionClaim{Partition: global, OwnerID: "writer-a", ExpiresAt: 42}
	if got != want {
		t.Fatalf("NewCommandPartitionClaim = %+v, want %+v", got, want)
	}
}

func TestCommandPartitionAssignmentSnapshotForLaggingOffsets(t *testing.T) {
	global := Partition{Kind: PartitionGlobal, Key: PartitionGlobal}.Normalize()
	board := Partition{Kind: PartitionBoard, Key: "board-a"}.Normalize()
	snapshot := CommandPartitionAssignmentSnapshotForLaggingOffsets([]CommandPartitionOffset{
		{Partition: Partition{}, TailOffset: 2, CommittedOffset: 1},
		{Partition: Partition{Kind: PartitionBoard, Key: "board-a"}, TailOffset: 5, CommittedOffset: 5},
		{Partition: Partition{Kind: PartitionBoard, Key: "board-a"}, TailOffset: 7, CommittedOffset: 4},
	}, []string{" writer-b ", "writer-a", "writer-a"}, 9)

	members := NormalizeCommandPartitionAssignmentMembers([]string{"writer-b", "writer-a"})
	want := CommandPartitionAssignmentSnapshot{
		Generation: 9,
		Owners: map[Partition]string{
			global: members[CommandPartitionAssignmentIndex(global, len(members))],
			board:  members[CommandPartitionAssignmentIndex(board, len(members))],
		},
	}
	if snapshot.Generation != want.Generation || len(snapshot.Owners) != len(want.Owners) {
		t.Fatalf("snapshot = %+v, want %+v", snapshot, want)
	}
	for partition, owner := range want.Owners {
		if snapshot.Owners[partition] != owner {
			t.Fatalf("snapshot owners = %+v, want %+v", snapshot.Owners, want.Owners)
		}
	}
}
