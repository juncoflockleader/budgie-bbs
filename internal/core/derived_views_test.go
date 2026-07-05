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

	applied, err := c.DerivedViewAppliedSeq(DerivedViewBoardRankings)
	if err != nil {
		t.Fatalf("default derived view applied seq: %v", err)
	}
	if applied != head {
		t.Fatalf("default applied seq = %d, want head %d", applied, head)
	}

	if err := c.RecordDerivedViewApplied(DerivedViewBoardRankings, head-1); err != nil {
		t.Fatalf("record watermark: %v", err)
	}
	applied, err = c.DerivedViewAppliedSeq(DerivedViewBoardRankings)
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
	if got := derivedViewMetricValue(samples, "budgie_derived_view_applied_seq", DerivedViewBoardRankings); got != float64(head-1) {
		t.Fatalf("applied sample = %v, want %d", got, head-1)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", DerivedViewBoardRankings); got != 1 {
		t.Fatalf("lag sample = %v, want 1", got)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", DerivedViewPostSearch); got != 0 {
		t.Fatalf("default search lag sample = %v, want 0", got)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", DerivedViewResidentFeed); got != 0 {
		t.Fatalf("default resident feed lag sample = %v, want 0", got)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", DerivedViewLatestFeed); got != 0 {
		t.Fatalf("default latest feed lag sample = %v, want 0", got)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", DerivedViewBoardSummaries); got != 0 {
		t.Fatalf("default board summaries lag sample = %v, want 0", got)
	}
	if got := derivedViewMetricValue(samples, "budgie_derived_view_lag_events", DerivedViewUnreadThreads); got != 0 {
		t.Fatalf("default unread threads lag sample = %v, want 0", got)
	}
}

func TestResolveDerivedViews(t *testing.T) {
	views, err := ResolveDerivedViews([]string{" rankings.threads ", DerivedViewBoardRankings, "rankings.threads"})
	if err != nil {
		t.Fatalf("ResolveDerivedViews: %v", err)
	}
	want := []string{DerivedViewBoardRankings, DerivedViewThreadRankings}
	if len(views) != len(want) {
		t.Fatalf("views = %v, want %v", views, want)
	}
	for i := range want {
		if views[i] != want[i] {
			t.Fatalf("views = %v, want %v", views, want)
		}
	}

	all, err := ResolveDerivedViews([]string{"all"})
	if err != nil {
		t.Fatalf("ResolveDerivedViews all: %v", err)
	}
	if len(all) != len(KnownDerivedViews()) {
		t.Fatalf("all view count = %d, want %d", len(all), len(KnownDerivedViews()))
	}

	search, err := ResolveDerivedViews([]string{"search"})
	if err != nil {
		t.Fatalf("ResolveDerivedViews search: %v", err)
	}
	assertDerivedViews(t, search, []string{DerivedViewDigestSearch, DerivedViewPostSearch})

	rankings, err := ResolveDerivedViews([]string{"rankings"})
	if err != nil {
		t.Fatalf("ResolveDerivedViews rankings: %v", err)
	}
	assertDerivedViews(t, rankings, []string{
		DerivedViewArchiveRankings,
		DerivedViewBlessingRankings,
		DerivedViewBoardRankings,
		DerivedViewReplyRankings,
		DerivedViewThreadRankings,
		DerivedViewUserRankings,
	})

	mixed, err := ResolveDerivedViews([]string{"search, summaries.boards", "summaries"})
	if err != nil {
		t.Fatalf("ResolveDerivedViews mixed groups: %v", err)
	}
	assertDerivedViews(t, mixed, []string{
		DerivedViewDigestSearch,
		DerivedViewPostSearch,
		DerivedViewBoardSummaries,
		DerivedViewUnreadThreads,
	})

	groups := DerivedViewGroups()
	if len(groups["search"]) != 2 {
		t.Fatalf("search group = %v, want two views", groups["search"])
	}
	if len(groups["feeds"]) != 2 {
		t.Fatalf("feeds group = %v, want two views", groups["feeds"])
	}
	groups["search"][0] = "mutated"
	if again := DerivedViewGroups()["search"][0]; again == "mutated" {
		t.Fatalf("DerivedViewGroups returned mutable backing slice")
	}

	if _, err := ResolveDerivedViews([]string{"rankings.unknown"}); err == nil {
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
	for _, view := range []string{DerivedViewBoardRankings, DerivedViewPostSearch, DerivedViewUserRankings} {
		if err := c.RecordDerivedViewApplied(view, stale); err != nil {
			t.Fatalf("record stale watermark %s: %v", view, err)
		}
	}
	if got, err := c.DerivedViewAppliedSeq(DerivedViewBoardRankings); err != nil || got != stale {
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

	result, err := c.BackfillDerivedViewsFromEventLog([]string{DerivedViewBoardRankings, DerivedViewPostSearch}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog: %v", err)
	}
	if result.HeadSeq != head {
		t.Fatalf("backfill head = %d, want %d", result.HeadSeq, head)
	}
	if len(result.Views) != 2 || result.Views[0] != DerivedViewBoardRankings || result.Views[1] != DerivedViewPostSearch {
		t.Fatalf("backfilled views = %v", result.Views)
	}
	for _, view := range []string{DerivedViewBoardRankings, DerivedViewPostSearch} {
		applied, err := c.DerivedViewAppliedSeq(view)
		if err != nil {
			t.Fatalf("applied %s: %v", view, err)
		}
		if applied != head {
			t.Fatalf("applied %s = %d, want %d", view, applied, head)
		}
	}
	userRankingApplied, err := c.DerivedViewAppliedSeq(DerivedViewUserRankings)
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
	for _, view := range []string{DerivedViewBoardRankings, DerivedViewPostSearch, DerivedViewUserRankings} {
		if err := c.RecordDerivedViewApplied(view, stale); err != nil {
			t.Fatalf("record stale watermark %s: %v", view, err)
		}
	}

	result, err := c.SyncDerivedViewsToHead([]string{DerivedViewPostSearch, DerivedViewBoardRankings})
	if err != nil {
		t.Fatalf("SyncDerivedViewsToHead: %v", err)
	}
	if result.HeadSeq != head {
		t.Fatalf("sync head = %d, want %d", result.HeadSeq, head)
	}
	if len(result.Views) != 2 || result.Views[0] != DerivedViewBoardRankings || result.Views[1] != DerivedViewPostSearch {
		t.Fatalf("synced views = %v", result.Views)
	}
	for _, view := range []string{DerivedViewBoardRankings, DerivedViewPostSearch} {
		applied, err := c.DerivedViewAppliedSeq(view)
		if err != nil {
			t.Fatalf("applied %s: %v", view, err)
		}
		if applied != head {
			t.Fatalf("applied %s = %d, want %d", view, applied, head)
		}
	}
	userRankingApplied, err := c.DerivedViewAppliedSeq(DerivedViewUserRankings)
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
	if err := c.RecordDerivedViewApplied(DerivedViewBoardRankings, 0); err != nil {
		t.Fatalf("record stale watermark: %v", err)
	}

	workerCtx, stopWorker := context.WithCancel(ctx)
	worker, err := c.StartDerivedViewWatermarkWorker(workerCtx, []string{DerivedViewBoardRankings}, 10*time.Millisecond)
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
	waitForDerivedViewApplied(t, c, DerivedViewBoardRankings, head)
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
