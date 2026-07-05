package commandexec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash/fnv"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const GlobalPartition = logmodel.PartitionGlobal

const (
	partitionAdvisoryLockNamespace = "budgie:partition-writer:"
	partitionAdvisoryLockHighBits  = uint64(0x4000000000000000)
	partitionAdvisoryLockLowMask   = uint64(0x3fffffffffffffff)
)

func HashCommand(name proto.CommandName, payload json.RawMessage) string {
	sum := sha256.Sum256(append([]byte(name+"\x00"), payload...))
	return hex.EncodeToString(sum[:])
}

func NormalizePartition(partition Partition) Partition {
	partition.Kind, partition.Key = NormalizePartitionFields(partition.Kind, partition.Key)
	return partition
}

func NormalizePartitionFields(kind, key string) (string, string) {
	return logmodel.NormalizePartitionFields(kind, key)
}

func PartitionLaneIndex(partition Partition, lanes int) int {
	if lanes <= 1 {
		return 0
	}
	partition = NormalizePartition(partition)
	h := fnv.New64a()
	_, _ = h.Write([]byte(partition.Kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(partition.Key))
	return int(h.Sum64() % uint64(lanes))
}

func PartitionAdvisoryLockKey(partition Partition) int64 {
	partition = NormalizePartition(partition)
	h := fnv.New64a()
	_, _ = h.Write([]byte(partitionAdvisoryLockNamespace))
	_, _ = h.Write([]byte(partition.Kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(partition.Key))
	return int64(partitionAdvisoryLockHighBits | (h.Sum64() & partitionAdvisoryLockLowMask))
}

func MailboxAdvisoryLockKey(mailbox string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("mailbox:" + mailbox))
	return int64(h.Sum64())
}
