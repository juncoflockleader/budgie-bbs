package kafkaconn

import (
	"context"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

type TopicPartitionAssignment struct {
	Topic     string
	Partition int32
}

type CommandPartitionAssignmentOptions struct {
	CommandTopic   string
	OwnerID        string
	Generation     int64
	PartitionCount int32
}

type CommandPartitionAssignmentSnapshotApplier interface {
	ApplySnapshot(core.CommandPartitionAssignmentSnapshot) int64
}

// CommandPartitionRebalanceAdapter applies Kafka/Redpanda consumer-group
// assignment callbacks to the core snapshot assigner used by command-log
// workers. It stays dependency-free so the broker client can be introduced
// later without changing the worker ownership contract.
type CommandPartitionRebalanceAdapter struct {
	target  CommandPartitionAssignmentSnapshotApplier
	options CommandPartitionAssignmentOptions
}

func NewCommandPartitionRebalanceAdapter(target CommandPartitionAssignmentSnapshotApplier, options CommandPartitionAssignmentOptions) *CommandPartitionRebalanceAdapter {
	return &CommandPartitionRebalanceAdapter{
		target:  target,
		options: options,
	}
}

func (a *CommandPartitionRebalanceAdapter) ApplyConsumerGroupAssignment(ctx context.Context, generation int64, owned []TopicPartitionAssignment, candidates []core.LogPartition) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a == nil || a.target == nil {
		return 0, fmt.Errorf("kafka command partition assignment: nil rebalance target")
	}
	if generation <= 0 {
		return 0, fmt.Errorf("kafka command partition assignment: rebalance generation is required")
	}
	options := a.options
	options.Generation = generation
	snapshot, err := CommandPartitionAssignmentSnapshotForOwnedKafkaPartitions(options, owned, candidates)
	if err != nil {
		return 0, err
	}
	return a.target.ApplySnapshot(snapshot), nil
}

func (a *CommandPartitionRebalanceAdapter) RevokeConsumerGroupAssignment(ctx context.Context, generation int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a == nil || a.target == nil {
		return 0, fmt.Errorf("kafka command partition assignment: nil rebalance target")
	}
	if generation <= 0 {
		return 0, fmt.Errorf("kafka command partition assignment: rebalance generation is required")
	}
	return a.target.ApplySnapshot(core.CommandPartitionAssignmentSnapshot{
		Generation: generation,
		Owners:     map[core.LogPartition]string{},
	}), nil
}

// CommandPartitionAssignmentSnapshotForOwnedKafkaPartitions converts a Kafka
// consumer-group assignment into the core snapshot assigner shape. Kafka owns
// physical topic partitions; Budgie workers drain logical command partitions
// whose deterministic key maps to one of those physical partitions.
func CommandPartitionAssignmentSnapshotForOwnedKafkaPartitions(options CommandPartitionAssignmentOptions, owned []TopicPartitionAssignment, candidates []core.LogPartition) (core.CommandPartitionAssignmentSnapshot, error) {
	options.CommandTopic = strings.TrimSpace(options.CommandTopic)
	if options.CommandTopic == "" {
		options.CommandTopic = DefaultCommandTopic
	}
	options.OwnerID = strings.TrimSpace(options.OwnerID)
	if options.OwnerID == "" {
		return core.CommandPartitionAssignmentSnapshot{}, fmt.Errorf("kafka command partition assignment: owner id is required")
	}
	if options.PartitionCount <= 0 {
		return core.CommandPartitionAssignmentSnapshot{}, fmt.Errorf("kafka command partition assignment: partition count is required")
	}
	ownedPartitions := map[int32]bool{}
	for _, assignment := range owned {
		if strings.TrimSpace(assignment.Topic) != options.CommandTopic {
			continue
		}
		if assignment.Partition < 0 || assignment.Partition >= options.PartitionCount {
			return core.CommandPartitionAssignmentSnapshot{}, fmt.Errorf("kafka command partition assignment: physical partition %d outside [0,%d)", assignment.Partition, options.PartitionCount)
		}
		ownedPartitions[assignment.Partition] = true
	}
	owners := map[core.LogPartition]string{}
	for _, candidate := range candidates {
		candidate = candidate.Normalize()
		physical, err := KafkaPartitionForLogicalPartition(candidate, options.PartitionCount)
		if err != nil {
			return core.CommandPartitionAssignmentSnapshot{}, err
		}
		if ownedPartitions[physical] {
			owners[candidate] = options.OwnerID
		}
	}
	return core.CommandPartitionAssignmentSnapshot{
		Generation: options.Generation,
		Owners:     owners,
	}, nil
}

func KafkaPartitionForLogicalPartition(partition core.LogPartition, partitionCount int32) (int32, error) {
	return KafkaPartitionForKey(LogicalPartitionKey(partition), partitionCount)
}

func KafkaPartitionForKey(key string, partitionCount int32) (int32, error) {
	if partitionCount <= 0 {
		return 0, fmt.Errorf("kafka partition: partition count is required")
	}
	hash := kafkaMurmur2([]byte(key))
	return int32(uint32(hash)&0x7fffffff) % partitionCount, nil
}

func kafkaMurmur2(data []byte) int32 {
	const (
		seed = uint32(0x9747b28c)
		m    = uint32(0x5bd1e995)
		r    = uint(24)
	)
	length := len(data)
	h := seed ^ uint32(length)
	offset := 0
	for length >= 4 {
		k := uint32(data[offset+0]&0xff) |
			uint32(data[offset+1]&0xff)<<8 |
			uint32(data[offset+2]&0xff)<<16 |
			uint32(data[offset+3]&0xff)<<24
		k *= m
		k ^= k >> r
		k *= m
		h *= m
		h ^= k
		offset += 4
		length -= 4
	}
	switch length {
	case 3:
		h ^= uint32(data[offset+2]&0xff) << 16
		fallthrough
	case 2:
		h ^= uint32(data[offset+1]&0xff) << 8
		fallthrough
	case 1:
		h ^= uint32(data[offset] & 0xff)
		h *= m
	}
	h ^= h >> 13
	h *= m
	h ^= h >> 15
	return int32(h)
}
