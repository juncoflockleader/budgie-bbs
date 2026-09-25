package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
)

// SwitchableCommandLog lets setup code use a lightweight producer for command
// submission and then switch drain/commit operations to the broker member that
// owns command consumption.
type SwitchableCommandLog struct {
	mu      sync.RWMutex
	produce CommandLog
	drain   CommandLog
}

var _ CommandLog = (*SwitchableCommandLog)(nil)
var _ CommandPartitionLister = (*SwitchableCommandLog)(nil)
var _ CommandPartitionOffsetLister = (*SwitchableCommandLog)(nil)
var _ CommandLogRebalanceAllower = (*SwitchableCommandLog)(nil)
var _ CommandLogCommitRecorder = (*SwitchableCommandLog)(nil)

func NewSwitchableCommandLog(produce CommandLog) *SwitchableCommandLog {
	return &SwitchableCommandLog{
		produce: produce,
		drain:   produce,
	}
}

func (l *SwitchableCommandLog) SetDrainLog(drain CommandLog) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.drain = drain
	l.mu.Unlock()
}

func (l *SwitchableCommandLog) Produce(ctx context.Context, record CommandLogRecord) (CommandLogRecord, error) {
	produce := l.produceLog()
	if produce == nil {
		return CommandLogRecord{}, fmt.Errorf("switchable command log: nil producer log")
	}
	return produce.Produce(ctx, record)
}

func (l *SwitchableCommandLog) FetchPartition(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]CommandLogRecord, error) {
	drain := l.drainLog()
	if drain == nil {
		return nil, fmt.Errorf("switchable command log: nil drain log")
	}
	return drain.FetchPartition(ctx, partition, afterOffset, limit)
}

func (l *SwitchableCommandLog) CommitPartition(ctx context.Context, partition LogPartition, offset int64) error {
	drain := l.drainLog()
	if drain == nil {
		return fmt.Errorf("switchable command log: nil drain log")
	}
	return drain.CommitPartition(ctx, partition, offset)
}

func (l *SwitchableCommandLog) RecordCommandLogCommit(ctx context.Context, partition LogPartition, offset int64) error {
	drain := l.drainLog()
	if recorder, ok := drain.(CommandLogCommitRecorder); ok {
		return recorder.RecordCommandLogCommit(ctx, partition, offset)
	}
	return nil
}

func (l *SwitchableCommandLog) CommittedOffset(ctx context.Context, partition LogPartition) (int64, error) {
	drain := l.drainLog()
	if drain == nil {
		return 0, fmt.Errorf("switchable command log: nil drain log")
	}
	return drain.CommittedOffset(ctx, partition)
}

func (l *SwitchableCommandLog) ListCommandPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	log := l.drainLog()
	if lister, ok := log.(CommandPartitionLister); ok {
		return lister.ListCommandPartitions(ctx, limit)
	}
	log = l.produceLog()
	if lister, ok := log.(CommandPartitionLister); ok {
		return lister.ListCommandPartitions(ctx, limit)
	}
	return nil, fmt.Errorf("switchable command log: partition listing unavailable")
}

func (l *SwitchableCommandLog) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]logmodel.CommandPartitionOffset, error) {
	log := l.drainLog()
	if lister, ok := log.(CommandPartitionOffsetLister); ok {
		return lister.ListCommandPartitionOffsets(ctx, limit)
	}
	log = l.produceLog()
	if lister, ok := log.(CommandPartitionOffsetLister); ok {
		return lister.ListCommandPartitionOffsets(ctx, limit)
	}
	return nil, fmt.Errorf("switchable command log: partition offset listing unavailable")
}

func (l *SwitchableCommandLog) AllowCommandLogRebalance() {
	log := l.drainLog()
	if allower, ok := log.(CommandLogRebalanceAllower); ok {
		allower.AllowCommandLogRebalance()
	}
}

func (l *SwitchableCommandLog) produceLog() CommandLog {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.produce
}

func (l *SwitchableCommandLog) drainLog() CommandLog {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.drain
}
