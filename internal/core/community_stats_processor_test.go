package core

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestCommunityStatsProcessorMaterializesAndRefreshesSnapshot(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	if rows := communityStatsSnapshotRows(t, c); rows != 0 {
		t.Fatalf("community stats snapshot rows before processor = %d, want 0", rows)
	}
	fallback, err := c.GetCommunityStats()
	if err != nil {
		t.Fatalf("fallback community stats: %v", err)
	}
	if fallback.TotalUsers != 2 || fallback.TotalBoards == 0 {
		t.Fatalf("unexpected fallback stats: %+v", fallback)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if err := c.RecordDerivedViewApplied(DerivedViewCommunityStats, head); err != nil {
		t.Fatalf("seed compatibility watermark: %v", err)
	}
	result, err := c.ProcessCommunityStatsOnce(100)
	if err != nil {
		t.Fatalf("ProcessCommunityStatsOnce: %v", err)
	}
	if result.FromSeq != head || result.AppliedSeq != head || result.HeadSeq != head || result.Events != 0 || !result.Rebuilt || result.Rows == 0 {
		t.Fatalf("community stats processor result = %+v, want seeded rebuild at head %d", result, head)
	}
	if rows := communityStatsSnapshotRows(t, c); rows != 1 {
		t.Fatalf("community stats snapshot rows after processor = %d, want 1", rows)
	}
	materialized, err := c.GetCommunityStats()
	if err != nil {
		t.Fatalf("materialized community stats: %v", err)
	}
	if materialized.TotalUsers != fallback.TotalUsers || materialized.TotalPosts != fallback.TotalPosts || materialized.HeadSeq != fallback.HeadSeq {
		t.Fatalf("materialized stats = %+v, want fallback %+v", materialized, fallback)
	}

	execPostSearchTestCommand(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Stats candidate",
		Body:  "first",
	})
	stale, err := c.GetCommunityStats()
	if err != nil {
		t.Fatalf("stale materialized community stats: %v", err)
	}
	if stale.TotalPosts != materialized.TotalPosts || stale.TotalThreads != materialized.TotalThreads {
		t.Fatalf("expected stale materialized counts before processor, got stale=%+v materialized=%+v", stale, materialized)
	}

	updated, err := c.ProcessCommunityStatsOnce(100)
	if err != nil {
		t.Fatalf("ProcessCommunityStatsOnce update: %v", err)
	}
	if updated.FromSeq != head || updated.Events == 0 || !updated.Rebuilt || updated.Rows == 0 {
		t.Fatalf("update result = %+v, want event-driven rebuild after %d", updated, head)
	}
	fresh, err := c.GetCommunityStats()
	if err != nil {
		t.Fatalf("fresh materialized community stats: %v", err)
	}
	if fresh.TotalPosts != materialized.TotalPosts+1 || fresh.TotalThreads != materialized.TotalThreads+1 {
		t.Fatalf("fresh stats = %+v, want post/thread counts advanced from %+v", fresh, materialized)
	}

	ts := projections.NowMS()
	if err := projections.SetUserPresence(c.DB, alice.ID, "web", "active", "", "", "", "", "", ts); err != nil {
		t.Fatalf("set user presence: %v", err)
	}
	presenceStale, err := c.GetCommunityStats()
	if err != nil {
		t.Fatalf("presence-stale materialized community stats: %v", err)
	}
	if presenceStale.OnlineUsers != fresh.OnlineUsers {
		t.Fatalf("expected stale online user count before no-event refresh, got stale=%+v fresh=%+v", presenceStale, fresh)
	}
	noEventRefresh, err := c.ProcessCommunityStatsOnce(100)
	if err != nil {
		t.Fatalf("ProcessCommunityStatsOnce no-event refresh: %v", err)
	}
	if noEventRefresh.Events != 0 || !noEventRefresh.Rebuilt || noEventRefresh.Rows == 0 {
		t.Fatalf("no-event refresh result = %+v, want rebuild without durable events", noEventRefresh)
	}
	presenceFresh, err := c.GetCommunityStats()
	if err != nil {
		t.Fatalf("presence-fresh materialized community stats: %v", err)
	}
	if presenceFresh.OnlineUsers < 1 {
		t.Fatalf("expected no-event refresh to capture online user, got %+v", presenceFresh)
	}

	if _, err := c.DB.Exec(`DELETE FROM community_stats_snapshot`); err != nil {
		t.Fatalf("delete community stats snapshot: %v", err)
	}
	if rows := communityStatsSnapshotRows(t, c); rows != 0 {
		t.Fatalf("community stats snapshot rows after delete = %d, want 0", rows)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{DerivedViewCommunityStats}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog community_stats: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != DerivedViewCommunityStats {
		t.Fatalf("backfill result = %+v", backfill)
	}
	if rows := communityStatsSnapshotRows(t, c); rows != 1 {
		t.Fatalf("community stats snapshot rows after backfill = %d, want 1", rows)
	}
	repaired, err := c.GetCommunityStats()
	if err != nil {
		t.Fatalf("repaired community stats: %v", err)
	}
	if repaired.TotalPosts != 1 || repaired.TotalThreads != 1 {
		t.Fatalf("repaired stats = %+v, want rebuilt post/thread counts", repaired)
	}
}

func communityStatsSnapshotRows(t *testing.T, c *Core) int {
	t.Helper()
	count, err := projections.CommunityStatsSnapshotRowCount(c.DB)
	if err != nil {
		t.Fatalf("count community stats snapshot rows: %v", err)
	}
	return count
}
