package logmodel

import (
	"hash/fnv"
	"sort"
	"strings"
)

type CommandPartitionClaim struct {
	Partition Partition
	OwnerID   string
	ExpiresAt int64
}

func NewCommandPartitionClaim(partition Partition, ownerID string, expiresAt int64) CommandPartitionClaim {
	return CommandPartitionClaim{
		Partition: partition.Normalize(),
		OwnerID:   strings.TrimSpace(ownerID),
		ExpiresAt: expiresAt,
	}
}

type CommandPartitionAssignment struct {
	Partition  Partition
	OwnerID    string
	Generation int64
}

func NewCommandPartitionAssignment(partition Partition, ownerID string, generation int64) CommandPartitionAssignment {
	return CommandPartitionAssignment{
		Partition:  partition.Normalize(),
		OwnerID:    strings.TrimSpace(ownerID),
		Generation: generation,
	}
}

func CommandPartitionAssignmentPartitions(assignments []CommandPartitionAssignment) []Partition {
	partitions := make([]Partition, 0, len(assignments))
	seen := map[Partition]bool{}
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

func CommandPartitionAssignmentsForOwner(partitions []Partition, ownerID string, generation int64) []CommandPartitionAssignment {
	assignments := make([]CommandPartitionAssignment, 0, len(partitions))
	for _, partition := range partitions {
		assignments = append(assignments, NewCommandPartitionAssignment(partition, ownerID, generation))
	}
	return assignments
}

// CommandPartitionAssignmentSnapshot is the in-memory shape a native broker
// consumer-group adapter can publish after a rebalance. Owners maps logical
// command partitions to the writer that owns them for Generation.
type CommandPartitionAssignmentSnapshot struct {
	Generation int64
	Owners     map[Partition]string
}

func NormalizeCommandPartitionAssignmentOwners(assignments map[Partition]string) map[Partition]string {
	out := map[Partition]string{}
	for partition, ownerID := range assignments {
		partition = partition.Normalize()
		ownerID = strings.TrimSpace(ownerID)
		if partition.Kind == "" || partition.Key == "" || ownerID == "" {
			continue
		}
		out[partition] = ownerID
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func NormalizeCommandPartitionAssignmentMembers(members []string) []string {
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

func NormalizeCommandPartitionAssignmentOverrides(overrides map[Partition]string, members []string) map[Partition]string {
	memberSet := map[string]bool{}
	for _, member := range NormalizeCommandPartitionAssignmentMembers(members) {
		memberSet[member] = true
	}
	out := map[Partition]string{}
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

func CloneCommandPartitionAssignmentOwners(overrides map[Partition]string) map[Partition]string {
	if len(overrides) == 0 {
		return nil
	}
	out := make(map[Partition]string, len(overrides))
	for partition, ownerID := range overrides {
		out[partition.Normalize()] = ownerID
	}
	return out
}

func CommandPartitionAssignmentIndex(partition Partition, memberCount int) int {
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
