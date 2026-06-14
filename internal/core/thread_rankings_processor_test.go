package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestThreadRankingsProcessorMaterializesAndRebuildsRankings(t *testing.T) {
	c, err := New(filepath.Join(t.TempDir(), "thread-rankings-processor.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
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
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "tech", Name: "Tech"})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "life", Name: "Life"})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "secret", Name: "Secret"})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "hidden_stats", Name: "Hidden Stats"})
	execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secret",
		MemberReadMode: threadRankingsBoolPtr(true),
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:         "hidden_stats",
		StatsExcluded: threadRankingsBoolPtr(true),
	})

	techThread := execPostSearchTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "tech",
		Title: "Hot topic",
		Body:  "first",
	})
	execPostSearchTestCommand(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: techThread.ID,
		Body:   "second",
	})
	lifeThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "life",
		Title: "Quiet topic",
		Body:  "first",
	})
	secretThread := execPostSearchTestCommand(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private topic",
		Body:  "hidden",
	})
	hiddenThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "hidden_stats",
		Title: "Hidden topic",
		Body:  "hidden",
	})

	techPosts, err := c.ListPosts(techThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list tech posts: %v", err)
	}
	if len(techPosts) == 0 {
		t.Fatal("expected tech posts")
	}
	execPostSearchTestCommand(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{
		Post:  techPosts[0].ID,
		Emoji: "+1",
	})

	if rows := threadRankingStatsRows(t, c); rows != 0 {
		t.Fatalf("thread ranking stats rows before processor = %d, want 0", rows)
	}
	fallback, err := c.ListThreadRankings(alice, "", 10, 0)
	if err != nil {
		t.Fatalf("fallback thread rankings: %v", err)
	}
	assertThreadRankingTop(t, fallback, techThread.ID, 2, 1)
	assertThreadRankingAbsent(t, fallback, secretThread.ID)
	assertThreadRankingAbsent(t, fallback, hiddenThread.ID)

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if err := c.RecordDerivedViewApplied(DerivedViewThreadRankings, head); err != nil {
		t.Fatalf("seed compatibility watermark: %v", err)
	}
	result, err := c.ProcessThreadRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessThreadRankingsOnce: %v", err)
	}
	if result.FromSeq != head || result.AppliedSeq != head || result.HeadSeq != head || result.Events != 0 || !result.Rebuilt || result.Rows < 4 {
		t.Fatalf("thread rankings processor result = %+v, want seeded rebuild at head %d", result, head)
	}
	if rows := threadRankingStatsRows(t, c); rows < 4 {
		t.Fatalf("thread ranking stats rows after processor = %d, want at least 4", rows)
	}
	materialized, err := c.ListThreadRankings(alice, "", 10, 0)
	if err != nil {
		t.Fatalf("materialized thread rankings: %v", err)
	}
	assertThreadRankingTop(t, materialized, techThread.ID, 2, 1)
	assertThreadRankingAbsent(t, materialized, secretThread.ID)
	assertThreadRankingAbsent(t, materialized, hiddenThread.ID)

	adminThreads, err := c.ListThreadRankings(admin, "", 10, 0)
	if err != nil {
		t.Fatalf("admin materialized thread rankings: %v", err)
	}
	if _, ok := findThreadRanking(adminThreads, secretThread.ID); !ok {
		t.Fatalf("admin should see member-read thread ranking, got %+v", adminThreads)
	}

	for i := 0; i < 3; i++ {
		execPostSearchTestCommand(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
			Thread: lifeThread.ID,
			Body:   "life reply",
		})
	}
	stale, err := c.ListThreadRankings(alice, "", 10, 0)
	if err != nil {
		t.Fatalf("stale materialized thread rankings: %v", err)
	}
	assertThreadRankingTop(t, stale, techThread.ID, 2, 1)

	updated, err := c.ProcessThreadRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessThreadRankingsOnce update: %v", err)
	}
	if updated.FromSeq != head || updated.Events == 0 || !updated.Rebuilt || updated.Rows < result.Rows {
		t.Fatalf("update result = %+v, want event-driven rebuild after %d", updated, head)
	}
	fresh, err := c.ListThreadRankings(alice, "", 10, 0)
	if err != nil {
		t.Fatalf("fresh materialized thread rankings: %v", err)
	}
	assertThreadRankingTop(t, fresh, lifeThread.ID, 4, 0)

	lifePosts, err := c.ListPosts(lifeThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list life posts: %v", err)
	}
	if len(lifePosts) == 0 {
		t.Fatal("expected life posts")
	}
	reactionHead, err := c.Head()
	if err != nil {
		t.Fatalf("reaction head before: %v", err)
	}
	execPostSearchTestCommand(t, c, alice, proto.CmdReactPost, proto.ReactPostPayload{
		Post:  lifePosts[0].ID,
		Emoji: "heart",
	})
	afterReactionHead, err := c.Head()
	if err != nil {
		t.Fatalf("reaction head after: %v", err)
	}
	if afterReactionHead != reactionHead {
		t.Fatalf("reaction changed durable head from %d to %d; want unordered side-store write", reactionHead, afterReactionHead)
	}
	reactionStale, err := c.ListThreadRankings(alice, "", 10, 0)
	if err != nil {
		t.Fatalf("reaction-stale materialized thread rankings: %v", err)
	}
	assertThreadRankingTop(t, reactionStale, lifeThread.ID, 4, 0)
	reactionRefresh, err := c.ProcessThreadRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessThreadRankingsOnce reaction refresh: %v", err)
	}
	if reactionRefresh.Events != 0 || !reactionRefresh.Rebuilt {
		t.Fatalf("reaction refresh = %+v, want refresh without durable events", reactionRefresh)
	}
	afterReaction, err := c.ListThreadRankings(alice, "", 10, 0)
	if err != nil {
		t.Fatalf("thread rankings after reaction refresh: %v", err)
	}
	assertThreadRankingTop(t, afterReaction, lifeThread.ID, 4, 1)

	if _, err := c.DB.Exec(`DELETE FROM thread_ranking_stats`); err != nil {
		t.Fatalf("delete thread ranking stats rows: %v", err)
	}
	if rows := threadRankingStatsRows(t, c); rows != 0 {
		t.Fatalf("thread ranking stats rows after delete = %d, want 0", rows)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{DerivedViewThreadRankings}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog rankings.threads: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != DerivedViewThreadRankings {
		t.Fatalf("backfill result = %+v", backfill)
	}
	if rows := threadRankingStatsRows(t, c); rows < 4 {
		t.Fatalf("thread ranking stats rows after backfill = %d, want at least 4", rows)
	}
	repaired, err := c.ListThreadRankings(alice, "", 10, 0)
	if err != nil {
		t.Fatalf("repaired thread rankings: %v", err)
	}
	assertThreadRankingTop(t, repaired, lifeThread.ID, 4, 1)
}

func threadRankingsBoolPtr(v bool) *bool {
	return &v
}

func threadRankingStatsRows(t *testing.T, c *Core) int {
	t.Helper()
	count, err := threadRankingStatsRowCount(c.DB)
	if err != nil {
		t.Fatalf("count thread ranking stats rows: %v", err)
	}
	return count
}

func assertThreadRankingTop(t *testing.T, threads []ThreadRanking, threadID string, postCount, reactionCount int) {
	t.Helper()
	if len(threads) == 0 || threads[0].ID != threadID || threads[0].PostCount != postCount || threads[0].ReactionCount != reactionCount {
		t.Fatalf("thread rankings top = %+v, want %s with %d posts and %d reactions", threads, threadID, postCount, reactionCount)
	}
}

func assertThreadRankingAbsent(t *testing.T, threads []ThreadRanking, threadID string) {
	t.Helper()
	if _, ok := findThreadRanking(threads, threadID); ok {
		t.Fatalf("thread rankings include %s, want absent: %+v", threadID, threads)
	}
}

func findThreadRanking(threads []ThreadRanking, threadID string) (ThreadRanking, bool) {
	for _, thread := range threads {
		if thread.ID == threadID {
			return thread, true
		}
	}
	return ThreadRanking{}, false
}
