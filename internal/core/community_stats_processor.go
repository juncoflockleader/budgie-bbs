package core

import (
	"context"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type CommunityStatsProcessResult = derivedRebuildProcessResult

type CommunityStatsProcessor struct {
	derivedRebuildProcessor
}

func NewCommunityStatsProcessor(c *Core, interval time.Duration, batchSize int) (*CommunityStatsProcessor, error) {
	processor, err := newDerivedRebuildProcessor(c, "community stats", interval, batchSize, (*Core).ProcessCommunityStatsOnce)
	if err != nil {
		return nil, err
	}
	return &CommunityStatsProcessor{derivedRebuildProcessor: processor}, nil
}

func (p *CommunityStatsProcessor) ProcessOnce() (CommunityStatsProcessResult, error) {
	if p == nil {
		return nilDerivedRebuildProcessResult("community stats")
	}
	return p.derivedRebuildProcessor.ProcessOnce()
}

func (p *CommunityStatsProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.derivedRebuildProcessor.Run(ctx)
}

func (c *Core) StartCommunityStatsProcessor(ctx context.Context, interval time.Duration, batchSize int) (*CommunityStatsProcessor, error) {
	processor, err := NewCommunityStatsProcessor(c, interval, batchSize)
	return startPeriodicProcessor(ctx, processor, err)
}

func (c *Core) ProcessCommunityStatsOnce(batchSize int) (CommunityStatsProcessResult, error) {
	return c.processDerivedRebuildOnce(batchSize, derivedRebuildSpec{
		view:    projections.DerivedViewCommunityStats,
		rebuild: projections.RebuildCommunityStatsSnapshot,
	})
}
