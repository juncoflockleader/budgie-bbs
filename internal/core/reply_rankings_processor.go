package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type ReplyRankingsProcessResult struct {
	FromSeq    int64
	AppliedSeq int64
	HeadSeq    int64
	Events     int
	Rebuilt    bool
	Rows       int64
}

type ReplyRankingsProcessor struct {
	Core      *Core
	BatchSize int
	Interval  time.Duration
}

func NewReplyRankingsProcessor(c *Core, interval time.Duration, batchSize int) (*ReplyRankingsProcessor, error) {
	if c == nil {
		return nil, fmt.Errorf("reply rankings processor: nil core")
	}
	if interval <= 0 {
		interval = time.Second
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &ReplyRankingsProcessor{
		Core:      c,
		BatchSize: batchSize,
		Interval:  interval,
	}, nil
}

func (p *ReplyRankingsProcessor) ProcessOnce() (ReplyRankingsProcessResult, error) {
	if p == nil || p.Core == nil {
		return ReplyRankingsProcessResult{}, fmt.Errorf("reply rankings processor: nil core")
	}
	return p.Core.ProcessReplyRankingsOnce(p.BatchSize)
}

func (p *ReplyRankingsProcessor) Run(ctx context.Context) {
	if p == nil || p.Core == nil {
		return
	}
	drain := func() {
		for ctx.Err() == nil {
			result, err := p.ProcessOnce()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("reply rankings processor failed", "err", err)
				}
				return
			}
			if result.Events > 0 || result.Rebuilt || result.AppliedSeq < result.HeadSeq {
				slog.Debug("reply rankings processor advanced",
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

func (c *Core) StartReplyRankingsProcessor(ctx context.Context, interval time.Duration, batchSize int) (*ReplyRankingsProcessor, error) {
	processor, err := NewReplyRankingsProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessReplyRankingsOnce(batchSize int) (ReplyRankingsProcessResult, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	fromSeq, found, err := lookupDerivedViewAppliedSeq(c.DB, DerivedViewReplyRankings)
	if err != nil {
		return ReplyRankingsProcessResult{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return ReplyRankingsProcessResult{}, err
	}
	result := ReplyRankingsProcessResult{
		FromSeq:    fromSeq,
		AppliedSeq: fromSeq,
		HeadSeq:    head,
	}
	events, err := c.Replay(fromSeq, nil, batchSize)
	if err != nil {
		return result, err
	}
	if len(events) == 0 {
		rows, countErr := replyRankingPostsRowCount(c.DB)
		if countErr != nil {
			return result, countErr
		}
		if rows == 0 {
			tx, err := c.DB.Begin()
			if err != nil {
				return result, err
			}
			defer tx.Rollback() //nolint
			rebuiltRows, err := rebuildReplyRankingPosts(tx)
			if err != nil {
				return result, err
			}
			result.Rebuilt = true
			result.Rows = rebuiltRows
			if err := recordDerivedViewAppliedTx(tx, DerivedViewReplyRankings, result.AppliedSeq); err != nil {
				return result, err
			}
			if err := tx.Commit(); err != nil {
				return result, err
			}
			return result, nil
		}
		if !found {
			if err := c.RecordDerivedViewApplied(DerivedViewReplyRankings, fromSeq); err != nil {
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

	rows, err := rebuildReplyRankingPosts(tx)
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
	if err := recordDerivedViewAppliedTx(tx, DerivedViewReplyRankings, result.AppliedSeq); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}
