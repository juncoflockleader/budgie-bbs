package core_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestMemoryCounterStoreBacksReactionAndPollReadsWithoutSQLCounterRows(t *testing.T) {
	store := core.NewMemoryCounterStore(8)
	c, err := core.New(filepath.Join(t.TempDir(), "memory-counter-store.db"), core.WithCounterStore(store))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	c.StartOutboxWorker(ctx)

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Memory counters",
		Body:  "react and vote\n[poll]\nPick one\nA\nB\n[/poll]",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v, want one post", posts)
	}
	postID := posts[0].ID
	pollsByPost, err := c.PollsForPosts([]string{postID}, bob.ID)
	if err != nil {
		t.Fatalf("polls for posts: %v", err)
	}
	poll := pollsByPost[postID]
	if poll == nil || len(poll.Options) != 2 {
		t.Fatalf("poll = %+v, want two options", poll)
	}
	optionA := poll.Options[0].ID
	optionB := poll.Options[1].ID

	headBeforeReaction, err := c.Head()
	if err != nil {
		t.Fatalf("head before reaction: %v", err)
	}
	exec(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{Post: postID, Emoji: "heart"})
	headAfterReaction, err := c.Head()
	if err != nil {
		t.Fatalf("head after reaction: %v", err)
	}
	if headAfterReaction != headBeforeReaction {
		t.Fatalf("reaction changed durable head from %d to %d", headBeforeReaction, headAfterReaction)
	}
	assertSQLTableRows(t, c, "post_reactions", 0)
	assertSQLTableRows(t, c, "post_reaction_count_shards", 0)
	assertCoreReactionCount(t, c, postID, 1)
	if !mustUserReacted(t, c, postID, bob.ID) {
		t.Fatal("memory counter store did not record reaction identity")
	}
	posts, err = c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after reaction: %v", err)
	}
	if posts[0].ReactionCount != 1 {
		t.Fatalf("post reaction count = %d, want memory-backed count 1", posts[0].ReactionCount)
	}
	if got := store.ReactionReceivedCount(alice.ID); got != 1 {
		t.Fatalf("memory reactions received = %d, want 1", got)
	}

	exec(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: optionA})
	assertSQLTableRows(t, c, "poll_votes", 0)
	assertSQLTableRows(t, c, "poll_vote_count_shards", 0)
	poll, err = c.GetPoll(poll.ID, bob.ID)
	if err != nil {
		t.Fatalf("get poll after vote: %v", err)
	}
	if poll.Options[0].VoteCount != 1 || poll.Options[1].VoteCount != 0 || poll.Voted != optionA {
		t.Fatalf("poll after option A vote = %+v, voted=%s; want 1/0 and voted option A", poll.Options, poll.Voted)
	}

	exec(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: optionB})
	poll, err = c.GetPoll(poll.ID, bob.ID)
	if err != nil {
		t.Fatalf("get poll after revote: %v", err)
	}
	if poll.Options[0].VoteCount != 0 || poll.Options[1].VoteCount != 1 || poll.Voted != optionB {
		t.Fatalf("poll after option B vote = %+v, voted=%s; want 0/1 and voted option B", poll.Options, poll.Voted)
	}

	checkpointSeq, err := c.CheckpointCounters(context.Background())
	if err != nil {
		t.Fatalf("checkpoint memory counters: %v", err)
	}
	if checkpointSeq <= headBeforeReaction {
		t.Fatalf("checkpoint seq = %d, want durable event after head %d", checkpointSeq, headBeforeReaction)
	}
	assertCounterCheckpointCount(t, c.DB, "post.reactions", postID, 1)
	assertCounterCheckpointCount(t, c.DB, "poll.option_votes", optionA, 0)
	assertCounterCheckpointCount(t, c.DB, "poll.option_votes", optionB, 1)
	assertSQLTableRows(t, c, "post_reactions", 0)
	assertSQLTableRows(t, c, "poll_votes", 0)

	exec(t, c, bob, proto.CmdUnreactPost, proto.ReactPostPayload{Post: postID})
	assertCoreReactionCount(t, c, postID, 0)
	if mustUserReacted(t, c, postID, bob.ID) {
		t.Fatal("memory counter store still reports reaction identity after unreact")
	}
	if got := store.ReactionReceivedCount(alice.ID); got != 0 {
		t.Fatalf("memory reactions received after unreact = %d, want 0", got)
	}
}

func TestMemoryCounterStoreBacksBoardMembershipReactionRequirements(t *testing.T) {
	store := core.NewMemoryCounterStore(8)
	c, err := core.New(filepath.Join(t.TempDir(), "memory-counter-requirements.db"), core.WithCounterStore(store))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:   "selective",
		Name: "Selective",
	})
	exec(t, c, admin, proto.CmdSetBoardMemberRequirements, proto.SetBoardMemberRequirementsPayload{
		Board:             "selective",
		MinScore:          intPtr(1),
		MinBoardMarkCount: intPtr(1),
		ApprovalMode:      stringPtr("auto"),
	})
	thread := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "selective",
		Title: "Store-backed marks",
		Body:  "please mark me",
	})

	execExpectErr(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "selective",
	}, proto.ErrForbidden)

	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v, want one", posts)
	}
	exec(t, c, alice, proto.CmdReactPost, proto.ReactPostPayload{
		Post:  posts[0].ID,
		Emoji: "heart",
	})
	assertSQLTableRows(t, c, "post_reactions", 0)
	assertSQLTableRows(t, c, "post_reaction_count_shards", 0)

	application := exec(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "selective",
	})
	app, err := c.GetBoardMemberApplication(application.ID)
	if err != nil {
		t.Fatalf("get board member application: %v", err)
	}
	if app == nil || app.Status != "approved" || app.ReviewerID != bob.ID {
		t.Fatalf("expected store-backed requirements to auto-approve, got %+v", app)
	}
	member, err := c.UserIsBoardMember("selective", bob.ID)
	if err != nil {
		t.Fatalf("user is board member: %v", err)
	}
	if !member {
		t.Fatal("expected store-backed admission to create board membership")
	}
}

func TestMemoryCounterStoreAccountDeletePurgesCounterIdentity(t *testing.T) {
	store := core.NewMemoryCounterStore(8)
	c, err := core.New(filepath.Join(t.TempDir(), "memory-counter-delete-user.db"), core.WithCounterStore(store))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	admin := registerAndGetUser(t, c, "admin", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	thread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Delete counter identity",
		Body:  "react and vote before deletion\n[poll]\nPick one\nA\nB\n[/poll]",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v, want one", posts)
	}
	postID := posts[0].ID
	pollsByPost, err := c.PollsForPosts([]string{postID}, bob.ID)
	if err != nil {
		t.Fatalf("polls for posts: %v", err)
	}
	poll := pollsByPost[postID]
	if poll == nil || len(poll.Options) == 0 {
		t.Fatalf("poll = %+v, want options", poll)
	}

	exec(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{Post: postID, Emoji: "heart"})
	exec(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: poll.Options[0].ID})
	assertSQLTableRows(t, c, "post_reactions", 0)
	assertSQLTableRows(t, c, "poll_votes", 0)
	assertCoreReactionCount(t, c, postID, 1)
	if got := store.ReactionReceivedCount(admin.ID); got != 1 {
		t.Fatalf("admin reactions received before delete = %d, want 1", got)
	}

	if err := c.DeleteUser(admin.ID, bob.ID, "operator purge"); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if mustUserReacted(t, c, postID, bob.ID) {
		t.Fatal("deleted user's reaction identity survived account deletion")
	}
	if optionID, ok, err := store.PollVote(poll.ID, bob.ID); err != nil || ok {
		t.Fatalf("deleted user's poll vote = %q ok=%v err=%v, want none", optionID, ok, err)
	}
	assertCoreReactionCount(t, c, postID, 0)
	if got := store.ReactionReceivedCount(admin.ID); got != 0 {
		t.Fatalf("admin reactions received after delete = %d, want 0", got)
	}
	poll, err = c.GetPoll(poll.ID, "")
	if err != nil {
		t.Fatalf("get poll after delete: %v", err)
	}
	if poll.Options[0].VoteCount != 0 {
		t.Fatalf("poll option vote count after delete = %d, want 0", poll.Options[0].VoteCount)
	}
}

func assertSQLTableRows(t *testing.T, c *core.Core, table string, want int) {
	t.Helper()
	var got int
	if err := c.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}

func assertCoreReactionCount(t *testing.T, c *core.Core, postID string, want int) {
	t.Helper()
	got, err := c.ReactionCount(postID)
	if err != nil {
		t.Fatalf("reaction count: %v", err)
	}
	if got != want {
		t.Fatalf("reaction count = %d, want %d", got, want)
	}
}

func mustUserReacted(t *testing.T, c *core.Core, postID, userID string) bool {
	t.Helper()
	got, err := c.UserReacted(postID, userID)
	if err != nil {
		t.Fatalf("user reacted: %v", err)
	}
	return got
}
