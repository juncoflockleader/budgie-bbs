package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestReplyRankingsProcessorMaterializesAndRebuildsRankings(t *testing.T) {
	c, err := New(filepath.Join(t.TempDir(), "reply-rankings-processor.db"))
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
		MemberReadMode: replyRankingsBoolPtr(true),
	})
	execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:         "hidden_stats",
		StatsExcluded: replyRankingsBoolPtr(true),
	})

	techThread := execPostSearchTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "tech",
		Title: "Hot topic",
		Body:  "first",
	})
	techReply := execPostSearchTestCommand(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: techThread.ID,
		Body:   "tech reply body",
	})
	lifeThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "life",
		Title: "Quiet topic",
		Body:  "life root",
	})
	secretThread := execPostSearchTestCommand(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private topic",
		Body:  "secret root",
	})
	secretReply := execPostSearchTestCommand(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: secretThread.ID,
		Body:   "secret reply body",
	})
	hiddenThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "hidden_stats",
		Title: "Hidden topic",
		Body:  "hidden root",
	})
	hiddenReply := execPostSearchTestCommand(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: hiddenThread.ID,
		Body:   "hidden reply body",
	})

	if rows := replyRankingPostsRows(t, c); rows != 0 {
		t.Fatalf("reply ranking rows before processor = %d, want 0", rows)
	}
	fallback, err := c.ListReplyRankings(alice, 10, 0)
	if err != nil {
		t.Fatalf("fallback reply rankings: %v", err)
	}
	assertReplyRankingTop(t, fallback, techReply.ID, "tech reply")
	assertReplyRankingAbsent(t, fallback, secretReply.ID)
	assertReplyRankingAbsent(t, fallback, hiddenReply.ID)

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if err := c.RecordDerivedViewApplied(DerivedViewReplyRankings, head); err != nil {
		t.Fatalf("seed compatibility watermark: %v", err)
	}
	result, err := c.ProcessReplyRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessReplyRankingsOnce: %v", err)
	}
	if result.FromSeq != head || result.AppliedSeq != head || result.HeadSeq != head || result.Events != 0 || !result.Rebuilt || result.Rows < 3 {
		t.Fatalf("reply rankings processor result = %+v, want seeded rebuild at head %d", result, head)
	}
	if rows := replyRankingPostsRows(t, c); rows < 3 {
		t.Fatalf("reply ranking rows after processor = %d, want at least 3", rows)
	}
	materialized, err := c.ListReplyRankings(alice, 10, 0)
	if err != nil {
		t.Fatalf("materialized reply rankings: %v", err)
	}
	assertReplyRankingTop(t, materialized, techReply.ID, "tech reply")
	assertReplyRankingAbsent(t, materialized, secretReply.ID)
	assertReplyRankingAbsent(t, materialized, hiddenReply.ID)

	adminReplies, err := c.ListReplyRankings(admin, 10, 0)
	if err != nil {
		t.Fatalf("admin materialized reply rankings: %v", err)
	}
	if _, ok := findReplyRanking(adminReplies, secretReply.ID); !ok {
		t.Fatalf("admin should see member-read reply ranking, got %+v", adminReplies)
	}

	lifeReply := execPostSearchTestCommand(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: lifeThread.ID,
		Body:   "life reply body",
	})
	stale, err := c.ListReplyRankings(alice, 10, 0)
	if err != nil {
		t.Fatalf("stale materialized reply rankings: %v", err)
	}
	assertReplyRankingTop(t, stale, techReply.ID, "tech reply")

	updated, err := c.ProcessReplyRankingsOnce(100)
	if err != nil {
		t.Fatalf("ProcessReplyRankingsOnce update: %v", err)
	}
	if updated.FromSeq != head || updated.Events == 0 || !updated.Rebuilt || updated.Rows < result.Rows {
		t.Fatalf("update result = %+v, want event-driven rebuild after %d", updated, head)
	}
	fresh, err := c.ListReplyRankings(alice, 10, 0)
	if err != nil {
		t.Fatalf("fresh materialized reply rankings: %v", err)
	}
	assertReplyRankingTop(t, fresh, lifeReply.ID, "life reply")

	if _, err := c.DB.Exec(`DELETE FROM reply_ranking_posts`); err != nil {
		t.Fatalf("delete reply ranking rows: %v", err)
	}
	if rows := replyRankingPostsRows(t, c); rows != 0 {
		t.Fatalf("reply ranking rows after delete = %d, want 0", rows)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{DerivedViewReplyRankings}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog rankings.replies: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != DerivedViewReplyRankings {
		t.Fatalf("backfill result = %+v", backfill)
	}
	if rows := replyRankingPostsRows(t, c); rows < 4 {
		t.Fatalf("reply ranking rows after backfill = %d, want at least 4", rows)
	}
	repaired, err := c.ListReplyRankings(alice, 10, 0)
	if err != nil {
		t.Fatalf("repaired reply rankings: %v", err)
	}
	assertReplyRankingTop(t, repaired, lifeReply.ID, "life reply")
}

func replyRankingsBoolPtr(v bool) *bool {
	return &v
}

func replyRankingPostsRows(t *testing.T, c *Core) int {
	t.Helper()
	count, err := replyRankingPostsRowCount(c.DB)
	if err != nil {
		t.Fatalf("count reply ranking rows: %v", err)
	}
	return count
}

func assertReplyRankingTop(t *testing.T, replies []ReplyRanking, postID string, excerptPart string) {
	t.Helper()
	if len(replies) == 0 || replies[0].PostID != postID || !strings.Contains(replies[0].Excerpt, excerptPart) {
		t.Fatalf("reply rankings top = %+v, want %s containing %q", replies, postID, excerptPart)
	}
}

func assertReplyRankingAbsent(t *testing.T, replies []ReplyRanking, postID string) {
	t.Helper()
	if _, ok := findReplyRanking(replies, postID); ok {
		t.Fatalf("reply rankings include %s, want absent: %+v", postID, replies)
	}
}

func findReplyRanking(replies []ReplyRanking, postID string) (ReplyRanking, bool) {
	for _, reply := range replies {
		if reply.PostID == postID {
			return reply, true
		}
	}
	return ReplyRanking{}, false
}
