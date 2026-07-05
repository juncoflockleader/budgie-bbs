package core

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/processorloop"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const defaultDerivedRebuildBatchSize = 500

type processorRunProgress = processorloop.Progress

type periodicProcessor struct {
	Core      *Core
	BatchSize int
	Interval  time.Duration

	runner processorloop.Runner
}

type derivedRebuildProcessResult struct {
	FromSeq    int64
	AppliedSeq int64
	HeadSeq    int64
	Events     int
	Rebuilt    bool
	Rows       int64
}

type derivedRebuildProcessor struct {
	periodicProcessor
	process func(*Core, int) (derivedRebuildProcessResult, error)
}

type derivedRebuildSpec struct {
	view     string
	rebuild  func(*sql.Tx) (int64, error)
	rowCount func(*sql.DB) (int, error)
}

type derivedViewEventBatch struct {
	View       string
	FromSeq    int64
	AppliedSeq int64
	HeadSeq    int64
	Found      bool
	Events     []*proto.Event
}

func newPeriodicProcessor(c *Core, name string, interval time.Duration, batchSize int, process func(context.Context, *Core, int) (processorRunProgress, error)) (periodicProcessor, error) {
	if c == nil {
		return periodicProcessor{}, nilProcessorError(name)
	}
	if interval <= 0 {
		interval = time.Second
	}
	if batchSize <= 0 {
		batchSize = defaultDerivedRebuildBatchSize
	}
	return periodicProcessor{
		Core:      c,
		BatchSize: batchSize,
		Interval:  interval,
		runner: processorloop.Runner{
			Name:      name,
			BatchSize: batchSize,
			Interval:  interval,
			Process: func(ctx context.Context, batchSize int) (processorRunProgress, error) {
				return process(ctx, c, batchSize)
			},
		},
	}, nil
}

func newCorePeriodicProcessor[T any](c *Core, name string, interval time.Duration, batchSize int, process func(*Core, int) (T, error), progress func(T) processorRunProgress) (periodicProcessor, error) {
	return newPeriodicProcessor(c, name, interval, batchSize, func(_ context.Context, c *Core, batchSize int) (processorRunProgress, error) {
		result, err := process(c, batchSize)
		return progress(result), err
	})
}

func nilProcessorError(name string) error {
	return fmt.Errorf("%s processor: nil core", name)
}

func (p *periodicProcessor) Run(ctx context.Context) {
	if p == nil || p.Core == nil {
		return
	}
	p.runner.Run(ctx)
}

func newDerivedRebuildProcessor(c *Core, name string, interval time.Duration, batchSize int, process func(*Core, int) (derivedRebuildProcessResult, error)) (derivedRebuildProcessor, error) {
	processor, err := newPeriodicProcessor(c, name, interval, batchSize, func(_ context.Context, c *Core, batchSize int) (processorRunProgress, error) {
		result, err := process(c, batchSize)
		return derivedRebuildRunProgress(result), err
	})
	if err != nil {
		return derivedRebuildProcessor{}, err
	}
	return derivedRebuildProcessor{
		periodicProcessor: processor,
		process:           process,
	}, nil
}

func nilDerivedRebuildProcessResult(name string) (derivedRebuildProcessResult, error) {
	return derivedRebuildProcessResult{}, nilProcessorError(name)
}

func (p *derivedRebuildProcessor) ProcessOnce() (derivedRebuildProcessResult, error) {
	if p == nil || p.Core == nil || p.process == nil {
		name := "derived rebuild"
		if p != nil && p.runner.Name != "" {
			name = p.runner.Name
		}
		return nilDerivedRebuildProcessResult(name)
	}
	return p.process(p.Core, p.BatchSize)
}

func (p *derivedRebuildProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.periodicProcessor.Run(ctx)
}

func derivedRebuildRunProgress(result derivedRebuildProcessResult) processorRunProgress {
	return processorRunProgress{
		FromSeq:    result.FromSeq,
		AppliedSeq: result.AppliedSeq,
		HeadSeq:    result.HeadSeq,
		Events:     result.Events,
		Log:        result.Events > 0 || result.Rebuilt || result.AppliedSeq < result.HeadSeq,
		Extra:      []any{"rebuilt", result.Rebuilt, "rows", result.Rows},
	}
}

func (c *Core) replayDerivedViewEventBatch(view string, batchSize int) (derivedViewEventBatch, error) {
	if batchSize <= 0 {
		batchSize = defaultDerivedRebuildBatchSize
	}
	fromSeq, found, err := projections.LookupProjectionWatermarkAppliedSeq(c.DB, view)
	if err != nil {
		return derivedViewEventBatch{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return derivedViewEventBatch{}, err
	}
	batch := derivedViewEventBatch{
		View:       view,
		FromSeq:    fromSeq,
		AppliedSeq: fromSeq,
		HeadSeq:    head,
		Found:      found,
	}
	events, err := c.Replay(fromSeq, nil, batchSize)
	batch.Events = events
	return batch, err
}

func (c *Core) finishEmptyDerivedViewEventBatch(batch derivedViewEventBatch) error {
	if len(batch.Events) > 0 || batch.Found {
		return nil
	}
	return c.RecordDerivedViewApplied(batch.View, batch.FromSeq)
}

func (c *Core) applyDerivedViewEventBatchTx(batch derivedViewEventBatch, errPrefix string, apply func(*sql.Tx, *proto.Event) (bool, error)) (events, changed int, appliedSeq int64, err error) {
	appliedSeq = batch.AppliedSeq
	tx, err := c.DB.Begin()
	if err != nil {
		return 0, 0, appliedSeq, err
	}
	defer tx.Rollback() //nolint

	for _, evt := range batch.Events {
		if evt == nil {
			continue
		}
		eventChanged, err := apply(tx, evt)
		if err != nil {
			return events, changed, appliedSeq, fmt.Errorf("%s event %d (%s): %w", errPrefix, evt.Seq, evt.Kind, err)
		}
		events++
		if eventChanged {
			changed++
		}
		if evt.Seq > appliedSeq {
			appliedSeq = evt.Seq
		}
	}
	if err := recordDerivedViewAppliedTx(tx, batch.View, appliedSeq); err != nil {
		return events, changed, appliedSeq, err
	}
	if err := tx.Commit(); err != nil {
		return events, changed, appliedSeq, err
	}
	return events, changed, appliedSeq, nil
}

func derivedViewEventPredicate(predicate func(*proto.Event) bool) func(*sql.Tx, *proto.Event) (bool, error) {
	return func(_ *sql.Tx, evt *proto.Event) (bool, error) {
		return predicate(evt), nil
	}
}

func (c *Core) processDerivedRebuildOnce(batchSize int, spec derivedRebuildSpec) (derivedRebuildProcessResult, error) {
	batch, err := c.replayDerivedViewEventBatch(spec.view, batchSize)
	result := derivedRebuildProcessResult{
		FromSeq:    batch.FromSeq,
		AppliedSeq: batch.AppliedSeq,
		HeadSeq:    batch.HeadSeq,
	}
	if err != nil {
		return result, err
	}
	if len(batch.Events) == 0 && spec.rowCount != nil {
		rows, countErr := spec.rowCount(c.DB)
		if countErr != nil {
			return result, countErr
		}
		if rows > 0 {
			if err := c.finishEmptyDerivedViewEventBatch(batch); err != nil {
				return result, err
			}
			return result, nil
		}
	}

	tx, err := c.DB.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback() //nolint

	rows, err := spec.rebuild(tx)
	if err != nil {
		return result, err
	}
	result.Rebuilt = true
	result.Rows = rows
	for _, evt := range batch.Events {
		if evt == nil {
			continue
		}
		result.Events++
		if evt.Seq > result.AppliedSeq {
			result.AppliedSeq = evt.Seq
		}
	}
	if err := recordDerivedViewAppliedTx(tx, spec.view, result.AppliedSeq); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}
