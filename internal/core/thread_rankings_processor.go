package core

import (
	"context"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type ThreadRankingsProcessResult = derivedRebuildProcessResult

type ThreadRankingsProcessor struct {
	derivedRebuildProcessor
}

func NewThreadRankingsProcessor(c *Core, interval time.Duration, batchSize int) (*ThreadRankingsProcessor, error) {
	processor, err := newDerivedRebuildProcessor(c, "thread rankings", interval, batchSize, (*Core).ProcessThreadRankingsOnce)
	if err != nil {
		return nil, err
	}
	return &ThreadRankingsProcessor{derivedRebuildProcessor: processor}, nil
}

func (p *ThreadRankingsProcessor) ProcessOnce() (ThreadRankingsProcessResult, error) {
	if p == nil {
		return nilDerivedRebuildProcessResult("thread rankings")
	}
	return p.derivedRebuildProcessor.ProcessOnce()
}

func (p *ThreadRankingsProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.derivedRebuildProcessor.Run(ctx)
}

func (c *Core) StartThreadRankingsProcessor(ctx context.Context, interval time.Duration, batchSize int) (*ThreadRankingsProcessor, error) {
	processor, err := NewThreadRankingsProcessor(c, interval, batchSize)
	return startPeriodicProcessor(ctx, processor, err)
}

func (c *Core) ProcessThreadRankingsOnce(batchSize int) (ThreadRankingsProcessResult, error) {
	return c.processDerivedRebuildOnce(batchSize, derivedRebuildSpec{
		view:    projections.DerivedViewThreadRankings,
		rebuild: projections.RebuildThreadRankingStats,
	})
}
