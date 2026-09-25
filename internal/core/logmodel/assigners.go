package logmodel

import (
	"context"
	"strings"
	"sync"
)

// HashCommandPartitionAssigner is a deterministic stand-in for broker
// consumer-group assignment. It lets tests and experimental writer nodes model
// partition ownership and rebalances without a separate lease table.
type HashCommandPartitionAssigner struct {
	mu         sync.RWMutex
	members    []string
	overrides  map[Partition]string
	generation int64
}

// SnapshotCommandPartitionAssigner applies broker-style assignment snapshots.
// Unlike the hash assigner, missing partitions are unassigned. This mirrors
// consumer-group rebalances where a writer must not drain a partition unless it
// appears in the current assigned set.
type SnapshotCommandPartitionAssigner struct {
	mu          sync.RWMutex
	assignments map[Partition]string
	generation  int64
}

func NewHashCommandPartitionAssigner(members []string, generation int64) *HashCommandPartitionAssigner {
	return NewHashCommandPartitionAssignerWithOverrides(members, nil, generation)
}

func NewSnapshotCommandPartitionAssigner(snapshot CommandPartitionAssignmentSnapshot) *SnapshotCommandPartitionAssigner {
	assigner := &SnapshotCommandPartitionAssigner{}
	assigner.ApplySnapshot(snapshot)
	return assigner
}

func (a *SnapshotCommandPartitionAssigner) ApplySnapshot(snapshot CommandPartitionAssignmentSnapshot) int64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if snapshot.Generation > 0 {
		if a.generation > 0 && snapshot.Generation <= a.generation {
			return a.generation
		}
		a.generation = snapshot.Generation
	} else {
		a.generation++
	}
	if a.generation <= 0 {
		a.generation = 1
	}
	a.assignments = NormalizeCommandPartitionAssignmentOwners(snapshot.Owners)
	return a.generation
}

func (a *SnapshotCommandPartitionAssigner) AssignCommandPartition(ctx context.Context, ownerID string, partition Partition) (CommandPartitionAssignment, bool, error) {
	if err := ctx.Err(); err != nil {
		return CommandPartitionAssignment{}, false, err
	}
	if a == nil {
		return CommandPartitionAssignment{}, false, nil
	}
	partition = partition.Normalize()
	ownerID = strings.TrimSpace(ownerID)
	a.mu.RLock()
	assignedOwner := a.assignments[partition]
	generation := a.generation
	a.mu.RUnlock()
	assignment := NewCommandPartitionAssignment(partition, assignedOwner, generation)
	return assignment, assignment.OwnerID != "" && assignment.OwnerID == ownerID, nil
}

func (a *SnapshotCommandPartitionAssigner) ListAssignedCommandPartitions(ctx context.Context, ownerID string, limit int) ([]CommandPartitionAssignment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil {
		return nil, nil
	}
	ownerID = strings.TrimSpace(ownerID)
	a.mu.RLock()
	generation := a.generation
	assignments := CloneCommandPartitionAssignmentOwners(a.assignments)
	a.mu.RUnlock()
	partitions := make([]Partition, 0, len(assignments))
	for partition, assignedOwner := range assignments {
		if assignedOwner == ownerID {
			partitions = append(partitions, partition.Normalize())
		}
	}
	SortPartitions(partitions)
	if limit > 0 && len(partitions) > limit {
		partitions = partitions[:limit]
	}
	return CommandPartitionAssignmentsForOwner(partitions, ownerID, generation), nil
}

func (a *SnapshotCommandPartitionAssigner) Generation() int64 {
	if a == nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.generation
}

func (a *SnapshotCommandPartitionAssigner) Snapshot() CommandPartitionAssignmentSnapshot {
	if a == nil {
		return CommandPartitionAssignmentSnapshot{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return CommandPartitionAssignmentSnapshot{
		Generation: a.generation,
		Owners:     CloneCommandPartitionAssignmentOwners(a.assignments),
	}
}

func NewHashCommandPartitionAssignerWithOverrides(members []string, overrides map[Partition]string, generation int64) *HashCommandPartitionAssigner {
	if generation <= 0 {
		generation = 1
	}
	members = NormalizeCommandPartitionAssignmentMembers(members)
	return &HashCommandPartitionAssigner{
		members:    members,
		overrides:  NormalizeCommandPartitionAssignmentOverrides(overrides, members),
		generation: generation,
	}
}

func (a *HashCommandPartitionAssigner) AssignCommandPartition(ctx context.Context, ownerID string, partition Partition) (CommandPartitionAssignment, bool, error) {
	if err := ctx.Err(); err != nil {
		return CommandPartitionAssignment{}, false, err
	}
	partition = partition.Normalize()
	ownerID = strings.TrimSpace(ownerID)
	if a == nil {
		return NewCommandPartitionAssignment(partition, ownerID, 1), true, nil
	}
	a.mu.RLock()
	members := append([]string(nil), a.members...)
	overrides := CloneCommandPartitionAssignmentOwners(a.overrides)
	generation := a.generation
	a.mu.RUnlock()
	if len(members) == 0 {
		return NewCommandPartitionAssignment(partition, ownerID, generation), true, nil
	}
	assignedOwner := overrides[partition]
	if assignedOwner == "" {
		assignedOwner = members[CommandPartitionAssignmentIndex(partition, len(members))]
	}
	assignment := NewCommandPartitionAssignment(partition, assignedOwner, generation)
	return assignment, assignment.OwnerID == ownerID, nil
}

func (a *HashCommandPartitionAssigner) SetMembers(members []string) int64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.members = NormalizeCommandPartitionAssignmentMembers(members)
	a.overrides = NormalizeCommandPartitionAssignmentOverrides(a.overrides, a.members)
	a.generation++
	if a.generation <= 0 {
		a.generation = 1
	}
	return a.generation
}

func (a *HashCommandPartitionAssigner) Members() []string {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]string(nil), a.members...)
}

func (a *HashCommandPartitionAssigner) Generation() int64 {
	if a == nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.generation
}

func (a *HashCommandPartitionAssigner) SetOverrides(overrides map[Partition]string) int64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.overrides = NormalizeCommandPartitionAssignmentOverrides(overrides, a.members)
	a.generation++
	if a.generation <= 0 {
		a.generation = 1
	}
	return a.generation
}

func (a *HashCommandPartitionAssigner) Overrides() map[Partition]string {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return CloneCommandPartitionAssignmentOwners(a.overrides)
}
