package core_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// newTestCore creates a temporary SQLite database for testing.
func newTestCore(t *testing.T) (*core.Core, context.CancelFunc) {
	t.Helper()
	f, err := os.CreateTemp("", "budgie_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	c, err := core.New(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)

	return c, cancel
}

func registerAndGetUser(t *testing.T, c *core.Core, name, password string) *core.User {
	t.Helper()
	u, err := c.RegisterUser(name, password)
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return u
}

// exec is a helper that submits a command and fails the test on error.
func exec(t *testing.T, c *core.Core, actor *core.User, cmd proto.CommandName, payload any) *proto.AckResult {
	t.Helper()
	raw, _ := json.Marshal(payload)
	reply := c.ExecCmd(context.Background(), actor, cmd, raw, "")
	if reply.Err != nil {
		t.Fatalf("command %s failed: %s (%s)", cmd, reply.Err.Message, reply.Err.Code)
	}
	return reply.Result
}

// execExpectErr submits a command and expects it to be rejected with the given code.
func execExpectErr(t *testing.T, c *core.Core, actor *core.User, cmd proto.CommandName, payload any, expectCode string) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	reply := c.ExecCmd(context.Background(), actor, cmd, raw, "")
	if reply.Err == nil {
		t.Fatalf("expected command %s to fail with %s but it succeeded", cmd, expectCode)
	}
	if reply.Err.Code != expectCode {
		t.Fatalf("expected error %s, got %s: %s", expectCode, reply.Err.Code, reply.Err.Message)
	}
}

// --- M1 spine assertions ---

// TestRejectedCommandDoesNotAppend verifies that a rejected command never
// produces an event in the log.
func TestRejectedCommandDoesNotAppend(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")

	headBefore, _ := c.Head()

	// Append to a non-existent thread — must be rejected.
	execExpectErr(t, c, alice, proto.CmdAppendPost,
		proto.AppendPostPayload{Thread: "nonexistent", Body: "hello"},
		proto.ErrNotFound)

	headAfter, _ := c.Head()
	if headAfter != headBefore {
		t.Errorf("rejected command appended %d event(s)", headAfter-headBefore)
	}
}

// TestReplayMatchesLiveTail verifies that replaying from a cursor N delivers
// the same events as live tailing from N.
func TestReplayMatchesLiveTail(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")

	// Subscribe before writing anything.
	sub := c.Subscribe([]string{"board:general"})
	defer c.Unsubscribe(sub)

	cursorBefore, _ := c.Head()

	// Write a thread + post.
	res := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Hello world", Body: "First post",
	})
	threadID := res.ID

	// Drain the subscription channel (live tail events).
	var liveEvents []*proto.Event
	for i := 0; i < 2; i++ {
		e := <-sub.Ch
		liveEvents = append(liveEvents, e)
	}

	// Replay from the same cursor.
	replayed, err := c.Replay(cursorBefore, []string{"board:general"}, 100)
	if err != nil {
		t.Fatal(err)
	}

	if len(replayed) != len(liveEvents) {
		t.Fatalf("replay returned %d events, live tail got %d", len(replayed), len(liveEvents))
	}
	for i := range replayed {
		if replayed[i].Seq != liveEvents[i].Seq {
			t.Errorf("event[%d]: replay seq=%d, live seq=%d", i, replayed[i].Seq, liveEvents[i].Seq)
		}
		if replayed[i].Kind != liveEvents[i].Kind {
			t.Errorf("event[%d]: replay kind=%s, live kind=%s", i, replayed[i].Kind, liveEvents[i].Kind)
		}
	}
	_ = threadID
}

// TestProjectionMatchesLogReplay verifies that projection tables built from the
// live write path match what you'd get from rebuilding via log replay.
func TestProjectionMatchesLogReplay(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")

	res := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Test thread", Body: "First post",
	})
	threadID := res.ID

	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadID, Body: "Reply one",
	})
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadID, Body: "Reply two",
	})

	// Check projection state.
	thread, err := c.GetThread(threadID)
	if err != nil || thread == nil {
		t.Fatal("thread not found in projection")
	}
	if thread.PostCount != 3 {
		t.Errorf("expected 3 posts, got %d", thread.PostCount)
	}

	posts, err := c.ListPosts(threadID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 3 {
		t.Errorf("expected 3 posts in projection, got %d", len(posts))
	}

	// Now count durable events for this thread in the log.
	events, err := c.Replay(0, []string{"thread:" + threadID}, 100)
	if err != nil {
		t.Fatal(err)
	}
	// thread.new (scoped to board), post.appended x3 scoped to thread.
	// We subscribed to thread: scope so we get the 3 post.appended events.
	postEvents := 0
	for _, evt := range events {
		if evt.Kind == proto.EvtPostAppended {
			postEvents++
		}
	}
	if postEvents != 3 {
		t.Errorf("expected 3 post.appended events in log, got %d", postEvents)
	}
}

// TestPermissions verifies that a regular user cannot perform moderator actions.
func TestPermissions(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	// First user is admin, second is regular user.
	admin := registerAndGetUser(t, c, "admin", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	res := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Thread", Body: "Start",
	})
	threadID := res.ID

	// Bob cannot lock the thread.
	execExpectErr(t, c, bob, proto.CmdLockThread,
		proto.LockThreadPayload{Thread: threadID, Locked: true},
		proto.ErrForbidden)

	// Lock it as admin.
	exec(t, c, admin, proto.CmdLockThread, proto.LockThreadPayload{Thread: threadID, Locked: true})

	// Bob cannot post in a locked thread.
	execExpectErr(t, c, bob, proto.CmdAppendPost,
		proto.AppendPostPayload{Thread: threadID, Body: "can I post?"},
		proto.ErrThreadLocked)

	// Admin can still post in a locked thread.
	exec(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadID, Body: "admin can post",
	})
}

// TestIdempotency verifies that replaying a command with the same cid
// returns the same result without duplicating events.
func TestIdempotency(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")

	raw, _ := json.Marshal(proto.CreateThreadPayload{
		Board: "general", Title: "Idempotent thread", Body: "First post",
	})

	const cid = "test-cid-1"
	reply1 := c.ExecCmd(context.Background(), alice, proto.CmdCreateThread, raw, cid)
	reply2 := c.ExecCmd(context.Background(), alice, proto.CmdCreateThread, raw, cid)

	if reply1.Err != nil {
		t.Fatal(reply1.Err.Message)
	}
	if reply2.Err != nil {
		t.Fatal(reply2.Err.Message)
	}
	if reply1.Result.ID != reply2.Result.ID {
		t.Errorf("idempotent retry returned different ID: %s vs %s", reply1.Result.ID, reply2.Result.ID)
	}

	// Only one thread should exist.
	threads, err := c.ListThreads("general", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 {
		t.Errorf("expected 1 thread after idempotent create, got %d", len(threads))
	}
}

// --- M11 trust / poll feature checks ---

func setTrustLevel(t *testing.T, c *core.Core, userID string, trustLevel int) {
	t.Helper()
	_, err := c.DB.Exec(
		`INSERT INTO user_activity (user_id, posts_created, days_visited, last_visit_day, reactions_recv, trust_level)
		 VALUES (?, ?, 1, ?, 0, ?)
		 ON CONFLICT(user_id) DO UPDATE SET trust_level=excluded.trust_level`,
		userID, 0, "1970-01-01", trustLevel,
	)
	if err != nil {
		t.Fatalf("seed trust level: %v", err)
	}
}

func TestPollCreationRespectsTrustLevel(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	threadBody := "[poll]\nQuestion?\nOption A\nOption B\n[/poll]"
	// Low-trust user cannot create polls.
	execExpectErr(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Blocked poll", Body: threadBody,
	}, proto.ErrForbidden)

	setTrustLevel(t, c, alice.ID, 2)
	// Trust level 2 can create a poll in a thread body.
	raw, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general", Title: "Allowed poll", Body: threadBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	create := c.ExecCmd(context.Background(), alice, proto.CmdCreateThread, raw, "")
	if create.Err != nil {
		t.Fatalf("trusted user should create poll thread: %s (%s)", create.Err.Message, create.Err.Code)
	}
	threadID := create.Result.ID

	threadPosts, err := c.ListPosts(threadID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threadPosts) != 1 {
		t.Fatalf("expected 1 post in new thread, got %d", len(threadPosts))
	}
	if strings.Contains(threadPosts[0].Body, "[poll]") {
		t.Fatalf("expected stored post body to strip poll block, got %q", threadPosts[0].Body)
	}

	poll, err := c.GetPollByPostID(threadPosts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatalf("expected poll row for thread post body")
	}

	// Trusted creation of poll is fine, but low-trust reply-polls are still blocked.
	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "base", Body: "No poll here",
	})
	execExpectErr(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID, Body: threadBody,
	}, proto.ErrForbidden)
}

func TestPollCreationSupportsExpiry(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	expiresAt := time.Now().Add(3 * time.Minute).UTC().Truncate(time.Second)
	expiresRaw := expiresAt.Format(time.RFC3339)
	threadBody := "[poll expires=" + expiresRaw + "]\nQuestion?\nOption A\nOption B\n[/poll]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Timed poll", Body: threadBody,
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}

	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatal("expected poll row for timed poll")
	}

	if poll.ExpiresAt != expiresAt.UnixMilli() {
		t.Fatalf("expected expiresAt=%d, got %d", expiresAt.UnixMilli(), poll.ExpiresAt)
	}
}

func TestPollCreationSupportsExpiryInUnixSeconds(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	expiresAt := time.Now().Add(4 * time.Minute).UTC().Truncate(time.Second)
	expiresRaw := fmt.Sprintf("%d", expiresAt.Unix())
	threadBody := "[poll expires=" + expiresRaw + "]\nQuestion?\nOption A\nOption B\n[/poll]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Unix expiry poll", Body: threadBody,
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}

	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatal("expected poll row for timed poll")
	}

	if poll.ExpiresAt != expiresAt.UnixMilli() {
		t.Fatalf("expected unix-second expiresAt=%d, got %d", expiresAt.UnixMilli(), poll.ExpiresAt)
	}
}

func TestPollCreationSupportsExpiryDuration(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[poll expires=2m]\nQuestion?\nOption A\nOption B\n[/poll]"
	start := time.Now().UTC().Truncate(time.Second)
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Duration expiry poll", Body: threadBody,
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}

	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatal("expected poll row for duration expiry")
	}

	end := start.Add(2 * time.Minute).UnixMilli()
	if poll.ExpiresAt < end-10_000 || poll.ExpiresAt > end+10_000 {
		t.Fatalf("expected approx %d, got %d", end, poll.ExpiresAt)
	}
}

func TestPollCreationRejectsInvalidExpires(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[poll expires=badformat]\nQuestion?\nOption A\nOption B\n[/poll]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Invalid expiry", Body: threadBody,
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}

	if posts[0].Body != threadBody {
		t.Fatalf("expected malformed expiry poll to remain intact, got %q", posts[0].Body)
	}

	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll != nil {
		t.Fatal("expected no poll row for malformed expiry")
	}
}

func TestPollCreationSupportsExpiryOnReply(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Base", Body: "before",
	})

	expiresAt := time.Now().Add(9 * time.Minute).UTC().Truncate(time.Second)
	threadBody := "[poll expires=" + expiresAt.Format(time.RFC3339) + "]\nQuestion?\nOption A\nOption B\n[/poll]"
	replyRes := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID,
		Body:   threadBody,
	})

	posts, err := c.ListPosts(base.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(posts))
	}

	var reply *core.Post
	for i := range posts {
		if posts[i].ID == replyRes.ID {
			reply = &posts[i]
			break
		}
	}
	if reply == nil {
		t.Fatal("reply post not found")
	}

	poll, err := c.GetPollByPostID(reply.ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatal("expected poll row for timed reply")
	}
	if poll.ExpiresAt != expiresAt.UnixMilli() {
		t.Fatalf("expected reply poll expiresAt=%d, got %d", expiresAt.UnixMilli(), poll.ExpiresAt)
	}
}

func TestPollCreationPreservesBodyAroundMarkup(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "before line\n[poll]\nQuestion?\nOption A\nOption B\n[/poll]\nafter line"
	raw, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general", Title: "Embedded poll", Body: threadBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	reply := c.ExecCmd(context.Background(), alice, proto.CmdCreateThread, raw, "")
	if reply.Err != nil {
		t.Fatalf("trusted user should create poll thread: %s (%s)", reply.Err.Message, reply.Err.Code)
	}

	posts, err := c.ListPosts(reply.Result.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post in new thread, got %d", len(posts))
	}

	expectedBody := "before line\nafter line"
	if posts[0].Body != expectedBody {
		t.Fatalf("expected poll-stripped body %q, got %q", expectedBody, posts[0].Body)
	}
}

func TestPollVoteReplacesPriorVote(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Voting poll", Body: "[poll]\nQuestion?\nOption A\nOption B\n[/poll]",
	})
	threadPosts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threadPosts) != 1 {
		t.Fatalf("expected 1 post in new poll thread, got %d", len(threadPosts))
	}

	poll, err := c.GetPollByPostID(threadPosts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatalf("expected poll row for thread post")
	}

	initialPoll := func() *core.Poll {
		pollState, err := c.GetPoll(poll.ID, bob.ID)
		if err != nil {
			t.Fatal(err)
		}
		return pollState
	}
	pollState := initialPoll()

	optionAID := ""
	optionBID := ""
	for _, opt := range pollState.Options {
		switch opt.Text {
		case "Option A":
			optionAID = opt.ID
		case "Option B":
			optionBID = opt.ID
		}
	}
	if optionAID == "" || optionBID == "" {
		t.Fatalf("missing poll options in projection")
	}

	exec(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: optionAID})
	pollState = initialPoll()
	if pollState.Voted != optionAID {
		t.Fatalf("expected voter option %q after first vote, got %q", optionAID, pollState.Voted)
	}
	votesA := 0
	votesB := 0
	for _, opt := range pollState.Options {
		switch opt.ID {
		case optionAID:
			votesA = opt.VoteCount
		case optionBID:
			votesB = opt.VoteCount
		}
	}
	if votesA != 1 || votesB != 0 {
		t.Fatalf("expected Option A=1 and Option B=0 after first vote, got %d/%d", votesA, votesB)
	}

	exec(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: optionBID})
	pollState = initialPoll()
	if pollState.Voted != optionBID {
		t.Fatalf("expected voter option %q after replacement vote, got %q", optionBID, pollState.Voted)
	}
	votesA = 0
	votesB = 0
	for _, opt := range pollState.Options {
		switch opt.ID {
		case optionAID:
			votesA = opt.VoteCount
		case optionBID:
			votesB = opt.VoteCount
		}
	}
	if votesA != 0 || votesB != 1 {
		t.Fatalf("expected Option A=0 and Option B=1 after replacement vote, got %d/%d", votesA, votesB)
	}
}

func TestPollVoteValidations(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Vote validation poll", Body: "[poll]\nQuestion?\nOption A\nOption B\n[/poll]",
	})
	threadPosts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threadPosts) != 1 {
		t.Fatalf("expected 1 post in new poll thread, got %d", len(threadPosts))
	}
	poll, err := c.GetPollByPostID(threadPosts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatalf("expected poll row for thread post")
	}
	pollState, err := c.GetPoll(poll.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var optionID string
	if len(pollState.Options) == 0 {
		t.Fatalf("expected poll options")
	}
	optionID = pollState.Options[0].ID

	execExpectErr(t, c, alice, proto.CmdVotePoll, proto.VotePollPayload{
		Poll: "pol_missing", Option: "whatever",
	}, proto.ErrNotFound)
	execExpectErr(t, c, alice, proto.CmdVotePoll, proto.VotePollPayload{
		Poll: poll.ID, Option: "option_missing",
	}, proto.ErrNotFound)

	_, err = c.DB.Exec(`UPDATE polls SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UnixMilli(), poll.ID)
	if err != nil {
		t.Fatalf("failed to expire poll: %v", err)
	}
	execExpectErr(t, c, alice, proto.CmdVotePoll, proto.VotePollPayload{
		Poll: poll.ID, Option: optionID,
	}, proto.ErrConflict)
}

func TestPollCreationRequiresEnoughOptions(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[poll]\nQuestion\nOnly one option\n[/poll]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Single option", Body: threadBody,
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}
	if posts[0].Body != threadBody {
		t.Fatalf("expected malformed poll body to remain intact, got %q", posts[0].Body)
	}
	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll != nil {
		t.Fatal("expected no poll for single-option block")
	}
}

func TestPollCreationStripsBulletedOptions(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[poll]\nQuestion?\n- Option A\n* Option B\n[/poll]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Bulleted options", Body: threadBody,
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}

	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatal("expected poll to be created from bulleted options")
	}

	fullPoll, err := c.GetPoll(poll.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if fullPoll == nil {
		t.Fatal("expected full poll projection")
	}
	if fullPoll.Question != "Question?" {
		t.Fatalf("expected question 'Question?', got %q", fullPoll.Question)
	}
	if len(fullPoll.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(fullPoll.Options))
	}
	if fullPoll.Options[0].Text != "Option A" || fullPoll.Options[1].Text != "Option B" {
		t.Fatalf("expected stripped option texts, got %q and %q", fullPoll.Options[0].Text, fullPoll.Options[1].Text)
	}

	if posts[0].Body != "" {
		t.Fatalf("expected poll body to be stripped to empty, got %q", posts[0].Body)
	}
}

func TestPollCreationMissingCloseTagLeavesBodyIntact(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "before\n[poll]\nQuestion?\nOption A\nOption B\nafter"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Open tag only", Body: threadBody,
	})
	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}
	if posts[0].Body != threadBody {
		t.Fatalf("expected missing-close poll to stay intact, got %q", posts[0].Body)
	}
	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll != nil {
		t.Fatal("expected no poll for missing close tag")
	}
}

func TestPollCreationOnReplyPost(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Base", Body: "hello",
	})
	postRes := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID, Body: "[poll]\nQuestion?\nOption A\nOption B\n[/poll]",
	})

	posts, err := c.ListPosts(base.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(posts))
	}

	var reply *core.Post
	for _, p := range posts {
		if p.ID == postRes.ID {
			reply = &p
			break
		}
	}
	if reply == nil {
		t.Fatal("append post response not found in thread")
	}
	if reply.Body != "" {
		t.Fatalf("expected reply poll body to be stripped, got %q", reply.Body)
	}

	poll, err := c.GetPollByPostID(reply.ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatal("expected poll row for reply post")
	}
	fullPoll, err := c.GetPoll(poll.ID, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fullPoll == nil {
		t.Fatal("expected full poll projection")
	}
	if fullPoll.Question != "Question?" {
		t.Fatalf("expected question text, got %q", fullPoll.Question)
	}
}

func TestPollCreationRequiresQuestionText(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[poll]\n- Option A\n- Option B\n[/poll]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Missing question", Body: threadBody,
	})
	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}
	if posts[0].Body != threadBody {
		t.Fatalf("expected malformed poll body to remain intact, got %q", posts[0].Body)
	}
	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll != nil {
		t.Fatal("expected no poll when question is missing")
	}
}

func TestPollMarkupCannotBeAddedOnEdit(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "No poll", Body: "hello",
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}

	execExpectErr(t, c, alice, proto.CmdEditPost, proto.EditPostPayload{
		Post: posts[0].ID,
		Body: "edited with poll [poll]\nQuestion?\nOption A\nOption B\n[/poll]",
	}, proto.ErrValidationFailed)
}

func TestPollPostCannotBeEdited(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Poll post", Body: "[poll]\nQuestion?\nOption A\nOption B\n[/poll]",
	})
	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}

	execExpectErr(t, c, alice, proto.CmdEditPost, proto.EditPostPayload{
		Post: posts[0].ID,
		Body: "I shouldn't be able to edit this",
	}, proto.ErrValidationFailed)
}

type forumSnapshot struct {
	thread          *core.Thread
	posts           []core.Post
	pollQuestion    string
	pollOptionTexts []string
	pollVoteTotal   int
	reviews         []core.ModerationReview
}

type sanctionRow struct {
	ID      string
	UserID  string
	Kind    string
	Scope   string
	Expires int64
	By      string
	Reason  string
	Seq     int64
}

func captureForumSnapshot(t *testing.T, c *core.Core, threadID, firstPostID string) forumSnapshot {
	t.Helper()

	thread, err := c.GetThread(threadID)
	if err != nil || thread == nil {
		t.Fatalf("thread not found before rebuild: %v", err)
	}

	posts, err := c.ListPosts(threadID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}

	snap := forumSnapshot{
		thread: thread,
		posts:  posts,
	}

	poll, err := c.GetPollByPostID(firstPostID)
	if err != nil {
		t.Fatal(err)
	}
	if poll != nil {
		snap.pollQuestion = poll.Question
		full, err := c.GetPoll(poll.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if full == nil {
			t.Fatalf("poll not found for post %s", firstPostID)
		}
		total := 0
		for _, opt := range full.Options {
			snap.pollOptionTexts = append(snap.pollOptionTexts, opt.Text)
			total += opt.VoteCount
		}
		snap.pollVoteTotal = total
	}

	reviews, err := c.ListModerationReviews("", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	snap.reviews = reviews
	return snap
}

func clearProjectionTablesForTest(t *testing.T, c *core.Core) {
	t.Helper()
	tables := []string{
		"posts_fts",
		"posts",
		"threads",
		"poll_votes",
		"poll_options",
		"polls",
		"post_reactions",
		"moderation_reviews",
		"user_sanctions",
		"user_activity",
	}
	for _, table := range tables {
		if _, err := c.DB.Exec(`DELETE FROM ` + table); err != nil {
			t.Fatalf("clear table %s: %v", table, err)
		}
	}
}

func loadSanctionsForTest(t *testing.T, c *core.Core) map[int64]sanctionRow {
	t.Helper()
	rows, err := c.DB.Query(`SELECT id, user_id, kind, scope, expires_at, by, reason, seq FROM user_sanctions ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	out := map[int64]sanctionRow{}
	for rows.Next() {
		var r sanctionRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.Kind, &r.Scope, &r.Expires, &r.By, &r.Reason, &r.Seq); err != nil {
			t.Fatal(err)
		}
		out[r.Seq] = r
	}
	if err := rows.Err(); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	return out
}

func reviewsMap(in []core.ModerationReview) map[string]core.ModerationReview {
	out := map[string]core.ModerationReview{}
	for _, r := range in {
		out[r.ID] = r
	}
	return out
}

func TestRebuildProjectionsFromEventLog(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:   "archive",
		Name: "Archive",
	})

	threadRes := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Migrations test",
		Body:  "First post",
	})
	threadID := threadRes.ID

	threadPosts, err := c.ListPosts(threadID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	threadPollPost := exec(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadID,
		Body:   "[poll]\nColor\n- red\n- blue\n[/poll]",
	})
	pollPostID := threadPollPost.ID
	threadPosts, err = c.ListPosts(threadID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threadPosts) < 2 {
		t.Fatalf("expected thread and poll posts before rebuild")
	}

	exec(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadID,
		Body:   "Reply for moderation",
	})
	exec(t, c, admin, proto.CmdMoveThread, proto.MoveThreadPayload{
		Thread:  threadID,
		ToBoard: "archive",
	})
	poll, err := c.GetPollByPostID(pollPostID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatalf("expected valid poll projection before rebuild")
	}

	review := exec(t, c, bob, proto.CmdFlagPost, proto.FlagPostPayload{
		Post:   pollPostID,
		Reason: "needs cleanup",
	})
	exec(t, c, admin, proto.CmdResolveReview, proto.ResolveReviewPayload{
		Review:     review.ID,
		Resolution: "approved",
	})
	exec(t, c, admin, proto.CmdSanctionUser, proto.SanctionUserPayload{
		User:   bob.ID,
		Kind:   "mute",
		Scope:  "global",
		Reason: "test path",
	})

	// Snapshot live projection state.
	beforeSnapshot := captureForumSnapshot(t, c, threadID, pollPostID)
	beforeSanctions := loadSanctionsForTest(t, c)

	clearProjectionTablesForTest(t, c)

	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}

	afterSnapshot := captureForumSnapshot(t, c, threadID, pollPostID)
	afterSanctions := loadSanctionsForTest(t, c)

	// Core state should be identical after replay.
	if beforeSnapshot.thread.ID != afterSnapshot.thread.ID {
		t.Fatalf("thread ID changed by rebuild: %q vs %q", beforeSnapshot.thread.ID, afterSnapshot.thread.ID)
	}
	if beforeSnapshot.thread.Board != afterSnapshot.thread.Board {
		t.Fatalf("thread board changed by rebuild: %q vs %q", beforeSnapshot.thread.Board, afterSnapshot.thread.Board)
	}
	if beforeSnapshot.thread.PostCount != afterSnapshot.thread.PostCount {
		t.Fatalf("thread post count changed by rebuild: %d vs %d", beforeSnapshot.thread.PostCount, afterSnapshot.thread.PostCount)
	}
	if len(beforeSnapshot.posts) != len(afterSnapshot.posts) {
		t.Fatalf("post count changed by rebuild: %d vs %d", len(beforeSnapshot.posts), len(afterSnapshot.posts))
	}
	for i := range beforeSnapshot.posts {
		if beforeSnapshot.posts[i].ID != afterSnapshot.posts[i].ID ||
			beforeSnapshot.posts[i].Body != afterSnapshot.posts[i].Body ||
			beforeSnapshot.posts[i].ReplyTo != afterSnapshot.posts[i].ReplyTo {
			t.Fatalf("post mismatch at index %d", i)
		}
	}
	if beforeSnapshot.pollQuestion != afterSnapshot.pollQuestion {
		t.Fatalf("poll question changed by rebuild: %q vs %q", beforeSnapshot.pollQuestion, afterSnapshot.pollQuestion)
	}
	if len(beforeSnapshot.pollOptionTexts) != len(afterSnapshot.pollOptionTexts) {
		t.Fatalf("poll option count changed by rebuild: %d vs %d", len(beforeSnapshot.pollOptionTexts), len(afterSnapshot.pollOptionTexts))
	}
	for i := range beforeSnapshot.pollOptionTexts {
		if beforeSnapshot.pollOptionTexts[i] != afterSnapshot.pollOptionTexts[i] {
			t.Fatalf("poll option changed at index %d: %q vs %q", i, beforeSnapshot.pollOptionTexts[i], afterSnapshot.pollOptionTexts[i])
		}
	}
	if beforeSnapshot.pollVoteTotal != afterSnapshot.pollVoteTotal {
		t.Fatalf("poll vote total changed by rebuild: %d vs %d", beforeSnapshot.pollVoteTotal, afterSnapshot.pollVoteTotal)
	}
	beforeReviews := reviewsMap(beforeSnapshot.reviews)
	afterReviews := reviewsMap(afterSnapshot.reviews)
	if len(beforeReviews) != len(afterReviews) {
		t.Fatalf("moderation review count changed by rebuild: %d vs %d", len(beforeReviews), len(afterReviews))
	}
	for id, reviewBefore := range beforeReviews {
		reviewAfter, ok := afterReviews[id]
		if !ok {
			t.Fatalf("rebuild missing review %s", id)
		}
		if reviewBefore.Kind != reviewAfter.Kind ||
			reviewBefore.Status != reviewAfter.Status ||
			reviewBefore.Resolution != reviewAfter.Resolution ||
			reviewBefore.Actor != reviewAfter.Actor {
			t.Fatalf("moderation review changed: %s", id)
		}
	}
	if len(beforeSanctions) != len(afterSanctions) {
		t.Fatalf("sanction count changed by rebuild: %d vs %d", len(beforeSanctions), len(afterSanctions))
	}
	for seq, sanctionBefore := range beforeSanctions {
		sanctionAfter, ok := afterSanctions[seq]
		if !ok {
			t.Fatalf("rebuild missing sanction seq=%d", seq)
		}
		if sanctionBefore.UserID != sanctionAfter.UserID ||
			sanctionBefore.Kind != sanctionAfter.Kind ||
			sanctionBefore.Scope != sanctionAfter.Scope ||
			sanctionBefore.Expires != sanctionAfter.Expires ||
			sanctionBefore.By != sanctionAfter.By ||
			sanctionBefore.Reason != sanctionAfter.Reason {
			t.Fatalf("sanction changed for seq=%d", seq)
		}
	}
}
