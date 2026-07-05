package core

import (
	"context"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type UserRankingsProcessResult = derivedRebuildProcessResult

type UserRankingsProcessor struct {
	derivedRebuildProcessor
}

func NewUserRankingsProcessor(c *Core, interval time.Duration, batchSize int) (*UserRankingsProcessor, error) {
	processor, err := newDerivedRebuildProcessor(c, "user rankings", interval, batchSize, (*Core).ProcessUserRankingsOnce)
	if err != nil {
		return nil, err
	}
	return &UserRankingsProcessor{derivedRebuildProcessor: processor}, nil
}

func (p *UserRankingsProcessor) ProcessOnce() (UserRankingsProcessResult, error) {
	if p == nil {
		return nilDerivedRebuildProcessResult("user rankings")
	}
	return p.derivedRebuildProcessor.ProcessOnce()
}

func (p *UserRankingsProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.derivedRebuildProcessor.Run(ctx)
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
	return c.processDerivedRebuildOnce(batchSize, derivedRebuildSpec{
		view:    projections.DerivedViewUserRankings,
		rebuild: projections.RebuildUserRankingStats,
	})
}
