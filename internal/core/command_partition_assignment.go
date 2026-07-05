package core

import "github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"

type HashCommandPartitionAssigner = logmodel.HashCommandPartitionAssigner
type CommandPartitionAssignmentSnapshot = logmodel.CommandPartitionAssignmentSnapshot
type SnapshotCommandPartitionAssigner = logmodel.SnapshotCommandPartitionAssigner

func NewHashCommandPartitionAssigner(members []string, generation int64) *HashCommandPartitionAssigner {
	return logmodel.NewHashCommandPartitionAssigner(members, generation)
}

func NewSnapshotCommandPartitionAssigner(snapshot CommandPartitionAssignmentSnapshot) *SnapshotCommandPartitionAssigner {
	return logmodel.NewSnapshotCommandPartitionAssigner(snapshot)
}

func NewHashCommandPartitionAssignerWithOverrides(members []string, overrides map[LogPartition]string, generation int64) *HashCommandPartitionAssigner {
	return logmodel.NewHashCommandPartitionAssignerWithOverrides(members, overrides, generation)
}
