package core

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

const (
	DerivedViewCommunityStats       = projections.DerivedViewCommunityStats
	DerivedViewCommunityStatHistory = projections.DerivedViewCommunityStatHistory
	DerivedViewLatestFeed           = projections.DerivedViewLatestFeed
	DerivedViewResidentFeed         = projections.DerivedViewResidentFeed
	DerivedViewBoardSummaries       = projections.DerivedViewBoardSummaries
	DerivedViewUnreadThreads        = projections.DerivedViewUnreadThreads
	DerivedViewBoardRankings        = projections.DerivedViewBoardRankings
	DerivedViewThreadRankings       = projections.DerivedViewThreadRankings
	DerivedViewReplyRankings        = projections.DerivedViewReplyRankings
	DerivedViewUserRankings         = projections.DerivedViewUserRankings
	DerivedViewBlessingRankings     = projections.DerivedViewBlessingRankings
	DerivedViewArchiveRankings      = projections.DerivedViewArchiveRankings
	DerivedViewPostSearch           = projections.DerivedViewPostSearch
	DerivedViewDigestSearch         = projections.DerivedViewDigestSearch
)

type DerivedViewWatermark = projections.DerivedViewWatermark

type DerivedViewBackfillResult struct {
	Views   []string
	HeadSeq int64
}

type DerivedViewWatermarkSyncResult struct {
	Views   []string
	HeadSeq int64
}

type DerivedViewWatermarkWorker struct {
	Core     *Core
	Views    []string
	Interval time.Duration
}

func KnownDerivedViews() []string {
	return projections.KnownDerivedViews()
}

func DerivedViewGroups() map[string][]string {
	return projections.DerivedViewGroups()
}

func ResolveDerivedViews(views []string) ([]string, error) {
	return projections.ResolveDerivedViews(views)
}

// DerivedViewAppliedSeq returns the applied durable position for a derived
// global view. Missing watermarks resolve to the current event head so today's
// synchronous projections keep reporting zero lag until a stream processor
// explicitly owns and advances the view.
func (c *Core) DerivedViewAppliedSeq(view string) (int64, error) {
	view = normalizeDerivedView(view)
	if view == "" {
		return c.Head()
	}
	applied, found, err := projections.LookupProjectionWatermarkAppliedSeq(c.DB, view)
	if err != nil {
		return 0, err
	}
	if found {
		return applied, nil
	}
	return c.Head()
}

func (c *Core) RecordDerivedViewApplied(view string, appliedSeq int64) error {
	return projections.RecordDerivedViewApplied(c.DB, view, appliedSeq, nowMS())
}

func (c *Core) ListDerivedViewWatermarks() ([]DerivedViewWatermark, error) {
	return projections.ListDerivedViewWatermarks(c.DB)
}

func (c *Core) BackfillDerivedViewsFromEventLog(views []string, fromSeq int64) (DerivedViewBackfillResult, error) {
	resolved, err := ResolveDerivedViews(views)
	if err != nil {
		return DerivedViewBackfillResult{}, err
	}
	if err := c.RebuildProjectionsFromEventLog(fromSeq); err != nil {
		return DerivedViewBackfillResult{}, err
	}
	if containsDerivedView(resolved, DerivedViewPostSearch) {
		if _, err := c.rebuildExternalPostSearchIndex(context.Background()); err != nil {
			return DerivedViewBackfillResult{}, err
		}
	}
	head, err := c.Head()
	if err != nil {
		return DerivedViewBackfillResult{}, err
	}
	if err := c.markDerivedViewsApplied(resolved, head); err != nil {
		return DerivedViewBackfillResult{}, err
	}
	return DerivedViewBackfillResult{Views: resolved, HeadSeq: head}, nil
}

func (c *Core) BackfillDerivedViewsFromEventStore(ctx context.Context, store EventStore, views []string, fromSeq int64) (DerivedViewBackfillResult, error) {
	resolved, err := ResolveDerivedViews(views)
	if err != nil {
		return DerivedViewBackfillResult{}, err
	}
	if store == nil {
		return DerivedViewBackfillResult{}, fmt.Errorf("backfill derived views from event store: nil store")
	}
	if err := c.RebuildProjectionsFromEventStore(ctx, store, fromSeq); err != nil {
		return DerivedViewBackfillResult{}, err
	}
	if containsDerivedView(resolved, DerivedViewPostSearch) {
		if _, err := c.rebuildExternalPostSearchIndex(ctx); err != nil {
			return DerivedViewBackfillResult{}, err
		}
	}
	head, err := store.Head(ctx)
	if err != nil {
		return DerivedViewBackfillResult{}, err
	}
	if err := c.markDerivedViewsApplied(resolved, head); err != nil {
		return DerivedViewBackfillResult{}, err
	}
	return DerivedViewBackfillResult{Views: resolved, HeadSeq: head}, nil
}

func (c *Core) SyncDerivedViewsToHead(views []string) (DerivedViewWatermarkSyncResult, error) {
	resolved, err := ResolveDerivedViews(views)
	if err != nil {
		return DerivedViewWatermarkSyncResult{}, err
	}
	head, err := c.Head()
	if err != nil {
		return DerivedViewWatermarkSyncResult{}, err
	}
	if err := c.markDerivedViewsApplied(resolved, head); err != nil {
		return DerivedViewWatermarkSyncResult{}, err
	}
	return DerivedViewWatermarkSyncResult{Views: resolved, HeadSeq: head}, nil
}

func NewDerivedViewWatermarkWorker(c *Core, views []string, interval time.Duration) (*DerivedViewWatermarkWorker, error) {
	if c == nil {
		return nil, fmt.Errorf("derived view watermark worker: nil core")
	}
	resolved, err := ResolveDerivedViews(views)
	if err != nil {
		return nil, err
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &DerivedViewWatermarkWorker{
		Core:     c,
		Views:    resolved,
		Interval: interval,
	}, nil
}

func (w *DerivedViewWatermarkWorker) SyncOnce() (DerivedViewWatermarkSyncResult, error) {
	if w == nil || w.Core == nil {
		return DerivedViewWatermarkSyncResult{}, fmt.Errorf("derived view watermark worker: nil core")
	}
	return w.Core.SyncDerivedViewsToHead(w.Views)
}

func (w *DerivedViewWatermarkWorker) Run(ctx context.Context) {
	if w == nil || w.Core == nil {
		return
	}
	sync := func() {
		result, err := w.SyncOnce()
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("derived view watermark sync failed", "err", err)
			}
			return
		}
		slog.Debug("derived view watermarks synced", "views", strings.Join(result.Views, ","), "headSeq", result.HeadSeq)
	}
	sync()
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sync()
		}
	}
}

func (c *Core) StartDerivedViewWatermarkWorker(ctx context.Context, views []string, interval time.Duration) (*DerivedViewWatermarkWorker, error) {
	worker, err := NewDerivedViewWatermarkWorker(c, views, interval)
	if err != nil {
		return nil, err
	}
	go worker.Run(ctx)
	return worker, nil
}

func (c *Core) markDerivedViewsApplied(views []string, head int64) error {
	for _, view := range views {
		if err := c.RecordDerivedViewApplied(view, head); err != nil {
			return fmt.Errorf("record derived view watermark %s: %w", view, err)
		}
	}
	return nil
}

func containsDerivedView(views []string, want string) bool {
	return projections.ContainsDerivedView(views, want)
}

func normalizeDerivedView(view string) string {
	return projections.NormalizeDerivedView(view)
}
