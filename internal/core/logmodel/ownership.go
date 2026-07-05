package logmodel

import "strings"

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
