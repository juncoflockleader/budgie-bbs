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

func TestBoardAutomodMultipleActions(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "combo", Name: "Combo"})
	// One rule, two actions: redact the post AND ban the author.
	exec(t, c, admin, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "combo", MatchType: "keyword", Pattern: "badword", Action: "redact,board_ban", Reason: "banned word",
	})
	base := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "combo", Title: "topic", Body: "welcome"})

	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{Thread: base.ID, Body: "contains badword here"})
	posts, _ := c.ListPosts(base.ID, 50, 0)
	redacted := 0
	for _, p := range posts {
		if p.Redacted {
			redacted++
		}
	}
	if redacted != 1 {
		t.Fatalf("expected the offending post redacted, got %d redacted of %+v", redacted, posts)
	}
	// alice was also banned -> her next post is rejected.
	execExpectErr(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{Thread: base.ID, Body: "hello again"}, proto.ErrBanned)

	activity, _ := c.ListBoardAutomodActivity("combo", 50, 0)
	var actions []string
	for _, a := range activity {
		actions = append(actions, a.Action)
	}
	for _, want := range []string{"redact", "board_ban"} {
		seen := false
		for _, a := range actions {
			if a == want {
				seen = true
			}
		}
		if !seen {
			t.Fatalf("audit log missing %q from multi-action rule; got %v", want, actions)
		}
	}
}

func TestBoardAutomodStaffExempt(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	admin := registerAndGetUser(t, c, "admin", "pw")
	carol := registerAndGetUser(t, c, "carol", "pw") // board moderator
	dave := registerAndGetUser(t, c, "dave", "pw")   // ordinary user

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "vip", Name: "VIP"})
	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{Board: "vip", User: carol.ID, Moderator: true})
	exec(t, c, admin, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "vip", MatchType: "keyword", Pattern: "nope", Action: "redact",
	})

	// The board moderator is exempt: her matching post is NOT redacted.
	modThread := exec(t, c, carol, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "vip", Title: "mod post", Body: "this says nope"})
	modPosts, _ := c.ListPosts(modThread.ID, 10, 0)
	if len(modPosts) != 1 || modPosts[0].Redacted {
		t.Fatalf("board moderator should be exempt from automod: %+v", modPosts)
	}
	// An ordinary user's matching post is still redacted.
	userThread := exec(t, c, dave, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "vip", Title: "user post", Body: "this says nope"})
	userPosts, _ := c.ListPosts(userThread.ID, 10, 0)
	if len(userPosts) != 1 || !userPosts[0].Redacted {
		t.Fatalf("ordinary user's matching post should be redacted: %+v", userPosts)
	}
}

func TestBoardAutomodRepost(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "src", Name: "Src"})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "dst", Name: "Dst"})
	exec(t, c, admin, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "dst", MatchType: "keyword", Pattern: "badword", Action: "redact", Reason: "no",
	})

	srcThread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "src", Title: "topic", Body: "contains badword here"})
	srcPosts, _ := c.ListPosts(srcThread.ID, 10, 0)
	if len(srcPosts) == 0 {
		t.Fatal("no source post")
	}

	// alice reposts the offending content into dst, where automod redacts it.
	rep := exec(t, c, alice, proto.CmdRepostPost, proto.RepostPostPayload{Post: srcPosts[0].ID, Board: "dst", Title: "reposted"})
	dstPosts, _ := c.ListPosts(rep.ID, 10, 0)
	if len(dstPosts) != 1 || !dstPosts[0].Redacted {
		t.Fatalf("reposted banned content should be redacted on dst: %+v", dstPosts)
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
