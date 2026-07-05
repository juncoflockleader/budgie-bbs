package core

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestArchiveRankingsProcessorMaterializesAndRebuildsRankings(t *testing.T) {
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
		MemberReadMode: archiveRankingsBoolPtr(true),
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:         "hidden_stats",
		StatsExcluded: archiveRankingsBoolPtr(true),
	})

	techThread := execPostSearchTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "tech",
		Title: "Archive root",
		Body:  "root",
	})
	techReply := execPostSearchTestCommand(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: techThread.ID,
		Body:   "child",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: techThread.ID,
		Kind:   "archive",
		Title:  "Tech guide root",
		Path:   "guide",
	})
	techEntry := execPostSearchTestCommand(t, c, admin, proto.CmdCuratePost, proto.CuratePostPayload{
		Post:  techReply.ID,
		Kind:  "archive",
		Title: "Tech guide child",
		Path:  "guide",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdSetDigestEntryBody, proto.SetDigestEntryBodyPayload{
		Entry: techEntry.ID,
		Body:  "Edited archive ranking copy",
	})
	lifeThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "life",
		Title: "Life archive",
		Body:  "root",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: lifeThread.ID,
		Kind:   "archive",
		Title:  "Life reference",
		Path:   "reference",
	})
	secretThread := execPostSearchTestCommand(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private archive",
		Body:  "hidden",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: secretThread.ID,
		Kind:   "archive",
		Title:  "Secret reference",
		Path:   "private",
	})
	hiddenThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "hidden_stats",
		Title: "Hidden archive",
		Body:  "hidden",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: hiddenThread.ID,
		Kind:   "archive",
		Title:  "Hidden reference",
		Path:   "hidden",
	})

	if rows := archiveRankingStatsRows(t, c); rows != 0 {
		t.Fatalf("archive ranking stats rows before processor = %d, want 0", rows)
	}
	fallback, err := c.ListArchiveRankings(alice, "archive", 10, 0)
	if err != nil {
		t.Fatalf("fallback archive rankings: %v", err)
	}
	assertArchiveRankingTop(t, fallback, "tech", "guide", 2, 1)
	assertArchiveRankingAbsent(t, fallback, "secret")
	assertArchiveRankingAbsent(t, fallback, "hidden_stats")

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if err := c.RecordDerivedViewApplied(projections.DerivedViewArchiveRankings, head); err != nil {
		t.Fatalf("seed compatibility watermark: %v", err)
	}
	result, err := c.ProcessArchiveRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessArchiveRankingsOnce: %v", err)
	}
	if result.FromSeq != head || result.AppliedSeq != head || result.HeadSeq != head || result.Events != 0 || !result.Rebuilt || result.Rows < 4 {
		t.Fatalf("archive rankings processor result = %+v, want seeded rebuild at head %d", result, head)
	}
	if rows := archiveRankingStatsRows(t, c); rows < 4 {
		t.Fatalf("archive ranking stats rows after processor = %d, want at least 4", rows)
	}
	materialized, err := c.ListArchiveRankings(alice, "archive", 10, 0)
	if err != nil {
		t.Fatalf("materialized archive rankings: %v", err)
	}
	assertArchiveRankingTop(t, materialized, "tech", "guide", 2, 1)
	assertArchiveRankingAbsent(t, materialized, "secret")
	assertArchiveRankingAbsent(t, materialized, "hidden_stats")

	adminArchives, err := c.ListArchiveRankings(admin, "archive", 10, 0)
	if err != nil {
		t.Fatalf("admin materialized archive rankings: %v", err)
	}
	if _, ok := findArchiveRanking(adminArchives, "secret", "private"); !ok {
		t.Fatalf("admin should see member-read archive ranking, got %+v", adminArchives)
	}

	lifeReplyOne := execPostSearchTestCommand(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: lifeThread.ID,
		Body:   "life child one",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdCuratePost, proto.CuratePostPayload{
		Post:  lifeReplyOne.ID,
		Kind:  "archive",
		Title: "Life reference child one",
		Path:  "reference",
	})
	lifeReplyTwo := execPostSearchTestCommand(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: lifeThread.ID,
		Body:   "life child two",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdCuratePost, proto.CuratePostPayload{
		Post:  lifeReplyTwo.ID,
		Kind:  "archive",
		Title: "Life reference child two",
		Path:  "reference",
	})
	stale, err := c.ListArchiveRankings(alice, "archive", 10, 0)
	if err != nil {
		t.Fatalf("stale materialized archive rankings: %v", err)
	}
	assertArchiveRankingTop(t, stale, "tech", "guide", 2, 1)

	updated, err := c.ProcessArchiveRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessArchiveRankingsOnce update: %v", err)
	}
	if updated.FromSeq != head || updated.Events == 0 || !updated.Rebuilt || updated.Rows < result.Rows {
		t.Fatalf("update result = %+v, want event-driven rebuild after %d", updated, head)
	}
	fresh, err := c.ListArchiveRankings(alice, "archive", 10, 0)
	if err != nil {
		t.Fatalf("fresh materialized archive rankings: %v", err)
	}
	assertArchiveRankingTop(t, fresh, "life", "reference", 3, 0)

	if _, err := c.DB.Exec(`DELETE FROM archive_ranking_stats`); err != nil {
		t.Fatalf("delete archive ranking stats rows: %v", err)
	}
	if rows := archiveRankingStatsRows(t, c); rows != 0 {
		t.Fatalf("archive ranking stats rows after delete = %d, want 0", rows)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{projections.DerivedViewArchiveRankings}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog rankings.archives: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != projections.DerivedViewArchiveRankings {
		t.Fatalf("backfill result = %+v", backfill)
	}
	if rows := archiveRankingStatsRows(t, c); rows < 4 {
		t.Fatalf("archive ranking stats rows after backfill = %d, want at least 4", rows)
	}
	repaired, err := c.ListArchiveRankings(alice, "archive", 10, 0)
	if err != nil {
		t.Fatalf("repaired archive rankings: %v", err)
	}
	assertArchiveRankingTop(t, repaired, "life", "reference", 3, 0)
}

func archiveRankingsBoolPtr(v bool) *bool {
	return &v
}

func archiveRankingStatsRows(t *testing.T, c *Core) int {
	t.Helper()
	count, err := projections.ArchiveRankingStatsRowCount(c.DB)
	if err != nil {
		t.Fatalf("count archive ranking stats rows: %v", err)
	}
	return count
}

func assertArchiveRankingTop(t *testing.T, rankings []projections.ArchiveRanking, boardID, path string, entryCount, editedCount int) {
	t.Helper()
	if len(rankings) == 0 ||
		rankings[0].BoardID != boardID ||
		rankings[0].Path != path ||
		rankings[0].EntryCount != entryCount ||
		rankings[0].EditedCount != editedCount {
		t.Fatalf("archive rankings top = %+v, want %s/%s with %d entries and %d edited", rankings, boardID, path, entryCount, editedCount)
	}
}

func assertArchiveRankingAbsent(t *testing.T, rankings []projections.ArchiveRanking, boardID string) {
	t.Helper()
	for _, ranking := range rankings {
		if ranking.BoardID == boardID {
			t.Fatalf("archive rankings include %s, want absent: %+v", boardID, rankings)
		}
	}
}

func findArchiveRanking(rankings []projections.ArchiveRanking, boardID, path string) (projections.ArchiveRanking, bool) {
	for _, ranking := range rankings {
		if ranking.BoardID == boardID && ranking.Path == path {
			return ranking, true
		}
	}
	return projections.ArchiveRanking{}, false
}
