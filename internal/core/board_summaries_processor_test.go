package core

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestBoardSummariesProcessorMaterializesAndRebuildsSummaries(t *testing.T) {
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

	generalThread := execPostSearchTestCommand(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "General news",
		Body:  "first",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "tech",
		Name:        "Tech",
		Description: "Computing desk",
	})
	techThread := execPostSearchTestCommand(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "tech",
		Title: "Hardware notes",
		Body:  "first",
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: techThread.ID,
		Body:   "second",
	})

	if rows := boardSummaryStatsRows(t, c); rows != 0 {
		t.Fatalf("board summary stats rows before processor = %d, want 0", rows)
	}
	fallback, err := c.ListBoardSummaries(alice.ID, false, projections.BoardSummaryOptions{Sort: "posts"})
	if err != nil {
		t.Fatalf("fallback board summaries: %v", err)
	}
	tech := mustFindBoardSummary(t, fallback, "tech")
	if tech.ThreadCount != 1 || tech.PostCount != 2 || tech.UnreadPosts != 2 || tech.UnreadThreads != 1 {
		t.Fatalf("fallback tech summary = %+v, want 1 thread, 2 posts, unread", tech)
	}
	general := mustFindBoardSummary(t, fallback, "general")
	if general.UnreadPosts != 1 || general.UnreadThreads != 1 {
		t.Fatalf("fallback general unread summary = %+v, want unread", general)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if err := c.RecordDerivedViewApplied(projections.DerivedViewBoardSummaries, head); err != nil {
		t.Fatalf("seed compatibility watermark: %v", err)
	}
	result, err := c.ProcessBoardSummariesOnce(100)
	if err != nil {
		t.Fatalf("ProcessBoardSummariesOnce: %v", err)
	}
	if result.FromSeq != head || result.AppliedSeq != head || result.HeadSeq != head || result.Events != 0 || !result.Rebuilt || result.Rows == 0 {
		t.Fatalf("board summaries processor result = %+v, want seeded rebuild at head %d", result, head)
	}
	if rows := boardSummaryStatsRows(t, c); rows == 0 {
		t.Fatalf("board summary stats rows after processor = %d, want rows", rows)
	}
	materialized, err := c.ListBoardSummaries(alice.ID, false, projections.BoardSummaryOptions{Sort: "posts"})
	if err != nil {
		t.Fatalf("materialized board summaries: %v", err)
	}
	tech = mustFindBoardSummary(t, materialized, "tech")
	if tech.ThreadCount != 1 || tech.PostCount != 2 || tech.UnreadPosts != 2 || tech.UnreadThreads != 1 {
		t.Fatalf("materialized tech summary = %+v, want 1 thread, 2 posts, unread", tech)
	}

	execPostSearchTestCommand(t, c, alice, proto.CmdMarkBoardRead, proto.MarkBoardReadPayload{Board: "general"})
	read, err := c.ListBoardSummaries(alice.ID, false)
	if err != nil {
		t.Fatalf("materialized summaries after mark-read: %v", err)
	}
	general = mustFindBoardSummary(t, read, "general")
	if general.UnreadPosts != 0 || general.UnreadThreads != 0 || general.ReadSeq != general.LastSeq {
		t.Fatalf("mark-read should stay live over materialized stats, got %+v", general)
	}

	execPostSearchTestCommand(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: techThread.ID,
		Body:   "third",
	})
	stale, err := c.ListBoardSummaries(alice.ID, false, projections.BoardSummaryOptions{Sort: "posts"})
	if err != nil {
		t.Fatalf("stale materialized board summaries: %v", err)
	}
	tech = mustFindBoardSummary(t, stale, "tech")
	if tech.PostCount != 2 || tech.UnreadPosts != 3 {
		t.Fatalf("expected stale global count but live unread count, got %+v", tech)
	}

	updated, err := c.ProcessBoardSummariesOnce(100)
	if err != nil {
		t.Fatalf("ProcessBoardSummariesOnce update: %v", err)
	}
	if updated.FromSeq != head || updated.Events == 0 || !updated.Rebuilt || updated.Rows < result.Rows {
		t.Fatalf("update result = %+v, want event-driven rebuild after %d", updated, head)
	}
	fresh, err := c.ListBoardSummaries(alice.ID, false, projections.BoardSummaryOptions{Sort: "posts"})
	if err != nil {
		t.Fatalf("fresh materialized board summaries: %v", err)
	}
	tech = mustFindBoardSummary(t, fresh, "tech")
	if tech.PostCount != 3 || tech.UnreadPosts != 3 {
		t.Fatalf("fresh tech summary = %+v, want 3 posts and 3 unread", tech)
	}

	if _, err := c.DB.Exec(`DELETE FROM board_summary_stats`); err != nil {
		t.Fatalf("delete board summary stats rows: %v", err)
	}
	if rows := boardSummaryStatsRows(t, c); rows != 0 {
		t.Fatalf("board summary stats rows after delete = %d, want 0", rows)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{projections.DerivedViewBoardSummaries}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog summaries.boards: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != projections.DerivedViewBoardSummaries {
		t.Fatalf("backfill result = %+v", backfill)
	}
	repaired, err := c.ListBoardSummaries(alice.ID, false, projections.BoardSummaryOptions{Sort: "posts"})
	if err != nil {
		t.Fatalf("repaired board summaries: %v", err)
	}
	tech = mustFindBoardSummary(t, repaired, "tech")
	if tech.PostCount != 3 {
		t.Fatalf("repaired tech summary = %+v, want 3 posts", tech)
	}

	execPostSearchTestCommand(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: generalThread.ID,
		Body:   "after read marker",
	})
	afterGeneralPost, err := c.ListBoardSummaries(alice.ID, true)
	if err != nil {
		t.Fatalf("unread summaries after general post: %v", err)
	}
	general = mustFindBoardSummary(t, afterGeneralPost, "general")
	if general.UnreadPosts != 1 || general.UnreadThreads != 1 {
		t.Fatalf("expected live unread board after post, got %+v", general)
	}
}

func boardSummaryStatsRows(t *testing.T, c *Core) int {
	t.Helper()
	count, err := projections.BoardSummaryStatsRowCount(c.DB)
	if err != nil {
		t.Fatalf("count board summary stats rows: %v", err)
	}
	return count
}

func mustFindBoardSummary(t *testing.T, boards []projections.BoardSummary, boardID string) projections.BoardSummary {
	t.Helper()
	for _, board := range boards {
		if board.ID == boardID {
			return board
		}
	}
	t.Fatalf("board summary %s missing: %+v", boardID, boards)
	return projections.BoardSummary{}
}
