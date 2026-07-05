package core

import (
	"context"
	"hash/fnv"
	"sort"
	"strings"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
)

// HashCommandPartitionAssigner is a deterministic stand-in for broker
// consumer-group assignment. It lets tests and experimental writer nodes model
// partition ownership and rebalances without a separate lease table.
type HashCommandPartitionAssigner struct {
	mu         sync.RWMutex
	members    []string
	overrides  map[LogPartition]string
	generation int64
}

type CommandPartitionAssignmentSnapshot = logmodel.CommandPartitionAssignmentSnapshot

// SnapshotCommandPartitionAssigner applies broker-style assignment snapshots.
// Unlike the hash assigner, missing partitions are unassigned. This mirrors
// consumer-group rebalances where a writer must not drain a partition unless it
// appears in the current assigned set.
type SnapshotCommandPartitionAssigner struct {
	mu          sync.RWMutex
	assignments map[LogPartition]string
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
	a.assignments = logmodel.NormalizeCommandPartitionAssignmentOwners(snapshot.Owners)
	return a.generation
}

func (a *SnapshotCommandPartitionAssigner) AssignCommandPartition(ctx context.Context, ownerID string, partition LogPartition) (CommandPartitionAssignment, bool, error) {
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
	assignment := commandPartitionAssignmentForOwner(partition, assignedOwner, generation)
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
	assignments := cloneCommandPartitionAssignmentOverrides(a.assignments)
	a.mu.RUnlock()
	partitions := make([]LogPartition, 0, len(assignments))
	for partition, assignedOwner := range assignments {
		if assignedOwner == ownerID {
			partitions = append(partitions, partition.Normalize())
		}
	}
	SortLogPartitions(partitions)
	if limit > 0 && len(partitions) > limit {
		partitions = partitions[:limit]
	}
	return commandPartitionAssignmentsForOwner(partitions, ownerID, generation), nil
}

func commandPartitionAssignmentPartitions(assignments []CommandPartitionAssignment) []LogPartition {
	partitions := make([]LogPartition, 0, len(assignments))
	seen := map[LogPartition]bool{}
	for _, assignment := range assignments {
		partition := assignment.Partition.Normalize()
		if seen[partition] {
			continue
		}
		seen[partition] = true
		partitions = append(partitions, partition)
	}
	return partitions
}

func commandPartitionAssignmentsForOwner(partitions []LogPartition, ownerID string, generation int64) []CommandPartitionAssignment {
	assignments := make([]CommandPartitionAssignment, 0, len(partitions))
	for _, partition := range partitions {
		assignments = append(assignments, commandPartitionAssignmentForOwner(partition, ownerID, generation))
	}
	return assignments
}

func commandPartitionAssignmentForOwner(partition LogPartition, ownerID string, generation int64) CommandPartitionAssignment {
	return logmodel.NewCommandPartitionAssignment(partition, ownerID, generation)
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
		Owners:     cloneCommandPartitionAssignmentOverrides(a.assignments),
	}
}

func NewHashCommandPartitionAssignerWithOverrides(members []string, overrides map[LogPartition]string, generation int64) *HashCommandPartitionAssigner {
	if generation <= 0 {
		generation = 1
	}
	members = normalizeCommandPartitionAssignmentMembers(members)
	return &HashCommandPartitionAssigner{
		members:    members,
		overrides:  normalizeCommandPartitionAssignmentOverrides(overrides, members),
		generation: generation,
	}
}

func (a *HashCommandPartitionAssigner) AssignCommandPartition(ctx context.Context, ownerID string, partition LogPartition) (CommandPartitionAssignment, bool, error) {
	if err := ctx.Err(); err != nil {
		return CommandPartitionAssignment{}, false, err
	}
	partition = partition.Normalize()
	ownerID = strings.TrimSpace(ownerID)
	if a == nil {
		return commandPartitionAssignmentForOwner(partition, ownerID, 1), true, nil
	}
	a.mu.RLock()
	members := append([]string(nil), a.members...)
	overrides := cloneCommandPartitionAssignmentOverrides(a.overrides)
	generation := a.generation
	a.mu.RUnlock()
	if len(members) == 0 {
		return commandPartitionAssignmentForOwner(partition, ownerID, generation), true, nil
	}
	assignedOwner := overrides[partition]
	if assignedOwner == "" {
		assignedOwner = members[commandPartitionAssignmentIndex(partition, len(members))]
	}
	assignment := commandPartitionAssignmentForOwner(partition, assignedOwner, generation)
	return assignment, assignment.OwnerID == ownerID, nil
}

func (a *HashCommandPartitionAssigner) SetMembers(members []string) int64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.members = normalizeCommandPartitionAssignmentMembers(members)
	a.overrides = normalizeCommandPartitionAssignmentOverrides(a.overrides, a.members)
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

func (a *HashCommandPartitionAssigner) SetOverrides(overrides map[LogPartition]string) int64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.overrides = normalizeCommandPartitionAssignmentOverrides(overrides, a.members)
	a.generation++
	if a.generation <= 0 {
		a.generation = 1
	}
	return a.generation
}

func (a *HashCommandPartitionAssigner) Overrides() map[LogPartition]string {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneCommandPartitionAssignmentOverrides(a.overrides)
}

func normalizeCommandPartitionAssignmentMembers(members []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(members))
	for _, member := range members {
		member = strings.TrimSpace(member)
		if member == "" || seen[member] {
			continue
		}
		seen[member] = true
		out = append(out, member)
	}
	sort.Strings(out)
	return out
}

func normalizeCommandPartitionAssignmentOverrides(overrides map[LogPartition]string, members []string) map[LogPartition]string {
	memberSet := map[string]bool{}
	for _, member := range normalizeCommandPartitionAssignmentMembers(members) {
		memberSet[member] = true
	}
	out := map[LogPartition]string{}
	for partition, ownerID := range overrides {
		partition = partition.Normalize()
		ownerID = strings.TrimSpace(ownerID)
		if partition.Kind == "" || partition.Key == "" || ownerID == "" {
			continue
		}
		if len(memberSet) > 0 && !memberSet[ownerID] {
			continue
		}
		out[partition] = ownerID
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneCommandPartitionAssignmentOverrides(overrides map[LogPartition]string) map[LogPartition]string {
	if len(overrides) == 0 {
		return nil
	}
	out := make(map[LogPartition]string, len(overrides))
	for partition, ownerID := range overrides {
		out[partition.Normalize()] = ownerID
	}
	return out
}

func commandPartitionAssignmentIndex(partition LogPartition, memberCount int) int {
	if memberCount <= 1 {
		return 0
	}
	partition = partition.Normalize()
	h := fnv.New64a()
	_, _ = h.Write([]byte(partition.Kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(partition.Key))
	return int(h.Sum64() % uint64(memberCount))
}
