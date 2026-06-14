package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type ThreadRankingsProcessResult struct {
	FromSeq    int64
	AppliedSeq int64
	HeadSeq    int64
	Events     int
	Rebuilt    bool
	Rows       int64
}

type ThreadRankingsProcessor struct {
	Core      *Core
	BatchSize int
	Interval  time.Duration
}

func NewThreadRankingsProcessor(c *Core, interval time.Duration, batchSize int) (*ThreadRankingsProcessor, error) {
	if c == nil {
		return nil, fmt.Errorf("thread rankings processor: nil core")
	}
	if interval <= 0 {
		interval = time.Second
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &ThreadRankingsProcessor{
		Core:      c,
		BatchSize: batchSize,
		Interval:  interval,
	}, nil
}

func (p *ThreadRankingsProcessor) ProcessOnce() (ThreadRankingsProcessResult, error) {
	if p == nil || p.Core == nil {
		return ThreadRankingsProcessResult{}, fmt.Errorf("thread rankings processor: nil core")
	}
	return p.Core.ProcessThreadRankingsOnce(p.BatchSize)
}

func (p *ThreadRankingsProcessor) Run(ctx context.Context) {
	if p == nil || p.Core == nil {
		return
	}
	drain := func() {
		for ctx.Err() == nil {
			result, err := p.ProcessOnce()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("thread rankings processor failed", "err", err)
				}
				return
			}
			if result.Events > 0 || result.Rebuilt || result.AppliedSeq < result.HeadSeq {
				slog.Debug("thread rankings processor advanced",
					"fromSeq", result.FromSeq,
					"appliedSeq", result.AppliedSeq,
					"headSeq", result.HeadSeq,
					"events", result.Events,
					"rebuilt", result.Rebuilt,
					"rows", result.Rows)
			}
			if result.Events < p.BatchSize || result.AppliedSeq >= result.HeadSeq {
				return
			}
		}
	}
	drain()
	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			drain()
		}
	}
}

func (c *Core) StartThreadRankingsProcessor(ctx context.Context, interval time.Duration, batchSize int) (*ThreadRankingsProcessor, error) {
	processor, err := NewThreadRankingsProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessThreadRankingsOnce(batchSize int) (ThreadRankingsProcessResult, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	fromSeq, found, err := lookupDerivedViewAppliedSeq(c.DB, DerivedViewThreadRankings)
	if err != nil {
		return ThreadRankingsProcessResult{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return ThreadRankingsProcessResult{}, err
	}
	result := ThreadRankingsProcessResult{
		FromSeq:    fromSeq,
		AppliedSeq: fromSeq,
		HeadSeq:    head,
	}
	events, err := c.Replay(fromSeq, nil, batchSize)
	if err != nil {
		return result, err
	}

	tx, err := c.DB.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback() //nolint

	rows, err := rebuildThreadRankingStats(tx)
	if err != nil {
		return result, err
	}
	result.Rebuilt = true
	result.Rows = rows
	for _, evt := range events {
		if evt == nil {
			continue
		}
		result.Events++
		if evt.Seq > result.AppliedSeq {
			result.AppliedSeq = evt.Seq
		}
	}
	if err := recordDerivedViewAppliedTx(tx, DerivedViewThreadRankings, result.AppliedSeq); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}
