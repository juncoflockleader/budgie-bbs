package core

import (
	"context"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type BlessingRankingsProcessResult = derivedRebuildProcessResult

type BlessingRankingsProcessor struct {
	derivedRebuildProcessor
}

func NewBlessingRankingsProcessor(c *Core, interval time.Duration, batchSize int) (*BlessingRankingsProcessor, error) {
	processor, err := newDerivedRebuildProcessor(c, "blessing rankings", interval, batchSize, (*Core).ProcessBlessingRankingsOnce)
	if err != nil {
		return nil, err
	}
	return &BlessingRankingsProcessor{derivedRebuildProcessor: processor}, nil
}

func (p *BlessingRankingsProcessor) ProcessOnce() (BlessingRankingsProcessResult, error) {
	if p == nil {
		return nilDerivedRebuildProcessResult("blessing rankings")
	}
	return p.derivedRebuildProcessor.ProcessOnce()
}

func (p *BlessingRankingsProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.derivedRebuildProcessor.Run(ctx)
}

func (c *Core) StartBlessingRankingsProcessor(ctx context.Context, interval time.Duration, batchSize int) (*BlessingRankingsProcessor, error) {
	processor, err := NewBlessingRankingsProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessBlessingRankingsOnce(batchSize int) (BlessingRankingsProcessResult, error) {
	return c.processDerivedRebuildOnce(batchSize, derivedRebuildSpec{
		view:     projections.DerivedViewBlessingRankings,
		rebuild:  projections.RebuildBlessingRankingStats,
		rowCount: projections.BlessingRankingStatsRowCount,
	})
}
