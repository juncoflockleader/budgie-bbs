package main

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

type indexedCommandLog struct {
	inner     core.CommandLog
	mu        sync.Mutex
	tails     map[core.LogPartition]int64
	committed map[core.LogPartition]int64
}

var _ core.CommandLog = (*indexedCommandLog)(nil)
var _ core.CommandPartitionLister = (*indexedCommandLog)(nil)
var _ core.CommandPartitionOffsetLister = (*indexedCommandLog)(nil)
var _ core.CommandLogCommitRecorder = (*indexedCommandLog)(nil)

func newIndexedCommandLog(inner core.CommandLog) *indexedCommandLog {
	return &indexedCommandLog{
		inner:     inner,
		tails:     map[core.LogPartition]int64{},
		committed: map[core.LogPartition]int64{},
	}
}

func (l *indexedCommandLog) Produce(ctx context.Context, record core.CommandLogRecord) (core.CommandLogRecord, error) {
	if l == nil || l.inner == nil {
		return core.CommandLogRecord{}, fmt.Errorf("indexed command log: nil inner log")
	}
	produced, err := l.inner.Produce(ctx, record)
	if err != nil {
		return core.CommandLogRecord{}, err
	}
	l.recordTail(produced.Partition, produced.Offset)
	return produced, nil
}

func (l *indexedCommandLog) FetchPartition(ctx context.Context, partition core.LogPartition, afterOffset int64, limit int) ([]core.CommandLogRecord, error) {
	if l == nil || l.inner == nil {
		return nil, fmt.Errorf("indexed command log: nil inner log")
	}
	records, err := l.inner.FetchPartition(ctx, partition, afterOffset, limit)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		l.recordTail(record.Partition, record.Offset)
	}
	return records, nil
}

func (l *indexedCommandLog) CommitPartition(ctx context.Context, partition core.LogPartition, offset int64) error {
	if l == nil || l.inner == nil {
		return fmt.Errorf("indexed command log: nil inner log")
	}
	if err := l.inner.CommitPartition(ctx, partition, offset); err != nil {
		return err
	}
	l.recordCommit(partition, offset)
	return nil
}

func (l *indexedCommandLog) RecordCommandLogCommit(ctx context.Context, partition core.LogPartition, offset int64) error {
	if l == nil {
		return fmt.Errorf("indexed command log: nil receiver")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if recorder, ok := l.inner.(core.CommandLogCommitRecorder); ok {
		if err := recorder.RecordCommandLogCommit(ctx, partition, offset); err != nil {
			return err
		}
	}
	l.recordCommit(partition, offset)
	return nil
}

func (l *indexedCommandLog) CommittedOffset(ctx context.Context, partition core.LogPartition) (int64, error) {
	if l == nil || l.inner == nil {
		return 0, fmt.Errorf("indexed command log: nil inner log")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	partition = partition.Normalize()
	l.mu.Lock()
	defer l.mu.Unlock()
	offset := l.committed[partition]
	tail := l.tails[partition]
	if offset > tail {
		offset = tail
	}
	if offset < 0 {
		offset = 0
	}
	return offset, nil
}

func (l *indexedCommandLog) ListCommandPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, fmt.Errorf("indexed command log: nil receiver")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	partitions := make([]core.LogPartition, 0, len(l.tails))
	for partition := range l.tails {
		partitions = append(partitions, partition.Normalize())
	}
	sort.Slice(partitions, func(i, j int) bool {
		if l.tails[partitions[i]] == l.tails[partitions[j]] {
			if partitions[i].Kind == partitions[j].Kind {
				return partitions[i].Key < partitions[j].Key
			}
			return partitions[i].Kind < partitions[j].Kind
		}
		return l.tails[partitions[i]] > l.tails[partitions[j]]
	})
	if limit > 0 && len(partitions) > limit {
		partitions = partitions[:limit]
	}
	return partitions, nil
}

func (l *indexedCommandLog) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]core.CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, fmt.Errorf("indexed command log: nil receiver")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	offsets := make([]core.CommandPartitionOffset, 0, len(l.tails))
	for partition, tail := range l.tails {
		partition = partition.Normalize()
		committed := l.committed[partition]
		if committed > tail {
			committed = tail
		}
		offsets = append(offsets, core.CommandPartitionOffset{
			Partition:       partition,
			TailOffset:      tail,
			CommittedOffset: committed,
		})
	}
	sort.Slice(offsets, func(i, j int) bool {
		li := offsets[i].TailOffset - offsets[i].CommittedOffset
		lj := offsets[j].TailOffset - offsets[j].CommittedOffset
		if li == lj {
			if offsets[i].TailOffset == offsets[j].TailOffset {
				if offsets[i].Partition.Kind == offsets[j].Partition.Kind {
					return offsets[i].Partition.Key < offsets[j].Partition.Key
				}
				return offsets[i].Partition.Kind < offsets[j].Partition.Kind
			}
			return offsets[i].TailOffset > offsets[j].TailOffset
		}
		return li > lj
	})
	if limit > 0 && len(offsets) > limit {
		offsets = offsets[:limit]
	}
	return offsets, nil
}

func (l *indexedCommandLog) recordTail(partition core.LogPartition, offset int64) {
	partition = partition.Normalize()
	if offset <= 0 {
		return
	}
	l.mu.Lock()
	if offset > l.tails[partition] {
		l.tails[partition] = offset
	}
	l.mu.Unlock()
}

func (l *indexedCommandLog) recordCommit(partition core.LogPartition, offset int64) {
	partition = partition.Normalize()
	if offset < 0 {
		offset = 0
	}
	l.mu.Lock()
	if offset > l.committed[partition] {
		l.committed[partition] = offset
	}
	l.mu.Unlock()
}
