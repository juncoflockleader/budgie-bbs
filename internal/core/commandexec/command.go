package commandexec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash/fnv"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const GlobalPartition = "global"

func HashCommand(name proto.CommandName, payload json.RawMessage) string {
	sum := sha256.Sum256(append([]byte(name+"\x00"), payload...))
	return hex.EncodeToString(sum[:])
}

func NormalizePartition(partition Partition) Partition {
	partition.Kind, partition.Key = NormalizePartitionFields(partition.Kind, partition.Key)
	return partition
}

func NormalizePartitionFields(kind, key string) (string, string) {
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	if kind == "" {
		kind = GlobalPartition
	}
	if key == "" {
		key = GlobalPartition
	}
	return kind, key
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
