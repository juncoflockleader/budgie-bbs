package loadtest

import (
	"context"
	"fmt"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
)

type memoryCommandLogPartitionIndex struct {
	mu        sync.Mutex
	tails     map[core.LogPartition]int64
	committed map[core.LogPartition]int64
}

var _ core.CommandLogPartitionIndexer = (*memoryCommandLogPartitionIndex)(nil)

func newIndexedCommandLog(inner core.CommandLog) *core.IndexedCommandLog {
	return core.NewIndexedCommandLog(inner, &memoryCommandLogPartitionIndex{
		tails:     map[core.LogPartition]int64{},
		committed: map[core.LogPartition]int64{},
	})
}

func (i *memoryCommandLogPartitionIndex) RecordCommandPartitionTail(ctx context.Context, partition core.LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if i == nil {
		return fmt.Errorf("memory command log partition index: nil receiver")
	}
	i.recordTail(partition, offset)
	return nil
}

func (i *memoryCommandLogPartitionIndex) RecordCommandPartitionCommit(ctx context.Context, partition core.LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if i == nil {
		return fmt.Errorf("memory command log partition index: nil receiver")
	}
	i.recordCommit(partition, offset)
	return nil
}

func (i *memoryCommandLogPartitionIndex) ListCommandPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	offsets, err := i.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		return nil, err
	}
	return logmodel.CommandPartitionsByTailOffset(offsets, limit), nil
}

func (i *memoryCommandLogPartitionIndex) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]logmodel.CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if i == nil {
		return nil, fmt.Errorf("memory command log partition index: nil receiver")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	offsets := make([]logmodel.CommandPartitionOffset, 0, len(i.tails))
	for partition, tail := range i.tails {
		offsets = append(offsets, logmodel.CommandPartitionOffset{
			Partition:       partition,
			TailOffset:      tail,
			CommittedOffset: i.committed[partition.Normalize()],
		}.Normalize())
	}
	logmodel.SortCommandPartitionOffsetsByLag(offsets)
	if limit > 0 && len(offsets) > limit {
		offsets = offsets[:limit]
	}
	return offsets, nil
}

func (i *memoryCommandLogPartitionIndex) recordTail(partition core.LogPartition, offset int64) {
	partition = partition.Normalize()
	offset = max(offset, 0)
	i.mu.Lock()
	if i.tails == nil {
		i.tails = map[core.LogPartition]int64{}
	}
	if i.committed == nil {
		i.committed = map[core.LogPartition]int64{}
	}
	i.tails[partition] = max(i.tails[partition], offset)
	i.mu.Unlock()
}

func (i *memoryCommandLogPartitionIndex) recordCommit(partition core.LogPartition, offset int64) {
	partition = partition.Normalize()
	offset = max(offset, 0)
	i.mu.Lock()
	if i.tails == nil {
		i.tails = map[core.LogPartition]int64{}
	}
	if i.committed == nil {
		i.committed = map[core.LogPartition]int64{}
	}
	i.committed[partition] = max(i.committed[partition], offset)
	if _, ok := i.tails[partition]; !ok {
		i.tails[partition] = 0
	}
	i.mu.Unlock()
}
