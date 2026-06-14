package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type UserRankingsProcessResult struct {
	FromSeq    int64
	AppliedSeq int64
	HeadSeq    int64
	Events     int
	Rebuilt    bool
	Rows       int64
}

type UserRankingsProcessor struct {
	Core      *Core
	BatchSize int
	Interval  time.Duration
}

func NewUserRankingsProcessor(c *Core, interval time.Duration, batchSize int) (*UserRankingsProcessor, error) {
	if c == nil {
		return nil, fmt.Errorf("user rankings processor: nil core")
	}
	if interval <= 0 {
		interval = time.Second
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &UserRankingsProcessor{
		Core:      c,
		BatchSize: batchSize,
		Interval:  interval,
	}, nil
}

func (p *UserRankingsProcessor) ProcessOnce() (UserRankingsProcessResult, error) {
	if p == nil || p.Core == nil {
		return UserRankingsProcessResult{}, fmt.Errorf("user rankings processor: nil core")
	}
	return p.Core.ProcessUserRankingsOnce(p.BatchSize)
}

func (p *UserRankingsProcessor) Run(ctx context.Context) {
	if p == nil || p.Core == nil {
		return
	}
	drain := func() {
		for ctx.Err() == nil {
			result, err := p.ProcessOnce()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("user rankings processor failed", "err", err)
				}
				return
			}
			if result.Events > 0 || result.Rebuilt || result.AppliedSeq < result.HeadSeq {
				slog.Debug("user rankings processor advanced",
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

func (c *Core) StartUserRankingsProcessor(ctx context.Context, interval time.Duration, batchSize int) (*UserRankingsProcessor, error) {
	processor, err := NewUserRankingsProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessUserRankingsOnce(batchSize int) (UserRankingsProcessResult, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	fromSeq, found, err := lookupDerivedViewAppliedSeq(c.DB, DerivedViewUserRankings)
	if err != nil {
		return UserRankingsProcessResult{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return UserRankingsProcessResult{}, err
	}
	result := UserRankingsProcessResult{
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

	rows, err := rebuildUserRankingStats(tx)
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
	if err := recordDerivedViewAppliedTx(tx, DerivedViewUserRankings, result.AppliedSeq); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}
