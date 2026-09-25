package logmodel

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
)

const hotThreadSplitPartitionSuffix = "#reply-"

func NormalizeHotThreadSplits(splits map[string]int) map[string]int {
	out := map[string]int{}
	for threadID, shards := range splits {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" || shards <= 1 {
			continue
		}
		out[threadID] = shards
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func MergeHotThreadSplits(persisted, overrides map[string]int) map[string]int {
	out := map[string]int{}
	for threadID, shards := range NormalizeHotThreadSplits(persisted) {
		out[threadID] = shards
	}
	for threadID, shards := range NormalizeHotThreadSplits(overrides) {
		out[threadID] = shards
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func HotThreadSplitPartitionSet(threadID string, currentShards, nextShards int) map[Partition]struct{} {
	out := map[Partition]struct{}{
		{Kind: PartitionThread, Key: threadID}: {},
	}
	for shard := 0; shard < currentShards; shard++ {
		out[Partition{Kind: PartitionThread, Key: HotThreadSplitPartitionKey(threadID, shard)}] = struct{}{}
	}
	for shard := 0; shard < nextShards; shard++ {
		out[Partition{Kind: PartitionThread, Key: HotThreadSplitPartitionKey(threadID, shard)}] = struct{}{}
	}
	return out
}

func HotThreadSplitPartitionKey(threadID string, shard int) string {
	return fmt.Sprintf("%s%s%d", threadID, hotThreadSplitPartitionSuffix, shard)
}

func HotThreadSplitPartitionThread(partitionKey string) (string, bool) {
	base, shardText, ok := strings.Cut(partitionKey, hotThreadSplitPartitionSuffix)
	if !ok || base == "" || shardText == "" {
		return "", false
	}
	shard, err := strconv.Atoi(shardText)
	if err != nil || shard < 0 {
		return "", false
	}
	return base, true
}

func HotThreadReplyShard(actorID string, payload []byte, shards int) int {
	if shards <= 1 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(actorID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload)
	return int(h.Sum64() % uint64(shards))
}
