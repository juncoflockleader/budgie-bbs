package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type BoardSummariesProcessResult struct {
	FromSeq    int64
	AppliedSeq int64
	HeadSeq    int64
	Events     int
	Rebuilt    bool
	Rows       int64
}

type BoardSummariesProcessor struct {
	Core      *Core
	BatchSize int
	Interval  time.Duration
}

func NewBoardSummariesProcessor(c *Core, interval time.Duration, batchSize int) (*BoardSummariesProcessor, error) {
	if c == nil {
		return nil, fmt.Errorf("board summaries processor: nil core")
	}
	if interval <= 0 {
		interval = time.Second
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &BoardSummariesProcessor{
		Core:      c,
		BatchSize: batchSize,
		Interval:  interval,
	}, nil
}

func (p *BoardSummariesProcessor) ProcessOnce() (BoardSummariesProcessResult, error) {
	if p == nil || p.Core == nil {
		return BoardSummariesProcessResult{}, fmt.Errorf("board summaries processor: nil core")
	}
	return p.Core.ProcessBoardSummariesOnce(p.BatchSize)
}

func (p *BoardSummariesProcessor) Run(ctx context.Context) {
	if p == nil || p.Core == nil {
		return
	}
	drain := func() {
		for ctx.Err() == nil {
			result, err := p.ProcessOnce()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("board summaries processor failed", "err", err)
				}
				return
			}
			if result.Events > 0 || result.Rebuilt || result.AppliedSeq < result.HeadSeq {
				slog.Debug("board summaries processor advanced",
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

func (c *Core) StartBoardSummariesProcessor(ctx context.Context, interval time.Duration, batchSize int) (*BoardSummariesProcessor, error) {
	processor, err := NewBoardSummariesProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessBoardSummariesOnce(batchSize int) (BoardSummariesProcessResult, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	fromSeq, found, err := lookupDerivedViewAppliedSeq(c.DB, DerivedViewBoardSummaries)
	if err != nil {
		return BoardSummariesProcessResult{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return BoardSummariesProcessResult{}, err
	}
	result := BoardSummariesProcessResult{
		FromSeq:    fromSeq,
		AppliedSeq: fromSeq,
		HeadSeq:    head,
	}
	events, err := c.Replay(fromSeq, nil, batchSize)
	if err != nil {
		return result, err
	}
	if len(events) == 0 {
		rows, countErr := boardSummaryStatsRowCount(c.DB)
		if countErr != nil {
			return result, countErr
		}
		if rows == 0 {
			tx, err := c.DB.Begin()
			if err != nil {
				return result, err
			}
			defer tx.Rollback() //nolint
			rebuiltRows, err := rebuildBoardSummaryStats(tx)
			if err != nil {
				return result, err
			}
			result.Rebuilt = true
			result.Rows = rebuiltRows
			if err := recordDerivedViewAppliedTx(tx, DerivedViewBoardSummaries, result.AppliedSeq); err != nil {
				return result, err
			}
			if err := tx.Commit(); err != nil {
				return result, err
			}
			return result, nil
		}
		if !found {
			if err := c.RecordDerivedViewApplied(DerivedViewBoardSummaries, fromSeq); err != nil {
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

	rows, err := rebuildBoardSummaryStats(tx)
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
	if err := recordDerivedViewAppliedTx(tx, DerivedViewBoardSummaries, result.AppliedSeq); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}
