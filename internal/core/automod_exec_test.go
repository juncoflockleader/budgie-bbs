package core_test

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestBoardAutomodExecution(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "garden", Name: "Garden"})

	// Rule 1: keyword "spam" -> redact the post.
	exec(t, c, admin, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "garden", Priority: 1, MatchType: "keyword", Pattern: "spam", Action: "redact", Reason: "no spam",
	})
	// Rule 2: >=2 links -> board mute the author.
	exec(t, c, admin, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "garden", Priority: 2, MatchType: "link_count", Threshold: 2, Action: "board_mute", Reason: "link spam",
	})

	// A clean thread is unaffected.
	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "garden", Title: "Hello", Body: "a friendly intro"})
	posts, _ := c.ListPosts(base.ID, 50, 0)
	if len(posts) != 1 || posts[0].Redacted {
		t.Fatalf("clean post should not be redacted: %+v", posts)
	}

	// A post containing "spam" is auto-redacted.
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{Thread: base.ID, Body: "buy cheap spam now"})
	posts, _ = c.ListPosts(base.ID, 50, 0)
	var redacted int
	for _, p := range posts {
		if p.Redacted {
			redacted++
		}
	}
	if redacted != 1 {
		t.Fatalf("expected exactly one redacted post, got %d (%+v)", redacted, posts)
	}

	// A post with two links trips the board_mute rule; the author is then muted.
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{Thread: base.ID, Body: "see http://a.test and https://b.test"})
	// The next post by alice is rejected because she is now muted on the board.
	execExpectErr(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{Thread: base.ID, Body: "another message"}, proto.ErrMuted)

	// A keyword -> manual_review rule enqueues a moderation review.
	exec(t, c, admin, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "garden", Priority: 0, MatchType: "keyword", Pattern: "report-me", Action: "manual_review", Reason: "flagged",
	})
	bob := registerAndGetUser(t, c, "bob", "pw")
	exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "garden", Title: "report-me please", Body: "look here"})
	reviews, err := c.ListModerationReviews("open", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range reviews {
		if r.Kind == "automod" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an automod moderation review, got %+v", reviews)
	}

	// Every fired rule is recorded in the audit log.
	activity, err := c.ListBoardAutomodActivity("garden", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, a := range activity {
		actions = append(actions, a.Action)
	}
	for _, want := range []string{"redact", "board_mute", "manual_review"} {
		seen := false
		for _, a := range actions {
			if a == want {
				seen = true
			}
		}
		if !seen {
			t.Fatalf("audit log missing %q action; got %v", want, actions)
		}
	}
}

func TestBoardAutomodRateThreshold(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "speed", Name: "Speed"})
	// More than 2 posts within an hour -> redact further posts.
	exec(t, c, admin, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "speed", MatchType: "rate_threshold", Threshold: 2, WindowSec: 3600, Action: "redact", Reason: "slow down",
	})

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "speed", Title: "go", Body: "first"})
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{Thread: base.ID, Body: "second"})
	// Third post: alice already has 2 posts in the window -> tripped -> redacted.
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{Thread: base.ID, Body: "third"})

	posts, _ := c.ListPosts(base.ID, 50, 0)
	if len(posts) != 3 {
		t.Fatalf("expected 3 posts, got %d", len(posts))
	}
	if posts[0].Redacted || posts[1].Redacted {
		t.Fatalf("first two posts should not be redacted: %+v", posts)
	}
	if !posts[2].Redacted {
		t.Fatalf("third post should be redacted by rate_threshold: %+v", posts)
	}
}
