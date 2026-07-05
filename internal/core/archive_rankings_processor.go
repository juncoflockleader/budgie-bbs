package core

import (
	"context"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type ArchiveRankingsProcessResult = derivedRebuildProcessResult

type ArchiveRankingsProcessor struct {
	derivedRebuildProcessor
}

func NewArchiveRankingsProcessor(c *Core, interval time.Duration, batchSize int) (*ArchiveRankingsProcessor, error) {
	processor, err := newDerivedRebuildProcessor(c, "archive rankings", interval, batchSize, (*Core).ProcessArchiveRankingsOnce)
	if err != nil {
		return nil, err
	}
	return &ArchiveRankingsProcessor{derivedRebuildProcessor: processor}, nil
}

func (p *ArchiveRankingsProcessor) ProcessOnce() (ArchiveRankingsProcessResult, error) {
	if p == nil {
		return nilDerivedRebuildProcessResult("archive rankings")
	}
	return p.derivedRebuildProcessor.ProcessOnce()
}

func (p *ArchiveRankingsProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.derivedRebuildProcessor.Run(ctx)
}

func (c *Core) StartArchiveRankingsProcessor(ctx context.Context, interval time.Duration, batchSize int) (*ArchiveRankingsProcessor, error) {
	processor, err := NewArchiveRankingsProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessArchiveRankingsOnce(batchSize int) (ArchiveRankingsProcessResult, error) {
	return c.processDerivedRebuildOnce(batchSize, derivedRebuildSpec{
		view:     projections.DerivedViewArchiveRankings,
		rebuild:  projections.RebuildArchiveRankingStats,
		rowCount: projections.ArchiveRankingStatsRowCount,
	})
}
