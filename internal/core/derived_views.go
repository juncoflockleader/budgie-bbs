package core

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

const (
	DerivedViewCommunityStats       = "community_stats"
	DerivedViewCommunityStatHistory = "community_stat_history"
	DerivedViewLatestFeed           = "feeds.latest"
	DerivedViewResidentFeed         = "feeds.resident"
	DerivedViewBoardSummaries       = "summaries.boards"
	DerivedViewUnreadThreads        = "summaries.unread_threads"
	DerivedViewBoardRankings        = "rankings.boards"
	DerivedViewThreadRankings       = "rankings.threads"
	DerivedViewReplyRankings        = "rankings.replies"
	DerivedViewUserRankings         = "rankings.users"
	DerivedViewBlessingRankings     = "rankings.blessings"
	DerivedViewArchiveRankings      = "rankings.archives"
	DerivedViewPostSearch           = "search.posts"
	DerivedViewDigestSearch         = "search.digest"
)

var knownDerivedViews = []string{
	DerivedViewCommunityStats,
	DerivedViewCommunityStatHistory,
	DerivedViewLatestFeed,
	DerivedViewResidentFeed,
	DerivedViewBoardSummaries,
	DerivedViewUnreadThreads,
	DerivedViewBoardRankings,
	DerivedViewThreadRankings,
	DerivedViewReplyRankings,
	DerivedViewUserRankings,
	DerivedViewBlessingRankings,
	DerivedViewArchiveRankings,
	DerivedViewPostSearch,
	DerivedViewDigestSearch,
}

var derivedViewGroups = map[string][]string{
	"community": {
		DerivedViewCommunityStats,
		DerivedViewCommunityStatHistory,
	},
	"feeds": {
		DerivedViewLatestFeed,
		DerivedViewResidentFeed,
	},
	"rankings": {
		DerivedViewBoardRankings,
		DerivedViewThreadRankings,
		DerivedViewReplyRankings,
		DerivedViewUserRankings,
		DerivedViewBlessingRankings,
		DerivedViewArchiveRankings,
	},
	"search": {
		DerivedViewPostSearch,
		DerivedViewDigestSearch,
	},
	"summaries": {
		DerivedViewBoardSummaries,
		DerivedViewUnreadThreads,
	},
}

type DerivedViewWatermark struct {
	View       string
	AppliedSeq int64
	UpdatedAt  int64
}

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
	return append([]string(nil), knownDerivedViews...)
}

func DerivedViewGroups() map[string][]string {
	out := make(map[string][]string, len(derivedViewGroups))
	for group, views := range derivedViewGroups {
		out[group] = append([]string(nil), views...)
	}
	return out
}

func ResolveDerivedViews(views []string) ([]string, error) {
	if len(views) == 0 {
		return nil, fmt.Errorf("derived view name required")
	}
	known := map[string]bool{}
	for _, view := range knownDerivedViews {
		known[view] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range views {
		for _, part := range strings.Split(raw, ",") {
			view := normalizeDerivedView(part)
			if view == "" {
				continue
			}
			if view == "all" || view == "*" {
				return KnownDerivedViews(), nil
			}
			if group, ok := derivedViewGroups[view]; ok {
				for _, groupedView := range group {
					if !seen[groupedView] {
						out = append(out, groupedView)
						seen[groupedView] = true
					}
				}
				continue
			}
			if !known[view] {
				return nil, fmt.Errorf("unknown derived view %q", view)
			}
			if !seen[view] {
				out = append(out, view)
				seen[view] = true
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("derived view name required")
	}
	sort.Strings(out)
	return out, nil
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
	applied, found, err := lookupDerivedViewAppliedSeq(c.DB, view)
	if err != nil {
		return 0, err
	}
	if found {
		return applied, nil
	}
	return c.Head()
}

func (c *Core) RecordDerivedViewApplied(view string, appliedSeq int64) error {
	view = normalizeDerivedView(view)
	if view == "" {
		return fmt.Errorf("derived view name required")
	}
	if appliedSeq < 0 {
		appliedSeq = 0
	}
	_, err := qExec(c.DB,
		`INSERT INTO derived_view_watermarks (view_name, applied_seq, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(view_name) DO UPDATE
		       SET applied_seq=excluded.applied_seq,
		           updated_at=excluded.updated_at`,
		view, appliedSeq, nowMS(),
	)
	return err
}

func (c *Core) ListDerivedViewWatermarks() ([]DerivedViewWatermark, error) {
	rows, err := qQuery(c.DB,
		`SELECT view_name, applied_seq, updated_at
		   FROM derived_view_watermarks
		  ORDER BY view_name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DerivedViewWatermark
	for rows.Next() {
		var mark DerivedViewWatermark
		if err := rows.Scan(&mark.View, &mark.AppliedSeq, &mark.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, mark)
	}
	return out, rows.Err()
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
	want = normalizeDerivedView(want)
	for _, view := range views {
		if normalizeDerivedView(view) == want {
			return true
		}
	}
	return false
}

func lookupDerivedViewAppliedSeq(db *sql.DB, view string) (int64, bool, error) {
	var applied int64
	err := qQueryRow(db, `SELECT applied_seq FROM derived_view_watermarks WHERE view_name=?`, view).Scan(&applied)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return applied, true, nil
}

func normalizeDerivedView(view string) string {
	return strings.TrimSpace(strings.ToLower(view))
}
