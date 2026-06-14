package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestResidentFeedProcessorMaterializesAndRebuildsFeed(t *testing.T) {
	c, err := New(filepath.Join(t.TempDir(), "resident-feed-processor.db"))
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

	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "club", Name: "Club"})
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "lab", Name: "Lab"})
	for _, board := range []string{"club", "lab"} {
		execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
			Board:  board,
			User:   "alice",
			Member: true,
			Title:  "resident",
		})
	}

	generalThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "General public",
		Body:  "ordinary board post",
	})
	clubThread := execPostSearchTestCommand(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "club",
		Title: "Resident club",
		Body:  "member board post",
	})
	labThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "lab",
		Title: "Resident lab",
		Body:  "public resident board post",
	})

	if rows := residentFeedRowCount(t, c); rows != 0 {
		t.Fatalf("resident feed rows before processor = %d, want 0", rows)
	}
	fallback, err := c.ListResidentBoardPosts(alice.ID, 10, 0)
	if err != nil {
		t.Fatalf("fallback resident feed: %v", err)
	}
	assertResidentFeedThreads(t, fallback, []string{clubThread.ID, labThread.ID}, []string{generalThread.ID})

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	result, err := c.ProcessResidentFeedOnce(100)
	if err != nil {
		t.Fatalf("ProcessResidentFeedOnce: %v", err)
	}
	if result.FromSeq != 0 || result.AppliedSeq != head || result.HeadSeq != head || result.FeedChanges < 3 {
		t.Fatalf("resident processor result = %+v, want catch-up through head %d with feed changes", result, head)
	}
	rowsAfterProcessor := residentFeedRowCount(t, c)
	if rowsAfterProcessor < 3 {
		t.Fatalf("resident feed rows after processor = %d, want at least 3", rowsAfterProcessor)
	}
	materialized, err := c.ListResidentBoardPosts(alice.ID, 10, 0)
	if err != nil {
		t.Fatalf("materialized resident feed: %v", err)
	}
	assertResidentFeedThreads(t, materialized, []string{clubThread.ID, labThread.ID}, []string{generalThread.ID})

	labPosts, err := c.ListPosts(labThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list lab posts: %v", err)
	}
	if len(labPosts) != 1 {
		t.Fatalf("lab posts = %+v, want one", labPosts)
	}
	execPostSearchTestCommand(t, c, admin, proto.CmdRedactPost, proto.RedactPostPayload{
		Post:   labPosts[0].ID,
		Reason: "hide from feed",
	})
	redactResult, err := c.ProcessResidentFeedOnce(100)
	if err != nil {
		t.Fatalf("ProcessResidentFeedOnce redact: %v", err)
	}
	if redactResult.FeedChanges == 0 {
		t.Fatalf("redact result = %+v, want feed cleanup", redactResult)
	}
	rowsAfterRedact := residentFeedRowCount(t, c)
	if rowsAfterRedact != rowsAfterProcessor-1 {
		t.Fatalf("resident feed rows after redact = %d, want %d", rowsAfterRedact, rowsAfterProcessor-1)
	}
	afterRedact, err := c.ListResidentBoardPosts(alice.ID, 10, 0)
	if err != nil {
		t.Fatalf("resident feed after redact: %v", err)
	}
	assertResidentFeedThreads(t, afterRedact, []string{clubThread.ID}, []string{generalThread.ID, labThread.ID})

	if _, err := c.DB.Exec(`DELETE FROM resident_feed_posts`); err != nil {
		t.Fatalf("delete resident feed rows: %v", err)
	}
	if rows := residentFeedRowCount(t, c); rows != 0 {
		t.Fatalf("resident feed rows after delete = %d, want 0", rows)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{DerivedViewResidentFeed}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog feeds.resident: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != DerivedViewResidentFeed {
		t.Fatalf("backfill result = %+v", backfill)
	}
	if rows := residentFeedRowCount(t, c); rows != rowsAfterRedact {
		t.Fatalf("resident feed rows after backfill = %d, want %d", rows, rowsAfterRedact)
	}
	repaired, err := c.ListResidentBoardPosts(alice.ID, 10, 0)
	if err != nil {
		t.Fatalf("resident feed after backfill: %v", err)
	}
	assertResidentFeedThreads(t, repaired, []string{clubThread.ID}, []string{generalThread.ID, labThread.ID})
}

func residentFeedRowCount(t *testing.T, c *Core) int {
	t.Helper()
	var count int
	if err := c.DB.QueryRow(`SELECT COUNT(*) FROM resident_feed_posts`).Scan(&count); err != nil {
		t.Fatalf("count resident feed rows: %v", err)
	}
	return count
}

func assertResidentFeedThreads(t *testing.T, posts []Post, wantThreads, absentThreads []string) {
	t.Helper()
	got := map[string]bool{}
	for _, post := range posts {
		got[post.Thread] = true
	}
	for _, threadID := range wantThreads {
		if !got[threadID] {
			t.Fatalf("resident feed threads = %v, want %s present; posts=%+v", got, threadID, posts)
		}
	}
	for _, threadID := range absentThreads {
		if got[threadID] {
			t.Fatalf("resident feed threads = %v, want %s absent; posts=%+v", got, threadID, posts)
		}
	}
}
