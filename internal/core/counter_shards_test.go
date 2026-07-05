package core_test

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestReactionCounterShardsTrackLifecycleAndRebuild(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	carol := registerAndGetUser(t, c, "carol", "pw")

	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Sharded reactions",
		Body:  "react here",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	postID := posts[0].ID

	exec(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{Post: postID, Emoji: "heart"})
	exec(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{Post: postID, Emoji: "star"})
	exec(t, c, carol, proto.CmdReactPost, proto.ReactPostPayload{Post: postID, Emoji: "heart"})
	assertPostReactionShardTotal(t, c, postID, 2)

	exec(t, c, bob, proto.CmdUnreactPost, proto.ReactPostPayload{Post: postID})
	assertPostReactionShardTotal(t, c, postID, 1)

	posts, err = c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after unreact: %v", err)
	}
	if got := posts[0].ReactionCount; got != 1 {
		t.Fatalf("post reaction count = %d, want 1", got)
	}

	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatalf("rebuild projections: %v", err)
	}
	assertPostReactionShardTotal(t, c, postID, 1)
}

func TestPollVoteCounterShardsTrackReplacementAndCheckpoint(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Sharded votes",
		Body:  "choose\n[poll]\nPick one\nA\nB\n[/poll]",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v, want one poll post", posts)
	}
	pollsByPost, err := c.PollsForPosts([]string{posts[0].ID}, alice.ID)
	if err != nil {
		t.Fatalf("polls for post: %v", err)
	}
	poll := pollsByPost[posts[0].ID]
	if poll == nil || len(poll.Options) != 2 {
		t.Fatalf("poll = %+v, want one poll with two options", poll)
	}
	optionA := poll.Options[0].ID
	optionB := poll.Options[1].ID

	exec(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: optionA})
	assertPollVoteShardTotal(t, c, optionA, 1)
	assertPollVoteShardTotal(t, c, optionB, 0)

	exec(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: optionB})
	assertPollVoteShardTotal(t, c, optionA, 0)
	assertPollVoteShardTotal(t, c, optionB, 1)

	poll, err = c.GetPoll(poll.ID, bob.ID)
	if err != nil {
		t.Fatalf("get poll: %v", err)
	}
	if poll.Options[0].VoteCount != 0 || poll.Options[1].VoteCount != 1 {
		t.Fatalf("poll counts = %+v, want replacement vote on option B", poll.Options)
	}

	if _, err := c.CheckpointCounters(context.Background()); err != nil {
		t.Fatalf("checkpoint counters: %v", err)
	}
	assertCounterCheckpointCount(t, c.DB, "poll.option_votes", optionA, 0)
	assertCounterCheckpointCount(t, c.DB, "poll.option_votes", optionB, 1)
}

func TestReactionCounterShardsFeedRankingsAndStats(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Shard-fed read models",
		Body:  "aggregate me",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	postID := posts[0].ID

	exec(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{Post: postID, Emoji: "heart"})
	if _, err := c.DB.Exec(testRebind(`UPDATE post_reaction_count_shards SET count_value=? WHERE post_id=?`), 5, postID); err != nil {
		t.Fatalf("skew post reaction shard total: %v", err)
	}
	assertPostReactionShardTotal(t, c, postID, 5)

	assertThreadRankingReactionCount(t, c, alice, thread.ID, 5)
	assertUserRankingReactionCount(t, c, "alice", 5)
	assertCommunityTotalReactions(t, c, 5)

	if _, err := c.ProcessThreadRankingsOnce(100); err != nil {
		t.Fatalf("process thread rankings: %v", err)
	}
	assertThreadRankingReactionCount(t, c, alice, thread.ID, 5)
	if _, err := c.ProcessUserRankingsOnce(100); err != nil {
		t.Fatalf("process user rankings: %v", err)
	}
	assertUserRankingReactionCount(t, c, "alice", 5)
	if _, err := c.ProcessCommunityStatsOnce(100); err != nil {
		t.Fatalf("process community stats: %v", err)
	}
	assertCommunityTotalReactions(t, c, 5)
}

func assertPostReactionShardTotal(t *testing.T, c *core.Core, postID string, want int) {
	t.Helper()
	var got int
	if err := c.DB.QueryRow(testRebind(`SELECT COALESCE(SUM(count_value), 0) FROM post_reaction_count_shards WHERE post_id=?`), postID).Scan(&got); err != nil {
		t.Fatalf("query post reaction shard total: %v", err)
	}
	if got != want {
		t.Fatalf("post reaction shard total = %d, want %d", got, want)
	}
}

func assertPollVoteShardTotal(t *testing.T, c *core.Core, optionID string, want int) {
	t.Helper()
	var got int
	if err := c.DB.QueryRow(testRebind(`SELECT COALESCE(SUM(count_value), 0) FROM poll_vote_count_shards WHERE option_id=?`), optionID).Scan(&got); err != nil {
		t.Fatalf("query poll vote shard total: %v", err)
	}
	if got != want {
		t.Fatalf("poll vote shard total = %d, want %d", got, want)
	}
}

func assertThreadRankingReactionCount(t *testing.T, c *core.Core, viewer *projections.User, threadID string, want int) {
	t.Helper()
	threads, err := c.ListThreadRankings(viewer, "", 10, 0)
	if err != nil {
		t.Fatalf("list thread rankings: %v", err)
	}
	for _, thread := range threads {
		if thread.ID == threadID {
			if thread.ReactionCount != want {
				t.Fatalf("thread ranking reaction count = %d, want %d; rankings=%+v", thread.ReactionCount, want, threads)
			}
			return
		}
	}
	t.Fatalf("thread %s missing from rankings: %+v", threadID, threads)
}

func assertUserRankingReactionCount(t *testing.T, c *core.Core, name string, want int) {
	t.Helper()
	users, err := c.ListUserRankings(10, 0)
	if err != nil {
		t.Fatalf("list user rankings: %v", err)
	}
	for _, user := range users {
		if user.Name == name {
			if user.ReactionsReceived != want {
				t.Fatalf("user ranking reactions = %d, want %d; rankings=%+v", user.ReactionsReceived, want, users)
			}
			return
		}
	}
	t.Fatalf("user %s missing from rankings: %+v", name, users)
}

func assertCommunityTotalReactions(t *testing.T, c *core.Core, want int) {
	t.Helper()
	stats, err := c.GetCommunityStats()
	if err != nil {
		t.Fatalf("get community stats: %v", err)
	}
	if stats.TotalReactions != want {
		t.Fatalf("community total reactions = %d, want %d; stats=%+v", stats.TotalReactions, want, stats)
	}
}
