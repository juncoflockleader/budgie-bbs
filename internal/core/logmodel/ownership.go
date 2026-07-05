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
