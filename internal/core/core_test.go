package core_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

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
