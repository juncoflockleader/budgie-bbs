package core

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestUnreadThreadSummariesProcessorMaterializesAndRebuildsSummaries(t *testing.T) {
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
	execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secret",
		MemberReadMode: unreadThreadSummariesBoolPtr(true),
	})

	work := execPostSearchTestCommand(t, c, alice, proto.CmdCreateFavoriteFolder, proto.CreateFavoriteFolderPayload{Name: "Work"})
	child := execPostSearchTestCommand(t, c, alice, proto.CmdCreateFavoriteFolder, proto.CreateFavoriteFolderPayload{Name: "Child", ParentID: work.ID})
	execPostSearchTestCommand(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{Board: "tech", Favorite: true, FolderID: work.ID})
	execPostSearchTestCommand(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{Board: "life", Favorite: true, FolderID: child.ID})

	tech := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "tech", Title: "Tech unread", Body: "first"})
	life := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "life", Title: "Life unread", Body: "first"})
	secret := execPostSearchTestCommand(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "secret", Title: "Secret unread", Body: "hidden"})

	if rows := unreadThreadSummaryStatsRows(t, c); rows != 0 {
		t.Fatalf("unread thread summary stats rows before processor = %d, want 0", rows)
	}
	fallback, err := c.ListUnreadThreadSummaries(alice, false, "", 10, 0)
	if err != nil {
		t.Fatalf("fallback unread threads: %v", err)
	}
	assertThreadPresent(t, fallback, tech.ID)
	assertThreadPresent(t, fallback, life.ID)
	assertThreadAbsent(t, fallback, secret.ID)
	if got := boardNameForUnreadThread(fallback, tech.ID); got != "Tech" {
		t.Fatalf("expected board name Tech for %s, got %q in %+v", tech.ID, got, fallback)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if err := c.RecordDerivedViewApplied(DerivedViewUnreadThreads, head); err != nil {
		t.Fatalf("seed compatibility watermark: %v", err)
	}
	result, err := c.ProcessUnreadThreadSummariesOnce(100)
	if err != nil {
		t.Fatalf("ProcessUnreadThreadSummariesOnce: %v", err)
	}
	if result.FromSeq != head || result.AppliedSeq != head || result.HeadSeq != head || result.Events != 0 || !result.Rebuilt || result.Rows < 3 {
		t.Fatalf("unread thread summaries processor result = %+v, want seeded rebuild at head %d", result, head)
	}
	if rows := unreadThreadSummaryStatsRows(t, c); rows < 3 {
		t.Fatalf("unread thread summary stats rows after processor = %d, want at least 3", rows)
	}

	materialized, err := c.ListUnreadThreadSummaries(alice, false, work.ID, 10, 0)
	if err != nil {
		t.Fatalf("materialized folder unread threads: %v", err)
	}
	assertThreadPresent(t, materialized, tech.ID)
	assertThreadPresent(t, materialized, life.ID)
	assertThreadAbsent(t, materialized, secret.ID)

	adminUnread, err := c.ListUnreadThreadSummaries(admin, false, "", 10, 0)
	if err != nil {
		t.Fatalf("admin materialized unread threads: %v", err)
	}
	assertThreadPresent(t, adminUnread, secret.ID)

	execPostSearchTestCommand(t, c, alice, proto.CmdMarkThreadRead, proto.MarkThreadReadPayload{Thread: tech.ID})
	afterMarkRead, err := c.ListUnreadThreadSummaries(alice, false, work.ID, 10, 0)
	if err != nil {
		t.Fatalf("materialized unread threads after mark-read: %v", err)
	}
	assertThreadAbsent(t, afterMarkRead, tech.ID)
	assertThreadPresent(t, afterMarkRead, life.ID)

	newTech := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "tech", Title: "New candidate", Body: "new"})
	stale, err := c.ListUnreadThreadSummaries(alice, false, "", 10, 0)
	if err != nil {
		t.Fatalf("stale materialized unread threads: %v", err)
	}
	assertThreadAbsent(t, stale, newTech.ID)

	updated, err := c.ProcessUnreadThreadSummariesOnce(100)
	if err != nil {
		t.Fatalf("ProcessUnreadThreadSummariesOnce update: %v", err)
	}
	if updated.FromSeq != head || updated.Events == 0 || !updated.Rebuilt || updated.Rows < result.Rows {
		t.Fatalf("update result = %+v, want event-driven rebuild after %d", updated, head)
	}
	fresh, err := c.ListUnreadThreadSummaries(alice, false, "", 10, 0)
	if err != nil {
		t.Fatalf("fresh materialized unread threads: %v", err)
	}
	assertThreadPresent(t, fresh, newTech.ID)

	if _, err := c.DB.Exec(`DELETE FROM unread_thread_summary_stats`); err != nil {
		t.Fatalf("delete unread thread summary stats rows: %v", err)
	}
	if rows := unreadThreadSummaryStatsRows(t, c); rows != 0 {
		t.Fatalf("unread thread summary stats rows after delete = %d, want 0", rows)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{DerivedViewUnreadThreads}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog summaries.unread_threads: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != DerivedViewUnreadThreads {
		t.Fatalf("backfill result = %+v", backfill)
	}
	repaired, err := c.ListUnreadThreadSummaries(alice, false, "", 10, 0)
	if err != nil {
		t.Fatalf("repaired unread threads: %v", err)
	}
	assertThreadPresent(t, repaired, newTech.ID)
}

func unreadThreadSummariesBoolPtr(v bool) *bool {
	return &v
}

func unreadThreadSummaryStatsRows(t *testing.T, c *Core) int {
	t.Helper()
	count, err := projections.UnreadThreadSummaryStatsRowCount(c.DB)
	if err != nil {
		t.Fatalf("count unread thread summary stats rows: %v", err)
	}
	return count
}

func assertThreadPresent(t *testing.T, threads []projections.ThreadSummary, threadID string) {
	t.Helper()
	if !hasUnreadThread(threads, threadID) {
		t.Fatalf("thread %s missing from unread summaries: %+v", threadID, threads)
	}
}

func assertThreadAbsent(t *testing.T, threads []projections.ThreadSummary, threadID string) {
	t.Helper()
	if hasUnreadThread(threads, threadID) {
		t.Fatalf("thread %s unexpectedly present in unread summaries: %+v", threadID, threads)
	}
}

func hasUnreadThread(threads []projections.ThreadSummary, threadID string) bool {
	for _, thread := range threads {
		if thread.ID == threadID {
			return true
		}
	}
	return false
}

func boardNameForUnreadThread(threads []projections.ThreadSummary, threadID string) string {
	for _, thread := range threads {
		if thread.ID == threadID {
			return thread.BoardName
		}
	}
	return ""
}
