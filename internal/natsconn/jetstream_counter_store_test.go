package natsconn

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/counterstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	nats "github.com/nats-io/nats.go"
)

func TestJetStreamCounterStoreTracksReactionLifecycle(t *testing.T) {
	store := newJetStreamCounterStoreWithKV(newFakeCounterStoreKV(), 8)
	mutation := beginJetStreamCounterMutation(t, store)
	if err := mutation.UpsertReaction("post-1", "bob", "heart", 100); err != nil {
		t.Fatalf("upsert reaction: %v", err)
	}
	if err := mutation.RecordReactionReceived("alice"); err != nil {
		t.Fatalf("record reaction received: %v", err)
	}
	if got, err := mutation.ReactionCount("post-1"); err != nil || got != 1 {
		t.Fatalf("mutation reaction count = %d, %v; want 1, nil", got, err)
	}
	if err := mutation.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertJetStreamUserReacted(t, store, "post-1", "bob", true)
	assertJetStreamReactionCount(t, store, "post-1", 1)
	assertJetStreamReactionReceivedCount(t, store, "alice", 1)

	mutation = beginJetStreamCounterMutation(t, store)
	if err := mutation.UpsertReaction("post-1", "bob", "star", 200); err != nil {
		t.Fatalf("upsert existing reaction: %v", err)
	}
	if err := mutation.Commit(); err != nil {
		t.Fatalf("commit existing: %v", err)
	}
	assertJetStreamReactionCount(t, store, "post-1", 1)

	mutation = beginJetStreamCounterMutation(t, store)
	if err := mutation.DeleteReaction("post-1", "bob"); err != nil {
		t.Fatalf("delete reaction: %v", err)
	}
	if err := mutation.RecordReactionRemoved("alice"); err != nil {
		t.Fatalf("record reaction removed: %v", err)
	}
	if err := mutation.Commit(); err != nil {
		t.Fatalf("commit delete: %v", err)
	}
	assertJetStreamUserReacted(t, store, "post-1", "bob", false)
	assertJetStreamReactionCount(t, store, "post-1", 0)
	assertJetStreamReactionReceivedCount(t, store, "alice", 0)
}

func TestJetStreamCounterStoreTracksPollVoteReplacement(t *testing.T) {
	store := newJetStreamCounterStoreWithKV(newFakeCounterStoreKV(), 8)
	mutation := beginJetStreamCounterMutation(t, store)
	if err := mutation.CastVote("poll-1", "option-a", "bob", 100); err != nil {
		t.Fatalf("cast option a: %v", err)
	}
	if err := mutation.Commit(); err != nil {
		t.Fatalf("commit option a: %v", err)
	}
	assertJetStreamPollVote(t, store, "poll-1", "bob", "option-a", true)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-a", 1)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-b", 0)

	mutation = beginJetStreamCounterMutation(t, store)
	if err := mutation.CastVote("poll-1", "option-b", "bob", 200); err != nil {
		t.Fatalf("cast option b: %v", err)
	}
	if err := mutation.Commit(); err != nil {
		t.Fatalf("commit option b: %v", err)
	}
	assertJetStreamPollVote(t, store, "poll-1", "bob", "option-b", true)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-a", 0)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-b", 1)

	mutation = beginJetStreamCounterMutation(t, store)
	if err := mutation.CastVote("poll-1", "option-b", "bob", 300); err != nil {
		t.Fatalf("cast option b again: %v", err)
	}
	if err := mutation.Commit(); err != nil {
		t.Fatalf("commit option b again: %v", err)
	}
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-a", 0)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-b", 1)
}

func TestJetStreamCounterStoreRollbackRestoresAppliedOperations(t *testing.T) {
	store := newJetStreamCounterStoreWithKV(newFakeCounterStoreKV(), 8)
	mutation := beginJetStreamCounterMutation(t, store)
	if err := mutation.UpsertReaction("post-1", "bob", "heart", 100); err != nil {
		t.Fatalf("upsert reaction: %v", err)
	}
	if err := mutation.RecordReactionReceived("alice"); err != nil {
		t.Fatalf("record reaction received: %v", err)
	}
	if err := mutation.CastVote("poll-1", "option-a", "bob", 100); err != nil {
		t.Fatalf("cast vote: %v", err)
	}
	if got, err := mutation.ReactionCount("post-1"); err != nil || got != 1 {
		t.Fatalf("mutation reaction count = %d, %v; want 1, nil", got, err)
	}
	if err := mutation.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	assertJetStreamUserReacted(t, store, "post-1", "bob", false)
	assertJetStreamReactionCount(t, store, "post-1", 0)
	assertJetStreamReactionReceivedCount(t, store, "alice", 0)
	assertJetStreamPollVote(t, store, "poll-1", "bob", "", false)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-a", 0)
}

func TestJetStreamCounterStoreListsAndDeletesUserCounterIdentity(t *testing.T) {
	store := newJetStreamCounterStoreWithKV(newFakeCounterStoreKV(), 8)
	mutation := beginJetStreamCounterMutation(t, store)
	for _, userID := range []string{"bob", "carol"} {
		if err := mutation.UpsertReaction("post-1", userID, "heart", 100); err != nil {
			t.Fatalf("upsert reaction for %s: %v", userID, err)
		}
		if err := mutation.RecordReactionReceived("alice"); err != nil {
			t.Fatalf("record reaction received for %s: %v", userID, err)
		}
		if err := mutation.CastVote("poll-1", "option-a", userID, 100); err != nil {
			t.Fatalf("cast vote for %s: %v", userID, err)
		}
	}
	if err := mutation.RecordReactionReceived("bob"); err != nil {
		t.Fatalf("record bob reaction received: %v", err)
	}
	if err := mutation.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	assertJetStreamReactionCount(t, store, "post-1", 2)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-a", 2)
	assertJetStreamReactionReceivedCount(t, store, "alice", 2)
	assertJetStreamReactionReceivedCount(t, store, "bob", 1)

	identity, err := store.UserCounterIdentity("bob")
	if err != nil {
		t.Fatalf("user counter identity: %v", err)
	}
	if len(identity.Reactions) != 1 || identity.Reactions[0].PostID != "post-1" {
		t.Fatalf("bob reaction identities = %+v, want post-1", identity.Reactions)
	}
	if len(identity.PollVotes) != 1 || identity.PollVotes[0].PollID != "poll-1" || identity.PollVotes[0].OptionID != "option-a" {
		t.Fatalf("bob poll identities = %+v, want poll-1 option-a", identity.PollVotes)
	}

	mutation = beginJetStreamCounterMutation(t, store)
	if err := mutation.DeleteReaction("post-1", "bob"); err != nil {
		t.Fatalf("delete bob reaction: %v", err)
	}
	if err := mutation.RecordReactionRemoved("alice"); err != nil {
		t.Fatalf("record bob reaction removed: %v", err)
	}
	if err := mutation.DeletePollVote("poll-1", "bob"); err != nil {
		t.Fatalf("delete bob poll vote: %v", err)
	}
	if err := mutation.ClearReactionReceived("bob"); err != nil {
		t.Fatalf("clear bob received count: %v", err)
	}
	if err := mutation.Commit(); err != nil {
		t.Fatalf("commit cleanup: %v", err)
	}

	identity, err = store.UserCounterIdentity("bob")
	if err != nil {
		t.Fatalf("user counter identity after cleanup: %v", err)
	}
	if len(identity.Reactions) != 0 || len(identity.PollVotes) != 0 {
		t.Fatalf("bob identities after cleanup = %+v, want none", identity)
	}
	assertJetStreamUserReacted(t, store, "post-1", "bob", false)
	assertJetStreamUserReacted(t, store, "post-1", "carol", true)
	assertJetStreamReactionCount(t, store, "post-1", 1)
	assertJetStreamPollVote(t, store, "poll-1", "bob", "", false)
	assertJetStreamPollVote(t, store, "poll-1", "carol", "option-a", true)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-a", 1)
	assertJetStreamReactionReceivedCount(t, store, "alice", 1)
	assertJetStreamReactionReceivedCount(t, store, "bob", 0)
}

func TestJetStreamCounterStoreRebuildsAggregateShardsFromIdentity(t *testing.T) {
	store := newJetStreamCounterStoreWithKV(newFakeCounterStoreKV(), 8)
	mutation := beginJetStreamCounterMutation(t, store)
	for _, userID := range []string{"bob", "carol"} {
		if err := mutation.UpsertReaction("post-1", userID, "heart", 100); err != nil {
			t.Fatalf("upsert reaction for %s: %v", userID, err)
		}
		if err := mutation.CastVote("poll-1", "option-a", userID, 100); err != nil {
			t.Fatalf("cast vote for %s: %v", userID, err)
		}
	}
	if err := mutation.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertJetStreamReactionCount(t, store, "post-1", 2)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-a", 2)

	reactionShardKeys := map[string]bool{}
	pollShardKeys := map[string]bool{}
	for _, userID := range []string{"bob", "carol"} {
		reactionShardKeys[jetStreamReactionCountKey("post-1", store.shardForIdentity(userID))] = true
		pollShardKeys[jetStreamPollOptionVoteCountKey("poll-1", "option-a", store.shardForIdentity(userID))] = true
	}
	for key := range reactionShardKeys {
		if err := store.deleteCountRecord(key); err != nil {
			t.Fatalf("delete reaction shard %s: %v", key, err)
		}
	}
	for key := range pollShardKeys {
		if err := store.deleteCountRecord(key); err != nil {
			t.Fatalf("delete poll shard %s: %v", key, err)
		}
	}
	if err := store.setCountRecord(jetStreamReactionCountKey("stale-post", 0), 9, 200); err != nil {
		t.Fatalf("seed stale reaction shard: %v", err)
	}
	if err := store.setCountRecord(jetStreamPollOptionVoteCountKey("poll-stale", "option-z", 0), 7, 200); err != nil {
		t.Fatalf("seed stale poll shard: %v", err)
	}
	assertJetStreamReactionCount(t, store, "post-1", 0)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-a", 0)
	assertJetStreamReactionCount(t, store, "stale-post", 9)
	assertJetStreamPollOptionVoteCount(t, store, "poll-stale", "option-z", 7)

	result, err := store.RebuildAggregateShardsFromIdentity(300)
	if err != nil {
		t.Fatalf("rebuild aggregate shards: %v", err)
	}
	if result.ReactionIdentityRecords != 2 || result.PollVoteIdentityRecords != 2 {
		t.Fatalf("identity records = %+v, want two reactions and two votes", result)
	}
	if result.ReactionShardRecords != len(reactionShardKeys) || result.PollVoteShardRecords != len(pollShardKeys) {
		t.Fatalf("rebuilt shard records = %+v, want %d reaction and %d poll shards", result, len(reactionShardKeys), len(pollShardKeys))
	}
	if result.DeletedShardRecords != 2 {
		t.Fatalf("deleted shard records = %d, want stale reaction and poll shards removed", result.DeletedShardRecords)
	}
	assertJetStreamReactionCount(t, store, "post-1", 2)
	assertJetStreamPollOptionVoteCount(t, store, "poll-1", "option-a", 2)
	assertJetStreamReactionCount(t, store, "stale-post", 0)
	assertJetStreamPollOptionVoteCount(t, store, "poll-stale", "option-z", 0)
	assertJetStreamUserReacted(t, store, "post-1", "bob", true)
	assertJetStreamPollVote(t, store, "poll-1", "carol", "option-a", true)
}

func TestJetStreamCounterStoreBacksCoreReadsAndCheckpoints(t *testing.T) {
	store := newJetStreamCounterStoreWithKV(newFakeCounterStoreKV(), 8)
	c, err := core.New(filepath.Join(t.TempDir(), "nats-counter-store.db"), core.WithCounterStore(store))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice := registerNATSCounterStoreUser(t, c, "alice")
	bob := registerNATSCounterStoreUser(t, c, "bob")
	thread := execNATSCounterStoreCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "NATS KV counters",
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
	polls, err := c.PollsForPosts([]string{postID}, bob.ID)
	if err != nil {
		t.Fatalf("polls for posts: %v", err)
	}
	poll := polls[postID]
	if poll == nil || len(poll.Options) != 2 {
		t.Fatalf("poll = %+v, want two options", poll)
	}
	optionA := poll.Options[0].ID
	optionB := poll.Options[1].ID

	execNATSCounterStoreCommand(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{Post: postID, Emoji: "heart"})
	assertNATSCounterSQLRows(t, c, "post_reactions", 0)
	assertNATSCounterSQLRows(t, c, "post_reaction_count_shards", 0)
	assertCoreNATSReactionCount(t, c, postID, 1)
	assertJetStreamReactionReceivedCount(t, store, alice.ID, 1)

	execNATSCounterStoreCommand(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: optionA})
	assertNATSCounterSQLRows(t, c, "poll_votes", 0)
	assertNATSCounterSQLRows(t, c, "poll_vote_count_shards", 0)
	execNATSCounterStoreCommand(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: optionB})
	poll, err = c.GetPoll(poll.ID, bob.ID)
	if err != nil {
		t.Fatalf("get poll: %v", err)
	}
	if poll.Options[0].VoteCount != 0 || poll.Options[1].VoteCount != 1 || poll.Voted != optionB {
		t.Fatalf("poll after revote = %+v voted=%s, want 0/1 voted option B", poll.Options, poll.Voted)
	}

	if _, err := c.CheckpointCounters(context.Background()); err != nil {
		t.Fatalf("checkpoint counters: %v", err)
	}
	assertNATSCounterCheckpoint(t, c, "post.reactions", postID, 1)
	assertNATSCounterCheckpoint(t, c, "poll.option_votes", optionA, 0)
	assertNATSCounterCheckpoint(t, c, "poll.option_votes", optionB, 1)
}

func TestCounterShardFailureChaosRepairRecoversCoreReadsAndCheckpoints(t *testing.T) {
	store := newJetStreamCounterStoreWithKV(newFakeCounterStoreKV(), 8)
	c, err := core.New(filepath.Join(t.TempDir(), "nats-counter-chaos.db"), core.WithCounterStore(store))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice := registerNATSCounterStoreUser(t, c, "alice")
	bob := registerNATSCounterStoreUser(t, c, "bob")
	thread := execNATSCounterStoreCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "counter shard failure chaos",
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
	polls, err := c.PollsForPosts([]string{postID}, bob.ID)
	if err != nil {
		t.Fatalf("polls for posts: %v", err)
	}
	poll := polls[postID]
	if poll == nil || len(poll.Options) != 2 {
		t.Fatalf("poll = %+v, want two options", poll)
	}
	optionA := poll.Options[0].ID

	execNATSCounterStoreCommand(t, c, bob, proto.CmdReactPost, proto.ReactPostPayload{Post: postID, Emoji: "heart"})
	execNATSCounterStoreCommand(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: optionA})
	assertCoreNATSReactionCount(t, c, postID, 1)
	poll = assertNATSCounterPollCounts(t, c, poll.ID, bob.ID, optionA, 1, 0)

	reactionShardKey := jetStreamReactionCountKey(postID, store.shardForIdentity(bob.ID))
	pollShardKey := jetStreamPollOptionVoteCountKey(poll.ID, optionA, store.shardForIdentity(bob.ID))
	if err := store.deleteCountRecord(reactionShardKey); err != nil {
		t.Fatalf("delete reaction aggregate shard: %v", err)
	}
	if err := store.deleteCountRecord(pollShardKey); err != nil {
		t.Fatalf("delete poll aggregate shard: %v", err)
	}

	assertJetStreamUserReacted(t, store, postID, bob.ID, true)
	assertJetStreamPollVote(t, store, poll.ID, bob.ID, optionA, true)
	assertCoreNATSReactionCount(t, c, postID, 0)
	assertNATSCounterPollCounts(t, c, poll.ID, bob.ID, optionA, 0, 0)

	result, err := store.RebuildAggregateShardsFromIdentity(400)
	if err != nil {
		t.Fatalf("rebuild aggregate shards after chaos: %v", err)
	}
	if result.ReactionIdentityRecords != 1 ||
		result.PollVoteIdentityRecords != 1 ||
		result.ReactionShardRecords != 1 ||
		result.PollVoteShardRecords != 1 {
		t.Fatalf("repair result = %+v, want one reaction and one poll identity/shard", result)
	}
	assertCoreNATSReactionCount(t, c, postID, 1)
	assertNATSCounterPollCounts(t, c, poll.ID, bob.ID, optionA, 1, 0)

	if _, err := c.CheckpointCounters(context.Background()); err != nil {
		t.Fatalf("checkpoint counters after repair: %v", err)
	}
	assertNATSCounterCheckpoint(t, c, "post.reactions", postID, 1)
	assertNATSCounterCheckpoint(t, c, "poll.option_votes", optionA, 1)
}

func beginJetStreamCounterMutation(t *testing.T, store *JetStreamCounterStore) counterstore.Mutation {
	t.Helper()
	mutation, err := store.BeginMutation()
	if err != nil {
		t.Fatalf("begin mutation: %v", err)
	}
	return mutation
}

func assertJetStreamUserReacted(t *testing.T, store *JetStreamCounterStore, postID, userID string, want bool) {
	t.Helper()
	got, err := store.UserReacted(postID, userID)
	if err != nil {
		t.Fatalf("user reacted: %v", err)
	}
	if got != want {
		t.Fatalf("user reacted = %v, want %v", got, want)
	}
}

func assertJetStreamReactionCount(t *testing.T, store *JetStreamCounterStore, postID string, want int) {
	t.Helper()
	got, err := store.ReactionCount(postID)
	if err != nil {
		t.Fatalf("reaction count: %v", err)
	}
	if got != want {
		t.Fatalf("reaction count = %d, want %d", got, want)
	}
}

func assertJetStreamReactionReceivedCount(t *testing.T, store *JetStreamCounterStore, userID string, want int) {
	t.Helper()
	got, err := store.ReactionReceivedCount(userID)
	if err != nil {
		t.Fatalf("reaction received count: %v", err)
	}
	if got != want {
		t.Fatalf("reaction received count = %d, want %d", got, want)
	}
}

func assertJetStreamPollVote(t *testing.T, store *JetStreamCounterStore, pollID, userID, wantOption string, wantFound bool) {
	t.Helper()
	gotOption, gotFound, err := store.PollVote(pollID, userID)
	if err != nil {
		t.Fatalf("poll vote: %v", err)
	}
	if gotFound != wantFound || gotOption != wantOption {
		t.Fatalf("poll vote = %q found=%v, want %q found=%v", gotOption, gotFound, wantOption, wantFound)
	}
}

func assertJetStreamPollOptionVoteCount(t *testing.T, store *JetStreamCounterStore, pollID, optionID string, want int) {
	t.Helper()
	got, err := store.PollOptionVoteCount(pollID, optionID)
	if err != nil {
		t.Fatalf("poll option vote count: %v", err)
	}
	if got != want {
		t.Fatalf("poll option vote count = %d, want %d", got, want)
	}
}

func registerNATSCounterStoreUser(t *testing.T, c *core.Core, name string) *projections.User {
	t.Helper()
	u, err := c.RegisterUser(name, "pw")
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return u
}

func execNATSCounterStoreCommand(t *testing.T, c *core.Core, actor *projections.User, cmd proto.CommandName, payload any) *proto.AckResult {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	reply := c.ExecCmd(context.Background(), actor, cmd, raw, "")
	if reply.Err != nil {
		t.Fatalf("command %s failed: %s (%s)", cmd, reply.Err.Message, reply.Err.Code)
	}
	return reply.Result
}

func assertNATSCounterSQLRows(t *testing.T, c *core.Core, table string, want int) {
	t.Helper()
	var got int
	if err := c.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}

func assertCoreNATSReactionCount(t *testing.T, c *core.Core, postID string, want int) {
	t.Helper()
	got, err := c.ReactionCount(postID)
	if err != nil {
		t.Fatalf("core reaction count: %v", err)
	}
	if got != want {
		t.Fatalf("core reaction count = %d, want %d", got, want)
	}
}

func assertNATSCounterPollCounts(t *testing.T, c *core.Core, pollID, userID, wantVote string, wantOptionA, wantOptionB int) *projections.Poll {
	t.Helper()
	poll, err := c.GetPoll(pollID, userID)
	if err != nil {
		t.Fatalf("get poll: %v", err)
	}
	if poll == nil || len(poll.Options) != 2 {
		t.Fatalf("poll = %+v, want two options", poll)
	}
	if poll.Voted != wantVote ||
		poll.Options[0].VoteCount != wantOptionA ||
		poll.Options[1].VoteCount != wantOptionB {
		t.Fatalf("poll counts = %+v voted=%q, want %d/%d voted %q", poll.Options, poll.Voted, wantOptionA, wantOptionB, wantVote)
	}
	return poll
}

func assertNATSCounterCheckpoint(t *testing.T, c *core.Core, kind, targetID string, want int) {
	t.Helper()
	var got int
	if err := c.DB.QueryRow(`SELECT count FROM counter_checkpoints WHERE counter_kind=? AND target_id=?`, kind, targetID).Scan(&got); err != nil {
		t.Fatalf("counter checkpoint %s/%s: %v", kind, targetID, err)
	}
	if got != want {
		t.Fatalf("counter checkpoint %s/%s = %d, want %d", kind, targetID, got, want)
	}
}

type fakeCounterStoreKV struct {
	mu       sync.Mutex
	revision uint64
	values   map[string]fakeCounterStoreKVEntry
}

func newFakeCounterStoreKV() *fakeCounterStoreKV {
	return &fakeCounterStoreKV{values: map[string]fakeCounterStoreKVEntry{}}
}

func (kv *fakeCounterStoreKV) Get(key string) (counterStoreKVEntry, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	entry, ok := kv.values[key]
	if !ok {
		return nil, nats.ErrKeyNotFound
	}
	return entry.clone(), nil
}

func (kv *fakeCounterStoreKV) Create(key string, value []byte) (uint64, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if _, ok := kv.values[key]; ok {
		return 0, nats.ErrKeyExists
	}
	kv.revision++
	kv.values[key] = fakeCounterStoreKVEntry{
		value:    append([]byte(nil), value...),
		revision: kv.revision,
	}
	return kv.revision, nil
}

func (kv *fakeCounterStoreKV) Update(key string, value []byte, revision uint64) (uint64, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	entry, ok := kv.values[key]
	if !ok {
		return 0, nats.ErrKeyNotFound
	}
	if entry.revision != revision {
		return 0, nats.ErrKeyExists
	}
	kv.revision++
	kv.values[key] = fakeCounterStoreKVEntry{
		value:    append([]byte(nil), value...),
		revision: kv.revision,
	}
	return kv.revision, nil
}

func (kv *fakeCounterStoreKV) Delete(key string, revision uint64) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	entry, ok := kv.values[key]
	if !ok {
		return nats.ErrKeyNotFound
	}
	if revision != 0 && entry.revision != revision {
		return nats.ErrKeyExists
	}
	kv.revision++
	delete(kv.values, key)
	return nil
}

func (kv *fakeCounterStoreKV) Keys() ([]string, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if len(kv.values) == 0 {
		return nil, nats.ErrNoKeysFound
	}
	keys := make([]string, 0, len(kv.values))
	for key := range kv.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

type fakeCounterStoreKVEntry struct {
	value    []byte
	revision uint64
}

func (e fakeCounterStoreKVEntry) Value() []byte {
	return append([]byte(nil), e.value...)
}

func (e fakeCounterStoreKVEntry) Revision() uint64 {
	return e.revision
}

func (e fakeCounterStoreKVEntry) clone() fakeCounterStoreKVEntry {
	return fakeCounterStoreKVEntry{
		value:    append([]byte(nil), e.value...),
		revision: e.revision,
	}
}
