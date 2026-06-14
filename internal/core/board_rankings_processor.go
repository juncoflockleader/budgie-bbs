package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type BoardRankingsProcessResult struct {
	FromSeq    int64
	AppliedSeq int64
	HeadSeq    int64
	Events     int
	Rebuilt    bool
	Rows       int64
}

type BoardRankingsProcessor struct {
	Core      *Core
	BatchSize int
	Interval  time.Duration
}

func NewBoardRankingsProcessor(c *Core, interval time.Duration, batchSize int) (*BoardRankingsProcessor, error) {
	if c == nil {
		return nil, fmt.Errorf("board rankings processor: nil core")
	}
	if interval <= 0 {
		interval = time.Second
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &BoardRankingsProcessor{
		Core:      c,
		BatchSize: batchSize,
		Interval:  interval,
	}, nil
}

func (p *BoardRankingsProcessor) ProcessOnce() (BoardRankingsProcessResult, error) {
	if p == nil || p.Core == nil {
		return BoardRankingsProcessResult{}, fmt.Errorf("board rankings processor: nil core")
	}
	return p.Core.ProcessBoardRankingsOnce(p.BatchSize)
}

func (p *BoardRankingsProcessor) Run(ctx context.Context) {
	if p == nil || p.Core == nil {
		return
	}
	drain := func() {
		for ctx.Err() == nil {
			result, err := p.ProcessOnce()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("board rankings processor failed", "err", err)
				}
				return
			}
			if result.Events > 0 || result.Rebuilt || result.AppliedSeq < result.HeadSeq {
				slog.Debug("board rankings processor advanced",
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

func (c *Core) StartBoardRankingsProcessor(ctx context.Context, interval time.Duration, batchSize int) (*BoardRankingsProcessor, error) {
	processor, err := NewBoardRankingsProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessBoardRankingsOnce(batchSize int) (BoardRankingsProcessResult, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	fromSeq, found, err := lookupDerivedViewAppliedSeq(c.DB, DerivedViewBoardRankings)
	if err != nil {
		return BoardRankingsProcessResult{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return BoardRankingsProcessResult{}, err
	}
	result := BoardRankingsProcessResult{
		FromSeq:    fromSeq,
		AppliedSeq: fromSeq,
		HeadSeq:    head,
	}
	events, err := c.Replay(fromSeq, nil, batchSize)
	if err != nil {
		return result, err
	}
	if len(events) == 0 {
		rows, countErr := boardRankingStatsRowCount(c.DB)
		if countErr != nil {
			return result, countErr
		}
		if rows == 0 {
			tx, err := c.DB.Begin()
			if err != nil {
				return result, err
			}
			defer tx.Rollback() //nolint
			rebuiltRows, err := rebuildBoardRankingStats(tx)
			if err != nil {
				return result, err
			}
			result.Rebuilt = true
			result.Rows = rebuiltRows
			if err := recordDerivedViewAppliedTx(tx, DerivedViewBoardRankings, result.AppliedSeq); err != nil {
				return result, err
			}
			if err := tx.Commit(); err != nil {
				return result, err
			}
			return result, nil
		}
		if !found {
			if err := c.RecordDerivedViewApplied(DerivedViewBoardRankings, fromSeq); err != nil {
				return result, err
			}
		}
		return result, nil
	}

	tx, err := c.DB.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback() //nolint

	rows, err := rebuildBoardRankingStats(tx)
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
	if err := recordDerivedViewAppliedTx(tx, DerivedViewBoardRankings, result.AppliedSeq); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}
