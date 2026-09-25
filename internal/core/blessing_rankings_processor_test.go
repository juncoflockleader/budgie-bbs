package core

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestBlessingRankingsProcessorMaterializesAndRebuildsRankings(t *testing.T) {
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
	if _, err := c.RegisterUser("carol", "pw"); err != nil {
		t.Fatalf("register carol: %v", err)
	}

	execPostSearchTestCommand(t, c, alice, proto.CmdBlessUser, proto.BlessUserPayload{
		User:    "bob",
		Message: "Good luck",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdBlessUser, proto.BlessUserPayload{
		User:    "bob",
		Message: "Ace it",
	})
	execPostSearchTestCommand(t, c, bob, proto.CmdBlessUser, proto.BlessUserPayload{
		User:    "carol",
		Message: "Welcome",
	})

	if rows := blessingRankingStatsRows(t, c); rows != 0 {
		t.Fatalf("blessing ranking stats rows before processor = %d, want 0", rows)
	}
	fallback, err := c.ListBlessingRankings(10, 0)
	if err != nil {
		t.Fatalf("fallback blessing rankings: %v", err)
	}
	assertBlessingRankingTop(t, fallback, "bob", 2)
	assertBlessingRankingCount(t, fallback, "carol", 1)

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if err := c.RecordDerivedViewApplied(projections.DerivedViewBlessingRankings, head); err != nil {
		t.Fatalf("seed compatibility watermark: %v", err)
	}
	result, err := c.ProcessBlessingRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessBlessingRankingsOnce: %v", err)
	}
	if result.FromSeq != head || result.AppliedSeq != head || result.HeadSeq != head || result.Events != 0 || !result.Rebuilt || result.Rows < 2 {
		t.Fatalf("blessing rankings processor result = %+v, want seeded rebuild at head %d", result, head)
	}
	if rows := blessingRankingStatsRows(t, c); rows < 2 {
		t.Fatalf("blessing ranking stats rows after processor = %d, want at least 2", rows)
	}
	materialized, err := c.ListBlessingRankings(10, 0)
	if err != nil {
		t.Fatalf("materialized blessing rankings: %v", err)
	}
	assertBlessingRankingTop(t, materialized, "bob", 2)
	assertBlessingRankingCount(t, materialized, "carol", 1)

	execPostSearchTestCommand(t, c, alice, proto.CmdBlessUser, proto.BlessUserPayload{
		User:    "carol",
		Message: "Another welcome",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdBlessUser, proto.BlessUserPayload{
		User:    "carol",
		Message: "Third cheer",
	})
	stale, err := c.ListBlessingRankings(10, 0)
	if err != nil {
		t.Fatalf("stale materialized blessing rankings: %v", err)
	}
	assertBlessingRankingTop(t, stale, "bob", 2)

	updated, err := c.ProcessBlessingRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessBlessingRankingsOnce update: %v", err)
	}
	if updated.FromSeq != head || updated.Events == 0 || !updated.Rebuilt || updated.Rows < result.Rows {
		t.Fatalf("update result = %+v, want event-driven rebuild after %d", updated, head)
	}
	fresh, err := c.ListBlessingRankings(10, 0)
	if err != nil {
		t.Fatalf("fresh materialized blessing rankings: %v", err)
	}
	assertBlessingRankingTop(t, fresh, "carol", 3)

	if _, err := c.DB.Exec(`DELETE FROM blessing_ranking_stats`); err != nil {
		t.Fatalf("delete blessing ranking stats rows: %v", err)
	}
	if rows := blessingRankingStatsRows(t, c); rows != 0 {
		t.Fatalf("blessing ranking stats rows after delete = %d, want 0", rows)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{projections.DerivedViewBlessingRankings}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog rankings.blessings: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != projections.DerivedViewBlessingRankings {
		t.Fatalf("backfill result = %+v", backfill)
	}
	if rows := blessingRankingStatsRows(t, c); rows < 2 {
		t.Fatalf("blessing ranking stats rows after backfill = %d, want at least 2", rows)
	}
	repaired, err := c.ListBlessingRankings(10, 0)
	if err != nil {
		t.Fatalf("repaired blessing rankings: %v", err)
	}
	assertBlessingRankingTop(t, repaired, "carol", 3)
}

func blessingRankingStatsRows(t *testing.T, c *Core) int {
	t.Helper()
	count, err := projections.BlessingRankingStatsRowCount(c.DB)
	if err != nil {
		t.Fatalf("count blessing ranking stats rows: %v", err)
	}
	return count
}

func assertBlessingRankingTop(t *testing.T, rankings []projections.BlessingRanking, name string, count int) {
	t.Helper()
	if len(rankings) == 0 || rankings[0].Name != name || rankings[0].BlessingCount != count {
		t.Fatalf("blessing rankings top = %+v, want %s with %d blessings", rankings, name, count)
	}
}

func assertBlessingRankingCount(t *testing.T, rankings []projections.BlessingRanking, name string, count int) {
	t.Helper()
	for _, ranking := range rankings {
		if ranking.Name == name {
			if ranking.BlessingCount != count {
				t.Fatalf("blessing ranking %s count = %d, want %d; rankings=%+v", name, ranking.BlessingCount, count, rankings)
			}
			return
		}
	}
	t.Fatalf("blessing ranking %s missing: %+v", name, rankings)
}
