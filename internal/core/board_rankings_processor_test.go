package core

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestBoardRankingsProcessorMaterializesAndRebuildsRankings(t *testing.T) {
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

	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "tech", Name: "Tech"})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "life", Name: "Life"})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "secret", Name: "Secret"})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "hidden_stats", Name: "Hidden Stats"})
	execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secret",
		MemberReadMode: boardRankingsBoolPtr(true),
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:         "hidden_stats",
		StatsExcluded: boardRankingsBoolPtr(true),
	})

	techThread := execPostSearchTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "tech",
		Title: "Hot topic",
		Body:  "first",
	})
	execPostSearchTestCommand(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: techThread.ID,
		Body:   "second",
	})
	lifeThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "life",
		Title: "Quiet topic",
		Body:  "first",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private topic",
		Body:  "hidden",
	})
	hiddenThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "hidden_stats",
		Title: "Hidden topic",
		Body:  "hidden",
	})
	execPostSearchTestCommand(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: hiddenThread.ID,
		Body:   "hidden reply",
	})

	if rows := boardRankingStatsRows(t, c); rows != 0 {
		t.Fatalf("board ranking stats rows before processor = %d, want 0", rows)
	}
	fallback, err := c.ListBoardRankings(alice, 10, 0)
	if err != nil {
		t.Fatalf("fallback board rankings: %v", err)
	}
	assertBoardRankingTop(t, fallback, "tech", 2)
	assertBoardRankingAbsent(t, fallback, "secret")
	assertBoardRankingAbsent(t, fallback, "hidden_stats")

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if err := c.RecordDerivedViewApplied(projections.DerivedViewBoardRankings, head); err != nil {
		t.Fatalf("seed compatibility watermark: %v", err)
	}
	result, err := c.ProcessBoardRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessBoardRankingsOnce: %v", err)
	}
	if result.FromSeq != head || result.AppliedSeq != head || result.HeadSeq != head || result.Events != 0 || !result.Rebuilt || result.Rows < 4 {
		t.Fatalf("board rankings processor result = %+v, want seeded rebuild at head %d", result, head)
	}
	if rows := boardRankingStatsRows(t, c); rows < 4 {
		t.Fatalf("board ranking stats rows after processor = %d, want at least 4", rows)
	}
	materialized, err := c.ListBoardRankings(alice, 10, 0)
	if err != nil {
		t.Fatalf("materialized board rankings: %v", err)
	}
	assertBoardRankingTop(t, materialized, "tech", 2)
	assertBoardRankingAbsent(t, materialized, "secret")
	assertBoardRankingAbsent(t, materialized, "hidden_stats")

	adminBoards, err := c.ListBoardRankings(admin, 10, 0)
	if err != nil {
		t.Fatalf("admin materialized board rankings: %v", err)
	}
	if _, ok := findBoardRanking(adminBoards, "secret"); !ok {
		t.Fatalf("admin should see member-read board ranking, got %+v", adminBoards)
	}

	for i := 0; i < 3; i++ {
		execPostSearchTestCommand(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
			Thread: lifeThread.ID,
			Body:   "life reply",
		})
	}
	stale, err := c.ListBoardRankings(alice, 10, 0)
	if err != nil {
		t.Fatalf("stale materialized board rankings: %v", err)
	}
	assertBoardRankingTop(t, stale, "tech", 2)

	updated, err := c.ProcessBoardRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessBoardRankingsOnce update: %v", err)
	}
	if updated.FromSeq != head || !updated.Rebuilt || updated.Rows < result.Rows {
		t.Fatalf("update result = %+v, want rebuild after %d", updated, head)
	}
	fresh, err := c.ListBoardRankings(alice, 10, 0)
	if err != nil {
		t.Fatalf("fresh materialized board rankings: %v", err)
	}
	assertBoardRankingTop(t, fresh, "life", 4)

	if _, err := c.DB.Exec(`DELETE FROM board_ranking_stats`); err != nil {
		t.Fatalf("delete board ranking stats rows: %v", err)
	}
	if rows := boardRankingStatsRows(t, c); rows != 0 {
		t.Fatalf("board ranking stats rows after delete = %d, want 0", rows)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{projections.DerivedViewBoardRankings}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog rankings.boards: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != projections.DerivedViewBoardRankings {
		t.Fatalf("backfill result = %+v", backfill)
	}
	if rows := boardRankingStatsRows(t, c); rows < 4 {
		t.Fatalf("board ranking stats rows after backfill = %d, want at least 4", rows)
	}
	repaired, err := c.ListBoardRankings(alice, 10, 0)
	if err != nil {
		t.Fatalf("repaired board rankings: %v", err)
	}
	assertBoardRankingTop(t, repaired, "life", 4)
}

func boardRankingsBoolPtr(v bool) *bool {
	return &v
}

func boardRankingStatsRows(t *testing.T, c *Core) int {
	t.Helper()
	count, err := projections.BoardRankingStatsRowCount(c.DB)
	if err != nil {
		t.Fatalf("count board ranking stats rows: %v", err)
	}
	return count
}

func assertBoardRankingTop(t *testing.T, boards []projections.BoardRanking, boardID string, postCount int) {
	t.Helper()
	if len(boards) == 0 || boards[0].ID != boardID || boards[0].PostCount != postCount {
		t.Fatalf("board rankings top = %+v, want %s with %d posts", boards, boardID, postCount)
	}
}

func assertBoardRankingAbsent(t *testing.T, boards []projections.BoardRanking, boardID string) {
	t.Helper()
	if _, ok := findBoardRanking(boards, boardID); ok {
		t.Fatalf("board rankings include %s, want absent: %+v", boardID, boards)
	}
}

func findBoardRanking(boards []projections.BoardRanking, boardID string) (projections.BoardRanking, bool) {
	for _, board := range boards {
		if board.ID == boardID {
			return board, true
		}
	}
	return projections.BoardRanking{}, false
}
