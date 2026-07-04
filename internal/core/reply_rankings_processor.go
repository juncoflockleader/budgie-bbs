package core

import (
	"context"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type ReplyRankingsProcessResult = derivedRebuildProcessResult

type ReplyRankingsProcessor struct {
	derivedRebuildProcessor
}

func NewReplyRankingsProcessor(c *Core, interval time.Duration, batchSize int) (*ReplyRankingsProcessor, error) {
	processor, err := newDerivedRebuildProcessor(c, "reply rankings", interval, batchSize, func(c *Core, batchSize int) (derivedRebuildProcessResult, error) {
		return c.ProcessReplyRankingsOnce(batchSize)
	})
	if err != nil {
		return nil, err
	}
	return &ReplyRankingsProcessor{derivedRebuildProcessor: processor}, nil
}

func (p *ReplyRankingsProcessor) ProcessOnce() (ReplyRankingsProcessResult, error) {
	if p == nil {
		return nilDerivedRebuildProcessResult("reply rankings")
	}
	return p.derivedRebuildProcessor.ProcessOnce()
}

func (p *ReplyRankingsProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.derivedRebuildProcessor.Run(ctx)
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
	return c.processDerivedRebuildOnce(batchSize, derivedRebuildSpec{
		view:     DerivedViewReplyRankings,
		rebuild:  projections.RebuildReplyRankingPosts,
		rowCount: projections.ReplyRankingPostsRowCount,
	})
}
