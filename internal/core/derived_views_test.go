package core

import (
	"context"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestDerivedViewWatermarkDefaultsAndMetrics(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	createDerivedViewTestThread(t, c, alice)
	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head == 0 {
		t.Fatal("expected seeded user event to advance head")
	}

	applied, err := c.DerivedViewAppliedSeq(projections.DerivedViewBoardRankings)
	if err != nil {
		t.Fatalf("default derived view applied seq: %v", err)
	}
	if applied != head {
		t.Fatalf("default applied seq = %d, want head %d", applied, head)
	}

	if err := c.RecordDerivedViewApplied(projections.DerivedViewBoardRankings, head-1); err != nil {
		t.Fatalf("record watermark: %v", err)
	}
	applied, err = c.DerivedViewAppliedSeq(projections.DerivedViewBoardRankings)
	if err != nil {
		t.Fatalf("explicit derived view applied seq: %v", err)
	}
	if applied != head-1 {
		t.Fatalf("explicit applied seq = %d, want %d", applied, head-1)
	}

	samples, err := derivedViewWatermarkSamples(c.DB)
	if err != nil {
		t.Fatalf("derivedViewWatermarkSamples: %v", err)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_applied_seq", projections.DerivedViewBoardRankings); got != float64(head-1) {
		t.Fatalf("applied sample = %v, want %d", got, head-1)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", projections.DerivedViewBoardRankings); got != 1 {
		t.Fatalf("lag sample = %v, want 1", got)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", projections.DerivedViewPostSearch); got != 0 {
		t.Fatalf("default search lag sample = %v, want 0", got)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", projections.DerivedViewResidentFeed); got != 0 {
		t.Fatalf("default resident feed lag sample = %v, want 0", got)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", projections.DerivedViewLatestFeed); got != 0 {
		t.Fatalf("default latest feed lag sample = %v, want 0", got)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", projections.DerivedViewBoardSummaries); got != 0 {
		t.Fatalf("default board summaries lag sample = %v, want 0", got)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", projections.DerivedViewUnreadThreads); got != 0 {
		t.Fatalf("default unread threads lag sample = %v, want 0", got)
	}
}

func TestResolveDerivedViews(t *testing.T) {
	views, err := projections.ResolveDerivedViews([]string{" rankings.threads ", projections.DerivedViewBoardRankings, "rankings.threads"})
	if err != nil {
		t.Fatalf("projections.ResolveDerivedViews: %v", err)
	}
	want := []string{projections.DerivedViewBoardRankings, projections.DerivedViewThreadRankings}
	if len(views) != len(want) {
		t.Fatalf("views = %v, want %v", views, want)
	}
	for i := range want {
		if views[i] != want[i] {
			t.Fatalf("views = %v, want %v", views, want)
		}
	}

	all, err := projections.ResolveDerivedViews([]string{"all"})
	if err != nil {
		t.Fatalf("projections.ResolveDerivedViews all: %v", err)
	}
	if len(all) != len(projections.KnownDerivedViews()) {
		t.Fatalf("all view count = %d, want %d", len(all), len(projections.KnownDerivedViews()))
	}

	search, err := projections.ResolveDerivedViews([]string{"search"})
	if err != nil {
		t.Fatalf("projections.ResolveDerivedViews search: %v", err)
	}
	assertDerivedViews(t, search, []string{projections.DerivedViewDigestSearch, projections.DerivedViewPostSearch})

	rankings, err := projections.ResolveDerivedViews([]string{"rankings"})
	if err != nil {
		t.Fatalf("projections.ResolveDerivedViews rankings: %v", err)
	}
	assertDerivedViews(t, rankings, []string{
		projections.DerivedViewArchiveRankings,
		projections.DerivedViewBlessingRankings,
		projections.DerivedViewBoardRankings,
		projections.DerivedViewReplyRankings,
		projections.DerivedViewThreadRankings,
		projections.DerivedViewUserRankings,
	})

	mixed, err := projections.ResolveDerivedViews([]string{"search, summaries.boards", "summaries"})
	if err != nil {
		t.Fatalf("projections.ResolveDerivedViews mixed groups: %v", err)
	}
	assertDerivedViews(t, mixed, []string{
		projections.DerivedViewDigestSearch,
		projections.DerivedViewPostSearch,
		projections.DerivedViewBoardSummaries,
		projections.DerivedViewUnreadThreads,
	})

	groups := projections.DerivedViewGroups()
	if len(groups["search"]) != 2 {
		t.Fatalf("search group = %v, want two views", groups["search"])
	}
	if len(groups["feeds"]) != 2 {
		t.Fatalf("feeds group = %v, want two views", groups["feeds"])
	}
	groups["search"][0] = "mutated"
	if again := projections.DerivedViewGroups()["search"][0]; again == "mutated" {
		t.Fatalf("projections.DerivedViewGroups returned mutable backing slice")
	}

	if _, err := projections.ResolveDerivedViews([]string{"rankings.unknown"}); err == nil {
		t.Fatal("expected unknown derived view to fail")
	}
}

func assertDerivedViews(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("views = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("views = %v, want %v", got, want)
		}
	}
}

func TestBackfillDerivedViewsFromEventLogAdvancesSelectedWatermarks(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	thread := createDerivedViewTestThread(t, c, alice)
	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head < 2 {
		t.Fatalf("head = %d, want at least 2", head)
	}

	stale := head - 1
	for _, view := range []string{projections.DerivedViewBoardRankings, projections.DerivedViewPostSearch, projections.DerivedViewUserRankings} {
		if err := c.RecordDerivedViewApplied(view, stale); err != nil {
			t.Fatalf("record stale watermark %s: %v", view, err)
		}
	}
	if got, err := c.DerivedViewAppliedSeq(projections.DerivedViewBoardRankings); err != nil || got != stale {
		t.Fatalf("stale board ranking watermark = %d err=%v, want %d", got, err, stale)
	}

	gotThread, err := c.GetThread(thread.ID)
	if err != nil || gotThread == nil {
		t.Fatalf("local thread read should not depend on ranking freshness: thread=%+v err=%v", gotThread, err)
	}
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil || len(posts) == 0 {
		t.Fatalf("local post read should not depend on search freshness: posts=%d err=%v", len(posts), err)
	}

	result, err := c.BackfillDerivedViewsFromEventLog([]string{projections.DerivedViewBoardRankings, projections.DerivedViewPostSearch}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog: %v", err)
	}
	if result.HeadSeq != head {
		t.Fatalf("backfill head = %d, want %d", result.HeadSeq, head)
	}
	if len(result.Views) != 2 || result.Views[0] != projections.DerivedViewBoardRankings || result.Views[1] != projections.DerivedViewPostSearch {
		t.Fatalf("backfilled views = %v", result.Views)
	}
	for _, view := range []string{projections.DerivedViewBoardRankings, projections.DerivedViewPostSearch} {
		applied, err := c.DerivedViewAppliedSeq(view)
		if err != nil {
			t.Fatalf("applied %s: %v", view, err)
		}
		if applied != head {
			t.Fatalf("applied %s = %d, want %d", view, applied, head)
		}
	}
	userRankingApplied, err := c.DerivedViewAppliedSeq(projections.DerivedViewUserRankings)
	if err != nil {
		t.Fatalf("user rankings applied: %v", err)
	}
	if userRankingApplied != stale {
		t.Fatalf("unselected user ranking watermark = %d, want stale %d", userRankingApplied, stale)
	}

	rebuiltThread, err := c.GetThread(thread.ID)
	if err != nil || rebuiltThread == nil || rebuiltThread.Title != "derived view backfill" {
		t.Fatalf("thread after backfill = %+v err=%v", rebuiltThread, err)
	}
	search, err := c.SearchReadablePosts(alice, "operational backfill", "", 10)
	if err != nil {
		t.Fatalf("search after backfill: %v", err)
	}
	if len(search) == 0 || search[0].Thread != thread.ID {
		t.Fatalf("search after backfill = %+v, want rebuilt post index", search)
	}
}

func TestSyncDerivedViewsToHeadAdvancesSelectedWatermarks(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	_ = createDerivedViewTestThread(t, c, alice)
	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head < 2 {
		t.Fatalf("head = %d, want at least 2", head)
	}

	stale := head - 1
	for _, view := range []string{projections.DerivedViewBoardRankings, projections.DerivedViewPostSearch, projections.DerivedViewUserRankings} {
		if err := c.RecordDerivedViewApplied(view, stale); err != nil {
			t.Fatalf("record stale watermark %s: %v", view, err)
		}
	}

	result, err := c.SyncDerivedViewsToHead([]string{projections.DerivedViewPostSearch, projections.DerivedViewBoardRankings})
	if err != nil {
		t.Fatalf("SyncDerivedViewsToHead: %v", err)
	}
	if result.HeadSeq != head {
		t.Fatalf("sync head = %d, want %d", result.HeadSeq, head)
	}
	if len(result.Views) != 2 || result.Views[0] != projections.DerivedViewBoardRankings || result.Views[1] != projections.DerivedViewPostSearch {
		t.Fatalf("synced views = %v", result.Views)
	}
	for _, view := range []string{projections.DerivedViewBoardRankings, projections.DerivedViewPostSearch} {
		applied, err := c.DerivedViewAppliedSeq(view)
		if err != nil {
			t.Fatalf("applied %s: %v", view, err)
		}
		if applied != head {
			t.Fatalf("applied %s = %d, want %d", view, applied, head)
		}
	}
	userRankingApplied, err := c.DerivedViewAppliedSeq(projections.DerivedViewUserRankings)
	if err != nil {
		t.Fatalf("user rankings applied: %v", err)
	}
	if userRankingApplied != stale {
		t.Fatalf("unselected user ranking watermark = %d, want stale %d", userRankingApplied, stale)
	}
}

func TestDerivedViewWatermarkWorkerTracksHead(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	if err := c.RecordDerivedViewApplied(projections.DerivedViewBoardRankings, 0); err != nil {
		t.Fatalf("record stale watermark: %v", err)
	}

	workerCtx, stopWorker := context.WithCancel(ctx)
	worker, err := c.StartDerivedViewWatermarkWorker(workerCtx, []string{projections.DerivedViewBoardRankings}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("StartDerivedViewWatermarkWorker: %v", err)
	}
	if worker.Interval != 10*time.Millisecond {
		t.Fatalf("worker interval = %s, want 10ms", worker.Interval)
	}

	_ = createDerivedViewTestThread(t, c, alice)
	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	waitForDerivedViewApplied(t, c, projections.DerivedViewBoardRankings, head)
	stopWorker()
}

func createDerivedViewTestThread(t *testing.T, c *Core, actor *projections.User) *proto.AckResult {
	t.Helper()
	raw := marshalCoreTestJSON(t, "marshal command", proto.CreateThreadPayload{
		Board: "general",
		Title: "derived view backfill",
		Body:  "operational backfill should repair search and ranking watermarks",
	})
	reply := c.ExecCmd(context.Background(), actor, proto.CmdCreateThread, raw, "")
	if reply.Err != nil {
		t.Fatalf("create thread: %s (%s)", reply.Err.Message, reply.Err.Code)
	}
	return reply.Result
}

func waitForDerivedViewApplied(t *testing.T, c *Core, view string, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := c.DerivedViewAppliedSeq(view)
		if err != nil {
			t.Fatalf("applied %s: %v", view, err)
		}
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, err := c.DerivedViewAppliedSeq(view)
	if err != nil {
		t.Fatalf("applied %s after timeout: %v", view, err)
	}
	t.Fatalf("applied %s = %d after timeout, want %d", view, got, want)
}

func derivedViewMetricValue(samples []metrics.Sample, name, view string) float64 {
	for _, sample := range samples {
		if sample.Name == name && sample.Labels["view"] == view {
			return sample.Value
		}
	}
	return -1
}
