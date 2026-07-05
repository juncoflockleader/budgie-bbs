package core

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestUserRankingsProcessorMaterializesAndRefreshesSideStoreStats(t *testing.T) {
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
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "tech", Name: "Tech"})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "life", Name: "Life"})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "hidden_stats", Name: "Hidden Stats"})
	execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:         "hidden_stats",
		StatsExcluded: userRankingsBoolPtr(true),
	})

	bobThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "tech",
		Title: "Bob topic",
		Body:  "bob root",
	})
	bobReply := execPostSearchTestCommand(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: bobThread.ID,
		Body:   "bob reply",
	})
	hiddenThread := execPostSearchTestCommand(t, c, carol, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "hidden_stats",
		Title: "Hidden topic",
		Body:  "hidden root",
	})
	execPostSearchTestCommand(t, c, carol, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: hiddenThread.ID,
		Body:   "hidden reply",
	})

	bobPosts, err := c.ListPosts(bobThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list bob posts: %v", err)
	}
	if len(bobPosts) == 0 {
		t.Fatal("expected bob posts")
	}
	execPostSearchTestCommand(t, c, alice, proto.CmdReactPost, proto.ReactPostPayload{
		Post:  bobPosts[0].ID,
		Emoji: "+1",
	})
	if err := c.RecordLogin(bob.ID); err != nil {
		t.Fatalf("record bob login: %v", err)
	}

	if rows := userRankingStatsRows(t, c); rows != 0 {
		t.Fatalf("user ranking stats rows before processor = %d, want 0", rows)
	}
	fallback, err := c.ListUserRankings(10, 0)
	if err != nil {
		t.Fatalf("fallback user rankings: %v", err)
	}
	assertUserRankingTop(t, fallback, "bob", 2, 1)
	assertUserRankingPosts(t, fallback, "carol", 0)

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if err := c.RecordDerivedViewApplied(DerivedViewUserRankings, head); err != nil {
		t.Fatalf("seed compatibility watermark: %v", err)
	}
	result, err := c.ProcessUserRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessUserRankingsOnce: %v", err)
	}
	if result.FromSeq != head || result.AppliedSeq != head || result.HeadSeq != head || result.Events != 0 || !result.Rebuilt || result.Rows < 4 {
		t.Fatalf("user rankings processor result = %+v, want seeded rebuild at head %d", result, head)
	}
	if rows := userRankingStatsRows(t, c); rows < 4 {
		t.Fatalf("user ranking stats rows after processor = %d, want at least 4", rows)
	}
	materialized, err := c.ListUserRankings(10, 0)
	if err != nil {
		t.Fatalf("materialized user rankings: %v", err)
	}
	assertUserRankingTop(t, materialized, "bob", 2, 1)
	assertUserRankingPosts(t, materialized, "carol", 0)

	aliceThread := execPostSearchTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "life",
		Title: "Alice topic",
		Body:  "alice root",
	})
	aliceReply := execPostSearchTestCommand(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: aliceThread.ID,
		Body:   "alice reply",
	})
	staleAfterPosts, err := c.ListUserRankings(10, 0)
	if err != nil {
		t.Fatalf("stale user rankings after posts: %v", err)
	}
	assertUserRankingTop(t, staleAfterPosts, "bob", 2, 1)

	updated, err := c.ProcessUserRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessUserRankingsOnce update: %v", err)
	}
	if updated.FromSeq != head || updated.Events == 0 || !updated.Rebuilt || updated.Rows < result.Rows {
		t.Fatalf("update result = %+v, want event-driven rebuild after %d", updated, head)
	}
	afterPosts, err := c.ListUserRankings(10, 0)
	if err != nil {
		t.Fatalf("user rankings after posts: %v", err)
	}
	assertUserRankingTop(t, afterPosts, "bob", 2, 1)
	assertUserRankingPosts(t, afterPosts, "alice", 2)

	alicePosts, err := c.ListPosts(aliceThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list alice posts: %v", err)
	}
	if len(alicePosts) == 0 || aliceReply.ID == "" || bobReply.ID == "" {
		t.Fatal("expected alice and bob reply ids")
	}
	sideStoreHead, err := c.Head()
	if err != nil {
		t.Fatalf("side-store head before: %v", err)
	}
	execPostSearchTestCommand(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{
		Post:  alicePosts[0].ID,
		Emoji: "heart",
	})
	if len(alicePosts) > 1 {
		execPostSearchTestCommand(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{
			Post:  alicePosts[1].ID,
			Emoji: "star",
		})
	}
	if err := c.RecordLogin(alice.ID); err != nil {
		t.Fatalf("record alice login: %v", err)
	}
	afterSideStoreHead, err := c.Head()
	if err != nil {
		t.Fatalf("side-store head after: %v", err)
	}
	if afterSideStoreHead != sideStoreHead {
		t.Fatalf("side-store updates changed durable head from %d to %d", sideStoreHead, afterSideStoreHead)
	}
	staleSideStore, err := c.ListUserRankings(10, 0)
	if err != nil {
		t.Fatalf("stale side-store user rankings: %v", err)
	}
	assertUserRankingTop(t, staleSideStore, "bob", 2, 1)

	sideStoreRefresh, err := c.ProcessUserRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessUserRankingsOnce side-store refresh: %v", err)
	}
	if sideStoreRefresh.Events != 0 || !sideStoreRefresh.Rebuilt {
		t.Fatalf("side-store refresh = %+v, want refresh without durable events", sideStoreRefresh)
	}
	afterSideStore, err := c.ListUserRankings(10, 0)
	if err != nil {
		t.Fatalf("user rankings after side-store refresh: %v", err)
	}
	assertUserRankingTop(t, afterSideStore, "alice", 2, 2)

	if _, err := c.DB.Exec(`DELETE FROM user_ranking_stats`); err != nil {
		t.Fatalf("delete user ranking stats rows: %v", err)
	}
	if rows := userRankingStatsRows(t, c); rows != 0 {
		t.Fatalf("user ranking stats rows after delete = %d, want 0", rows)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{DerivedViewUserRankings}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog rankings.users: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != DerivedViewUserRankings {
		t.Fatalf("backfill result = %+v", backfill)
	}
	if rows := userRankingStatsRows(t, c); rows < 4 {
		t.Fatalf("user ranking stats rows after backfill = %d, want at least 4", rows)
	}
	repaired, err := c.ListUserRankings(10, 0)
	if err != nil {
		t.Fatalf("repaired user rankings: %v", err)
	}
	assertUserRankingTop(t, repaired, "alice", 2, 2)
}

func userRankingsBoolPtr(v bool) *bool {
	return &v
}

func userRankingStatsRows(t *testing.T, c *Core) int {
	t.Helper()
	count, err := projections.UserRankingStatsRowCount(c.DB)
	if err != nil {
		t.Fatalf("count user ranking stats rows: %v", err)
	}
	return count
}

func assertUserRankingTop(t *testing.T, users []projections.UserRanking, name string, posts, reactions int) {
	t.Helper()
	if len(users) == 0 || users[0].Name != name || users[0].PostsCreated != posts || users[0].ReactionsReceived != reactions {
		t.Fatalf("user rankings top = %+v, want %s with %d posts and %d reactions", users, name, posts, reactions)
	}
}

func assertUserRankingPosts(t *testing.T, users []projections.UserRanking, name string, posts int) {
	t.Helper()
	for _, user := range users {
		if user.Name == name {
			if user.PostsCreated != posts {
				t.Fatalf("user %s posts = %d, want %d; rankings=%+v", name, user.PostsCreated, posts, users)
			}
			return
		}
	}
	t.Fatalf("user %s missing from rankings: %+v", name, users)
}
