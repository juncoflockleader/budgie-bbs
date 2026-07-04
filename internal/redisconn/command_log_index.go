package redisconn

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

const redisCommandLogMaxScript = `
local current = tonumber(redis.call('HGET', KEYS[1], ARGV[1]) or '0')
local requested = tonumber(ARGV[2]) or 0
if requested > current then
  redis.call('HSET', KEYS[1], ARGV[1], requested)
  return requested
end
return current
`

type CommandLogPartitionIndexOptions struct {
	Prefix string
}

type CommandLogPartitionIndex struct {
	client Commander
	prefix string
}

var _ core.CommandLogPartitionIndexer = (*CommandLogPartitionIndex)(nil)

func NewCommandLogPartitionIndex(client Commander, options CommandLogPartitionIndexOptions) *CommandLogPartitionIndex {
	prefix := strings.TrimSpace(options.Prefix)
	if prefix == "" {
		prefix = "budgie"
	}
	return &CommandLogPartitionIndex{
		client: client,
		prefix: prefix,
	}
}

func (i *CommandLogPartitionIndex) RecordCommandPartitionTail(ctx context.Context, partition core.LogPartition, offset int64) error {
	return i.recordMax(ctx, i.tailKey(), partition, offset)
}

func (i *CommandLogPartitionIndex) RecordCommandPartitionCommit(ctx context.Context, partition core.LogPartition, offset int64) error {
	return i.recordMax(ctx, i.commitKey(), partition, offset)
}

func (i *CommandLogPartitionIndex) ListCommandPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	offsets, err := i.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		return nil, err
	}
	return core.CommandPartitionsByTailOffset(offsets, limit), nil
}

func (i *CommandLogPartitionIndex) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]core.CommandPartitionOffset, error) {
	if i == nil || i.client == nil {
		return nil, fmt.Errorf("redis command log partition index: nil client")
	}
	tails, err := i.readOffsetHash(ctx, i.tailKey())
	if err != nil {
		return nil, err
	}
	commits, err := i.readOffsetHash(ctx, i.commitKey())
	if err != nil {
		return nil, err
	}
	seen := map[core.LogPartition]bool{}
	for partition := range tails {
		seen[partition] = true
	}
	for partition := range commits {
		seen[partition] = true
	}
	offsets := make([]core.CommandPartitionOffset, 0, len(seen))
	for partition := range seen {
		offsets = append(offsets, core.CommandPartitionOffset{
			Partition:       partition,
			TailOffset:      tails[partition],
			CommittedOffset: commits[partition],
		}.Normalize())
	}
	core.SortCommandPartitionOffsetsByLag(offsets)
	if limit > 0 && len(offsets) > limit {
		offsets = offsets[:limit]
	}
	return offsets, nil
}

func (i *CommandLogPartitionIndex) recordMax(ctx context.Context, key string, partition core.LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if i == nil || i.client == nil {
		return fmt.Errorf("redis command log partition index: nil client")
	}
	if offset < 0 {
		offset = 0
	}
	_, err := i.client.Do(ctx, "EVAL", redisCommandLogMaxScript, 1, key, encodeCommandPartitionField(partition), offset)
	return err
}

func (i *CommandLogPartitionIndex) readOffsetHash(ctx context.Context, key string) (map[core.LogPartition]int64, error) {
	reply, err := i.client.Do(ctx, "HGETALL", key)
	if err != nil {
		return nil, err
	}
	values, ok := reply.([]any)
	if !ok && reply != nil {
		return nil, fmt.Errorf("redis command log partition index: HGETALL %s returned %T", key, reply)
	}
	out := map[core.LogPartition]int64{}
	for j := 0; j+1 < len(values); j += 2 {
		partition, err := decodeCommandPartitionField(redisString(values[j]))
		if err != nil {
			return nil, err
		}
		offset, err := strconv.ParseInt(redisString(values[j+1]), 10, 64)
		if err != nil {
			return nil, err
		}
		out[partition.Normalize()] = offset
	}
	return out, nil
}

func (i *CommandLogPartitionIndex) tailKey() string {
	return i.prefix + ":command-log:tail"
}

func (i *CommandLogPartitionIndex) commitKey() string {
	return i.prefix + ":command-log:commit"
}

func encodeCommandPartitionField(partition core.LogPartition) string {
	partition = partition.Normalize()
	return base64.RawURLEncoding.EncodeToString([]byte(partition.Kind)) + ":" +
		base64.RawURLEncoding.EncodeToString([]byte(partition.Key))
}

func decodeCommandPartitionField(field string) (core.LogPartition, error) {
	left, right, ok := strings.Cut(field, ":")
	if !ok {
		return core.LogPartition{}, fmt.Errorf("redis command log partition index: invalid partition field %q", field)
	}
	kind, err := base64.RawURLEncoding.DecodeString(left)
	if err != nil {
		return core.LogPartition{}, err
	}
	key, err := base64.RawURLEncoding.DecodeString(right)
	if err != nil {
		return core.LogPartition{}, err
	}
	return core.LogPartition{Kind: string(kind), Key: string(key)}.Normalize(), nil
}

func redisString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}
