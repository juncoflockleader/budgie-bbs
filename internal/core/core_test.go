package core_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
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

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }
func stringPtr(v string) *string {
	return &v
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

func TestAppendPostQuotedReply(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	thread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Quote me",
		Body:  "First line\nSecond line",
	})
	sourcePosts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("expected source post, got %+v", sourcePosts)
	}

	reply := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread:    thread.ID,
		ReplyTo:   sourcePosts[0].ID,
		QuotePost: true,
		Body:      "My answer",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	var quoted *core.Post
	for i := range posts {
		if posts[i].ID == reply.ID {
			quoted = &posts[i]
			break
		}
	}
	if quoted == nil {
		t.Fatalf("expected quoted reply post, got %+v", posts)
	}
	for _, want := range []string{"> admin wrote:", "> First line", "> Second line", "My answer"} {
		if !strings.Contains(quoted.Body, want) {
			t.Fatalf("expected quoted reply body to contain %q, got:\n%s", want, quoted.Body)
		}
	}
	if quoted.ReplyTo != sourcePosts[0].ID {
		t.Fatalf("expected quoted reply to keep direct reply target %q, got %+v", sourcePosts[0].ID, quoted)
	}

	execExpectErr(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread:    thread.ID,
		QuotePost: true,
		Body:      "No target",
	}, proto.ErrValidationFailed)
	exec(t, c, admin, proto.CmdRedactPost, proto.RedactPostPayload{
		Post:   sourcePosts[0].ID,
		Reason: "test redaction",
	})
	execExpectErr(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread:    thread.ID,
		ReplyTo:   sourcePosts[0].ID,
		QuotePost: true,
		Body:      "No leak",
	}, proto.ErrConflict)
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

func TestSetThreadTitleWorkflow(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	res := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Original topic", Body: "Start",
	})
	threadID := res.ID

	exec(t, c, alice, proto.CmdSetThreadTitle, proto.SetThreadTitlePayload{
		Thread: threadID,
		Title:  "Edited by author",
	})
	thread, err := c.GetThread(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if thread == nil || thread.Title != "Edited by author" {
		t.Fatalf("expected author title edit, got %+v", thread)
	}

	execExpectErr(t, c, bob, proto.CmdSetThreadTitle, proto.SetThreadTitlePayload{
		Thread: threadID,
		Title:  "Bob takeover",
	}, proto.ErrForbidden)

	oldCreatedAt := time.Now().Add(-48 * time.Hour).UnixMilli()
	if _, err := c.DB.Exec(`UPDATE threads SET created_at=? WHERE id=?`, oldCreatedAt, threadID); err != nil {
		t.Fatal(err)
	}
	execExpectErr(t, c, alice, proto.CmdSetThreadTitle, proto.SetThreadTitlePayload{
		Thread: threadID,
		Title:  "Late author edit",
	}, proto.ErrEditWindowExpired)

	exec(t, c, admin, proto.CmdSetThreadTitle, proto.SetThreadTitlePayload{
		Thread: threadID,
		Title:  "Moderator title",
	})
	thread, err = c.GetThread(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if thread == nil || thread.Title != "Moderator title" {
		t.Fatalf("expected moderator title edit, got %+v", thread)
	}

	execExpectErr(t, c, admin, proto.CmdSetThreadTitle, proto.SetThreadTitlePayload{
		Thread: threadID,
		Title:  strings.Repeat("x", 161),
	}, proto.ErrValidationFailed)
}

func TestSyssecuritySystemBoardLogs(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	exec(t, c, admin, proto.CmdGrantRole, proto.GrantRolePayload{
		User: alice.ID,
		Role: "mod",
	})
	systemBoard, err := c.GetBoard("syssecurity")
	if err != nil {
		t.Fatal(err)
	}
	if systemBoard == nil || systemBoard.Name != "syssecurity" {
		t.Fatalf("expected generated syssecurity board, got %+v", systemBoard)
	}
	threads, err := c.ListThreads("syssecurity", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].Title != "Role granted: alice" {
		t.Fatalf("expected role-grant syssecurity thread, got %+v", threads)
	}
	posts, err := c.ListPosts(threads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected one role-grant syssecurity post, got %+v", posts)
	}
	for _, want := range []string{"Action: role granted", "User: alice", "Role: mod", "Actor: admin"} {
		if !strings.Contains(posts[0].Body, want) {
			t.Fatalf("expected syssecurity role-grant post to contain %q, got %q", want, posts[0].Body)
		}
	}

	exec(t, c, admin, proto.CmdRevokeRole, proto.RevokeRolePayload{
		User: alice.ID,
		Role: "mod",
	})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "policy", Name: "Policy"})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:    "policy",
		ReadOnly: boolPtr(true),
	})
	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "policy",
		User:      "alice",
		Moderator: true,
	})
	threads, err = c.ListThreads("syssecurity", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 4 {
		t.Fatalf("expected role grant, role revoke, settings, and moderator syssecurity threads, got %+v", threads)
	}
	moderatorPosts, err := c.ListPosts(threads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(moderatorPosts) != 1 || !strings.Contains(moderatorPosts[0].Body, "Action: board moderator appointed") || !strings.Contains(moderatorPosts[0].Body, "Board: policy") {
		t.Fatalf("expected moderator appointment syssecurity post, got %+v", moderatorPosts)
	}
	settingsPosts, err := c.ListPosts(threads[1].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(settingsPosts) != 1 || !strings.Contains(settingsPosts[0].Body, "Action: board settings changed") || !strings.Contains(settingsPosts[0].Body, "readOnly: true") {
		t.Fatalf("expected board settings syssecurity post, got %+v", settingsPosts)
	}

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "secretpolicy", Name: "Secret Policy"})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secretpolicy",
		MemberReadMode: boolPtr(true),
	})
	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "secretpolicy",
		User:      "alice",
		Moderator: true,
	})
	threads, err = c.ListThreads("syssecurity", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 4 {
		t.Fatalf("member-read board moderator appointment should not generate public syssecurity post, got %+v", threads)
	}
}

func TestUserSignatureSnapshotsOnPosts(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	if err := c.UpdateUserProfile(alice.ID, "Alice", "", "bio", "A", "first signature", "", ""); err != nil {
		t.Fatal(err)
	}
	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Signature thread",
		Body:  "first body",
	})
	if err := c.UpdateUserProfile(alice.ID, "Alice", "", "bio", "A", "second signature", "", ""); err != nil {
		t.Fatal(err)
	}
	reply := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "second body",
	})

	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected starter and reply posts, got %+v", posts)
	}
	if posts[0].Signature != "first signature" {
		t.Fatalf("expected starter post to keep first signature, got %+v", posts[0])
	}
	if posts[1].ID != reply.ID || posts[1].Signature != "second signature" {
		t.Fatalf("expected reply post to snapshot second signature, got %+v", posts[1])
	}

	exec(t, c, alice, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:            "general",
		AnonymousAllowed: boolPtr(true),
	})
	anonymousThread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board:     "general",
		Title:     "Anonymous signature",
		Body:      "anonymous body",
		Anonymous: true,
	})
	anonymousPosts, err := c.ListPosts(anonymousThread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(anonymousPosts) != 1 || anonymousPosts[0].Signature != "" {
		t.Fatalf("anonymous post should not expose a signature, got %+v", anonymousPosts)
	}

	profile, err := c.UserProfileByName("alice")
	if err != nil {
		t.Fatal(err)
	}
	if profile == nil || profile.Signature != "second signature" {
		t.Fatalf("expected profile to expose current signature, got %+v", profile)
	}
}

func TestUserPlanAndHomepageProfileFields(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	if err := c.UpdateUserProfile(alice.ID, "Alice", "BBS Elder", "bio", "A", "signature", "Learning Go on KBS", "example.edu/~alice"); err != nil {
		t.Fatal(err)
	}

	profile, err := c.UserProfileByName("alice")
	if err != nil {
		t.Fatal(err)
	}
	if profile == nil {
		t.Fatal("expected profile")
	}
	if profile.Title != "BBS Elder" || profile.Plan != "Learning Go on KBS" || profile.Homepage != "example.edu/~alice" {
		t.Fatalf("expected title, plan, and homepage to round trip, got %+v", profile)
	}
}

func TestUserPrivateProfileFields(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	if err := c.UpdateUserPrivateProfile(&core.UserPrivateProfile{
		UserID:            alice.ID,
		RealName:          "Alice Zhang",
		RealEmail:         "alice@real.example",
		RegistrationEmail: "alice@register.example",
		Address:           "Dorm 7",
		Phone:             "010-123456",
		Mobile:            "13900000000",
		Birthday:          "1984-05-04",
		School:            "Computer Science",
		ContactNote:       "class of 2006",
	}); err != nil {
		t.Fatal(err)
	}

	privateProfile, err := c.UserPrivateProfile(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if privateProfile.RealName != "Alice Zhang" ||
		privateProfile.RealEmail != "alice@real.example" ||
		privateProfile.RegistrationEmail != "alice@register.example" ||
		privateProfile.Address != "Dorm 7" ||
		privateProfile.Phone != "010-123456" ||
		privateProfile.Mobile != "13900000000" ||
		privateProfile.Birthday != "1984-05-04" ||
		privateProfile.School != "Computer Science" ||
		privateProfile.ContactNote != "class of 2006" {
		t.Fatalf("expected private profile to round trip, got %+v", privateProfile)
	}

	publicProfile, err := c.UserProfileByName("alice")
	if err != nil {
		t.Fatal(err)
	}
	if publicProfile == nil {
		t.Fatal("expected public profile")
	}
	if publicProfile.DisplayName != "alice" {
		t.Fatalf("private profile update should not affect public profile, got %+v", publicProfile)
	}
}

func TestUserPersonalFiles(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	publicFile, err := c.SaveUserPersonalFile(alice.ID, "resume.txt", "public body", true)
	if err != nil {
		t.Fatal(err)
	}
	if publicFile.Name != "resume.txt" || publicFile.Body != "public body" || !publicFile.Public {
		t.Fatalf("expected public personal file, got %+v", publicFile)
	}
	privateFile, err := c.SaveUserPersonalFile(alice.ID, "secret.txt", "private body", false)
	if err != nil {
		t.Fatal(err)
	}
	if privateFile.Public {
		t.Fatalf("expected private personal file, got %+v", privateFile)
	}
	publicFiles, err := c.ListUserPersonalFiles(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(publicFiles) != 1 || publicFiles[0].Name != "resume.txt" {
		t.Fatalf("expected only public file in public list, got %+v", publicFiles)
	}
	ownFiles, err := c.ListUserPersonalFiles(alice.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ownFiles) != 2 {
		t.Fatalf("expected owner to see public and private files, got %+v", ownFiles)
	}
	if hidden, err := c.GetUserPersonalFile(alice.ID, "secret.txt", false); err != nil || hidden != nil {
		t.Fatalf("expected private file hidden from public reads, file=%+v err=%v", hidden, err)
	}
	if err := c.DeleteUserPersonalFile(alice.ID, "resume.txt"); err != nil {
		t.Fatal(err)
	}
	publicFiles, err = c.ListUserPersonalFiles(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(publicFiles) != 0 {
		t.Fatalf("expected deleted public file to disappear, got %+v", publicFiles)
	}
}

func TestUserSignatureBankSelection(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	first, err := c.SaveUserSignature(alice.ID, "", "First", "signature one", -1, true)
	if err != nil {
		t.Fatalf("save first signature: %v", err)
	}
	second, err := c.SaveUserSignature(alice.ID, "", "Second", "signature two", -1, true)
	if err != nil {
		t.Fatalf("save second signature: %v", err)
	}
	if err := c.SetUserSignatureSettings(alice.ID, second.ID, false); err != nil {
		t.Fatalf("select second signature: %v", err)
	}

	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Signature bank",
		Body:  "first post",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Signature != "signature two" {
		t.Fatalf("expected selected signature snapshot, got %+v", posts)
	}

	if err := c.SetUserSignatureSettings(alice.ID, first.ID, true); err != nil {
		t.Fatalf("enable random signatures: %v", err)
	}
	reply := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "reply",
	})
	posts, err = c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 || posts[1].ID != reply.ID {
		t.Fatalf("expected reply post, got %+v", posts)
	}
	if posts[1].Signature != "signature one" && posts[1].Signature != "signature two" {
		t.Fatalf("expected random signature from active bank, got %+v", posts[1])
	}

	bundle, err := c.ListUserSignatures(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Signatures) != 2 || !bundle.Settings.RandomEnabled || bundle.Settings.SelectedSignatureID != first.ID || bundle.MaxCount == 0 {
		t.Fatalf("expected signature bundle with random settings, got %+v", bundle)
	}
	profile, err := c.UserProfileByName("alice")
	if err != nil {
		t.Fatal(err)
	}
	if profile == nil || profile.Signature != "signature one" {
		t.Fatalf("expected public profile signature to follow selected fallback, got %+v", profile)
	}
}

func TestUserSignatureRecountRepairsSelection(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	first, err := c.SaveUserSignature(alice.ID, "", "First", "signature one", -1, true)
	if err != nil {
		t.Fatalf("save first signature: %v", err)
	}
	second, err := c.SaveUserSignature(alice.ID, "", "Second", "signature two", -1, true)
	if err != nil {
		t.Fatalf("save second signature: %v", err)
	}
	if err := c.SetUserSignatureSettings(alice.ID, second.ID, false); err != nil {
		t.Fatalf("select second signature: %v", err)
	}
	if _, err := c.DB.Exec(`UPDATE user_signatures SET active=0 WHERE user_id=? AND id=?`, alice.ID, second.ID); err != nil {
		t.Fatal(err)
	}

	recount, err := c.RecountUserSignatures(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recount.Count != 2 || recount.ActiveCount != 1 || recount.SelectedSignatureID != "" || recount.CurrentSignature != "signature one" {
		t.Fatalf("expected recount to repair stale selection, got %+v", recount)
	}
	bundle, err := c.ListUserSignatures(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Settings.SelectedSignatureID != "" {
		t.Fatalf("expected stale selected signature to be cleared, got %+v", bundle.Settings)
	}
	if first.ID == "" {
		t.Fatal("expected first signature id")
	}
}

func TestChangePasswordLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	if err := c.ChangePassword(alice.ID, "bad", "newpw"); err == nil {
		t.Fatalf("expected wrong current password to fail")
	}
	if err := c.ChangePassword(alice.ID, "pw", "newpw"); err != nil {
		t.Fatalf("change password failed: %v", err)
	}
	if _, err := c.AuthenticateUser("alice", "pw"); err == nil {
		t.Fatalf("old password should no longer authenticate")
	}
	if _, err := c.AuthenticateUser("alice", "newpw"); err != nil {
		t.Fatalf("new password should authenticate: %v", err)
	}
}

func TestPasswordRecoveryAdminReset(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "oldpw")
	req, err := c.RequestPasswordRecovery("alice", "Alice Zhang", "alice@example.edu", "lost password")
	if err != nil {
		t.Fatal(err)
	}
	if req == nil || req.UserID != alice.ID || req.Status != "pending" {
		t.Fatalf("expected pending recovery request, got %+v", req)
	}
	pending, err := c.ListPasswordRecoveryRequests("pending", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].UserName != "alice" || pending[0].SubmittedName != "Alice Zhang" {
		t.Fatalf("expected alice pending recovery request, got %+v", pending)
	}
	review, err := c.ReviewPasswordRecoveryRequest(req.ID, admin.ID, "reset", "newpw", "verified")
	if err != nil {
		t.Fatal(err)
	}
	if review.Status != "resolved" || review.ReviewerID != admin.ID {
		t.Fatalf("expected resolved recovery request, got %+v", review)
	}
	if _, err := c.AuthenticateUser("alice", "oldpw"); !errors.Is(err, core.ErrInvalidCredentials) {
		t.Fatalf("expected old password rejected, got %v", err)
	}
	if _, err := c.AuthenticateUser("alice", "newpw"); err != nil {
		t.Fatalf("expected recovery password to authenticate: %v", err)
	}
}

func TestTransferUserID(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Transfer thread",
		Body:  "before transfer",
	})

	renamed, err := c.TransferUserID(alice.ID, "alice2")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != alice.ID || renamed.Name != "alice2" {
		t.Fatalf("expected same user id with new name, got %+v", renamed)
	}
	if old, err := c.UserByName("alice"); err != nil || old != nil {
		t.Fatalf("expected old name lookup empty, user=%+v err=%v", old, err)
	}
	if _, err := c.AuthenticateUser("alice", "pw"); !errors.Is(err, core.ErrInvalidCredentials) {
		t.Fatalf("expected old login name to fail, got %v", err)
	}
	if _, err := c.AuthenticateUser("alice2", "pw"); err != nil {
		t.Fatalf("expected new login name to authenticate: %v", err)
	}

	gotThread, err := c.GetThread(thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotThread.Author != "alice2" || gotThread.AuthorID != alice.ID {
		t.Fatalf("expected thread author display to transfer, got %+v", gotThread)
	}
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Author != "alice2" || posts[0].AuthorID != alice.ID {
		t.Fatalf("expected post author display to transfer, got %+v", posts)
	}
	if _, err := c.TransferUserID(alice.ID, admin.Name); err == nil {
		t.Fatalf("expected duplicate transfer target to fail")
	}
}

func TestDeleteUserHardPurgesAccount(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	if err := c.UpdateUserProfile(alice.ID, "Alice", "", "", "", "secret sig", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateUserPrivateProfile(&core.UserPrivateProfile{
		UserID:            alice.ID,
		RealName:          "Alice Real",
		RegistrationEmail: "alice@register.example",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveUserPersonalFile(alice.ID, "plan.txt", "private text", false); err != nil {
		t.Fatal(err)
	}
	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Delete thread",
		Body:  "before deletion",
	})

	if err := c.DeleteUser(admin.ID, alice.ID, "operator purge"); err != nil {
		t.Fatal(err)
	}
	if old, err := c.UserByName("alice"); err != nil || old != nil {
		t.Fatalf("expected deleted name lookup empty, user=%+v err=%v", old, err)
	}
	if old, err := c.UserByID(alice.ID); err != nil || old != nil {
		t.Fatalf("expected deleted id lookup empty, user=%+v err=%v", old, err)
	}
	if _, err := c.AuthenticateUser("alice", "pw"); !errors.Is(err, core.ErrInvalidCredentials) {
		t.Fatalf("expected deleted login name to fail, got %v", err)
	}

	gotThread, err := c.GetThread(thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotThread.Author != "[deleted]" || gotThread.AuthorID == alice.ID {
		t.Fatalf("expected thread author tombstoned, got %+v", gotThread)
	}
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Author != "[deleted]" || posts[0].AuthorID == alice.ID || posts[0].Signature != "" {
		t.Fatalf("expected post author/signature tombstoned, got %+v", posts)
	}
	files, err := c.ListUserPersonalFiles(alice.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("expected personal files purged, got %+v", files)
	}
	privateProfile, err := c.UserPrivateProfile(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if privateProfile.RealName != "" || privateProfile.RegistrationEmail != "" {
		t.Fatalf("expected private profile purged, got %+v", privateProfile)
	}
	if err := c.DeleteUser(admin.ID, admin.ID, "self"); !errors.Is(err, core.ErrAccountDeleteForbidden) {
		t.Fatalf("expected self deletion forbidden, got %v", err)
	}
}

func TestUserLoginACL(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	if allowed, err := c.UserLoginAllowed(alice.ID, "198.51.100.10"); err != nil || !allowed {
		t.Fatalf("disabled login ACL should allow by default, allowed=%v err=%v", allowed, err)
	}
	if err := c.SetUserLoginACLSettings(alice.ID, true); err == nil {
		t.Fatalf("expected enabling empty login ACL to fail")
	}
	rule, err := c.SaveUserLoginACLRule(alice.ID, "", "198.51.100.0/24", "campus vpn", -1, true)
	if err != nil {
		t.Fatalf("save login ACL rule: %v", err)
	}
	if err := c.SetUserLoginACLSettings(alice.ID, true); err != nil {
		t.Fatalf("enable login ACL: %v", err)
	}
	if _, err := c.AuthenticateUserFromHost("alice", "pw", "203.0.113.9"); err == nil {
		t.Fatalf("expected disallowed host to fail login")
	}
	if _, err := c.AuthenticateUserFromHost("alice", "pw", "198.51.100.10"); err != nil {
		t.Fatalf("expected CIDR-allowed host to login: %v", err)
	}
	if _, err := c.SaveUserLoginACLRule(alice.ID, rule.ID, "203.0.113.*", "lab", -1, true); err != nil {
		t.Fatalf("update login ACL rule: %v", err)
	}
	if _, err := c.AuthenticateUserFromHost("alice", "pw", "203.0.113.9"); err != nil {
		t.Fatalf("expected wildcard-allowed host to login: %v", err)
	}
	bundle, err := c.ListUserLoginACL(alice.ID, "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.Settings.Enabled || !bundle.Allowed || len(bundle.Rules) != 1 || bundle.Rules[0].Pattern != "203.0.113.*" {
		t.Fatalf("expected enabled matching ACL bundle, got %+v", bundle)
	}
	if err := c.DeleteUserLoginACLRule(alice.ID, rule.ID); err != nil {
		t.Fatalf("delete login ACL rule: %v", err)
	}
	if allowed, err := c.UserLoginAllowed(alice.ID, "203.0.113.9"); err != nil || allowed {
		t.Fatalf("enabled empty ACL should deny, allowed=%v err=%v", allowed, err)
	}
	if err := c.SetUserLoginACLSettings(alice.ID, false); err != nil {
		t.Fatalf("disable login ACL: %v", err)
	}
	if _, err := c.AuthenticateUserFromHost("alice", "pw", "203.0.113.9"); err != nil {
		t.Fatalf("disabled ACL should allow login: %v", err)
	}
}

func TestDeactivateAccountCreatesGoodbyeRecord(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	if err := c.DeactivateAccount(alice.ID, "bad", "private farewell note"); err == nil {
		t.Fatalf("expected wrong password to block account deactivation")
	}
	if err := c.DeactivateAccount(alice.ID, "pw", "private farewell note"); err != nil {
		t.Fatalf("deactivate account failed: %v", err)
	}
	if _, err := c.AuthenticateUser("alice", "pw"); err == nil {
		t.Fatalf("deactivated account should not authenticate")
	}
	closed, err := c.UserByID(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed == nil || closed.DeactivatedAt == 0 || closed.DeactivatedBy != alice.ID || closed.DeactivatedReason != "private farewell note" {
		t.Fatalf("expected deactivated account metadata, got %+v", closed)
	}

	systemBoard, err := c.GetBoard("Goodbye")
	if err != nil {
		t.Fatal(err)
	}
	if systemBoard == nil || systemBoard.Name != "Goodbye" {
		t.Fatalf("expected generated Goodbye board, got %+v", systemBoard)
	}
	threads, err := c.ListThreads("Goodbye", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].Title != "Goodbye: alice" {
		t.Fatalf("expected generated Goodbye thread, got %+v", threads)
	}
	posts, err := c.ListPosts(threads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || !strings.Contains(posts[0].Body, "Status: deactivated") {
		t.Fatalf("expected generated Goodbye post, got %+v", posts)
	}
	if strings.Contains(posts[0].Body, "private farewell note") {
		t.Fatalf("Goodbye post leaked private deactivation note: %q", posts[0].Body)
	}
}

func TestRegisterUserCreatesNewcomerRecord(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	systemBoard, err := c.GetBoard("newcomers")
	if err != nil {
		t.Fatal(err)
	}
	if systemBoard == nil || systemBoard.Name != "newcomers" {
		t.Fatalf("expected generated newcomers board, got %+v", systemBoard)
	}
	threads, err := c.ListThreads("newcomers", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 || threads[0].Title != "New user: alice" {
		t.Fatalf("expected generated newcomer threads, got %+v", threads)
	}
	if threads[0].AuthorID != alice.ID {
		t.Fatalf("expected newcomer thread to snapshot account author id, got %+v", threads[0])
	}
	posts, err := c.ListPosts(threads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || !strings.Contains(posts[0].Body, "Status: registered") || !strings.Contains(posts[0].Body, "Role: user") {
		t.Fatalf("expected generated newcomer post, got %+v", posts)
	}
	if strings.Contains(posts[0].Body, "pw") {
		t.Fatalf("newcomer post leaked private password data: %q", posts[0].Body)
	}
}

func TestAccountRegistrationApprovalQueue(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	settings, err := c.SetAccountRegistrationSettings(true)
	if err != nil {
		t.Fatal(err)
	}
	if settings == nil || !settings.RequireApproval {
		t.Fatalf("expected registration approval enabled, got %+v", settings)
	}

	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if bob.RegistrationStatus != "pending" {
		t.Fatalf("expected bob to be pending, got %+v", bob)
	}
	if _, err := c.AuthenticateUser("bob", "pw"); !errors.Is(err, core.ErrAccountPending) {
		t.Fatalf("expected pending login rejection, got %v", err)
	}
	pending, err := c.ListAccountRegistrations("pending", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Name != "bob" {
		t.Fatalf("expected bob pending registration, got %+v", pending)
	}

	review, err := c.ReviewAccountRegistration(bob.ID, admin.ID, "approved", "welcome")
	if err != nil {
		t.Fatal(err)
	}
	if review.Status != "approved" || review.ReviewedBy != admin.ID {
		t.Fatalf("expected approved review, got %+v", review)
	}
	if _, err := c.AuthenticateUser("bob", "pw"); err != nil {
		t.Fatalf("expected approved bob to authenticate: %v", err)
	}
	threads, err := c.ListThreads("newcomers", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundBob := false
	for _, thread := range threads {
		if strings.Contains(thread.Title, "bob") {
			foundBob = true
		}
	}
	if !foundBob {
		t.Fatalf("expected approved bob to create newcomer record, got %+v", threads)
	}

	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReviewAccountRegistration(carol.ID, admin.ID, "rejected", "not this time"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AuthenticateUser("carol", "pw"); !errors.Is(err, core.ErrAccountRejected) {
		t.Fatalf("expected rejected login rejection, got %v", err)
	}
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

func TestBoardFavoritesLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	if favorites, err := c.ListFavoriteBoards(alice.ID); err != nil {
		t.Fatal(err)
	} else if len(favorites) != 1 || favorites[0].ID != "general" {
		t.Fatalf("expected general default favorite for new user, got %+v", favorites)
	}

	exec(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{
		Board:    "general",
		Favorite: true,
	})

	favorites, err := c.ListFavoriteBoards(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(favorites) != 1 || favorites[0].ID != "general" {
		t.Fatalf("expected alice to favorite general, got %+v", favorites)
	}

	bobFavorites, err := c.ListFavoriteBoards(bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobFavorites) != 1 || bobFavorites[0].ID != "general" {
		t.Fatalf("expected bob to get independent general default favorite, got %+v", bobFavorites)
	}

	exec(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{
		Board:    "general",
		Favorite: false,
	})

	favorites, err = c.ListFavoriteBoards(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(favorites) != 0 {
		t.Fatalf("expected favorite removal, got %+v", favorites)
	}

	execExpectErr(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{
		Board:    "missing",
		Favorite: true,
	}, proto.ErrNotFound)
}

func TestFavoriteFoldersLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, alice, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "tech",
		Name:        "Tech",
		Description: "Technology",
	})
	exec(t, c, alice, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "life",
		Name:        "Life",
		Description: "Life",
	})

	work := exec(t, c, alice, proto.CmdCreateFavoriteFolder, proto.CreateFavoriteFolderPayload{
		Name: "Work",
	})
	project := exec(t, c, alice, proto.CmdCreateFavoriteFolder, proto.CreateFavoriteFolderPayload{
		Name:     "Projects",
		ParentID: work.ID,
	})

	parentID := project.ID
	execExpectErr(t, c, alice, proto.CmdUpdateFavoriteFolder, proto.UpdateFavoriteFolderPayload{
		Folder:   work.ID,
		ParentID: &parentID,
	}, proto.ErrValidationFailed)

	exec(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{
		Board:    "general",
		Favorite: true,
	})
	exec(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{
		Board:    "tech",
		Favorite: true,
		FolderID: work.ID,
	})
	zero := 0
	exec(t, c, alice, proto.CmdMoveBoardFavorite, proto.MoveBoardFavoritePayload{
		Board:    "life",
		FolderID: work.ID,
		Position: &zero,
	})

	tree, err := c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Folders) != 2 {
		t.Fatalf("expected two favorite folders, got %+v", tree.Folders)
	}
	if len(tree.Boards) != 3 {
		t.Fatalf("expected three favorite boards, got %+v", tree.Boards)
	}
	if tree.Boards[0].ID != "general" || tree.Boards[0].FolderID != "" {
		t.Fatalf("expected general in root favorites, got %+v", tree.Boards)
	}
	if tree.Boards[1].ID != "life" || tree.Boards[1].FolderID != work.ID {
		t.Fatalf("expected life to move ahead inside work folder, got %+v", tree.Boards)
	}
	if tree.Boards[2].ID != "tech" || tree.Boards[2].FolderID != work.ID {
		t.Fatalf("expected tech to remain inside work folder, got %+v", tree.Boards)
	}

	root := ""
	exec(t, c, alice, proto.CmdUpdateFavoriteFolder, proto.UpdateFavoriteFolderPayload{
		Folder:   project.ID,
		Name:     "Projects Renamed",
		ParentID: &root,
		Position: &zero,
	})

	tree, err = c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Folders[0].ID != project.ID || tree.Folders[0].ParentID != "" || tree.Folders[0].Name != "Projects Renamed" {
		t.Fatalf("expected project folder renamed and moved to root, got %+v", tree.Folders)
	}

	execExpectErr(t, c, bob, proto.CmdMoveBoardFavorite, proto.MoveBoardFavoritePayload{
		Board:    "general",
		FolderID: work.ID,
	}, proto.ErrNotFound)

	exec(t, c, alice, proto.CmdDeleteFavoriteFolder, proto.DeleteFavoriteFolderPayload{Folder: work.ID})

	tree, err = c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, board := range tree.Boards {
		if board.ID == "life" && board.FolderID != "" {
			t.Fatalf("expected deleting work to move life to root, got %+v", board)
		}
		if board.ID == "tech" && board.FolderID != "" {
			t.Fatalf("expected deleting work to move tech to root, got %+v", board)
		}
	}
}

func TestFavoriteFolderReadMarkersAndImport(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	carol := registerAndGetUser(t, c, "carol", "pw")

	exec(t, c, alice, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "tech", Name: "Tech"})
	exec(t, c, alice, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "life", Name: "Life"})

	work := exec(t, c, alice, proto.CmdCreateFavoriteFolder, proto.CreateFavoriteFolderPayload{Name: "Work"})
	child := exec(t, c, alice, proto.CmdCreateFavoriteFolder, proto.CreateFavoriteFolderPayload{Name: "Child", ParentID: work.ID})
	exec(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{Board: "tech", Favorite: true, FolderID: work.ID})
	exec(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{Board: "life", Favorite: true, FolderID: child.ID})

	tech := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "tech", Title: "Tech unread", Body: "first"})
	life := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "life", Title: "Life unread", Body: "first"})

	unread, err := c.ListUnreadThreadSummaries(alice, false, work.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasThread(unread, tech.ID) || !hasThread(unread, life.ID) {
		t.Fatalf("expected favorite folder unread to include descendant boards, got %+v", unread)
	}

	exec(t, c, alice, proto.CmdMarkFavoriteFolderRead, proto.MarkFavoriteFolderReadPayload{Folder: work.ID})
	unread, err = c.ListUnreadThreadSummaries(alice, false, work.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if hasThread(unread, tech.ID) || hasThread(unread, life.ID) {
		t.Fatalf("expected favorite folder mark-read to clear scoped unread threads, got %+v", unread)
	}

	exec(t, c, alice, proto.CmdRestoreFavoriteFolderRead, proto.RestoreFavoriteFolderReadPayload{Folder: work.ID})
	unread, err = c.ListUnreadThreadSummaries(alice, false, work.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasThread(unread, tech.ID) || !hasThread(unread, life.ID) {
		t.Fatalf("expected favorite folder restore to restore scoped unread threads, got %+v", unread)
	}

	exported, err := c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := c.ImportFavoriteTree(carol.ID, exported, true)
	if err != nil {
		t.Fatal(err)
	}
	importedWork := favoriteFolderByName(imported, "Work")
	importedChild := favoriteFolderByName(imported, "Child")
	if importedWork == nil || importedChild == nil {
		t.Fatalf("expected imported nested folders, got %+v", imported.Folders)
	}
	if importedChild.ParentID != importedWork.ID {
		t.Fatalf("expected imported child folder parent remapped to imported work folder, got %+v", imported.Folders)
	}
	if got := favoriteFolderForBoard(imported, "tech"); got != importedWork.ID {
		t.Fatalf("expected imported tech favorite in work folder %q, got %q in %+v", importedWork.ID, got, imported.Boards)
	}
	if got := favoriteFolderForBoard(imported, "life"); got != importedChild.ID {
		t.Fatalf("expected imported life favorite in child folder %q, got %q in %+v", importedChild.ID, got, imported.Boards)
	}
}

func TestBoardDirectoryHierarchy(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")

	execExpectErr(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:       "orphan",
		Name:     "Orphan",
		ParentID: "missing",
	}, proto.ErrNotFound)

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "clubs",
		Name:        "Clubs",
		Description: "Campus clubs",
		ParentID:    "general",
	})
	zero := 0
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "music",
		Name:        "Music",
		Description: "Music club",
		ParentID:    "clubs",
		Position:    &zero,
	})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:       "sports",
		Name:     "Sports",
		ParentID: "general",
	})

	categories, err := c.ListCategories()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]core.Category{}
	for _, category := range categories {
		byID[category.ID] = category
	}

	if byID["general"].ParentID != "" {
		t.Fatalf("expected general to be a root category, got %+v", byID["general"])
	}
	if byID["clubs"].ParentID != "general" || byID["clubs"].Position != 0 {
		t.Fatalf("expected clubs under general at position 0, got %+v", byID["clubs"])
	}
	if byID["music"].ParentID != "clubs" || byID["music"].Position != 0 {
		t.Fatalf("expected music under clubs at position 0, got %+v", byID["music"])
	}
	if byID["sports"].ParentID != "general" || byID["sports"].Position != 1 {
		t.Fatalf("expected sports under general at appended position, got %+v", byID["sports"])
	}
	if _, ok := byID["orphan"]; ok {
		t.Fatalf("rejected board should not create a category, got %+v", byID["orphan"])
	}

	updated, err := c.UpdateCategory(admin.ID, "sports", core.CategoryUpdate{
		Name:        stringPtr("Athletics"),
		Description: stringPtr("Sports desk"),
		ParentID:    stringPtr("clubs"),
		Visibility:  stringPtr("staff"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Athletics" || updated.Description != "Sports desk" || updated.ParentID != "clubs" || updated.Visibility != "staff" {
		t.Fatalf("expected updated sports category, got %+v", updated)
	}
	board, err := c.GetBoard("sports")
	if err != nil {
		t.Fatal(err)
	}
	if board.Name != "Athletics" || board.Description != "Sports desk" {
		t.Fatalf("expected board metadata to follow category update, got %+v", board)
	}
	alice := registerAndGetUser(t, c, "alice", "pw")
	visible, err := c.ListCategoriesForUser(alice)
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range visible {
		if category.ID == "sports" {
			t.Fatalf("staff category should be hidden from normal users, got %+v", visible)
		}
	}
	if _, err := c.UpdateCategory(admin.ID, "clubs", core.CategoryUpdate{ParentID: stringPtr("music")}); err == nil {
		t.Fatalf("expected category cycle to be rejected")
	}
}

func TestCommunityRankingsAndStats(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	snapshotAt := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC).UnixMilli()

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "tech", Name: "Tech"})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "life", Name: "Life"})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "secret", Name: "Secret"})
	if _, err := c.DB.Exec(`UPDATE categories SET created_at=? WHERE id IN ('tech','life','secret')`, snapshotAt); err != nil {
		t.Fatal(err)
	}
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secret",
		MemberReadMode: boolPtr(true),
	})
	execExpectErr(t, c, alice, proto.CmdSetRecommendedBoard, proto.SetRecommendedBoardPayload{
		Board:       "tech",
		Recommended: true,
	}, proto.ErrForbidden)
	execExpectErr(t, c, admin, proto.CmdSetRecommendedBoard, proto.SetRecommendedBoardPayload{
		Board:       "secret",
		Recommended: true,
	}, proto.ErrValidationFailed)
	exec(t, c, admin, proto.CmdSetRecommendedBoard, proto.SetRecommendedBoardPayload{
		Board:       "tech",
		Recommended: true,
		Note:        "Start here for campus computing.",
		Position:    intPtr(10),
	})
	exec(t, c, admin, proto.CmdSetRecommendedBoard, proto.SetRecommendedBoardPayload{
		Board:       "life",
		Recommended: true,
		Note:        "Campus life and clubs.",
		Position:    intPtr(20),
	})
	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "tech",
		User:      "bob",
		Moderator: true,
		Position:  intPtr(0),
	})
	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "life",
		User:      "alice",
		Moderator: true,
		Position:  intPtr(0),
	})
	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "secret",
		User:      "admin",
		Moderator: true,
		Position:  intPtr(0),
	})
	recommended, err := c.ListRecommendedBoards(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recommended) != 2 || recommended[0].ID != "tech" || recommended[0].Note != "Start here for campus computing." || recommended[1].ID != "life" {
		t.Fatalf("expected ordered recommended public boards, got %+v", recommended)
	}

	hot := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "tech",
		Title: "Hot topic",
		Body:  "first",
	})
	hotReply := exec(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: hot.ID,
		Body:   "second",
	})
	life := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "life",
		Title: "Quiet topic",
		Body:  "first",
	})
	secret := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private topic",
		Body:  "hidden",
	})
	exec(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: secret.ID,
		Body:   "classified reply",
	})

	posts, err := c.ListPosts(hot.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) == 0 {
		t.Fatal("expected hot topic posts")
	}
	exec(t, c, alice, proto.CmdReactPost, proto.ReactPostPayload{
		Post:  posts[0].ID,
		Emoji: "+1",
	})
	archivePost := exec(t, c, admin, proto.CmdCuratePost, proto.CuratePostPayload{
		Post:  posts[0].ID,
		Kind:  "archive",
		Title: "Hot archive post",
		Path:  "guide",
	})
	exec(t, c, admin, proto.CmdSetDigestEntryBody, proto.SetDigestEntryBodyPayload{
		Entry: archivePost.ID,
		Body:  "Edited archive ranking copy",
	})
	exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: hot.ID,
		Kind:   "archive",
		Title:  "Hot archive thread",
		Path:   "guide",
	})
	exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: secret.ID,
		Kind:   "archive",
		Title:  "Private archive thread",
		Path:   "private",
	})
	exec(t, c, admin, proto.CmdCuratePost, proto.CuratePostPayload{
		Post:  posts[0].ID,
		Kind:  "recommended",
		Title: "Hot recommended post",
		Path:  "frontpage",
		Note:  "Worth reading.",
	})
	exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: secret.ID,
		Kind:   "recommended",
		Title:  "Private recommended thread",
		Path:   "private",
		Note:   "Do not expose.",
	})
	exec(t, c, alice, proto.CmdSetPresence, proto.SetPresencePayload{Status: "active", Board: "tech"})
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{Status: "active", Board: "tech"})
	if err := c.RecordLogin(bob.ID); err != nil {
		t.Fatal(err)
	}

	stats, err := c.GetCommunityStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalUsers != 3 || stats.TotalLogins != 1 || stats.TotalBoards != 4 || stats.TotalThreads != 3 || stats.TotalPosts != 5 || stats.TotalReactions != 1 || stats.OnlineUsers != 2 || stats.MaxOnlineUsers != 2 || stats.MaxOnlineAt == 0 {
		t.Fatalf("unexpected community stats: %+v", stats)
	}
	if stats.HeadSeq == 0 {
		t.Fatalf("expected head seq in community stats, got %+v", stats)
	}
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{Status: "offline"})
	stats, err = c.GetCommunityStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.OnlineUsers != 1 || stats.MaxOnlineUsers != 2 || stats.MaxOnlineAt == 0 {
		t.Fatalf("expected max-online history to preserve peak after offline, got %+v", stats)
	}
	if _, err := c.DB.Exec(`INSERT INTO user_activity (user_id, total_online_seconds)
		VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET total_online_seconds=excluded.total_online_seconds`, bob.ID, int64(120)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Exec(`UPDATE user_activity SET last_visit_day='2026-06-04' WHERE user_id IN (?, ?)`, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(c.DB, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	stats, err = c.GetCommunityStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalOnlineSeconds != 120 {
		t.Fatalf("expected total online seconds in community stats, got %+v", stats)
	}
	if err := projections.SetGuestPresence(c.DB, "guest_web", "active", "web", "203.0.113.10", time.Now().UTC().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	stats, err = c.GetCommunityStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.OnlineGuests != 1 || stats.MaxOnlineGuests != 1 || stats.MaxOnlineGuestsAt == 0 {
		t.Fatalf("expected guest counters in community stats, got %+v", stats)
	}
	previousAt := time.Now().UTC().Add(-24 * time.Hour)
	if _, err := c.DB.Exec(`INSERT INTO community_stat_history (
		day, snapshot_at, total_users, total_boards, total_threads, total_posts,
		total_reactions, total_mail, total_direct_messages, total_logins, total_online_seconds, online_users,
		online_guests, max_online_users, max_online_at, max_online_guests,
		max_online_guests_at, head_seq
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		previousAt.Format("2006-01-02"), previousAt.UnixMilli(),
		2, 3, 1, 2, 0, 0, 0, 0, int64(60), 0, 0, 1, previousAt.UnixMilli(), 0, int64(0), 1,
	); err != nil {
		t.Fatal(err)
	}
	history, err := c.ListCommunityStatHistory(7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].OnlineUsers != 1 || history[0].MaxOnlineUsers != 2 || history[0].MaxOnlineAt == 0 || history[0].TotalPosts != 5 {
		t.Fatalf("expected daily stat history with preserved max-online peak, got %+v", history)
	}
	if history[0].TotalLogins != 1 || history[0].DeltaUsers != 1 || history[0].DeltaLogins != 1 || history[0].DeltaBoards != 1 || history[0].DeltaThreads != 2 || history[0].DeltaPosts != 3 || history[0].DeltaReactions != 1 || history[0].DeltaMail != 0 || history[0].DeltaDirectMessages != 0 {
		t.Fatalf("expected newest daily stat history row to include deltas, got %+v", history[0])
	}
	if history[0].TotalOnlineSeconds != 120 || history[0].DeltaOnlineSeconds != 60 {
		t.Fatalf("expected newest daily stat history row to include online-time totals and deltas, got %+v", history[0])
	}
	if history[0].OnlineGuests != 1 || history[0].DeltaGuests != 1 || history[0].MaxOnlineGuests != 1 || history[0].MaxOnlineGuestsAt == 0 {
		t.Fatalf("expected newest daily stat history row to include guest counters and deltas, got %+v", history[0])
	}
	if history[1].DeltaUsers != 0 || history[1].DeltaLogins != 0 || history[1].DeltaPosts != 0 || history[1].DeltaReactions != 0 {
		t.Fatalf("expected oldest fetched daily stat history row to have zero deltas without an older comparison row, got %+v", history[1])
	}

	boards, err := c.ListBoardRankings(alice, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(boards) == 0 || boards[0].ID != "tech" || boards[0].PostCount != 2 || boards[0].OnlineUsers != 1 {
		t.Fatalf("expected tech to lead board rankings, got %+v", boards)
	}
	for _, board := range boards {
		if board.ID == "secret" {
			t.Fatalf("ordinary user should not see member-read board ranking, got %+v", boards)
		}
	}
	adminBoards, err := c.ListBoardRankings(admin, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	secretVisible := false
	for _, board := range adminBoards {
		if board.ID == "secret" {
			secretVisible = true
		}
	}
	if !secretVisible {
		t.Fatalf("admin should see member-read board ranking, got %+v", adminBoards)
	}

	archives, err := c.ListArchiveRankings(alice, "archive", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) == 0 || archives[0].BoardID != "tech" || archives[0].Path != "guide" || archives[0].EntryCount != 2 || archives[0].EditedCount != 1 {
		t.Fatalf("expected tech guide to lead archive rankings, got %+v", archives)
	}
	for _, archive := range archives {
		if archive.BoardID == "secret" {
			t.Fatalf("ordinary user should not see member-read archive ranking, got %+v", archives)
		}
	}
	adminArchives, err := c.ListArchiveRankings(admin, "archive", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	secretArchiveVisible := false
	for _, archive := range adminArchives {
		if archive.BoardID == "secret" && archive.Path == "private" {
			secretArchiveVisible = true
		}
	}
	if !secretArchiveVisible {
		t.Fatalf("admin should see member-read archive ranking, got %+v", adminArchives)
	}

	threads, err := c.ListThreadRankings(alice, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) == 0 || threads[0].ID != hot.ID || threads[0].ParticipantCount != 1 || threads[0].Score <= threads[0].PostCount {
		t.Fatalf("expected hot topic to lead thread rankings with participant/reaction-weighted score, got %+v", threads)
	}
	for _, thread := range threads {
		if thread.ID == secret.ID {
			t.Fatalf("ordinary user should not see member-read thread ranking, got %+v", threads)
		}
	}
	boardThreads, err := c.ListThreadRankings(alice, "life", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(boardThreads) != 1 || boardThreads[0].ID != life.ID {
		t.Fatalf("expected board-scoped life ranking, got %+v", boardThreads)
	}
	replies, err := c.ListReplyRankings(alice, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || replies[0].PostID != hotReply.ID || replies[0].ThreadID != hot.ID || !strings.Contains(replies[0].Excerpt, "second") {
		t.Fatalf("expected latest public reply only, got %+v", replies)
	}
	adminReplies, err := c.ListReplyRankings(admin, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminReplies) == 0 || adminReplies[0].ThreadID != secret.ID || !strings.Contains(adminReplies[0].Excerpt, "classified reply") {
		t.Fatalf("expected admin latest reply to include private board, got %+v", adminReplies)
	}

	users, err := c.ListUserRankings(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) == 0 || users[0].Name != "bob" || users[0].PostsCreated != 2 || users[0].ReactionsReceived != 1 || users[0].LoginCount != 1 || users[0].TotalOnlineSeconds != 120 {
		t.Fatalf("expected bob to lead user rankings, got %+v", users)
	}
	exec(t, c, alice, proto.CmdBlessUser, proto.BlessUserPayload{
		User:    "bob",
		Message: "Good luck on finals.",
	})
	exec(t, c, admin, proto.CmdBlessUser, proto.BlessUserPayload{
		User:    "bob",
		Message: "Ace the lab.",
	})
	if _, err := c.DB.Exec(`UPDATE blessings SET created_at=?`, snapshotAt); err != nil {
		t.Fatal(err)
	}
	blessings, err := c.ListBlessingRankings(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(blessings) == 0 || blessings[0].Name != "bob" || blessings[0].BlessingCount != 2 {
		t.Fatalf("expected bob to lead blessing rankings, got %+v", blessings)
	}

	execExpectErr(t, c, alice, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{
		Date: "2026-06-04",
	}, proto.ErrForbidden)
	snapshot := exec(t, c, admin, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{
		Date: "2026-06-04",
	})
	if snapshot.ID == "" {
		t.Fatalf("expected generated stats snapshot thread id, got %+v", snapshot)
	}
	systemBoard, err := c.GetBoard("BBSLists")
	if err != nil {
		t.Fatal(err)
	}
	if systemBoard == nil || systemBoard.Name != "BBSLists" {
		t.Fatalf("expected generated BBSLists board, got %+v", systemBoard)
	}
	systemThreads, err := c.ListThreads("BBSLists", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(systemThreads) != 13 {
		t.Fatalf("expected generated stats, login-history, user-activity, board-online, online-user roster, board-moderator activity, board-activity, board-rank, new-board, recommended-board, recommended-article, hot-topic, and blessing threads, got %+v", systemThreads)
	}
	if !hasThreadSummary(systemThreads, snapshot.ID, "2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_countlogins_20260604", "Login count history 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_statguy_20260604", "User activity rankings 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_bonline_20260604", "Board online occupancy 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_uonline_20260604", "Online user roster 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_statbm_20260604", "Board moderator activity 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_boardlog_20260604", "Board activity history 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_boardrank_20260604", "Board popularity list 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_newboards_20260604", "New board list 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_rcmdbrd_20260604", "Recommended board list 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_commend_20260604", "Recommended article list 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_toplog_20260604", "Hot topic history 2026-06-04") ||
		!hasThreadSummary(systemThreads, "bbslists_bless_20260604", "Daily blessing list 2026-06-04") {
		t.Fatalf("expected generated stats, login-history, user-activity, board-online, online-user roster, board-moderator activity, board-activity, board-rank, new-board, recommended-board, recommended-article, hot-topic, and blessing threads, got %+v", systemThreads)
	}
	systemPosts, err := c.ListPosts(snapshot.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(systemPosts) != 1 {
		t.Fatalf("expected one generated stats post, got %+v", systemPosts)
	}
	body := systemPosts[0].Body
	for _, want := range []string{"Total users: 3", "Total logins: 1", "Total posts: 5", "Total online time: 2m", "Online guests: 1", "Max online users: 2", "Max online guests: 1", "Recent daily history", "3 users (+1)", "1 login (+1)", "1 guests (+1)", "5 posts (+3)", "1 reactions (+1)", "2m online time (+1m)", "max 2 users", "max 1 guests", "Active boards", "(tech): 2 posts", "Hot threads", "Hot topic", "1 participants", "Latest replies", "second", "Top users", "bob", "Blessings", "bob: 2 blessings", "Archive paths", "guide"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected stats snapshot body to contain %q, got:\n%s", want, body)
		}
	}
	loginPosts, err := c.ListPosts("bbslists_countlogins_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(loginPosts) != 1 {
		t.Fatalf("expected one generated login-history post, got %+v", loginPosts)
	}
	loginBody := loginPosts[0].Body
	for _, want := range []string{"Login count history 2026-06-04", "Total logins: 1", "Recent login and guest history", "1 login (+1)", "3 users (+1)", "1 guests (+1)", "2m online time (+1m)"} {
		if !strings.Contains(loginBody, want) {
			t.Fatalf("expected login-history body to contain %q, got:\n%s", want, loginBody)
		}
	}
	userActivityPosts, err := c.ListPosts("bbslists_statguy_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(userActivityPosts) != 1 {
		t.Fatalf("expected one generated user-activity post, got %+v", userActivityPosts)
	}
	userActivityBody := userActivityPosts[0].Body
	for _, want := range []string{"User activity rankings 2026-06-04", "Ranked active users: 3", "Ranked posts: 5", "Ranked reactions received: 1", "Ranked logins: 1", "Ranked stay time: 2m", "Top posters", "bob: 2 posts, 1 reactions received, 1 login, 2m stay time", "Top login counts", "Top stay time", "Top community score", "score 24"} {
		if !strings.Contains(userActivityBody, want) {
			t.Fatalf("expected user-activity body to contain %q, got:\n%s", want, userActivityBody)
		}
	}
	boardOnlinePosts, err := c.ListPosts("bbslists_bonline_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(boardOnlinePosts) != 1 {
		t.Fatalf("expected one generated board-online post, got %+v", boardOnlinePosts)
	}
	boardOnlineBody := boardOnlinePosts[0].Body
	for _, want := range []string{"Board online occupancy 2026-06-04", "Online users: 1", "Online guests: 1", "Boards with online users: 1", "Public board online ranking", "Tech (tech): 1 users online, 2 posts, 1 threads"} {
		if !strings.Contains(boardOnlineBody, want) {
			t.Fatalf("expected board-online body to contain %q, got:\n%s", want, boardOnlineBody)
		}
	}
	onlineRosterPosts, err := c.ListPosts("bbslists_uonline_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(onlineRosterPosts) != 1 {
		t.Fatalf("expected one generated online-user roster post, got %+v", onlineRosterPosts)
	}
	onlineRosterBody := onlineRosterPosts[0].Body
	for _, want := range []string{"Online user roster 2026-06-04", "Online user sessions: 1", "Distinct online users: 1", "Online guests: 1", "Visible online users", "alice: active on Tech (tech)"} {
		if !strings.Contains(onlineRosterBody, want) {
			t.Fatalf("expected online-user roster body to contain %q, got:\n%s", want, onlineRosterBody)
		}
	}
	boardModeratorPosts, err := c.ListPosts("bbslists_statbm_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(boardModeratorPosts) != 1 {
		t.Fatalf("expected one generated board-moderator activity post, got %+v", boardModeratorPosts)
	}
	boardModeratorBody := boardModeratorPosts[0].Body
	for _, want := range []string{"Board moderator activity 2026-06-04", "Public boards with moderators: 2", "Moderator assignments: 2", "Online moderators: 1", "Public board moderator roster", "Tech (tech): 1 moderators, 2 posts, 1 threads, 1 users online", "bob: position 0, offline, 1 login, 2 posts, 2m stay time, last activity 2026-06-04", "Life (life): 1 moderators, 1 posts, 1 threads", "alice: position 0, online, 0 logins, 1 post, 0s stay time, last activity 2026-06-04"} {
		if !strings.Contains(boardModeratorBody, want) {
			t.Fatalf("expected board-moderator body to contain %q, got:\n%s", want, boardModeratorBody)
		}
	}
	boardLogPosts, err := c.ListPosts("bbslists_boardlog_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(boardLogPosts) != 1 {
		t.Fatalf("expected one generated board-activity post, got %+v", boardLogPosts)
	}
	boardLogBody := boardLogPosts[0].Body
	for _, want := range []string{"Board activity history 2026-06-04", "Total boards", "Total threads", "Total posts: 5", "Ranked public boards", "Top public boards", "(tech): 2 posts", "Recent board activity history", "5 posts (+3)", "1 reactions (+1)"} {
		if !strings.Contains(boardLogBody, want) {
			t.Fatalf("expected board-activity body to contain %q, got:\n%s", want, boardLogBody)
		}
	}
	boardRankPosts, err := c.ListPosts("bbslists_boardrank_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(boardRankPosts) != 1 {
		t.Fatalf("expected one generated board-rank post, got %+v", boardRankPosts)
	}
	boardRankBody := boardRankPosts[0].Body
	for _, want := range []string{"Board popularity list 2026-06-04", "Ranked public boards: 2", "Users currently on ranked boards: 1", "Public board ranking", "Tech (tech): 2 posts, 1 threads, 1 users online", "Life (life): 1 posts, 1 threads, 0 users online"} {
		if !strings.Contains(boardRankBody, want) {
			t.Fatalf("expected board-rank body to contain %q, got:\n%s", want, boardRankBody)
		}
	}
	newBoardPosts, err := c.ListPosts("bbslists_newboards_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(newBoardPosts) != 1 {
		t.Fatalf("expected one generated new-board post, got %+v", newBoardPosts)
	}
	newBoardBody := newBoardPosts[0].Body
	for _, want := range []string{"New board list 2026-06-04", "Window: 2026-05-06 to 2026-06-04 UTC", "New public boards: 2", "Tech (tech)", "Life (life)"} {
		if !strings.Contains(newBoardBody, want) {
			t.Fatalf("expected new-board body to contain %q, got:\n%s", want, newBoardBody)
		}
	}
	recommendedBoardPosts, err := c.ListPosts("bbslists_rcmdbrd_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recommendedBoardPosts) != 1 {
		t.Fatalf("expected one generated recommended-board post, got %+v", recommendedBoardPosts)
	}
	recommendedBoardBody := recommendedBoardPosts[0].Body
	for _, want := range []string{"Recommended board list 2026-06-04", "Recommended public boards: 2", "Recommended public boards", "Tech (tech): 2 posts, 1 threads", "Curator note: Start here for campus computing.", "Life (life): 1 posts, 1 threads", "Curator note: Campus life and clubs."} {
		if !strings.Contains(recommendedBoardBody, want) {
			t.Fatalf("expected recommended-board body to contain %q, got:\n%s", want, recommendedBoardBody)
		}
	}
	recommendedArticlePosts, err := c.ListPosts("bbslists_commend_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recommendedArticlePosts) != 1 {
		t.Fatalf("expected one generated recommended-article post, got %+v", recommendedArticlePosts)
	}
	recommendedArticleBody := recommendedArticlePosts[0].Body
	for _, want := range []string{"Recommended article list 2026-06-04", "Recommended public articles: 1", "Recommended public articles", "Tech / Hot recommended post", "post recommendation", "Author: bob", "Path: frontpage", "Curator note: Worth reading.", "Curated by admin", "Excerpt: first"} {
		if !strings.Contains(recommendedArticleBody, want) {
			t.Fatalf("expected recommended-article body to contain %q, got:\n%s", want, recommendedArticleBody)
		}
	}
	topLogPosts, err := c.ListPosts("bbslists_toplog_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(topLogPosts) != 1 {
		t.Fatalf("expected one generated hot-topic post, got %+v", topLogPosts)
	}
	topLogBody := topLogPosts[0].Body
	for _, want := range []string{"Hot topic history 2026-06-04", "Ranked public hot topics", "Top public hot topics", "Tech / Hot topic", "1 participants", "2 posts", "1 reactions", "score", "Category hot topics"} {
		if !strings.Contains(topLogBody, want) {
			t.Fatalf("expected hot-topic body to contain %q, got:\n%s", want, topLogBody)
		}
	}
	blessPosts, err := c.ListPosts("bbslists_bless_20260604", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(blessPosts) != 1 {
		t.Fatalf("expected one generated blessing-list post, got %+v", blessPosts)
	}
	blessBody := blessPosts[0].Body
	for _, want := range []string{"Daily blessing list 2026-06-04", "Window: 2026-06-04 to 2026-06-04 UTC", "Blessed users: 1", "Recent blessings: 2", "bob: 2 blessings", "alice -> bob", "admin -> bob", "Good luck on finals.", "Ace the lab."} {
		if !strings.Contains(blessBody, want) {
			t.Fatalf("expected blessing-list body to contain %q, got:\n%s", want, blessBody)
		}
	}
	for _, forbidden := range []string{"Secret", "Private topic", "classified reply", "private"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("expected stats snapshot body to hide private %q, got:\n%s", forbidden, body)
		}
		if strings.Contains(boardLogBody, forbidden) {
			t.Fatalf("expected board-activity body to hide private %q, got:\n%s", forbidden, boardLogBody)
		}
		if strings.Contains(boardRankBody, forbidden) {
			t.Fatalf("expected board-rank body to hide private %q, got:\n%s", forbidden, boardRankBody)
		}
		if strings.Contains(boardOnlineBody, forbidden) {
			t.Fatalf("expected board-online body to hide private %q, got:\n%s", forbidden, boardOnlineBody)
		}
		if strings.Contains(onlineRosterBody, forbidden) {
			t.Fatalf("expected online-user roster body to hide private %q, got:\n%s", forbidden, onlineRosterBody)
		}
		if strings.Contains(boardModeratorBody, forbidden) {
			t.Fatalf("expected board-moderator body to hide private %q, got:\n%s", forbidden, boardModeratorBody)
		}
		if strings.Contains(userActivityBody, forbidden) {
			t.Fatalf("expected user-activity body to hide private %q, got:\n%s", forbidden, userActivityBody)
		}
		if strings.Contains(topLogBody, forbidden) {
			t.Fatalf("expected hot-topic body to hide private %q, got:\n%s", forbidden, topLogBody)
		}
		if strings.Contains(newBoardBody, forbidden) {
			t.Fatalf("expected new-board body to hide private %q, got:\n%s", forbidden, newBoardBody)
		}
		if strings.Contains(recommendedBoardBody, forbidden) {
			t.Fatalf("expected recommended-board body to hide private %q, got:\n%s", forbidden, recommendedBoardBody)
		}
		if strings.Contains(recommendedArticleBody, forbidden) {
			t.Fatalf("expected recommended-article body to hide private %q, got:\n%s", forbidden, recommendedArticleBody)
		}
	}
	again := exec(t, c, admin, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{
		Date: "2026-06-04",
	})
	if again.ID != snapshot.ID {
		t.Fatalf("expected repeated snapshot publish to reuse thread %q, got %+v", snapshot.ID, again)
	}
	systemThreads, err = c.ListThreads("BBSLists", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(systemThreads) != 13 {
		t.Fatalf("expected repeated snapshot publish not to duplicate thread, got %+v", systemThreads)
	}
}

func TestPresenceAccruesTotalOnlineTime(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	base := time.Now().UTC().Add(-10 * time.Minute).UnixMilli()
	if err := projections.SetUserPresence(c.DB, alice.ID, "web", "active", "", "", "", "", "", base); err != nil {
		t.Fatal(err)
	}
	if err := projections.SetUserPresence(c.DB, alice.ID, "web", "reading", "general", "", "", "", "", base+2*60*1000); err != nil {
		t.Fatal(err)
	}
	if err := projections.SetUserPresence(c.DB, alice.ID, "web", "offline", "", "", "", "", "", base+10*60*1000); err != nil {
		t.Fatal(err)
	}
	stats, err := c.GetCommunityStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalOnlineSeconds != 420 {
		t.Fatalf("expected two minutes plus capped five-minute offline accrual, got %+v", stats)
	}
	history, err := c.ListCommunityStatHistory(7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].TotalOnlineSeconds != 420 {
		t.Fatalf("expected online-time total in daily stat history, got %+v", history)
	}
}

func TestThreadRankingsUseRecencyDecay(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "decay", Name: "Decay"})
	stale := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "decay",
		Title: "Busy but old",
		Body:  "root",
	})
	for i := 0; i < 7; i++ {
		exec(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
			Thread: stale.ID,
			Body:   fmt.Sprintf("old reply %d", i),
		})
	}
	recent := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "decay",
		Title: "Fresh reply",
		Body:  "root",
	})
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: recent.ID,
		Body:   "new reply",
	})
	staleUpdatedAt := time.Now().Add(-14 * 24 * time.Hour).UnixMilli()
	if _, err := c.DB.Exec(`UPDATE threads SET updated_at=? WHERE id=?`, staleUpdatedAt, stale.ID); err != nil {
		t.Fatalf("age stale thread: %v", err)
	}

	threads, err := c.ListThreadRankings(alice, "decay", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) < 2 {
		t.Fatalf("expected recent and stale thread rankings, got %+v", threads)
	}
	if threads[0].ID != recent.ID || threads[1].ID != stale.ID || threads[0].Score <= threads[1].Score {
		t.Fatalf("expected recency decay to rank fresh thread before stale raw activity, got %+v", threads)
	}
}

func TestThreadRankingsCountDistinctParticipants(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "toplog", Name: "Top Log"})
	thread := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "toplog",
		Title: "Participant topic",
		Body:  "root",
	})
	exec(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "same participant again",
	})
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "second participant",
	})

	threads, err := c.ListThreadRankings(alice, "toplog", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != thread.ID || threads[0].PostCount != 3 || threads[0].ParticipantCount != 2 {
		t.Fatalf("expected distinct participant count on hot-topic ranking, got %+v", threads)
	}
}

func TestStatsExcludedBoardHiddenFromRankingSurfaces(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "visible_stats", Name: "Visible Stats"})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "hidden_stats", Name: "Hidden Stats"})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:         "hidden_stats",
		StatsExcluded: boolPtr(true),
	})
	hiddenInfo, err := c.GetBoardInfo("hidden_stats")
	if err != nil {
		t.Fatal(err)
	}
	if hiddenInfo == nil || !hiddenInfo.Board.StatsExcluded || !hiddenInfo.Settings.StatsExcluded {
		t.Fatalf("expected hidden_stats to expose statsExcluded setting, got %+v", hiddenInfo)
	}
	execExpectErr(t, c, admin, proto.CmdSetRecommendedBoard, proto.SetRecommendedBoardPayload{
		Board:       "hidden_stats",
		Recommended: true,
	}, proto.ErrValidationFailed)
	exec(t, c, admin, proto.CmdSetRecommendedBoard, proto.SetRecommendedBoardPayload{
		Board:       "visible_stats",
		Recommended: true,
		Note:        "Visible board recommendation",
	})

	visibleThread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "visible_stats",
		Title: "Visible topic",
		Body:  "visible root",
	})
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: visibleThread.ID,
		Body:   "visible reply",
	})
	hiddenThread := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "hidden_stats",
		Title: "Hidden topic",
		Body:  "hidden root",
	})
	exec(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: hiddenThread.ID,
		Body:   "hidden reply",
	})
	exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: visibleThread.ID,
		Kind:   "archive",
		Title:  "Visible archive",
		Path:   "stats",
	})
	exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: hiddenThread.ID,
		Kind:   "archive",
		Title:  "Hidden archive",
		Path:   "stats",
	})
	exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: visibleThread.ID,
		Kind:   "recommended",
		Title:  "Visible recommended article",
		Path:   "stats",
		Note:   "Visible recommendation",
	})
	exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: hiddenThread.ID,
		Kind:   "recommended",
		Title:  "Hidden recommended article",
		Path:   "stats",
		Note:   "Hidden recommendation",
	})

	boards, err := c.ListBoardRankings(alice, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasBoardRanking(boards, "visible_stats") || hasBoardRanking(boards, "hidden_stats") {
		t.Fatalf("expected stats-excluded board hidden from board rankings, got %+v", boards)
	}
	threads, err := c.ListThreadRankings(alice, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasThreadRanking(threads, visibleThread.ID) || hasThreadRanking(threads, hiddenThread.ID) {
		t.Fatalf("expected stats-excluded board hidden from thread rankings, got %+v", threads)
	}
	replies, err := c.ListReplyRankings(alice, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasReplyRankingThread(replies, visibleThread.ID) || hasReplyRankingThread(replies, hiddenThread.ID) {
		t.Fatalf("expected stats-excluded board hidden from reply rankings, got %+v", replies)
	}
	archives, err := c.ListArchiveRankings(alice, "archive", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArchiveRankingBoard(archives, "visible_stats") || hasArchiveRankingBoard(archives, "hidden_stats") {
		t.Fatalf("expected stats-excluded board hidden from archive rankings, got %+v", archives)
	}
	users, err := c.ListUserRankings(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.Name == "bob" && user.PostsCreated != 0 {
			t.Fatalf("expected stats-excluded posts omitted from user rankings, got %+v", users)
		}
	}
	exec(t, c, alice, proto.CmdSetPresence, proto.SetPresencePayload{Status: "active", Board: "visible_stats"})
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{Status: "active", Board: "hidden_stats"})
	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "visible_stats",
		User:      "alice",
		Moderator: true,
		Position:  intPtr(0),
	})
	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "hidden_stats",
		User:      "bob",
		Moderator: true,
		Position:  intPtr(0),
	})

	snapshot := exec(t, c, admin, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{
		Date: "2026-06-07",
	})
	for _, threadID := range []string{snapshot.ID, "bbslists_statguy_20260607", "bbslists_bonline_20260607", "bbslists_uonline_20260607", "bbslists_statbm_20260607", "bbslists_boardlog_20260607", "bbslists_boardrank_20260607", "bbslists_newboards_20260607", "bbslists_rcmdbrd_20260607", "bbslists_commend_20260607", "bbslists_toplog_20260607"} {
		posts, err := c.ListPosts(threadID, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(posts) != 1 {
			t.Fatalf("expected one generated post in %s, got %+v", threadID, posts)
		}
		if strings.Contains(posts[0].Body, "Hidden") || strings.Contains(posts[0].Body, "hidden_stats") {
			t.Fatalf("expected generated stats post %s to hide stats-excluded board, got:\n%s", threadID, posts[0].Body)
		}
		if !strings.Contains(posts[0].Body, "Visible") && threadID != snapshot.ID && threadID != "bbslists_statguy_20260607" {
			t.Fatalf("expected generated stats post %s to include visible board activity, got:\n%s", threadID, posts[0].Body)
		}
	}
}

func TestStatsPeriodHistorySystemPosts(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	insertCommunityStatHistory(t, c.DB, "2026-06-07", 10, 1, 1, 10, 1, 5, 60)
	insertCommunityStatHistory(t, c.DB, "2026-06-08", 11, 2, 2, 12, 2, 8, 120)
	insertCommunityStatHistory(t, c.DB, "2026-06-14", 12, 3, 4, 24, 5, 20, 600)
	insertCommunityStatHistory(t, c.DB, "2026-06-30", 13, 4, 6, 30, 8, 25, 900)
	insertCommunityStatHistory(t, c.DB, "2026-07-01", 14, 5, 7, 35, 9, 30, 1200)
	insertCommunityStatHistory(t, c.DB, "2026-07-31", 15, 6, 9, 60, 15, 50, 2400)
	insertCommunityStatHistory(t, c.DB, "2026-12-31", 16, 7, 11, 80, 18, 80, 3000)
	insertCommunityStatHistory(t, c.DB, "2027-12-31", 20, 9, 20, 175, 35, 159, 7200)

	exec(t, c, admin, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{Date: "2026-06-14"})
	exec(t, c, admin, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{Date: "2026-07-31"})
	exec(t, c, admin, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{Date: "2027-12-31"})

	for _, want := range []struct {
		threadID string
		title    string
		contains []string
	}{
		{
			threadID: "bbslists_week_2026w24",
			title:    "Weekly activity history 2026-W24",
			contains: []string{"Period: 2026-06-08 to 2026-06-14", "Days captured: 2", "New posts: 14", "Logins: 15", "2026-06-14: 24 posts (+12)", "2026-06-08: 12 posts (+2)"},
		},
		{
			threadID: "bbslists_month_202607",
			title:    "Monthly activity history 2026-07",
			contains: []string{"Period: 2026-07-01 to 2026-07-31", "Days captured: 2", "New posts: 30", "Logins: 25", "2026-07-31: 60 posts (+25)"},
		},
		{
			threadID: "bbslists_year_2027",
			title:    "Yearly activity history 2027",
			contains: []string{"Period: 2027-01-01 to 2027-12-31", "Days captured: 1", "New posts: 95", "Logins: 79", "2027-12-31: 175 posts (+95)"},
		},
	} {
		threads, err := c.ListThreads("BBSLists", 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !hasThreadSummary(threads, want.threadID, want.title) {
			t.Fatalf("expected generated period thread %s / %s, got %+v", want.threadID, want.title, threads)
		}
		posts, err := c.ListPosts(want.threadID, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(posts) != 1 {
			t.Fatalf("expected one generated period post for %s, got %+v", want.threadID, posts)
		}
		for _, text := range want.contains {
			if !strings.Contains(posts[0].Body, text) {
				t.Fatalf("expected generated period post %s to contain %q, got:\n%s", want.threadID, text, posts[0].Body)
			}
		}
	}
}

func TestStatsHotTopicPeriodHistorySystemPosts(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "academics", Name: "Academics"})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "period_top", Name: "Tech", ParentID: "academics"})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "period_hidden", Name: "Hidden"})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:         "period_hidden",
		StatsExcluded: boolPtr(true),
	})
	weekly := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "period_top",
		Title: "Weekly period topic",
		Body:  "root",
	})
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: weekly.ID,
		Body:   "reply",
	})
	hidden := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "period_hidden",
		Title: "Hidden period topic",
		Body:  "hidden",
	})
	old := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "period_top",
		Title: "Old period topic",
		Body:  "old",
	})
	july := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "period_top",
		Title: "July period topic",
		Body:  "july",
	})
	year := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "period_top",
		Title: "Year period topic",
		Body:  "year",
	})
	setThreadPostsCreatedAt(t, c.DB, weekly.ID, time.Date(2026, time.June, 12, 12, 0, 0, 0, time.UTC))
	setThreadPostsCreatedAt(t, c.DB, hidden.ID, time.Date(2026, time.June, 12, 12, 0, 0, 0, time.UTC))
	setThreadPostsCreatedAt(t, c.DB, old.ID, time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC))
	setThreadPostsCreatedAt(t, c.DB, july.ID, time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC))
	setThreadPostsCreatedAt(t, c.DB, year.ID, time.Date(2027, time.December, 31, 12, 0, 0, 0, time.UTC))

	exec(t, c, admin, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{Date: "2026-06-14"})
	exec(t, c, admin, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{Date: "2026-07-31"})
	exec(t, c, admin, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{Date: "2027-12-31"})

	for _, want := range []struct {
		threadID  string
		title     string
		contains  []string
		forbidden []string
	}{
		{
			threadID:  "bbslists_toplog_week_2026w24",
			title:     "Weekly hot-topic history 2026-W24",
			contains:  []string{"Period: 2026-06-08 to 2026-06-14", "Ranked public hot topics: 1", "Tech / Weekly period topic", "2 participants", "2 period posts", "Category period hot topics", "### Academics"},
			forbidden: []string{"Hidden period topic", "Old period topic"},
		},
		{
			threadID: "bbslists_toplog_month_202607",
			title:    "Monthly hot-topic history 2026-07",
			contains: []string{"Period: 2026-07-01 to 2026-07-31", "Ranked public hot topics: 1", "Tech / July period topic", "1 period posts"},
		},
		{
			threadID: "bbslists_toplog_year_2027",
			title:    "Yearly hot-topic history 2027",
			contains: []string{"Period: 2027-01-01 to 2027-12-31", "Ranked public hot topics: 1", "Tech / Year period topic", "1 period posts"},
		},
	} {
		threads, err := c.ListThreads("BBSLists", 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !hasThreadSummary(threads, want.threadID, want.title) {
			t.Fatalf("expected generated period hot-topic thread %s / %s, got %+v", want.threadID, want.title, threads)
		}
		posts, err := c.ListPosts(want.threadID, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(posts) != 1 {
			t.Fatalf("expected one generated period hot-topic post for %s, got %+v", want.threadID, posts)
		}
		for _, text := range want.contains {
			if !strings.Contains(posts[0].Body, text) {
				t.Fatalf("expected generated period hot-topic post %s to contain %q, got:\n%s", want.threadID, text, posts[0].Body)
			}
		}
		for _, text := range want.forbidden {
			if strings.Contains(posts[0].Body, text) {
				t.Fatalf("expected generated period hot-topic post %s to hide %q, got:\n%s", want.threadID, text, posts[0].Body)
			}
		}
	}
}

func TestRecordLoginTracksHourlyStats(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	bob := registerAndGetUser(t, c, "bob", "pw")
	for _, ts := range []time.Time{
		time.Date(2026, time.August, 15, 5, 12, 0, 0, time.UTC),
		time.Date(2026, time.August, 15, 5, 45, 0, 0, time.UTC),
		time.Date(2026, time.August, 15, 23, 1, 0, 0, time.UTC),
	} {
		if err := projections.RecordLoginAt(c.DB, bob.ID, ts.UnixMilli()); err != nil {
			t.Fatalf("record login at %s: %v", ts, err)
		}
	}

	stats, err := c.GetCommunityStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalLogins != 3 {
		t.Fatalf("expected cumulative logins to track hourly samples, got %+v", stats)
	}
	hourly, err := projections.ListLoginHourlyStats(c.DB, "2026-08-15")
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) != 24 || hourly[5].LoginCount != 2 || hourly[23].LoginCount != 1 || hourly[4].LoginCount != 0 {
		t.Fatalf("expected 24 hourly login buckets with recorded samples, got %+v", hourly)
	}
}

func TestStatsLoginHistoryHourlyHistogramSystemPost(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	insertLoginHourlyStat(t, c.DB, "2026-08-15", 5, 42)
	insertLoginHourlyStat(t, c.DB, "2026-08-15", 23, 17)

	exec(t, c, admin, proto.CmdPublishStatsSnapshot, proto.PublishStatsSnapshotPayload{Date: "2026-08-15"})
	posts, err := c.ListPosts("bbslists_countlogins_20260815", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected one generated login-history post, got %+v", posts)
	}
	for _, want := range []string{"Hourly login histogram", "Day login samples: 59", "Peak hour: 05:00 UTC (42 logins)", "| 05:00 | 42 |", "| 23:00 | 17 |"} {
		if !strings.Contains(posts[0].Body, want) {
			t.Fatalf("expected generated login-history post to contain %q, got:\n%s", want, posts[0].Body)
		}
	}
}

func TestAutomaticDailyStatsSnapshot(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Daily stats source",
		Body:  "activity",
	})

	day := time.Date(2026, 6, 5, 18, 30, 0, 0, time.FixedZone("campus", 8*60*60))
	snapshot, err := c.PublishDailyStatsSnapshot(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.ID != "bbslists_stats_20260605" {
		t.Fatalf("expected deterministic automatic snapshot id, got %+v", snapshot)
	}
	again, err := c.PublishDailyStatsSnapshot(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	if again == nil || again.ID != snapshot.ID {
		t.Fatalf("expected repeated automatic snapshot to reuse %q, got %+v", snapshot.ID, again)
	}
	next, err := c.PublishDailyStatsSnapshot(context.Background(), day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.ID != "bbslists_stats_20260606" {
		t.Fatalf("expected next-day automatic snapshot, got %+v", next)
	}
	systemThreads, err := c.ListThreads("BBSLists", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(systemThreads) != 26 {
		t.Fatalf("expected stats, login-history, user-activity, board-online, online-user roster, board-moderator activity, board-activity, board-rank, new-board, recommended-board, recommended-article, hot-topic, and blessing threads for two days, got %+v", systemThreads)
	}
	for _, want := range []struct {
		id    string
		title string
	}{
		{"bbslists_stats_20260605", "Community stats 2026-06-05"},
		{"bbslists_countlogins_20260605", "Login count history 2026-06-05"},
		{"bbslists_statguy_20260605", "User activity rankings 2026-06-05"},
		{"bbslists_bonline_20260605", "Board online occupancy 2026-06-05"},
		{"bbslists_uonline_20260605", "Online user roster 2026-06-05"},
		{"bbslists_statbm_20260605", "Board moderator activity 2026-06-05"},
		{"bbslists_boardlog_20260605", "Board activity history 2026-06-05"},
		{"bbslists_boardrank_20260605", "Board popularity list 2026-06-05"},
		{"bbslists_newboards_20260605", "New board list 2026-06-05"},
		{"bbslists_rcmdbrd_20260605", "Recommended board list 2026-06-05"},
		{"bbslists_commend_20260605", "Recommended article list 2026-06-05"},
		{"bbslists_toplog_20260605", "Hot topic history 2026-06-05"},
		{"bbslists_bless_20260605", "Daily blessing list 2026-06-05"},
		{"bbslists_stats_20260606", "Community stats 2026-06-06"},
		{"bbslists_countlogins_20260606", "Login count history 2026-06-06"},
		{"bbslists_statguy_20260606", "User activity rankings 2026-06-06"},
		{"bbslists_bonline_20260606", "Board online occupancy 2026-06-06"},
		{"bbslists_uonline_20260606", "Online user roster 2026-06-06"},
		{"bbslists_statbm_20260606", "Board moderator activity 2026-06-06"},
		{"bbslists_boardlog_20260606", "Board activity history 2026-06-06"},
		{"bbslists_boardrank_20260606", "Board popularity list 2026-06-06"},
		{"bbslists_newboards_20260606", "New board list 2026-06-06"},
		{"bbslists_rcmdbrd_20260606", "Recommended board list 2026-06-06"},
		{"bbslists_commend_20260606", "Recommended article list 2026-06-06"},
		{"bbslists_toplog_20260606", "Hot topic history 2026-06-06"},
		{"bbslists_bless_20260606", "Daily blessing list 2026-06-06"},
	} {
		if !hasThreadSummary(systemThreads, want.id, want.title) {
			t.Fatalf("expected generated thread %s / %s, got %+v", want.id, want.title, systemThreads)
		}
	}
	posts, err := c.ListPosts(snapshot.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Author != "system" || !strings.Contains(posts[0].Body, "Community stats 2026-06-05") {
		t.Fatalf("expected system-authored automatic stats post, got %+v", posts)
	}
}

func hasThreadSummary(threads []core.Thread, id, titlePart string) bool {
	for _, thread := range threads {
		if thread.ID == id && strings.Contains(thread.Title, titlePart) {
			return true
		}
	}
	return false
}

func hasBoardRanking(boards []core.BoardRanking, id string) bool {
	for _, board := range boards {
		if board.ID == id {
			return true
		}
	}
	return false
}

func hasThreadRanking(threads []core.ThreadRanking, id string) bool {
	for _, thread := range threads {
		if thread.ID == id {
			return true
		}
	}
	return false
}

func hasReplyRankingThread(replies []core.ReplyRanking, threadID string) bool {
	for _, reply := range replies {
		if reply.ThreadID == threadID {
			return true
		}
	}
	return false
}

func hasArchiveRankingBoard(archives []core.ArchiveRanking, boardID string) bool {
	for _, archive := range archives {
		if archive.BoardID == boardID {
			return true
		}
	}
	return false
}

func insertCommunityStatHistory(t *testing.T, db *sql.DB, day string, users, boards, threads, posts, reactions, logins int, onlineSeconds int64) {
	t.Helper()
	at, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO community_stat_history (
		day, snapshot_at, total_users, total_boards, total_threads, total_posts,
		total_reactions, total_mail, total_direct_messages, total_logins, total_online_seconds, online_users,
		online_guests, max_online_users, max_online_at, max_online_guests,
		max_online_guests_at, head_seq
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		day, at.UnixMilli(), users, boards, threads, posts, reactions, 0, 0, logins, onlineSeconds, 0, 0, users, at.UnixMilli(), 0, int64(0), posts,
	); err != nil {
		t.Fatal(err)
	}
}

func insertLoginHourlyStat(t *testing.T, db *sql.DB, day string, hour, loginCount int) {
	t.Helper()
	at, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO login_hourly_stats (day, hour, login_count, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(day, hour)
		DO UPDATE SET login_count=excluded.login_count, updated_at=excluded.updated_at`,
		day, hour, loginCount, at.Add(time.Duration(hour)*time.Hour).UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
}

func setThreadPostsCreatedAt(t *testing.T, db *sql.DB, threadID string, at time.Time) {
	t.Helper()
	ms := at.UTC().UnixMilli()
	if _, err := db.Exec(`UPDATE posts SET created_at=?, updated_at=? WHERE thread=?`, ms, ms, threadID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE threads SET updated_at=? WHERE id=?`, ms, threadID); err != nil {
		t.Fatal(err)
	}
}

func TestPublishSystemNoticeCreatesPublicNoticeBoard(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	execExpectErr(t, c, alice, proto.CmdPublishSystemNotice, proto.PublishSystemNoticePayload{
		Title: "Campus notice",
		Body:  "Maintenance tonight",
	}, proto.ErrForbidden)
	execExpectErr(t, c, admin, proto.CmdPublishSystemNotice, proto.PublishSystemNoticePayload{
		Board: "Filter",
		Title: "Filtered",
		Body:  "not a public notice board",
	}, proto.ErrValidationFailed)

	notice := exec(t, c, admin, proto.CmdPublishSystemNotice, proto.PublishSystemNoticePayload{
		Title:  "Campus notice",
		Body:   "Maintenance tonight at 23:00.",
		Source: "operator broadcast",
	})
	if notice.ID == "" || notice.Seq == 0 {
		t.Fatalf("expected generated notice thread ack, got %+v", notice)
	}
	board, err := c.GetBoard("notepad")
	if err != nil {
		t.Fatal(err)
	}
	if board == nil || board.Name != "notepad" {
		t.Fatalf("expected generated notepad board, got %+v", board)
	}
	threads, err := c.ListThreads("notepad", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != notice.ID || threads[0].Title != "Campus notice" {
		t.Fatalf("expected one generated notepad thread, got %+v", threads)
	}
	posts, err := c.ListPosts(notice.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Author != "admin" {
		t.Fatalf("expected one admin-authored notice post, got %+v", posts)
	}
	for _, want := range []string{"# Campus notice", "Notice board: notepad", "Actor: admin", "Source: operator broadcast", "Maintenance tonight at 23:00.", "Generated public system notice"} {
		if !strings.Contains(posts[0].Body, want) {
			t.Fatalf("expected notice body to contain %q, got:\n%s", want, posts[0].Body)
		}
	}

	giveup := exec(t, c, admin, proto.CmdPublishSystemNotice, proto.PublishSystemNoticePayload{
		Board: "GiveupNotice",
		Title: "Network withdrawal notice",
		Body:  "Legacy peering endpoint retired.",
	})
	if giveup.ID == notice.ID {
		t.Fatalf("expected a separate GiveupNotice thread, got %+v", giveup)
	}
	giveupBoard, err := c.GetBoard("GiveupNotice")
	if err != nil {
		t.Fatal(err)
	}
	if giveupBoard == nil || giveupBoard.Name != "GiveupNotice" {
		t.Fatalf("expected generated GiveupNotice board, got %+v", giveupBoard)
	}
	rankings, err := c.ListBoardRankings(alice, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ranking := range rankings {
		if ranking.ID == "notepad" || ranking.ID == "GiveupNotice" {
			t.Fatalf("generated notice board should not appear in organic rankings, got %+v", rankings)
		}
	}
}

func TestBlessUserCreatesBlessingBoardAndRankings(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	carol := registerAndGetUser(t, c, "carol", "pw")

	execExpectErr(t, c, alice, proto.CmdBlessUser, proto.BlessUserPayload{
		User: "alice",
	}, proto.ErrValidationFailed)

	blessing := exec(t, c, alice, proto.CmdBlessUser, proto.BlessUserPayload{
		User:    "bob",
		Message: "Good luck on finals.",
	})
	if blessing.ID == "" || blessing.Seq == 0 {
		t.Fatalf("expected blessing ack, got %+v", blessing)
	}
	board, err := c.GetBoard("Blessing")
	if err != nil {
		t.Fatal(err)
	}
	if board == nil || board.Name != "Blessing" {
		t.Fatalf("expected generated Blessing board, got %+v", board)
	}
	threads, err := c.ListThreads("Blessing", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || !strings.Contains(threads[0].Title, "alice -> bob") {
		t.Fatalf("expected generated blessing thread, got %+v", threads)
	}
	posts, err := c.ListPosts(threads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Author != "alice" {
		t.Fatalf("expected one alice-authored blessing post, got %+v", posts)
	}
	for _, want := range []string{"# Blessing for bob", "From: alice", "To: bob", "Good luck on finals.", "Generated public blessing record"} {
		if !strings.Contains(posts[0].Body, want) {
			t.Fatalf("expected blessing post to contain %q, got:\n%s", want, posts[0].Body)
		}
	}

	exec(t, c, carol, proto.CmdBlessUser, proto.BlessUserPayload{User: "bob"})
	rankings, err := c.ListBlessingRankings(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rankings) == 0 || rankings[0].Name != "bob" || rankings[0].BlessingCount != 2 {
		t.Fatalf("expected bob to lead blessing rankings, got %+v", rankings)
	}
	recent, err := c.ListBlessings(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].ToName != "bob" || recent[0].FromName != "carol" {
		t.Fatalf("expected recent blessings newest first, got %+v", recent)
	}

	exec(t, c, bob, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "alice",
		Kind:   "ignore",
		Active: true,
	})
	execExpectErr(t, c, alice, proto.CmdBlessUser, proto.BlessUserPayload{
		User: "bob",
	}, proto.ErrForbidden)

	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatal(err)
	}
	rankings, err = c.ListBlessingRankings(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rankings) == 0 || rankings[0].Name != "bob" || rankings[0].BlessingCount != 2 {
		t.Fatalf("expected blessing rankings to rebuild from events, got %+v", rankings)
	}
}

func TestBoardSettingsAndModeratorsEnforcePostingPolicy(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "policy",
		Name:        "Policy",
		Description: "Policy board",
	})

	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:    "policy",
		ReadOnly: boolPtr(true),
	})
	execExpectErr(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "policy",
		Title: "blocked",
		Body:  "blocked",
	}, proto.ErrForbidden)
	execExpectErr(t, c, alice, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:    "policy",
		ReadOnly: boolPtr(false),
	}, proto.ErrForbidden)

	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "policy",
		User:      "alice",
		Moderator: true,
	})
	exec(t, c, alice, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:            "policy",
		ReadOnly:         boolPtr(false),
		NoReply:          boolPtr(true),
		AnonymousAllowed: boolPtr(true),
	})

	thread := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board:     "policy",
		Title:     "anonymous topic",
		Body:      "hello",
		Anonymous: true,
	})
	threads, err := c.ListThreadSummaries(bob.ID, "policy", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].Author != "Anonymous" || threads[0].AuthorID != "" {
		t.Fatalf("expected anonymous public thread identity, got %+v", threads)
	}

	execExpectErr(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "blocked reply",
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "moderator reply",
	})

	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "policy",
		User:      "alice",
		Moderator: false,
	})
	execExpectErr(t, c, alice, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:   "policy",
		NoReply: boolPtr(false),
	}, proto.ErrForbidden)
}

func TestBoardMailInPosting(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:   "mailbox",
		Name: "Mailbox",
	})
	execExpectErr(t, c, bob, proto.CmdPostBoardMail, proto.PostBoardMailPayload{
		Board:   "mailbox",
		Subject: "Mail thread",
		Body:    "posted from mail",
	}, proto.ErrForbidden)

	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:         "mailbox",
		MailInAllowed: boolPtr(true),
	})
	thread := exec(t, c, bob, proto.CmdPostBoardMail, proto.PostBoardMailPayload{
		Board:   "mailbox",
		Subject: "Mail thread",
		Body:    "posted from mail",
	})
	threads, err := c.ListThreads("mailbox", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != thread.ID || threads[0].Title != "Mail thread" {
		t.Fatalf("expected mail-in thread, got %+v", threads)
	}

	reply := exec(t, c, bob, proto.CmdPostBoardMail, proto.PostBoardMailPayload{
		Board:  "mailbox",
		Thread: thread.ID,
		Body:   "mail reply",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 || posts[1].ID != reply.ID || posts[1].Body != "mail reply" {
		t.Fatalf("expected mail-in reply, got %+v", posts)
	}
}

func TestBoardRelayDeliveries(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:   "relay",
		Name: "Relay",
	})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:        "relay",
		RelayEnabled: boolPtr(true),
	})
	thread := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "relay",
		Title: "Relay topic",
		Body:  "first relay body",
	})
	reply := exec(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "second relay body",
	})

	deliveries, err := c.ListRelayDeliveries("pending", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("expected two relay deliveries, got %+v", deliveries)
	}
	if deliveries[0].BoardID != "relay" || deliveries[0].ThreadID != thread.ID || deliveries[0].Title != "Relay topic" || deliveries[0].Body != "first relay body" {
		t.Fatalf("unexpected first relay delivery: %+v", deliveries[0])
	}
	if deliveries[1].PostID != reply.ID || deliveries[1].Body != "second relay body" || deliveries[1].Status != "pending" {
		t.Fatalf("unexpected reply relay delivery: %+v", deliveries[1])
	}
}

func TestBoardMembersEnforceMemberModes(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "members",
		Name:        "Members",
		Description: "Members only",
	})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "members",
		MemberReadMode: boolPtr(true),
		MemberPostMode: boolPtr(true),
	})

	execExpectErr(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "members",
		Title: "blocked",
		Body:  "blocked",
	}, proto.ErrForbidden)

	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:  "members",
		User:   "alice",
		Member: true,
		Title:  "alumna",
	})
	members, err := c.ListBoardMembers("members")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Name != "alice" || members[0].Title != "alumna" {
		t.Fatalf("expected alice member with title, got %+v", members)
	}

	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "members",
		Title: "member topic",
		Body:  "hello",
	})
	execExpectErr(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "blocked reply",
	}, proto.ErrForbidden)

	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:  "members",
		User:   bob.Name,
		Member: true,
	})
	exec(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "member reply",
	})

	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:  "members",
		User:   bob.Name,
		Member: false,
	})
	isMember, err := c.UserIsBoardMember("members", bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if isMember {
		t.Fatal("expected bob to be removed from board members")
	}
	execExpectErr(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "blocked again",
	}, proto.ErrForbidden)
}

func TestBoardMemberApplicationsLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "club",
		Name:        "Club",
		Description: "Resident board",
	})

	application := exec(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "club",
		Note:  "I read this board daily.",
	})
	execExpectErr(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "club",
	}, proto.ErrConflict)

	apps, err := c.ListBoardMemberApplications("club", "pending", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].ID != application.ID || apps[0].Name != "bob" || apps[0].Note == "" {
		t.Fatalf("expected bob pending application, got %+v", apps)
	}

	execExpectErr(t, c, alice, proto.CmdReviewBoardMembership, proto.ReviewBoardMembershipPayload{
		Application: application.ID,
		Status:      "approved",
	}, proto.ErrForbidden)

	exec(t, c, admin, proto.CmdReviewBoardMembership, proto.ReviewBoardMembershipPayload{
		Application: application.ID,
		Status:      "approved",
		Title:       "resident",
	})
	registryBoard, err := c.GetBoard("Registry")
	if err != nil {
		t.Fatal(err)
	}
	if registryBoard == nil || registryBoard.Name != "Registry" {
		t.Fatalf("expected generated Registry board, got %+v", registryBoard)
	}
	registryThreads, err := c.ListThreads("Registry", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(registryThreads) != 1 || registryThreads[0].ID != "registry_approved_thr_"+application.ID {
		t.Fatalf("expected approved registration system thread, got %+v", registryThreads)
	}
	registryPosts, err := c.ListPosts(registryThreads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(registryPosts) != 1 {
		t.Fatalf("expected one approved registration post, got %+v", registryPosts)
	}
	for _, want := range []string{"Status: approved", "Board: Club (club)", "Applicant: bob", "Reviewer: admin"} {
		if !strings.Contains(registryPosts[0].Body, want) {
			t.Fatalf("expected approved registration log to contain %q, got %q", want, registryPosts[0].Body)
		}
	}
	if strings.Contains(registryPosts[0].Body, "I read this board daily.") || strings.Contains(registryPosts[0].Body, "resident") {
		t.Fatalf("approved registration log leaked private note/title: %q", registryPosts[0].Body)
	}
	isMember, err := c.UserIsBoardMember("club", bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !isMember {
		t.Fatal("expected approved applicant to become a board member")
	}
	zero := 0
	one := 1
	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:    "club",
		User:     alice.Name,
		Member:   true,
		Title:    "lead",
		Position: &zero,
	})
	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:    "club",
		User:     bob.Name,
		Member:   true,
		Title:    "resident",
		Position: &one,
	})
	members, err := c.ListBoardMembers("club")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) < 2 || members[0].Name != "alice" || members[0].Position != 0 || members[1].Name != "bob" || members[1].Position != 1 {
		t.Fatalf("expected board members ordered by explicit position, got %+v", members)
	}

	exec(t, c, bob, proto.CmdLeaveBoardMembership, proto.LeaveBoardMembershipPayload{Board: "club"})
	isMember, err = c.UserIsBoardMember("club", bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if isMember {
		t.Fatal("expected member leave to remove membership")
	}

	rejected := exec(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{Board: "club"})
	exec(t, c, admin, proto.CmdReviewBoardMembership, proto.ReviewBoardMembershipPayload{
		Application: rejected.ID,
		Status:      "rejected",
		Note:        "try later",
	})
	rejectThreads, err := c.ListThreads("reject_registry", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejectThreads) != 1 || rejectThreads[0].ID != "registry_rejected_thr_"+rejected.ID {
		t.Fatalf("expected rejected registration system thread, got %+v", rejectThreads)
	}
	rejectPosts, err := c.ListPosts(rejectThreads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejectPosts) != 1 || !strings.Contains(rejectPosts[0].Body, "Status: rejected") || strings.Contains(rejectPosts[0].Body, "try later") {
		t.Fatalf("expected sanitized rejected registration log, got %+v", rejectPosts)
	}
	blacklisted := exec(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{Board: "club"})
	exec(t, c, admin, proto.CmdReviewBoardMembership, proto.ReviewBoardMembershipPayload{
		Application: blacklisted.ID,
		Status:      "blacklisted",
		Note:        "not eligible",
	})
	rejectThreads, err = c.ListThreads("reject_registry", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejectThreads) != 2 {
		t.Fatalf("expected rejected and blacklisted registration system threads, got %+v", rejectThreads)
	}
	execExpectErr(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "club",
	}, proto.ErrForbidden)

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "secretclub", Name: "Secret Club"})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secretclub",
		MemberReadMode: boolPtr(true),
	})
	privateApplication := exec(t, c, alice, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "secretclub",
		Note:  "private application note",
	})
	exec(t, c, admin, proto.CmdReviewBoardMembership, proto.ReviewBoardMembershipPayload{
		Application: privateApplication.ID,
		Status:      "approved",
		Note:        "private approval note",
	})
	registryThreads, err = c.ListThreads("Registry", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(registryThreads) != 1 {
		t.Fatalf("member-read board application should not generate public Registry records, got %+v", registryThreads)
	}
}

func TestDelegatedBoardMemberPermissions(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	carol := registerAndGetUser(t, c, "carol", "pw")
	_ = registerAndGetUser(t, c, "dave", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "club",
		Name:        "Club",
		Description: "Resident board",
	})
	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:               "club",
		User:                "alice",
		Member:              true,
		Title:               "steward",
		CanManageMembers:    boolPtr(true),
		CanCurate:           boolPtr(true),
		CanModeratePosts:    boolPtr(true),
		CanModerateThreads:  boolPtr(true),
		CanManagePolls:      boolPtr(true),
		CanSetBoardSettings: boolPtr(true),
	})

	members, err := c.ListBoardMembers("club")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 ||
		!members[0].CanManageMembers ||
		!members[0].CanCurate ||
		!members[0].CanModeratePosts ||
		!members[0].CanModerateThreads ||
		!members[0].CanManagePolls ||
		!members[0].CanSetBoardSettings {
		t.Fatalf("expected alice delegated permissions, got %+v", members)
	}

	application := exec(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{Board: "club"})
	exec(t, c, alice, proto.CmdReviewBoardMembership, proto.ReviewBoardMembershipPayload{
		Application: application.ID,
		Status:      "approved",
		Title:       "resident",
	})
	execExpectErr(t, c, alice, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:               "club",
		User:                "bob",
		Member:              true,
		CanCurate:           boolPtr(true),
		CanSetBoardSettings: boolPtr(true),
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:    "club",
		User:     "bob",
		Member:   true,
		Title:    "resident",
		Position: intPtr(1),
	})
	carolApplication := exec(t, c, carol, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{Board: "club"})
	execExpectErr(t, c, alice, proto.CmdReviewBoardMembership, proto.ReviewBoardMembershipPayload{
		Application: carolApplication.ID,
		Status:      "blacklisted",
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdReviewBoardMembership, proto.ReviewBoardMembershipPayload{
		Application: carolApplication.ID,
		Status:      "rejected",
	})
	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:  "club",
		User:   "dave",
		Member: true,
		Title:  "operator",
	})
	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "club",
		User:      "dave",
		Moderator: true,
	})
	execExpectErr(t, c, alice, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:  "club",
		User:   "dave",
		Member: false,
	}, proto.ErrForbidden)

	thread := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "club",
		Title: "local notes",
		Body:  "first post",
	})
	execExpectErr(t, c, bob, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: thread.ID,
		Kind:   "digest",
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: thread.ID,
		Kind:   "digest",
		Title:  "local notes",
	})

	entries, err := c.ListDigestEntries("club", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].CreatedByName != "alice" {
		t.Fatalf("expected alice-curated digest entry, got %+v", entries)
	}

	execExpectErr(t, c, bob, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:   "club",
		NoReply: boolPtr(true),
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:   "club",
		NoReply: boolPtr(true),
	})
	execExpectErr(t, c, bob, proto.CmdLockThread, proto.LockThreadPayload{
		Thread: thread.ID,
		Locked: true,
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdLockThread, proto.LockThreadPayload{
		Thread: thread.ID,
		Locked: true,
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) == 0 {
		t.Fatal("expected thread to have a post")
	}
	exec(t, c, alice, proto.CmdRedactPost, proto.RedactPostPayload{
		Post:   posts[0].ID,
		Reason: "duplicate",
	})
	execExpectErr(t, c, bob, proto.CmdRestorePost, proto.RestorePostPayload{
		Post: posts[0].ID,
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdRestorePost, proto.RestorePostPayload{
		Post: posts[0].ID,
	})
	exec(t, c, alice, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:  "club",
		User:   "bob",
		Member: false,
	})
	isMember, err := c.UserIsBoardMember("club", bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if isMember {
		t.Fatal("expected delegated manager to remove ordinary member")
	}

	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:       "club",
		User:        "bob",
		Member:      true,
		Title:       "resident",
		CanAnnounce: boolPtr(true),
	})
	execExpectErr(t, c, bob, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: thread.ID,
		Kind:   "archive",
	}, proto.ErrForbidden)
	exec(t, c, bob, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: thread.ID,
		Kind:   "announcement",
		Title:  "club notice",
	})

	execExpectErr(t, c, alice, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:  "club",
		User:   "bob",
		Member: false,
	}, proto.ErrForbidden)
}

func TestBoardMemberRequirementsAdmission(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "selective",
		Name:        "Selective",
		Description: "Members by rule",
	})
	exec(t, c, admin, proto.CmdSetBoardMemberRequirements, proto.SetBoardMemberRequirementsPayload{
		Board:                     "selective",
		MinLoginCount:             intPtr(1),
		MinPostCount:              intPtr(1),
		MinScore:                  intPtr(1),
		MinBoardPostCount:         intPtr(2),
		MinBoardOriginalPostCount: intPtr(1),
		MinBoardDigestCount:       intPtr(1),
		MinBoardMarkCount:         intPtr(1),
		MaxMembers:                intPtr(1),
		ApprovalMode:              stringPtr("auto"),
	})

	info, err := c.GetBoardInfo("selective")
	if err != nil {
		t.Fatal(err)
	}
	if info.Requirements.MinLoginCount != 1 ||
		info.Requirements.MinPostCount != 1 ||
		info.Requirements.MinScore != 1 ||
		info.Requirements.MinBoardPostCount != 2 ||
		info.Requirements.MinBoardOriginalPostCount != 1 ||
		info.Requirements.MinBoardDigestCount != 1 ||
		info.Requirements.MinBoardMarkCount != 1 ||
		info.Requirements.MaxMembers != 1 ||
		info.Requirements.ApprovalMode != "auto" {
		t.Fatalf("expected stored member requirements, got %+v", info.Requirements)
	}

	execExpectErr(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "selective",
	}, proto.ErrForbidden)

	exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "activity",
		Body:  "first post",
	})
	execExpectErr(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "selective",
	}, proto.ErrForbidden)
	if err := c.RecordLogin(bob.ID); err != nil {
		t.Fatalf("record login: %v", err)
	}
	execExpectErr(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "selective",
	}, proto.ErrForbidden)

	boardThread := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "selective",
		Title: "board activity",
		Body:  "first local post",
	})
	execExpectErr(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "selective",
	}, proto.ErrForbidden)

	exec(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: boardThread.ID,
		Body:   "second local post",
	})
	execExpectErr(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "selective",
	}, proto.ErrForbidden)

	boardPosts, err := c.ListPosts(boardThread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(boardPosts) == 0 {
		t.Fatal("expected local thread post")
	}
	exec(t, c, admin, proto.CmdCuratePost, proto.CuratePostPayload{
		Post:  boardPosts[0].ID,
		Kind:  "digest",
		Title: "Local digest credit",
	})
	execExpectErr(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "selective",
	}, proto.ErrForbidden)

	exec(t, c, alice, proto.CmdReactPost, proto.ReactPostPayload{
		Post:  boardPosts[0].ID,
		Emoji: "heart",
	})

	application := exec(t, c, bob, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "selective",
	})
	app, err := c.GetBoardMemberApplication(application.ID)
	if err != nil {
		t.Fatal(err)
	}
	if app == nil || app.Status != "approved" || app.ReviewerID != bob.ID {
		t.Fatalf("expected auto-approved application, got %+v", app)
	}
	isMember, err := c.UserIsBoardMember("selective", bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !isMember {
		t.Fatal("expected auto-approved applicant to become a member")
	}

	exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "activity 2",
		Body:  "first post",
	})
	execExpectErr(t, c, alice, proto.CmdApplyBoardMembership, proto.ApplyBoardMembershipPayload{
		Board: "selective",
	}, proto.ErrConflict)
}

func TestPostAttachmentsRoundTrip(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	execExpectErr(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "blocked file",
		Body:  "hello",
		Attachments: []proto.AttachmentPayload{{
			Filename: "blocked.zip",
		}},
	}, proto.ErrForbidden)

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "files",
		Name:        "Files",
		Description: "File board",
	})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:              "files",
		AttachmentsAllowed: boolPtr(true),
	})

	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "files",
		Title: "manual",
		Body:  "read this",
		Attachments: []proto.AttachmentPayload{{
			Filename:    "manual.pdf",
			ContentType: "application/pdf",
			SizeBytes:   4096,
			URL:         "https://example.test/manual.pdf",
		}},
	})
	reply := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "and source",
		Attachments: []proto.AttachmentPayload{{
			Filename:  "source.tar.gz",
			SizeBytes: 2048,
		}},
	})

	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(posts))
	}
	if len(posts[0].Attachments) != 1 || posts[0].Attachments[0].Filename != "manual.pdf" || posts[0].Attachments[0].ContentType != "application/pdf" {
		t.Fatalf("expected manual attachment on first post, got %+v", posts[0].Attachments)
	}
	if posts[0].Attachments[0].ID == "" || posts[0].Attachments[0].PostID != posts[0].ID {
		t.Fatalf("expected generated attachment identity, got %+v", posts[0].Attachments[0])
	}
	if len(posts[1].Attachments) != 1 || posts[1].ID != reply.ID || posts[1].Attachments[0].Filename != "source.tar.gz" {
		t.Fatalf("expected source attachment on reply, got post=%+v attachments=%+v", posts[1], posts[1].Attachments)
	}
}

func TestDigestCurationLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	thread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Curatable topic",
		Body:  "First post",
	})
	reply := exec(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "Useful reply",
	})

	execExpectErr(t, c, bob, proto.CmdCuratePost, proto.CuratePostPayload{
		Post: reply.ID,
		Kind: "digest",
	}, proto.ErrForbidden)

	postDigest := exec(t, c, admin, proto.CmdCuratePost, proto.CuratePostPayload{
		Post:  reply.ID,
		Kind:  "digest",
		Title: "Useful reply",
		Path:  "faq",
		Note:  "Worth saving",
	})
	threadDigest := exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: thread.ID,
		Kind:   "recommended",
		Title:  "Curatable topic",
	})

	entries, err := c.ListDigestEntries("general", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected two digest entries, got %+v", entries)
	}
	if entries[0].ID != threadDigest.ID || entries[0].Kind != "recommended" {
		t.Fatalf("expected recommended thread first, got %+v", entries)
	}
	if entries[1].ID != postDigest.ID || entries[1].PostID != reply.ID || entries[1].Path != "faq" {
		t.Fatalf("expected saved post digest entry, got %+v", entries)
	}
	recommendBoard, err := c.GetBoard("Recommend")
	if err != nil {
		t.Fatal(err)
	}
	if recommendBoard == nil || recommendBoard.Name != "Recommend" {
		t.Fatalf("expected generated Recommend board, got %+v", recommendBoard)
	}
	recommendThreads, err := c.ListThreads("Recommend", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recommendThreads) != 1 || recommendThreads[0].ID != "recommend_thr_"+threadDigest.ID || recommendThreads[0].Title != "Curatable topic" {
		t.Fatalf("expected generated Recommend thread, got %+v", recommendThreads)
	}
	recommendPosts, err := c.ListPosts(recommendThreads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recommendPosts) != 1 || !strings.Contains(recommendPosts[0].Body, "Kind: recommended") || !strings.Contains(recommendPosts[0].Body, "Curatable topic") {
		t.Fatalf("expected generated Recommend post body, got %+v", recommendPosts)
	}

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "private_digest", Name: "Private digest"})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "private_digest",
		MemberReadMode: boolPtr(true),
	})
	privateThread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "private_digest",
		Title: "Private recommendation",
		Body:  "Members only",
	})
	privateRecommend := exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: privateThread.ID,
		Kind:   "recommended",
		Title:  "Private recommendation",
	})
	if privateRecommend.ID == "" {
		t.Fatal("expected private recommended digest entry")
	}
	recommendThreads, err = c.ListThreads("Recommend", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recommendThreads) != 1 {
		t.Fatalf("private member-read recommendation should not generate public Recommend post, got %+v", recommendThreads)
	}

	updated := exec(t, c, admin, proto.CmdCuratePost, proto.CuratePostPayload{
		Post:  reply.ID,
		Kind:  "digest",
		Title: "Useful reply updated",
		Path:  "faq",
	})
	if updated.ID != postDigest.ID {
		t.Fatalf("expected duplicate curation to update same entry, got %s vs %s", updated.ID, postDigest.ID)
	}

	filtered, err := c.ListDigestEntries("general", "digest", "faq", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Title != "Useful reply updated" {
		t.Fatalf("expected updated filtered digest entry, got %+v", filtered)
	}

	exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: thread.ID,
		Kind:   "archive",
		Title:  "Archive root",
		Path:   "faq",
	})
	archivePost := exec(t, c, admin, proto.CmdCuratePost, proto.CuratePostPayload{
		Post:  reply.ID,
		Kind:  "archive",
		Title: "Archive child",
		Path:  "faq/howto",
	})
	execExpectErr(t, c, bob, proto.CmdCreateDigestDirectory, proto.CreateDigestDirectoryPayload{
		Board: "general",
		Kind:  "archive",
		Path:  "faq/empty",
	}, proto.ErrForbidden)
	emptyDir := exec(t, c, admin, proto.CmdCreateDigestDirectory, proto.CreateDigestDirectoryPayload{
		Board: "general",
		Kind:  "archive",
		Path:  "faq/empty",
	})
	if emptyDir.ID == "" {
		t.Fatal("expected empty archive directory id")
	}
	tree, err := c.ListDigestPathTree("general", "archive")
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]core.DigestPathNode{}
	for _, node := range tree {
		nodes[node.Path] = node
	}
	if nodes[""].ChildCount != 1 ||
		nodes["faq"].EntryCount != 1 ||
		nodes["faq"].ChildCount != 2 ||
		nodes["faq/howto"].EntryCount != 1 ||
		nodes["faq/howto"].ParentPath != "faq" ||
		!nodes["faq/empty"].Explicit ||
		nodes["faq/empty"].EntryCount != 0 {
		t.Fatalf("expected derived archive path tree, got %+v", tree)
	}
	archiveSearch, err := c.SearchDigestEntries(bob, "", "archive", "", "howto", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(archiveSearch) != 1 || archiveSearch[0].ID != archivePost.ID || archiveSearch[0].Path != "faq/howto" {
		t.Fatalf("expected archive search to find nested archive entry, got %+v", archiveSearch)
	}
	exported, err := c.GetDigestExport(archivePost.ID)
	if err != nil {
		t.Fatal(err)
	}
	exportText := core.FormatDigestExportText(exported)
	if exported == nil || !strings.Contains(exportText, "Archive child") || !strings.Contains(exportText, "Useful reply") {
		t.Fatalf("expected exported archive text, got export=%+v text=%q", exported, exportText)
	}
	execExpectErr(t, c, bob, proto.CmdUpdateDigestEntry, proto.UpdateDigestEntryPayload{
		Entry: archivePost.ID,
		Title: stringPtr("Not yours"),
	}, proto.ErrForbidden)
	exec(t, c, admin, proto.CmdUpdateDigestEntry, proto.UpdateDigestEntryPayload{
		Entry: archivePost.ID,
		Title: stringPtr("Archive child edited"),
		Path:  stringPtr("faq/howto/edited"),
		Note:  stringPtr("Cleaned up for the archive"),
	})
	moved, err := c.ListDigestEntries("general", "archive", "faq/howto/edited", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || moved[0].ID != archivePost.ID || moved[0].Title != "Archive child edited" || moved[0].Note != "Cleaned up for the archive" {
		t.Fatalf("expected moved archive entry with edited metadata, got %+v", moved)
	}
	oldPath, err := c.ListDigestEntries("general", "archive", "faq/howto", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldPath) != 0 {
		t.Fatalf("expected archive entry moved out of old path, got %+v", oldPath)
	}
	exec(t, c, admin, proto.CmdSetDigestEntryBody, proto.SetDigestEntryBodyPayload{
		Entry: archivePost.ID,
		Body:  "Edited archive body\nWith curator notes.",
	})
	editedExport, err := c.GetDigestExport(archivePost.ID)
	if err != nil {
		t.Fatal(err)
	}
	editedText := core.FormatDigestExportText(editedExport)
	if editedExport == nil || !editedExport.Entry.BodyEdited || !strings.Contains(editedText, "Edited archive body") || strings.Contains(editedText, "Useful reply") {
		t.Fatalf("expected edited archive export body, got export=%+v text=%q", editedExport, editedText)
	}
	editedSearch, err := c.SearchDigestEntries(bob, "", "archive", "", "curator notes", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(editedSearch) != 1 || editedSearch[0].ID != archivePost.ID || !editedSearch[0].BodyEdited {
		t.Fatalf("expected search to find edited archive body, got %+v", editedSearch)
	}
	archiveMail := exec(t, c, bob, proto.CmdSendDigestEntryMail, proto.SendDigestEntryMailPayload{
		Entry: archivePost.ID,
		To:    []string{"alice"},
		Note:  "Please keep this one.",
	})
	aliceMail, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	foundArchiveMail := false
	for _, item := range aliceMail {
		if item.ID == archiveMail.ID {
			foundArchiveMail = strings.Contains(item.Subject, "Archive child edited") && strings.Contains(item.Body, "Please keep this one.") && strings.Contains(item.Body, "Edited archive body")
		}
	}
	if !foundArchiveMail {
		t.Fatalf("expected mailed archive entry in alice inbox, got %+v", aliceMail)
	}
	exec(t, c, admin, proto.CmdSetDigestEntryBody, proto.SetDigestEntryBodyPayload{
		Entry: archivePost.ID,
		Reset: true,
	})
	resetExport, err := c.GetDigestExport(archivePost.ID)
	if err != nil {
		t.Fatal(err)
	}
	resetText := core.FormatDigestExportText(resetExport)
	if resetExport == nil || resetExport.Entry.BodyEdited || !strings.Contains(resetText, "Useful reply") || strings.Contains(resetText, "Edited archive body") {
		t.Fatalf("expected reset archive export to use source body, got export=%+v text=%q", resetExport, resetText)
	}
	execExpectErr(t, c, bob, proto.CmdCopyDigestPath, proto.CopyDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq",
		ToPath:   "faq-copy",
	}, proto.ErrForbidden)
	exec(t, c, admin, proto.CmdCopyDigestPath, proto.CopyDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq",
		ToPath:   "faq-copy",
	})
	copied, err := c.ListDigestEntries("general", "archive", "faq-copy/howto/edited", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 1 || copied[0].ID == archivePost.ID || copied[0].TargetID != reply.ID || copied[0].Title != "Archive child edited" {
		t.Fatalf("expected copied archive subtree entry, got %+v", copied)
	}
	copiedTree, err := c.ListDigestPathTree("general", "archive")
	if err != nil {
		t.Fatal(err)
	}
	copiedNodes := map[string]core.DigestPathNode{}
	for _, node := range copiedTree {
		copiedNodes[node.Path] = node
	}
	if !copiedNodes["faq-copy/empty"].Explicit || copiedNodes["faq-copy/empty"].EntryCount != 0 {
		t.Fatalf("expected copied empty archive directory, got %+v", copiedTree)
	}
	exec(t, c, admin, proto.CmdMoveDigestPath, proto.MoveDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq-copy",
		ToPath:   "faq-moved",
	})
	movedCopy, err := c.ListDigestEntries("general", "archive", "faq-moved/howto/edited", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(movedCopy) != 1 || movedCopy[0].TargetID != reply.ID {
		t.Fatalf("expected moved copied archive subtree, got %+v", movedCopy)
	}
	movedTree, err := c.ListDigestPathTree("general", "archive")
	if err != nil {
		t.Fatal(err)
	}
	movedNodes := map[string]core.DigestPathNode{}
	for _, node := range movedTree {
		movedNodes[node.Path] = node
	}
	if !movedNodes["faq-moved/empty"].Explicit {
		t.Fatalf("expected moved empty archive directory, got %+v", movedTree)
	}
	exec(t, c, admin, proto.CmdDeleteDigestPath, proto.DeleteDigestPathPayload{
		Board: "general",
		Kind:  "archive",
		Path:  "faq-moved",
	})
	deletedCopy, err := c.ListDigestEntries("general", "archive", "faq-moved/howto/edited", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(deletedCopy) != 0 {
		t.Fatalf("expected copied archive subtree deletion, got %+v", deletedCopy)
	}
	deletedTree, err := c.ListDigestPathTree("general", "archive")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range deletedTree {
		if strings.HasPrefix(node.Path, "faq-moved") {
			t.Fatalf("expected copied archive subtree directory deletion, got %+v", deletedTree)
		}
	}

	exec(t, c, admin, proto.CmdSetBoardModerator, proto.SetBoardModeratorPayload{
		Board:     "general",
		User:      "alice",
		Moderator: true,
	})
	exec(t, c, alice, proto.CmdRemoveDigestEntry, proto.RemoveDigestEntryPayload{Entry: postDigest.ID})

	filtered, err = c.ListDigestEntries("general", "digest", "faq", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 {
		t.Fatalf("expected digest entry removal, got %+v", filtered)
	}
}

func TestSiteDigestEntriesRespectMemberReadBoards(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	publicThread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Campus notice",
		Body:  "Public",
	})
	publicDigest := exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: publicThread.ID,
		Kind:   "announcement",
		Title:  "Public announcement",
	})
	systemBoard, err := c.GetBoard("0announce")
	if err != nil {
		t.Fatal(err)
	}
	if systemBoard == nil || systemBoard.Name != "0Announce" {
		t.Fatalf("expected generated 0Announce board, got %+v", systemBoard)
	}
	systemThreads, err := c.ListThreads("0announce", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(systemThreads) != 1 || systemThreads[0].Title != "Public announcement" || systemThreads[0].ID != "ann_thr_"+publicDigest.ID {
		t.Fatalf("expected generated public announcement thread, got %+v", systemThreads)
	}
	systemPosts, err := c.ListPosts(systemThreads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(systemPosts) != 1 || !strings.Contains(systemPosts[0].Body, "Public announcement") || !strings.Contains(systemPosts[0].Body, "Public") {
		t.Fatalf("expected generated public announcement post body, got %+v", systemPosts)
	}

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "secret", Name: "Secret"})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secret",
		MemberReadMode: boolPtr(true),
	})
	secretThread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private notice",
		Body:  "Members only",
	})
	secretDigest := exec(t, c, admin, proto.CmdCurateThread, proto.CurateThreadPayload{
		Thread: secretThread.ID,
		Kind:   "announcement",
		Title:  "Private announcement",
	})
	systemThreads, err = c.ListThreads("0announce", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(systemThreads) != 1 {
		t.Fatalf("private member-read announcement should not generate public 0Announce post, got %+v", systemThreads)
	}

	bobEntries, err := c.ListSiteDigestEntries(bob, "announcement", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobEntries) != 1 || bobEntries[0].BoardID != "general" || bobEntries[0].BoardName == "" {
		t.Fatalf("expected non-member to see only public announcement, got %+v", bobEntries)
	}

	adminEntries, err := c.ListSiteDigestEntries(admin, "announcement", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range adminEntries {
		seen[entry.BoardID] = true
	}
	if !seen["general"] || !seen["secret"] {
		t.Fatalf("expected admin to see public and private announcements, got %+v", adminEntries)
	}

	bobSearch, err := c.SearchDigestEntries(bob, "", "announcement", "", "announcement", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobSearch) != 1 || bobSearch[0].BoardID != "general" {
		t.Fatalf("expected non-member search to hide private announcement, got %+v", bobSearch)
	}
	adminSearch, err := c.SearchDigestEntries(admin, "", "announcement", "", "announcement", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen = map[string]bool{}
	for _, entry := range adminSearch {
		seen[entry.BoardID] = true
	}
	if !seen["general"] || !seen["secret"] {
		t.Fatalf("expected admin search to include public and private announcements, got %+v", adminSearch)
	}
	execExpectErr(t, c, bob, proto.CmdSendDigestEntryMail, proto.SendDigestEntryMailPayload{
		Entry: secretDigest.ID,
		To:    []string{"bob"},
	}, proto.ErrForbidden)
}

func TestModerationSystemBoardLogsRespectMemberReadBoards(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	publicThread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Public review target",
		Body:  "public body should stay out of logs",
	})
	publicPosts, err := c.ListPosts(publicThread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(publicPosts) == 0 {
		t.Fatalf("expected public thread starter post")
	}
	review := exec(t, c, bob, proto.CmdFlagPost, proto.FlagPostPayload{
		Post:   publicPosts[0].ID,
		Reason: "sensitive report reason",
	})

	systemBoard, err := c.GetBoard("0moderation")
	if err != nil {
		t.Fatal(err)
	}
	if systemBoard == nil || systemBoard.Name != "0Moderation" {
		t.Fatalf("expected generated 0Moderation board, got %+v", systemBoard)
	}
	systemThreads, err := c.ListThreads("0moderation", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(systemThreads) != 1 || systemThreads[0].ID != "mod_flag_thr_"+review.ID {
		t.Fatalf("expected generated public moderation flag thread, got %+v", systemThreads)
	}
	flagPosts, err := c.ListPosts(systemThreads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(flagPosts) != 1 {
		t.Fatalf("expected one generated flag post, got %+v", flagPosts)
	}
	flagBody := flagPosts[0].Body
	for _, want := range []string{
		"Status: opened",
		"Board: general",
		"Thread: " + publicThread.ID,
		"Post: " + publicPosts[0].ID,
		"Actor: bob",
	} {
		if !strings.Contains(flagBody, want) {
			t.Fatalf("expected generated flag log to contain %q, got %q", want, flagBody)
		}
	}
	for _, secret := range []string{"sensitive report reason", "public body should stay out of logs"} {
		if strings.Contains(flagBody, secret) {
			t.Fatalf("generated flag log leaked %q: %q", secret, flagBody)
		}
	}

	exec(t, c, admin, proto.CmdResolveReview, proto.ResolveReviewPayload{
		Review:     review.ID,
		Resolution: "private moderator note",
	})
	systemThreads, err = c.ListThreads("0moderation", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(systemThreads) != 2 {
		t.Fatalf("expected flag and resolution log threads, got %+v", systemThreads)
	}
	resolvePosts, err := c.ListPosts("mod_resolve_thr_"+review.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolvePosts) != 1 || !strings.Contains(resolvePosts[0].Body, "Status: resolved") {
		t.Fatalf("expected generated resolution log post, got %+v", resolvePosts)
	}
	if strings.Contains(resolvePosts[0].Body, "private moderator note") {
		t.Fatalf("generated resolution log leaked moderator note: %q", resolvePosts[0].Body)
	}

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "secret", Name: "Secret"})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secret",
		MemberReadMode: boolPtr(true),
	})
	privateThread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private review target",
		Body:  "members-only body",
	})
	privatePosts, err := c.ListPosts(privateThread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	privateReview := exec(t, c, admin, proto.CmdFlagPost, proto.FlagPostPayload{
		Post:   privatePosts[0].ID,
		Reason: "private report reason",
	})
	exec(t, c, admin, proto.CmdResolveReview, proto.ResolveReviewPayload{
		Review:     privateReview.ID,
		Resolution: "private resolution",
	})
	systemThreads, err = c.ListThreads("0moderation", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(systemThreads) != 2 {
		t.Fatalf("private member-read review should not generate public moderation logs, got %+v", systemThreads)
	}
}

func TestContentFilterCreatesReviewAndFilterBoardRecord(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	filter := exec(t, c, admin, proto.CmdSetContentFilter, proto.SetContentFilterPayload{
		ID:      "filter_policy",
		Pattern: "classified",
		Scope:   "global",
	})
	if filter.ID != "filter_policy" || filter.Seq == 0 {
		t.Fatalf("expected filter ack, got %+v", filter)
	}
	filters, err := c.ListContentFilters("", true, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(filters) != 1 || filters[0].ID != "filter_policy" || !filters[0].Active {
		t.Fatalf("expected active content filter, got %+v", filters)
	}

	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Campus note",
		Body:  "this mentions a classified thing that should enter review",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected starter post, got %+v", posts)
	}
	reviews, err := c.ListModerationReviews("open", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 1 || reviews[0].Kind != "content_filter" || reviews[0].TargetID != posts[0].ID {
		t.Fatalf("expected content-filter review, got %+v", reviews)
	}

	filterBoard, err := c.GetBoard("Filter")
	if err != nil {
		t.Fatal(err)
	}
	if filterBoard == nil || filterBoard.Name != "Filter" {
		t.Fatalf("expected generated Filter board, got %+v", filterBoard)
	}
	filterThreads, err := c.ListThreads("Filter", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(filterThreads) != 1 || filterThreads[0].ID != "filter_thr_"+reviews[0].ID {
		t.Fatalf("expected generated Filter thread, got %+v", filterThreads)
	}
	filterPosts, err := c.ListPosts(filterThreads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(filterPosts) != 1 {
		t.Fatalf("expected generated Filter post, got %+v", filterPosts)
	}
	filterBody := filterPosts[0].Body
	for _, want := range []string{"Status: opened", "Filter: filter_policy", "Board: general", "Public author: alice"} {
		if !strings.Contains(filterBody, want) {
			t.Fatalf("expected generated Filter body to contain %q, got:\n%s", want, filterBody)
		}
	}
	for _, secret := range []string{"classified", "this mentions"} {
		if strings.Contains(filterBody, secret) {
			t.Fatalf("generated Filter body leaked %q:\n%s", secret, filterBody)
		}
	}

	inactive := false
	exec(t, c, admin, proto.CmdSetContentFilter, proto.SetContentFilterPayload{
		ID:      "filter_policy",
		Pattern: "classified",
		Scope:   "global",
		Active:  &inactive,
	})
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "classified appears again but the rule is off",
	})
	reviews, err = c.ListModerationReviews("open", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 1 {
		t.Fatalf("expected inactive filter not to create more reviews, got %+v", reviews)
	}

	clearProjectionTablesForTest(t, c)
	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	rebuiltFilters, err := c.ListContentFilters("", true, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuiltFilters) != 1 || rebuiltFilters[0].Active {
		t.Fatalf("expected inactive rebuilt filter, got %+v", rebuiltFilters)
	}
	rebuiltReviews, err := c.ListModerationReviews("open", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuiltReviews) != 1 || rebuiltReviews[0].Kind != "content_filter" {
		t.Fatalf("expected rebuilt content-filter review, got %+v", rebuiltReviews)
	}
}

func TestPrivateMailAndDirectMessagesLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	carol := registerAndGetUser(t, c, "carol", "pw")

	mail := exec(t, c, bob, proto.CmdSendMail, proto.SendMailPayload{
		To:      []string{"alice", "carol"},
		Subject: "Campus plans",
		Body:    "Meet in the lab at six.",
		Attachments: []proto.AttachmentPayload{{
			Filename:    "plan.txt",
			ContentType: "text/plain",
			SizeBytes:   12,
			URL:         "https://example.edu/plan.txt",
		}},
	})

	aliceInbox, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceInbox) != 1 || aliceInbox[0].ID != mail.ID || aliceInbox[0].FromName != "bob" || aliceInbox[0].Read {
		t.Fatalf("expected unread mail from bob, got %+v", aliceInbox)
	}
	if len(aliceInbox[0].ToNames) != 2 {
		t.Fatalf("expected multi-recipient mail, got %+v", aliceInbox[0].ToNames)
	}
	if len(aliceInbox[0].Attachments) != 1 || aliceInbox[0].Attachments[0].Filename != "plan.txt" || aliceInbox[0].Attachments[0].Stored {
		t.Fatalf("expected mail attachment metadata, got %+v", aliceInbox[0].Attachments)
	}
	aliceUsage, err := c.GetMailUsage(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if aliceUsage.UsedBytes <= 0 || aliceUsage.QuotaBytes <= aliceUsage.UsedBytes || aliceUsage.RemainingBytes != aliceUsage.QuotaBytes-aliceUsage.UsedBytes {
		t.Fatalf("expected mail usage with remaining quota, got %+v", aliceUsage)
	}
	execExpectErr(t, c, bob, proto.CmdSendMail, proto.SendMailPayload{
		To:      []string{"alice"},
		Subject: "Too large",
		Body:    strings.Repeat("x", 11<<20),
	}, proto.ErrValidationFailed)
	unreadMail, err := c.CountUnreadMail(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unreadMail != 1 {
		t.Fatalf("expected one unread mail, got %d", unreadMail)
	}

	bobSent, err := c.ListMail(bob.ID, "sent", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobSent) != 1 || bobSent[0].ID != mail.ID || !bobSent[0].Read {
		t.Fatalf("expected sent copy for sender, got %+v", bobSent)
	}

	read := true
	kept := true
	box := "keep"
	exec(t, c, alice, proto.CmdUpdateMail, proto.UpdateMailPayload{
		Mail:    mail.ID,
		Read:    &read,
		Kept:    &kept,
		Mailbox: &box,
	})
	aliceKeep, err := c.ListMail(alice.ID, "keep", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceKeep) != 1 || !aliceKeep[0].Read || !aliceKeep[0].Kept {
		t.Fatalf("expected kept read mail, got %+v", aliceKeep)
	}
	unreadMail, err = c.CountUnreadMail(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unreadMail != 0 {
		t.Fatalf("expected no unread mail after mark-read, got %d", unreadMail)
	}

	exec(t, c, carol, proto.CmdDeleteMail, proto.DeleteMailPayload{Mail: mail.ID})
	carolTrash, err := c.ListMail(carol.ID, "trash", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(carolTrash) != 1 || carolTrash[0].ID != mail.ID {
		t.Fatalf("expected deleted mail in trash, got %+v", carolTrash)
	}

	reply := exec(t, c, alice, proto.CmdSendMail, proto.SendMailPayload{
		To:      []string{"bob"},
		Subject: "Re: Campus plans",
		Body:    "See you there.",
		ReplyTo: mail.ID,
	})
	bobInbox, err := c.ListMail(bob.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobInbox) != 1 || bobInbox[0].ID != reply.ID || bobInbox[0].ParentID != mail.ID {
		t.Fatalf("expected reply in bob inbox, got %+v", bobInbox)
	}
	execExpectErr(t, c, carol, proto.CmdUpdateMail, proto.UpdateMailPayload{Mail: reply.ID, Read: &read}, proto.ErrNotFound)
	uploaded := exec(t, c, bob, proto.CmdAttachMail, proto.AttachMailPayload{
		Mail:        mail.ID,
		Filename:    "lab.zip",
		ContentType: "application/zip",
		SizeBytes:   2048,
	})
	execExpectErr(t, c, bob, proto.CmdAttachMail, proto.AttachMailPayload{
		Mail:        mail.ID,
		Filename:    "too-large.zip",
		ContentType: "application/zip",
		SizeBytes:   11 << 20,
	}, proto.ErrValidationFailed)
	execExpectErr(t, c, carol, proto.CmdAttachMail, proto.AttachMailPayload{
		Mail:        mail.ID,
		Filename:    "not-mine.txt",
		ContentType: "text/plain",
		SizeBytes:   1,
	}, proto.ErrForbidden)
	aliceMail, err := c.GetMail(alice.ID, mail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if aliceMail == nil || len(aliceMail.Attachments) != 2 || aliceMail.Attachments[1].ID != uploaded.ID || aliceMail.Attachments[1].Filename != "lab.zip" {
		t.Fatalf("expected uploaded mail attachment visible to alice, got %+v", aliceMail)
	}

	exec(t, c, bob, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "alice",
		Kind:   "friend",
		Active: true,
	})
	exec(t, c, bob, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "carol",
		Kind:   "friend",
		Active: true,
	})
	group := exec(t, c, bob, proto.CmdSetMailGroup, proto.SetMailGroupPayload{
		Name:    "lab",
		Members: []string{"alice", "carol"},
	})
	groups, err := c.ListMailGroups(bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].ID != "friends" || !groups[0].BuiltIn || len(groups[0].Members) != 2 || groups[1].ID != group.ID || groups[1].Name != "lab" || len(groups[1].Members) != 2 {
		t.Fatalf("expected built-in friends group and bob mail group, got %+v", groups)
	}
	groupMail := exec(t, c, bob, proto.CmdSendMail, proto.SendMailPayload{
		ToGroups: []string{"lab", "friends"},
		Subject:  "Lab broadcast",
		Body:     "Bring your notebook.",
	})
	aliceInbox, err = c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	foundGroupMail := false
	for _, item := range aliceInbox {
		if item.ID == groupMail.ID {
			foundGroupMail = len(item.ToNames) == 2
		}
	}
	if !foundGroupMail {
		t.Fatalf("expected deduplicated group/friends mail in alice inbox, got %+v", aliceInbox)
	}

	exec(t, c, alice, proto.CmdSetDirectMessageSettings, proto.SetDirectMessageSettingsPayload{Policy: "friends"})
	settings, err := c.GetDirectMessageSettings(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Policy != "friends" {
		t.Fatalf("expected friends-only direct-message policy, got %+v", settings)
	}
	execExpectErr(t, c, bob, proto.CmdSendDirectMessage, proto.SendDirectMessagePayload{
		To:   "alice",
		Body: "Blocked short ping",
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "friend",
		Active: true,
	})

	dm := exec(t, c, bob, proto.CmdSendDirectMessage, proto.SendDirectMessagePayload{
		To:   "alice",
		Body: "Short ping",
	})
	aliceConvos, err := c.ListDirectMessageConversations(alice.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceConvos) != 1 || aliceConvos[0].Name != "bob" || aliceConvos[0].UnreadCount != 1 {
		t.Fatalf("expected unread conversation with bob, got %+v", aliceConvos)
	}
	aliceMessages, err := c.ListDirectMessages(alice.ID, bob.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceMessages) != 1 || aliceMessages[0].ID != dm.ID || aliceMessages[0].Mine || aliceMessages[0].Read {
		t.Fatalf("expected unread incoming direct message, got %+v", aliceMessages)
	}
	exec(t, c, alice, proto.CmdMarkDirectMessageRead, proto.MarkDirectMessageReadPayload{Message: dm.ID})
	unreadDM, err := c.CountUnreadDirectMessages(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unreadDM != 0 {
		t.Fatalf("expected no unread direct messages, got %d", unreadDM)
	}

	replyDM := exec(t, c, alice, proto.CmdSendDirectMessage, proto.SendDirectMessagePayload{
		To:   "bob",
		Body: "Short pong",
	})
	exec(t, c, bob, proto.CmdDeleteDirectMessage, proto.DeleteDirectMessagePayload{Message: replyDM.ID})
	bobMessages, err := c.ListDirectMessages(bob.ID, alice.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobMessages) != 1 || bobMessages[0].ID != dm.ID {
		t.Fatalf("expected bob deletion to hide only reply message, got %+v", bobMessages)
	}
	aliceMessages, err = c.ListDirectMessages(alice.ID, bob.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceMessages) != 2 {
		t.Fatalf("expected sender to retain deleted recipient copy, got %+v", aliceMessages)
	}
	exec(t, c, alice, proto.CmdSetDirectMessageSettings, proto.SetDirectMessageSettingsPayload{Policy: "none"})
	execExpectErr(t, c, bob, proto.CmdSendDirectMessage, proto.SendDirectMessagePayload{
		To:   "alice",
		Body: "Blocked again",
	}, proto.ErrForbidden)
}

func TestMailPostAuthorFromArticle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Reading actions",
		Body:  "Original article body",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected root post, got %+v", posts)
	}

	mail := exec(t, c, bob, proto.CmdMailPostAuthor, proto.MailPostAuthorPayload{
		Post:    posts[0].ID,
		Subject: "Question from reading",
		Body:    "Can you say more?",
	})
	aliceInbox, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceInbox) != 1 || aliceInbox[0].ID != mail.ID || aliceInbox[0].FromName != "bob" || aliceInbox[0].Subject != "Question from reading" {
		t.Fatalf("expected article-author mail in alice inbox, got %+v", aliceInbox)
	}
	aliceMail, err := c.GetMail(alice.ID, mail.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Can you say more?", "Board: general", "Thread: Reading actions", "Original article body"} {
		if aliceMail == nil || !strings.Contains(aliceMail.Body, want) {
			t.Fatalf("expected mail body to contain %q, got %+v", want, aliceMail)
		}
	}
	bobSent, err := c.ListMail(bob.ID, "sent", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobSent) != 1 || bobSent[0].ID != mail.ID {
		t.Fatalf("expected sent copy for article-author mail, got %+v", bobSent)
	}

	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:            "general",
		AnonymousAllowed: boolPtr(true),
	})
	anonymous := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board:     "general",
		Title:     "Anonymous article",
		Body:      "no author mail target",
		Anonymous: true,
	})
	anonPosts, err := c.ListPosts(anonymous.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	execExpectErr(t, c, bob, proto.CmdMailPostAuthor, proto.MailPostAuthorPayload{
		Post: anonPosts[0].ID,
		Body: "who wrote this?",
	}, proto.ErrValidationFailed)
	exec(t, c, alice, proto.CmdRedactPost, proto.RedactPostPayload{
		Post: posts[0].ID,
	})
	execExpectErr(t, c, bob, proto.CmdMailPostAuthor, proto.MailPostAuthorPayload{
		Post: posts[0].ID,
		Body: "still there?",
	}, proto.ErrConflict)
}

func TestSysopMailAll(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	carol := registerAndGetUser(t, c, "carol", "pw")

	execExpectErr(t, c, bob, proto.CmdSendMail, proto.SendMailPayload{
		ToAll:   true,
		Subject: "Not sysop",
		Body:    "hello everyone",
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "admin",
		Kind:   "ignore",
		Active: true,
	})

	broadcast := exec(t, c, admin, proto.CmdSendMail, proto.SendMailPayload{
		ToAll:   true,
		Subject: "Campus bulletin",
		Body:    "Maintenance at midnight.",
	})
	for _, user := range []*core.User{alice, bob, carol} {
		inbox, err := c.ListMail(user.ID, "inbox", 10, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(inbox) != 1 || inbox[0].ID != broadcast.ID || inbox[0].FromName != "admin" || inbox[0].Read {
			t.Fatalf("expected sysop broadcast in %s inbox, got %+v", user.Name, inbox)
		}
		if len(inbox[0].ToNames) != 3 {
			t.Fatalf("expected mail-all to address three users, got %+v", inbox[0].ToNames)
		}
	}

	adminInbox, err := c.ListMail(admin.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminInbox) != 0 {
		t.Fatalf("expected mail-all not to create admin inbox copy, got %+v", adminInbox)
	}
	adminSent, err := c.ListMail(admin.ID, "sent", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminSent) != 1 || adminSent[0].ID != broadcast.ID || !adminSent[0].Read {
		t.Fatalf("expected sysop sent copy, got %+v", adminSent)
	}

	sysmail, err := c.GetBoard("sysmail")
	if err != nil {
		t.Fatal(err)
	}
	if sysmail == nil || sysmail.Name != "sysmail" || !sysmail.ReadOnly || !sysmail.NoReply || !sysmail.MemberReadMode || !sysmail.MemberPostMode {
		t.Fatalf("expected restricted sysmail board, got %+v", sysmail)
	}
	sysmailThreads, err := c.ListThreads("sysmail", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	expectedThreadID := "sysmail_thr_" + broadcast.ID
	if len(sysmailThreads) != 1 || sysmailThreads[0].ID != expectedThreadID || sysmailThreads[0].Title != "Sysop mail: Campus bulletin" {
		t.Fatalf("expected generated sysmail thread, got %+v", sysmailThreads)
	}
	sysmailPosts, err := c.ListPosts(expectedThreadID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sysmailPosts) != 1 || sysmailPosts[0].Author != "admin" {
		t.Fatalf("expected one admin-authored sysmail post, got %+v", sysmailPosts)
	}
	for _, want := range []string{"# Sysop mail: Campus bulletin", "From: admin", "Recipients: 3 users", "Source: admin mail-all broadcast", "Maintenance at midnight.", "Generated restricted sysop mail record"} {
		if !strings.Contains(sysmailPosts[0].Body, want) {
			t.Fatalf("expected sysmail post body to contain %q, got:\n%s", want, sysmailPosts[0].Body)
		}
	}

	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatal(err)
	}
	sysmail, err = c.GetBoard("sysmail")
	if err != nil {
		t.Fatal(err)
	}
	if sysmail == nil || !sysmail.ReadOnly || !sysmail.NoReply || !sysmail.MemberReadMode || !sysmail.MemberPostMode {
		t.Fatalf("expected rebuild to preserve restricted sysmail board, got %+v", sysmail)
	}
	sysmailThreads, err = c.ListThreads("sysmail", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sysmailThreads) != 1 || sysmailThreads[0].ID != expectedThreadID {
		t.Fatalf("expected rebuild to preserve generated sysmail thread, got %+v", sysmailThreads)
	}
}

func TestSocialGraphAndIgnoreLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	carol := registerAndGetUser(t, c, "carol", "pw")

	exec(t, c, alice, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "friend",
		Active: true,
		Note:   "lab partner",
	})
	exec(t, c, bob, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "alice",
		Kind:   "friend",
		Active: true,
	})
	exec(t, c, alice, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "carol",
		Kind:   "ignore",
		Active: true,
		Note:   "too noisy",
	})

	friends, err := c.ListSocialUsers(alice.ID, "friends", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(friends) != 1 || friends[0].Name != "bob" || friends[0].Note != "lab partner" || !friends[0].Mutual {
		t.Fatalf("expected mutual friend bob with note, got %+v", friends)
	}
	fans, err := c.ListSocialUsers(alice.ID, "fans", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fans) != 1 || fans[0].Name != "bob" || !fans[0].Mutual {
		t.Fatalf("expected bob fan/mutual, got %+v", fans)
	}
	ignores, err := c.ListSocialUsers(alice.ID, "ignores", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ignores) != 1 || ignores[0].Name != "carol" || !ignores[0].Ignored {
		t.Fatalf("expected ignored carol, got %+v", ignores)
	}

	online, err := c.ListSocialUsers(alice.ID, "friends", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(online) != 0 {
		t.Fatalf("expected no online friends before presence, got %+v", online)
	}
	execExpectErr(t, c, carol, proto.CmdSetLoginWatch, proto.SetLoginWatchPayload{
		User:   "bob",
		Active: true,
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdSetLoginWatch, proto.SetLoginWatchPayload{
		User:   "bob",
		Active: true,
	})
	notifs, err := c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 0 {
		t.Fatalf("expected login watch to wait while bob is offline, got %+v", notifs)
	}
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{Status: "reading:general"})
	notifs, err = c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 1 || notifs[0].Kind != "login" || notifs[0].Actor != "bob" || notifs[0].ThreadID != "" {
		t.Fatalf("expected one login notification for bob, got %+v", notifs)
	}
	online, err = c.ListSocialUsers(alice.ID, "friends", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(online) != 1 || online[0].Name != "bob" || !online[0].Online || online[0].Status != "reading:general" {
		t.Fatalf("expected bob online friend, got %+v", online)
	}
	if online[0].Mode != "reading" || online[0].BoardID != "general" {
		t.Fatalf("expected legacy presence to derive board/mode, got %+v", online[0])
	}
	globalOnline, err := c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(globalOnline) != 1 || globalOnline[0].Name != "bob" || globalOnline[0].BoardID != "general" {
		t.Fatalf("expected bob in global online list, got %+v", globalOnline)
	}
	boardOnline, err := c.ListOnlineUsers(alice.ID, "general", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(boardOnline) != 1 || boardOnline[0].Name != "bob" || boardOnline[0].Mode != "reading" {
		t.Fatalf("expected bob in board online list, got %+v", boardOnline)
	}
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{Status: "active"})
	notifs, err = c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected login watch to clear after one notification, got %+v", notifs)
	}
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{Status: "invisible", Board: "general", Location: "Hidden"})
	online, err = c.ListSocialUsers(alice.ID, "friends", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(online) != 0 {
		t.Fatalf("expected invisible bob to be hidden from online friends, got %+v", online)
	}
	globalOnline, err = c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(globalOnline) != 0 {
		t.Fatalf("expected invisible bob to be hidden from global online list, got %+v", globalOnline)
	}
	exec(t, c, alice, proto.CmdSetLoginWatch, proto.SetLoginWatchPayload{
		User:   "bob",
		Active: true,
	})
	notifs, err = c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected invisible bob not to satisfy login watch immediately, got %+v", notifs)
	}
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{Status: "active"})
	notifs, err = c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 2 || notifs[0].Kind != "login" || notifs[0].Actor != "bob" {
		t.Fatalf("expected visible bob to satisfy pending login watch, got %+v", notifs)
	}

	exec(t, c, carol, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "ignore",
		Active: true,
	})
	execExpectErr(t, c, bob, proto.CmdSendMail, proto.SendMailPayload{
		To:      []string{"carol"},
		Subject: "blocked",
		Body:    "hello",
	}, proto.ErrForbidden)
	execExpectErr(t, c, bob, proto.CmdSendDirectMessage, proto.SendDirectMessagePayload{
		To:   "carol",
		Body: "hello",
	}, proto.ErrForbidden)

	exec(t, c, alice, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "friend",
		Active: false,
	})
	friends, err = c.ListSocialUsers(alice.ID, "friends", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(friends) != 0 {
		t.Fatalf("expected friend removal, got %+v", friends)
	}
}

func TestPrivilegedCloakPresence(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	execExpectErr(t, c, alice, proto.CmdSetPresence, proto.SetPresencePayload{
		Status: "cloak",
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "admin",
		Kind:   "friend",
		Active: true,
	})
	exec(t, c, admin, proto.CmdSetPresence, proto.SetPresencePayload{
		Status:   "cloaked",
		Mode:     "reading",
		Board:    "general",
		Location: "Control room",
		FromHost: "ops.test",
	})

	find := func(users []core.SocialUser, name string) *core.SocialUser {
		for i := range users {
			if users[i].Name == name {
				return &users[i]
			}
		}
		return nil
	}

	aliceOnline, err := c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if found := find(aliceOnline, "admin"); found != nil {
		t.Fatalf("expected ordinary user not to see cloaked admin globally, got %+v", found)
	}
	adminOnline, err := c.ListOnlineUsers(admin.ID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	cloaked := find(adminOnline, "admin")
	if cloaked == nil || cloaked.Status != "cloak" || cloaked.BoardID != "general" || cloaked.LocationLabel != "Control room" || cloaked.FromHost != "ops.test" {
		t.Fatalf("expected admin to see own cloaked presence, got %+v", adminOnline)
	}
	aliceBoardOnline, err := c.ListOnlineUsers(alice.ID, "general", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if found := find(aliceBoardOnline, "admin"); found != nil {
		t.Fatalf("expected ordinary user not to see cloaked admin on board, got %+v", found)
	}
	adminBoardOnline, err := c.ListOnlineUsers(admin.ID, "general", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cloaked := find(adminBoardOnline, "admin"); cloaked == nil || cloaked.Status != "cloak" || cloaked.Mode != "reading" {
		t.Fatalf("expected privileged board online list to include cloaked admin, got %+v", adminBoardOnline)
	}
	stats, err := c.GetCommunityStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.OnlineUsers != 0 {
		t.Fatalf("expected cloaked presence excluded from public online count, got %+v", stats)
	}

	exec(t, c, alice, proto.CmdSetLoginWatch, proto.SetLoginWatchPayload{
		User:   "admin",
		Active: true,
	})
	notifs, err := c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 0 {
		t.Fatalf("expected cloak not to satisfy login watch immediately, got %+v", notifs)
	}
	exec(t, c, admin, proto.CmdSetPresence, proto.SetPresencePayload{Status: "active"})
	notifs, err = c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 1 || notifs[0].Kind != "login" || notifs[0].Actor != "admin" {
		t.Fatalf("expected visible admin presence to satisfy login watch, got %+v", notifs)
	}
}

func TestMultiSessionPresenceLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, alice, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "friend",
		Active: true,
	})
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{
		SessionID: "web",
		Status:    "reading:general",
		Mode:      "reading",
		Board:     "general",
		Location:  "General",
	})
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{
		SessionID: "ssh",
		Status:    "active",
		Mode:      "mail",
		Location:  "Mailbox",
		FromHost:  "ssh.test",
	})

	sessionNames := func(users []core.SocialUser, name string) map[string]core.SocialUser {
		out := map[string]core.SocialUser{}
		for _, user := range users {
			if user.Name == name {
				out[user.SessionID] = user
			}
		}
		return out
	}

	globalOnline, err := c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	bobSessions := sessionNames(globalOnline, "bob")
	if len(bobSessions) != 2 || bobSessions["web"].BoardID != "general" || bobSessions["ssh"].Mode != "mail" || bobSessions["ssh"].FromHost != "ssh.test" {
		t.Fatalf("expected two bob sessions in global online list, got %+v", globalOnline)
	}
	boardOnline, err := c.ListOnlineUsers(alice.ID, "general", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	bobSessions = sessionNames(boardOnline, "bob")
	if len(bobSessions) != 1 || bobSessions["web"].SessionID != "web" {
		t.Fatalf("expected only bob web session on general board, got %+v", boardOnline)
	}
	stats, err := c.GetCommunityStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.OnlineUsers != 1 {
		t.Fatalf("expected public online count to count distinct users, got %+v", stats)
	}

	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{SessionID: "web", Status: "offline"})
	globalOnline, err = c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	bobSessions = sessionNames(globalOnline, "bob")
	if len(bobSessions) != 1 || bobSessions["ssh"].SessionID != "ssh" {
		t.Fatalf("expected bob to remain online via ssh session, got %+v", globalOnline)
	}
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{SessionID: "ssh", Status: "invisible"})
	globalOnline, err = c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bobSessions = sessionNames(globalOnline, "bob"); len(bobSessions) != 0 {
		t.Fatalf("expected bob hidden after all sessions hidden, got %+v", globalOnline)
	}

	exec(t, c, alice, proto.CmdSetLoginWatch, proto.SetLoginWatchPayload{
		User:   "bob",
		Active: true,
	})
	notifs, err := c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 0 {
		t.Fatalf("expected hidden sessions not to satisfy login watch immediately, got %+v", notifs)
	}
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{SessionID: "web", Status: "active"})
	notifs, err = c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 1 || notifs[0].Kind != "login" || notifs[0].Actor != "bob" {
		t.Fatalf("expected visible session to satisfy login watch, got %+v", notifs)
	}
}

func TestBoardReadMarkersLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	thread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Unread thread",
		Body:  "First post",
	})

	summaries, err := c.ListBoardSummaries(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	var general *core.BoardSummary
	for i := range summaries {
		if summaries[i].ID == "general" {
			general = &summaries[i]
			break
		}
	}
	if general == nil {
		t.Fatalf("expected general board summary, got %+v", summaries)
	}
	if general.UnreadPosts != 1 || general.UnreadThreads != 1 {
		t.Fatalf("expected one unread post/thread before marker, got %+v", general)
	}

	exec(t, c, alice, proto.CmdMarkBoardRead, proto.MarkBoardReadPayload{Board: "general"})

	summaries, err = c.ListBoardSummaries(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	general = nil
	for i := range summaries {
		if summaries[i].ID == "general" {
			general = &summaries[i]
			break
		}
	}
	if general == nil || general.UnreadPosts != 0 || general.UnreadThreads != 0 {
		t.Fatalf("expected mark-read to clear unread state, got %+v", summaries)
	}
	if general.ReadSeq != general.LastSeq {
		t.Fatalf("expected read seq to match board head, got read=%d last=%d", general.ReadSeq, general.LastSeq)
	}

	exec(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "Second post",
	})

	unread, err := c.ListBoardSummaries(alice.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].UnreadPosts != 1 || unread[0].UnreadThreads != 1 {
		t.Fatalf("expected new post to make board unread again, got %+v", unread)
	}

	exec(t, c, alice, proto.CmdRestoreBoardRead, proto.RestoreBoardReadPayload{Board: "general"})

	summaries, err = c.ListBoardSummaries(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].UnreadPosts != 2 {
		t.Fatalf("expected restore to bring previous unread posts back, got %+v", summaries[0])
	}

	execExpectErr(t, c, alice, proto.CmdMarkBoardRead, proto.MarkBoardReadPayload{
		Board: "missing",
	}, proto.ErrNotFound)
	execExpectErr(t, c, alice, proto.CmdRestoreBoardRead, proto.RestoreBoardReadPayload{
		Board: "missing",
	}, proto.ErrNotFound)
}

func TestBoardSummaryDiscoveryFilters(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "tech",
		Name:        "Tech Talk",
		Description: "Computing laboratory",
	})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:          "music",
		Name:        "Music Club",
		Description: "Campus bands",
	})
	techThread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "tech",
		Title: "Build notes",
		Body:  "First post",
	})
	exec(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: techThread.ID,
		Body:   "Second post",
	})
	exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "music",
		Title: "Gig list",
		Body:  "First post",
	})
	exec(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{
		SessionID: "web",
		Status:    "active",
		Mode:      "reading",
		Board:     "tech",
	})

	byPosts, err := c.ListBoardSummaries(alice.ID, false, core.BoardSummaryOptions{Sort: "posts"})
	if err != nil {
		t.Fatal(err)
	}
	var tech *core.BoardSummary
	var general *core.BoardSummary
	for i := range byPosts {
		if byPosts[i].ID == "tech" {
			tech = &byPosts[i]
		}
		if byPosts[i].ID == "general" {
			general = &byPosts[i]
		}
	}
	if tech == nil || tech.ThreadCount != 1 || tech.PostCount != 2 || tech.OnlineUsers != 1 || !tech.NewBoard || tech.CreatedAt == 0 {
		t.Fatalf("expected tech summary to include article, online, and new-board metadata, got %+v", tech)
	}
	if general == nil || general.NewBoard || general.CreatedAt != 0 {
		t.Fatalf("expected seeded general board to remain old, got %+v", general)
	}

	search, err := c.ListBoardSummaries(alice.ID, false, core.BoardSummaryOptions{Search: "computing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(search) != 1 || search[0].ID != "tech" {
		t.Fatalf("expected board search to match description, got %+v", search)
	}
	online, err := c.ListBoardSummaries(alice.ID, false, core.BoardSummaryOptions{Sort: "online"})
	if err != nil {
		t.Fatal(err)
	}
	if len(online) == 0 || online[0].ID != "tech" || online[0].OnlineUsers != 1 {
		t.Fatalf("expected online-sorted board summaries to lead with tech, got %+v", online)
	}
	newBoards, err := c.ListBoardSummaries(alice.ID, false, core.BoardSummaryOptions{NewOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	foundTech := false
	foundMusic := false
	foundGeneral := false
	for _, board := range newBoards {
		if board.ID == "tech" {
			foundTech = true
		}
		if board.ID == "music" {
			foundMusic = true
		}
		if board.ID == "general" {
			foundGeneral = true
		}
	}
	if !foundTech || !foundMusic || foundGeneral {
		t.Fatalf("expected new-board filter to include created boards and exclude seeded general, got %+v", newBoards)
	}
}

func TestThreadReadMarkersLifecycle(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	thread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Thread unread",
		Body:  "First post",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected first post")
	}

	summaries, err := c.ListThreadSummaries(alice.ID, "general", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected one thread summary, got %+v", summaries)
	}
	if summaries[0].UnreadPosts != 1 || summaries[0].FirstUnreadPostID != posts[0].ID {
		t.Fatalf("expected first post unread, got %+v", summaries[0])
	}

	exec(t, c, alice, proto.CmdMarkThreadRead, proto.MarkThreadReadPayload{Thread: thread.ID})

	summaries, err = c.ListThreadSummaries(alice.ID, "general", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].UnreadPosts != 0 || summaries[0].FirstUnreadPostID != "" {
		t.Fatalf("expected thread mark-read to clear unread posts, got %+v", summaries[0])
	}

	reply := exec(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "Second post",
	})

	summaries, err = c.ListThreadSummaries(alice.ID, "general", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].UnreadPosts != 1 || summaries[0].FirstUnreadPostID != reply.ID {
		t.Fatalf("expected reply to be first unread, got %+v", summaries[0])
	}

	exec(t, c, alice, proto.CmdRestoreThreadRead, proto.RestoreThreadReadPayload{Thread: thread.ID})

	summaries, err = c.ListThreadSummaries(alice.ID, "general", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].UnreadPosts != 2 || summaries[0].FirstUnreadPostID != posts[0].ID {
		t.Fatalf("expected restore to expose both posts again, got %+v", summaries[0])
	}

	exec(t, c, alice, proto.CmdMarkBoardRead, proto.MarkBoardReadPayload{Board: "general"})

	summaries, err = c.ListThreadSummaries(alice.ID, "general", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].UnreadPosts != 0 {
		t.Fatalf("expected board mark-read to clear thread summary unread state, got %+v", summaries[0])
	}

	execExpectErr(t, c, alice, proto.CmdMarkThreadRead, proto.MarkThreadReadPayload{
		Thread: "missing",
	}, proto.ErrNotFound)
	execExpectErr(t, c, alice, proto.CmdRestoreThreadRead, proto.RestoreThreadReadPayload{
		Thread: "missing",
	}, proto.ErrNotFound)
}

func TestUnreadOnlyThreadSummaries(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	first := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "First unread topic",
		Body:  "First post",
	})
	second := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Second unread topic",
		Body:  "Second post",
	})

	unread, err := c.ListThreadSummaries(alice.ID, "general", 10, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 2 {
		t.Fatalf("expected both threads unread, got %+v", unread)
	}

	exec(t, c, alice, proto.CmdMarkThreadRead, proto.MarkThreadReadPayload{Thread: second.ID})

	unread, err = c.ListThreadSummaries(alice.ID, "general", 10, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].ID != first.ID {
		t.Fatalf("expected only first thread unread after marking second, got %+v", unread)
	}

	exec(t, c, alice, proto.CmdMarkBoardRead, proto.MarkBoardReadPayload{Board: "general"})

	unread, err = c.ListThreadSummaries(alice.ID, "general", 10, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 0 {
		t.Fatalf("expected no unread threads after board marker, got %+v", unread)
	}

	reply := exec(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: first.ID,
		Body:   "Fresh reply",
	})

	unread, err = c.ListThreadSummaries(alice.ID, "general", 10, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].ID != first.ID || unread[0].FirstUnreadPostID != reply.ID {
		t.Fatalf("expected fresh reply to restore first thread unread state, got %+v", unread)
	}
}

func TestSiteWideAndFavoriteFolderUnreadThreadSummaries(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "tech", Name: "Tech"})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "life", Name: "Life"})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "secret", Name: "Secret"})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secret",
		MemberReadMode: boolPtr(true),
	})

	work := exec(t, c, alice, proto.CmdCreateFavoriteFolder, proto.CreateFavoriteFolderPayload{Name: "Work"})
	child := exec(t, c, alice, proto.CmdCreateFavoriteFolder, proto.CreateFavoriteFolderPayload{Name: "Child", ParentID: work.ID})
	exec(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{Board: "tech", Favorite: true, FolderID: work.ID})
	exec(t, c, alice, proto.CmdSetBoardFavorite, proto.SetBoardFavoritePayload{Board: "life", Favorite: true, FolderID: child.ID})

	tech := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "tech", Title: "Tech unread", Body: "first"})
	life := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "life", Title: "Life unread", Body: "first"})
	secret := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "secret", Title: "Secret unread", Body: "hidden"})

	siteWide, err := c.ListUnreadThreadSummaries(alice, false, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasThread(siteWide, tech.ID) || !hasThread(siteWide, life.ID) || hasThread(siteWide, secret.ID) {
		t.Fatalf("expected site-wide visible unread threads only, got %+v", siteWide)
	}
	if got := boardNameForThread(siteWide, tech.ID); got != "Tech" {
		t.Fatalf("expected board name on cross-board unread summary, got %q in %+v", got, siteWide)
	}

	favorites, err := c.ListUnreadThreadSummaries(alice, true, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasThread(favorites, tech.ID) || !hasThread(favorites, life.ID) || hasThread(favorites, secret.ID) {
		t.Fatalf("expected favorite unread threads, got %+v", favorites)
	}

	workUnread, err := c.ListUnreadThreadSummaries(alice, false, work.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasThread(workUnread, tech.ID) || !hasThread(workUnread, life.ID) {
		t.Fatalf("expected favorite folder unread traversal to include descendant folder boards, got %+v", workUnread)
	}

	exec(t, c, alice, proto.CmdMarkThreadRead, proto.MarkThreadReadPayload{Thread: tech.ID})
	workUnread, err = c.ListUnreadThreadSummaries(alice, false, work.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if hasThread(workUnread, tech.ID) || !hasThread(workUnread, life.ID) {
		t.Fatalf("expected marking tech read to leave only life unread in folder traversal, got %+v", workUnread)
	}

	adminUnread, err := c.ListUnreadThreadSummaries(admin, false, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasThread(adminUnread, secret.ID) {
		t.Fatalf("expected admin to see member-read unread thread, got %+v", adminUnread)
	}
}

func TestReadablePostsByAuthorRespectBoardReadPolicy(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "tech", Name: "Tech"})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "life", Name: "Life"})
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "secret", Name: "Secret"})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secret",
		MemberReadMode: boolPtr(true),
	})
	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:  "secret",
		User:   "bob",
		Member: true,
	})

	tech := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "tech", Title: "Tech notes", Body: "tech first"})
	life := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "life", Title: "Life notes", Body: "life first"})
	secret := exec(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "secret", Title: "Secret notes", Body: "secret first"})

	publicPosts, err := c.ListPostsByAuthor("bob", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPostInThread(publicPosts, tech.ID) || !hasPostInThread(publicPosts, life.ID) || hasPostInThread(publicPosts, secret.ID) {
		t.Fatalf("expected public author posts to hide member-read board posts, got %+v", publicPosts)
	}
	if got := postBoardNameForThread(publicPosts, tech.ID); got != "Tech" {
		t.Fatalf("expected board name on public author post, got %q in %+v", got, publicPosts)
	}
	if got := postThreadTitleForThread(publicPosts, tech.ID); got != "Tech notes" {
		t.Fatalf("expected thread title on public author post, got %q in %+v", got, publicPosts)
	}

	alicePosts, err := c.ListReadablePostsByAuthor(alice, "bob", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPostInThread(alicePosts, tech.ID) || !hasPostInThread(alicePosts, life.ID) || hasPostInThread(alicePosts, secret.ID) {
		t.Fatalf("expected alice author stream to hide member-read board posts, got %+v", alicePosts)
	}

	adminPosts, err := c.ListReadablePostsByAuthor(admin, "bob", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPostInThread(adminPosts, secret.ID) {
		t.Fatalf("expected admin author stream to include member-read board posts, got %+v", adminPosts)
	}

	bobPosts, err := c.ListReadablePostsByAuthor(bob, "bob", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPostInThread(bobPosts, secret.ID) {
		t.Fatalf("expected board member author stream to include member-read board posts, got %+v", bobPosts)
	}
}

func TestReplyTreePostsReturnRootAndDescendants(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")

	thread := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Reply tree",
		Body:  "root",
	})
	rootPosts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rootPosts) != 1 {
		t.Fatalf("expected root post, got %+v", rootPosts)
	}
	root := rootPosts[0]
	firstReply := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread:  thread.ID,
		ReplyTo: root.ID,
		Body:    "first reply",
	})
	secondReply := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread:  thread.ID,
		ReplyTo: root.ID,
		Body:    "second reply",
	})
	unrelated := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "not in reply tree",
	})

	tree, err := c.ListReplyTreePosts(root.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPostID(tree, root.ID) || !hasPostID(tree, firstReply.ID) || !hasPostID(tree, secondReply.ID) || hasPostID(tree, unrelated.ID) {
		t.Fatalf("expected reply tree to include root and direct descendants only, got %+v", tree)
	}
	if got := postReplyDepth(tree, root.ID); got != 0 {
		t.Fatalf("expected root depth 0, got %d in %+v", got, tree)
	}
	if got := postReplyDepth(tree, firstReply.ID); got != 1 {
		t.Fatalf("expected reply depth 1, got %d in %+v", got, tree)
	}
	if got := postThreadTitleForPost(tree, root.ID); got != "Reply tree" {
		t.Fatalf("expected thread title on reply-tree posts, got %q in %+v", got, tree)
	}
}

func hasThread(threads []core.ThreadSummary, threadID string) bool {
	for _, thread := range threads {
		if thread.ID == threadID {
			return true
		}
	}
	return false
}

func boardNameForThread(threads []core.ThreadSummary, threadID string) string {
	for _, thread := range threads {
		if thread.ID == threadID {
			return thread.BoardName
		}
	}
	return ""
}

func hasPostID(posts []core.Post, postID string) bool {
	for _, post := range posts {
		if post.ID == postID {
			return true
		}
	}
	return false
}

func hasPostInThread(posts []core.Post, threadID string) bool {
	for _, post := range posts {
		if post.Thread == threadID {
			return true
		}
	}
	return false
}

func postBoardNameForThread(posts []core.Post, threadID string) string {
	for _, post := range posts {
		if post.Thread == threadID {
			return post.BoardName
		}
	}
	return ""
}

func postThreadTitleForThread(posts []core.Post, threadID string) string {
	for _, post := range posts {
		if post.Thread == threadID {
			return post.ThreadTitle
		}
	}
	return ""
}

func postThreadTitleForPost(posts []core.Post, postID string) string {
	for _, post := range posts {
		if post.ID == postID {
			return post.ThreadTitle
		}
	}
	return ""
}

func postReplyDepth(posts []core.Post, postID string) int {
	for _, post := range posts {
		if post.ID == postID {
			return post.ReplyDepth
		}
	}
	return -1
}

func favoriteFolderByName(tree *core.FavoriteTree, name string) *core.FavoriteFolder {
	for i := range tree.Folders {
		if tree.Folders[i].Name == name {
			return &tree.Folders[i]
		}
	}
	return nil
}

func favoriteFolderForBoard(tree *core.FavoriteTree, boardID string) string {
	for _, board := range tree.Boards {
		if board.ID == boardID {
			return board.FolderID
		}
	}
	return ""
}

func TestMarkPostReadAdvancesThreadMarkerThroughPost(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	thread := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Article markers",
		Body:  "First post",
	})
	second := exec(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "Second post",
	})
	third := exec(t, c, admin, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "Third post",
	})

	exec(t, c, alice, proto.CmdMarkPostRead, proto.MarkPostReadPayload{Post: second.ID})

	summaries, err := c.ListThreadSummaries(alice.ID, "general", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected thread summary")
	}
	if summaries[0].UnreadPosts != 1 || summaries[0].FirstUnreadPostID != third.ID {
		t.Fatalf("expected third post to be first unread after marking second read, got %+v", summaries[0])
	}

	exec(t, c, alice, proto.CmdRestoreThreadRead, proto.RestoreThreadReadPayload{Thread: thread.ID})

	summaries, err = c.ListThreadSummaries(alice.ID, "general", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].UnreadPosts != 3 {
		t.Fatalf("expected restore to return all posts to unread, got %+v", summaries[0])
	}

	execExpectErr(t, c, alice, proto.CmdMarkPostRead, proto.MarkPostReadPayload{
		Post: "missing",
	}, proto.ErrNotFound)
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

func TestPollCreationSupportsExpiryWeek(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[poll expires=1w]\nQuestion?\nOption A\nOption B\n[/poll]"
	start := time.Now().UTC().Truncate(time.Second)
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Week expiry poll", Body: threadBody,
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
		t.Fatal("expected poll row for week expiry poll")
	}

	end := start.Add(7 * 24 * time.Hour).UnixMilli()
	if poll.ExpiresAt < end-10_000 || poll.ExpiresAt > end+10_000 {
		t.Fatalf("expected approx %d, got %d", end, poll.ExpiresAt)
	}
}

func TestPollCreationSupportsExpiryUppercaseWeek(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[poll expires=1W]\nQuestion?\nOption A\nOption B\n[/poll]"
	start := time.Now().UTC().Truncate(time.Second)
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Week expiry poll uppercase", Body: threadBody,
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
		t.Fatal("expected poll row for uppercase week expiry poll")
	}

	end := start.Add(7 * 24 * time.Hour).UnixMilli()
	if poll.ExpiresAt < end-10_000 || poll.ExpiresAt > end+10_000 {
		t.Fatalf("expected approx %d, got %d", end, poll.ExpiresAt)
	}
}

func TestPollCreationSupportsExpiryUppercaseDuration(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[poll expires=2H]\nQuestion?\nOption A\nOption B\n[/poll]"
	start := time.Now().UTC().Truncate(time.Second)
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Uppercase duration expiry poll", Body: threadBody,
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
		t.Fatal("expected poll row for uppercase duration expiry poll")
	}

	end := start.Add(2 * time.Hour).UnixMilli()
	if poll.ExpiresAt < end-10_000 || poll.ExpiresAt > end+10_000 {
		t.Fatalf("expected approx %d, got %d", end, poll.ExpiresAt)
	}
}

func TestPollCreationSupportsUppercasePollTags(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[POLL]\nQuestion?\nOption A\nOption B\n[/POLL]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Uppercase poll tag", Body: threadBody,
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
		t.Fatal("expected poll row for uppercase tags")
	}

	fullPoll, err := c.GetPoll(poll.ID, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fullPoll == nil {
		t.Fatal("expected full poll for uppercase tags")
	}
	if len(fullPoll.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(fullPoll.Options))
	}
}

func TestPollCreationSupportsUppercasePollCloseTag(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[poll]\nQuestion?\nOption A\nOption B\n[/POLL]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Uppercase poll close tag", Body: threadBody,
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
		t.Fatal("expected poll row for uppercase close tag")
	}

	fullPoll, err := c.GetPoll(poll.ID, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fullPoll == nil {
		t.Fatal("expected full poll for uppercase close tag")
	}
	if len(fullPoll.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(fullPoll.Options))
	}
}

func TestPollCreationSupportsUppercasePollTagsAfterRebuild(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[POLL]\nQuestion?\nOption A\nOption B\n[/POLL]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Uppercase poll tags after rebuild", Body: threadBody,
	})

	threadPosts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threadPosts) != 1 {
		t.Fatalf("expected 1 post before rebuild, got %d", len(threadPosts))
	}

	prePoll, err := c.GetPollByPostID(threadPosts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if prePoll == nil {
		t.Fatal("expected poll row before rebuild")
	}

	clearProjectionTablesForTest(t, c)

	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}

	postPoll, err := c.GetPollByPostID(threadPosts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if postPoll == nil {
		t.Fatal("expected poll row after rebuild")
	}
	if postPoll.ExpiresAt != prePoll.ExpiresAt {
		t.Fatalf("expected same poll expiry after rebuild: %d vs %d", prePoll.ExpiresAt, postPoll.ExpiresAt)
	}

	rebuiltPosts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuiltPosts) != 1 {
		t.Fatalf("expected 1 post after rebuild, got %d", len(rebuiltPosts))
	}

	fullPoll, err := c.GetPoll(postPoll.ID, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fullPoll == nil {
		t.Fatal("expected full poll after rebuild")
	}
	if len(fullPoll.Options) != 2 {
		t.Fatalf("expected 2 options after rebuild, got %d", len(fullPoll.Options))
	}
}

func TestPollCreationSupportsUppercasePollTagsWithSurroundingText(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "before line\n[POLL]\nQuestion?\nOption A\nOption B\n[/POLL]\nafter line"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Uppercase poll with surrounding text", Body: threadBody,
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}

	expectedBody := "before line\nafter line"
	if posts[0].Body != expectedBody {
		t.Fatalf("expected poll-stripped body %q, got %q", expectedBody, posts[0].Body)
	}

	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatal("expected poll row for uppercase poll with surrounding text")
	}

	fullPoll, err := c.GetPoll(poll.ID, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fullPoll == nil {
		t.Fatal("expected full poll projection")
	}
	if fullPoll.Question != "Question?" {
		t.Fatalf("expected question %q, got %q", "Question?", fullPoll.Question)
	}
	if len(fullPoll.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(fullPoll.Options))
	}
}

func TestPollCreationSupportsExpiryLocalDateTime(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	raw := time.Now().In(time.Local).Add(15 * time.Minute).Truncate(time.Minute).Format("2006-01-02T15:04")
	threadBody := "[poll expires=" + raw + "]\nQuestion?\nOption A\nOption B\n[/poll]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Local datetime expiry poll", Body: threadBody,
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
		t.Fatal("expected poll row for local datetime expiry poll")
	}

	expected, err := time.ParseInLocation("2006-01-02T15:04", raw, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if poll.ExpiresAt != expected.UnixMilli() {
		t.Fatalf("expected local parsed expiresAt=%d, got=%d", expected.UnixMilli(), poll.ExpiresAt)
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

func TestPublishPollResultCreatesVoteBoardRecord(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	carol := registerAndGetUser(t, c, "carol", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Vote result poll", Body: "[poll]\nBest option?\nOption A\nOption B\n[/poll]",
	})
	threadPosts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	poll, err := c.GetPollByPostID(threadPosts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	pollState, err := c.GetPoll(poll.ID, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pollState.Options) != 2 {
		t.Fatalf("expected poll options, got %+v", pollState.Options)
	}
	exec(t, c, bob, proto.CmdVotePoll, proto.VotePollPayload{Poll: poll.ID, Option: pollState.Options[0].ID})

	execExpectErr(t, c, bob, proto.CmdPublishPollResult, proto.PublishPollResultPayload{
		Poll: poll.ID,
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:          "general",
		User:           "carol",
		Member:         true,
		CanManagePolls: boolPtr(true),
	})
	result := exec(t, c, carol, proto.CmdPublishPollResult, proto.PublishPollResultPayload{
		Poll: poll.ID,
	})
	if result.ID == "" {
		t.Fatalf("expected generated vote result thread, got %+v", result)
	}
	board, err := c.GetBoard("vote")
	if err != nil {
		t.Fatal(err)
	}
	if board == nil || board.Name != "vote" {
		t.Fatalf("expected generated vote board, got %+v", board)
	}
	threads, err := c.ListThreads("vote", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != result.ID || !strings.Contains(threads[0].Title, "Best option?") {
		t.Fatalf("expected generated vote result thread, got %+v", threads)
	}
	posts, err := c.ListPosts(result.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected one vote result post, got %+v", posts)
	}
	for _, want := range []string{"# Poll result: Best option?", "Source thread: Vote result poll", "Total votes: 1", "Option A: 1 vote", "Option B: 0 vote", "Generated public poll result"} {
		if !strings.Contains(posts[0].Body, want) {
			t.Fatalf("expected vote result body to contain %q, got:\n%s", want, posts[0].Body)
		}
	}
	again := exec(t, c, carol, proto.CmdPublishPollResult, proto.PublishPollResultPayload{Poll: poll.ID})
	if again.ID != result.ID {
		t.Fatalf("expected repeated result publish to reuse %q, got %+v", result.ID, again)
	}
	threads, err = c.ListThreads("vote", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 {
		t.Fatalf("expected repeated publish not to duplicate vote result, got %+v", threads)
	}
}

func TestPostArticleFlagsAndNoReply(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")
	carol := registerAndGetUser(t, c, "carol", "pw")

	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Article flags", Body: "root post",
	})
	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected root post, got %+v", posts)
	}
	rootPostID := posts[0].ID

	execExpectErr(t, c, bob, proto.CmdSetPostFlag, proto.SetPostFlagPayload{
		Post:   rootPostID,
		Marked: boolPtr(true),
	}, proto.ErrForbidden)

	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:     "general",
		User:      "alice",
		Member:    true,
		CanCurate: boolPtr(true),
	})
	exec(t, c, admin, proto.CmdSetBoardMember, proto.SetBoardMemberPayload{
		Board:              "general",
		User:               "bob",
		Member:             true,
		CanModerateThreads: boolPtr(true),
	})

	exec(t, c, alice, proto.CmdSetPostFlag, proto.SetPostFlagPayload{
		Post:        rootPostID,
		Marked:      boolPtr(true),
		Recommended: boolPtr(true),
	})
	exec(t, c, bob, proto.CmdSetPostFlag, proto.SetPostFlagPayload{
		Post:    rootPostID,
		NoReply: boolPtr(true),
	})

	root, err := c.GetPost(rootPostID)
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || !root.Marked || !root.Recommended || !root.NoReply {
		t.Fatalf("expected marked/recommended/no-reply root post, got %+v", root)
	}
	execExpectErr(t, c, carol, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadRes.ID,
		Body:   "ordinary reply",
	}, proto.ErrForbidden)
	reply := exec(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadRes.ID,
		Body:   "thread manager reply",
	})
	if reply.ID == "" {
		t.Fatalf("expected thread manager to bypass article no-reply, got %+v", reply)
	}

	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatal(err)
	}
	root, err = c.GetPost(rootPostID)
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || !root.Marked || !root.Recommended || !root.NoReply {
		t.Fatalf("expected rebuild to preserve article flags, got %+v", root)
	}
}

func TestPostTeXAndMailBackFlags(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "TeX mail-back", Body: "root post",
	})
	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected root post, got %+v", posts)
	}
	rootPostID := posts[0].ID

	execExpectErr(t, c, bob, proto.CmdSetPostFlag, proto.SetPostFlagPayload{
		Post: rootPostID,
		TeX:  boolPtr(true),
	}, proto.ErrForbidden)
	exec(t, c, alice, proto.CmdSetPostFlag, proto.SetPostFlagPayload{
		Post:     rootPostID,
		TeX:      boolPtr(true),
		MailBack: boolPtr(true),
	})
	root, err := c.GetPost(rootPostID)
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || !root.TeX || !root.MailBack {
		t.Fatalf("expected tex/mail-back flags, got %+v", root)
	}

	reply := exec(t, c, bob, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread:  threadRes.ID,
		ReplyTo: rootPostID,
		Body:    "mail me back",
	})
	if reply.ID == "" {
		t.Fatalf("expected reply id, got %+v", reply)
	}
	aliceInbox, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceInbox) != 1 || aliceInbox[0].FromName != "bob" || !strings.Contains(aliceInbox[0].Subject, "TeX mail-back") || !strings.Contains(aliceInbox[0].Body, "mail me back") || !strings.Contains(aliceInbox[0].Body, rootPostID) {
		t.Fatalf("expected article mail-back in alice inbox, got %+v", aliceInbox)
	}
	bobSent, err := c.ListMail(bob.ID, "sent", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobSent) != 0 {
		t.Fatalf("expected automatic mail-back not to create sent copy, got %+v", bobSent)
	}

	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatal(err)
	}
	root, err = c.GetPost(rootPostID)
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || !root.TeX || !root.MailBack {
		t.Fatalf("expected rebuild to preserve tex/mail-back flags, got %+v", root)
	}
	aliceInbox, err = c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceInbox) != 1 || !strings.Contains(aliceInbox[0].Body, "mail me back") {
		t.Fatalf("expected rebuild to preserve article mail-back, got %+v", aliceInbox)
	}
}

func TestRepostPostCreatesThreadWithLineage(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	bob := registerAndGetUser(t, c, "bob", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID: "campus", Name: "Campus", Description: "Shared campus notes",
	})
	source := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Original article", Body: "source body",
	})
	sourcePosts, err := c.ListPosts(source.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("expected source root post, got %+v", sourcePosts)
	}

	repost := exec(t, c, bob, proto.CmdRepostPost, proto.RepostPostPayload{
		Post: sourcePosts[0].ID, Board: "campus", Title: "Shared original article",
	})
	repostedPosts, err := c.ListPosts(repost.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(repostedPosts) != 1 {
		t.Fatalf("expected repost root post, got %+v", repostedPosts)
	}
	got := repostedPosts[0]
	if got.Author != "bob" || got.SourcePost != sourcePosts[0].ID || got.SourceThread != source.ID || got.SourceBoard != "general" || got.SourceAuthor != "alice" || got.SourceTitle != "Original article" {
		t.Fatalf("expected repost lineage, got %+v", got)
	}
	if !strings.Contains(got.Body, "source body") || !strings.Contains(got.Body, "Original post: "+sourcePosts[0].ID) {
		t.Fatalf("expected repost body to include source article context, got %q", got.Body)
	}

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID: "secret", Name: "Secret", Description: "Members only",
	})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board: "secret", MemberReadMode: boolPtr(true),
	})
	secret := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "secret", Title: "Private article", Body: "hidden source",
	})
	secretPosts, err := c.ListPosts(secret.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(secretPosts) != 1 {
		t.Fatalf("expected private source root post, got %+v", secretPosts)
	}
	execExpectErr(t, c, bob, proto.CmdRepostPost, proto.RepostPostPayload{
		Post: secretPosts[0].ID, Board: "campus",
	}, proto.ErrForbidden)

	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatal(err)
	}
	repostedPosts, err = c.ListPosts(repost.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(repostedPosts) != 1 || repostedPosts[0].SourcePost != sourcePosts[0].ID || repostedPosts[0].SourceBoard != "general" {
		t.Fatalf("expected rebuild to preserve repost lineage, got %+v", repostedPosts)
	}
}

func TestBoardPostingSanctionsCreateDenyPostRecords(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:   "policy",
		Name: "Policy",
	})
	base := exec(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "policy",
		Title: "Board rules",
		Body:  "Please keep this board tidy.",
	})
	exec(t, c, admin, proto.CmdSanctionUser, proto.SanctionUserPayload{
		User:   alice.ID,
		Kind:   "mute",
		Scope:  "policy",
		Reason: "cooldown",
	})
	execExpectErr(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID,
		Body:   "I should be muted",
	}, proto.ErrMuted)

	denyThreads, err := c.ListThreads("denypost", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(denyThreads) != 1 || !strings.Contains(denyThreads[0].Title, "Board posting denied: alice on policy") {
		t.Fatalf("expected denypost generated thread, got %+v", denyThreads)
	}
	denyPosts, err := c.ListPosts(denyThreads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(denyPosts) != 1 {
		t.Fatalf("expected one denypost generated post, got %+v", denyPosts)
	}
	for _, want := range []string{"# Board posting denied", "- Action: board posting denied", "- User: alice", "- Board: Policy (policy)", "- Kind: mute", "- Actor: admin", "- Reason: cooldown"} {
		if !strings.Contains(denyPosts[0].Body, want) {
			t.Fatalf("expected denypost body to contain %q, got:\n%s", want, denyPosts[0].Body)
		}
	}

	exec(t, c, admin, proto.CmdClearUserSanction, proto.ClearUserSanctionPayload{
		User:   alice.ID,
		Kind:   "mute",
		Scope:  "policy",
		Reason: "served",
	})
	exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID,
		Body:   "I can post again",
	})

	undenyThreads, err := c.ListThreads("undenypost", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(undenyThreads) != 1 || !strings.Contains(undenyThreads[0].Title, "Board posting restored: alice on policy") {
		t.Fatalf("expected undenypost generated thread, got %+v", undenyThreads)
	}
	undenyPosts, err := c.ListPosts(undenyThreads[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(undenyPosts) != 1 {
		t.Fatalf("expected one undenypost generated post, got %+v", undenyPosts)
	}
	for _, want := range []string{"# Board posting restored", "- Action: board posting restored", "- User: alice", "- Board: Policy (policy)", "- Kind: mute", "- Actor: admin", "- Reason: served"} {
		if !strings.Contains(undenyPosts[0].Body, want) {
			t.Fatalf("expected undenypost body to contain %q, got:\n%s", want, undenyPosts[0].Body)
		}
	}
	if sanctions := loadSanctionsForTest(t, c); len(sanctions) != 0 {
		t.Fatalf("expected sanction clear to remove active sanctions, got %+v", sanctions)
	}

	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:   "secret",
		Name: "Secret",
	})
	exec(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secret",
		MemberReadMode: boolPtr(true),
	})
	exec(t, c, admin, proto.CmdSanctionUser, proto.SanctionUserPayload{
		User:   alice.ID,
		Kind:   "mute",
		Scope:  "secret",
		Reason: "private board",
	})
	exec(t, c, admin, proto.CmdClearUserSanction, proto.ClearUserSanctionPayload{
		User:  alice.ID,
		Kind:  "mute",
		Scope: "secret",
	})
	denyThreads, err = c.ListThreads("denypost", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(denyThreads) != 1 {
		t.Fatalf("expected member-read sanction not to create extra denypost records, got %+v", denyThreads)
	}
	undenyThreads, err = c.ListThreads("undenypost", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(undenyThreads) != 1 {
		t.Fatalf("expected member-read clear not to create extra undenypost records, got %+v", undenyThreads)
	}

	clearProjectionTablesForTest(t, c)
	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	if sanctions := loadSanctionsForTest(t, c); len(sanctions) != 0 {
		t.Fatalf("expected sanction clear to replay, got %+v", sanctions)
	}
	rebuiltDenyThreads, err := c.ListThreads("denypost", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuiltDenyThreads) != 1 {
		t.Fatalf("expected denypost generated record after rebuild, got %+v", rebuiltDenyThreads)
	}
	rebuiltUndenyThreads, err := c.ListThreads("undenypost", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuiltUndenyThreads) != 1 {
		t.Fatalf("expected undenypost generated record after rebuild, got %+v", rebuiltUndenyThreads)
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

func TestPollCreationOnReplyWithMissingCloseTagLeavesBodyIntact(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Base", Body: "hello",
	})
	replyBody := "before\n[poll]\nQuestion?\nOption A\nOption B\nafter"
	replyRes := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID,
		Body:   replyBody,
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
		t.Fatal("append post response not found in thread")
	}
	if reply.Body != replyBody {
		t.Fatalf("expected missing-close reply poll body to remain intact, got %q", reply.Body)
	}
	poll, err := c.GetPollByPostID(reply.ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll != nil {
		t.Fatal("expected no poll for missing close tag reply")
	}
}

func TestPollCreationOnReplySupportsUppercasePollTags(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Base", Body: "hello",
	})
	replyRes := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID,
		Body:   "[POLL]\nQuestion?\nOption A\nOption B\n[/POLL]",
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
		t.Fatal("expected poll row for uppercase poll reply")
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
	if len(fullPoll.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(fullPoll.Options))
	}
}

func TestPollCreationOnReplyPreservesBodyAroundMarkup(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Base", Body: "hello",
	})
	replyBody := "before line\n[poll]\nQuestion?\nOption A\nOption B\n[/poll]\nafter line"
	replyRes := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID,
		Body:   replyBody,
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
		t.Fatal("append post response not found in thread")
	}
	if strings.TrimSpace(reply.Body) != "before line\nafter line" {
		t.Fatalf("expected surrounding text to remain, got %q", reply.Body)
	}

	poll, err := c.GetPollByPostID(reply.ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatal("expected poll row for reply body with surrounding text")
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
	if len(fullPoll.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(fullPoll.Options))
	}
}

func TestPollCreationOnReplyRejectsInvalidExpires(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Base", Body: "hello",
	})
	replyBody := "[poll expires=badformat]\nQuestion?\nOption A\nOption B\n[/poll]"
	replyRes := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID,
		Body:   replyBody,
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
		t.Fatal("append post response not found in thread")
	}
	if reply.Body != replyBody {
		t.Fatalf("expected malformed expiry reply poll to stay intact, got %q", reply.Body)
	}

	poll, err := c.GetPollByPostID(reply.ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll != nil {
		t.Fatal("expected no poll for malformed expiry reply post")
	}
}

func TestPollCreationOnReplyRequiresQuestionText(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Base for missing question", Body: "hello",
	})
	replyBody := "[poll]\n- Option A\n- Option B\n[/poll]"
	replyRes := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID,
		Body:   replyBody,
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
		t.Fatal("append post response not found in thread")
	}
	if reply.Body != replyBody {
		t.Fatalf("expected malformed question reply poll to remain intact, got %q", reply.Body)
	}
	poll, err := c.GetPollByPostID(reply.ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll != nil {
		t.Fatal("expected no poll for malformed question reply post")
	}
}

func TestPollCreationWithMultiplePollBlocksUsesFirstBlock(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "intro\n[poll]\nFirst question?\nOption A\nOption B\n[/poll]\nbetween\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]\nafter"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Thread with duplicate poll markup", Body: threadBody,
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post in thread, got %d", len(posts))
	}

	expectedBody := "intro\nbetween\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]\nafter"
	if posts[0].Body != expectedBody {
		t.Fatalf("expected first poll to be stripped and second preserved, got %q", posts[0].Body)
	}

	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatal("expected poll for first block only")
	}
	if poll.Question != "First question?" {
		t.Fatalf("expected first poll question %q, got %q", "First question?", poll.Question)
	}
}

func TestPollCreationWithMalformedFirstPollLeavesLaterPollUntouched(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadBody := "[poll expires=badtime]\nFirst question?\nOption A\nOption B\n[/poll]\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]"
	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Thread with malformed first poll", Body: threadBody,
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post in thread, got %d", len(posts))
	}
	if posts[0].Body != threadBody {
		t.Fatalf("expected malformed first poll to keep body intact, got %q", posts[0].Body)
	}

	poll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll != nil {
		t.Fatal("expected no poll when first poll block is malformed")
	}
}

func TestPollCreationOnReplyWithMultiplePollBlocksUsesFirstBlock(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Base", Body: "hello",
	})
	replyBody := "reply intro\n[poll]\nFirst question?\nOption A\nOption B\n[/poll]\nafter poll\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]"
	reply := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID,
		Body:   replyBody,
	})

	posts, err := c.ListPosts(base.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts in thread, got %d", len(posts))
	}

	var replyPost *core.Post
	for i := range posts {
		if posts[i].ID == reply.ID {
			replyPost = &posts[i]
			break
		}
	}
	if replyPost == nil {
		t.Fatal("reply post not found in thread")
	}

	expectedBody := "reply intro\nafter poll\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]"
	if replyPost.Body != expectedBody {
		t.Fatalf("expected first reply poll to be stripped and second preserved, got %q", replyPost.Body)
	}

	poll, err := c.GetPollByPostID(reply.ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll == nil {
		t.Fatal("expected poll for reply post")
	}
	if poll.Question != "First question?" {
		t.Fatalf("expected first poll question %q, got %q", "First question?", poll.Question)
	}
}

func TestPollCreationOnReplyWithMalformedFirstPollLeavesLaterPollUntouched(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Base", Body: "hello",
	})
	replyBody := "[poll expires=badtime]\nFirst question?\nOption A\nOption B\n[/poll]\n[poll]\nSecond question?\nOption C\nOption D\n[/poll]"
	reply := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: base.ID,
		Body:   replyBody,
	})

	posts, err := c.ListPosts(base.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts in thread, got %d", len(posts))
	}

	var replyPost *core.Post
	for i := range posts {
		if posts[i].ID == reply.ID {
			replyPost = &posts[i]
			break
		}
	}
	if replyPost == nil {
		t.Fatal("reply post not found in thread")
	}
	if replyPost.Body != replyBody {
		t.Fatalf("expected malformed reply poll body to stay intact, got %q", replyPost.Body)
	}

	poll, err := c.GetPollByPostID(replyPost.ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll != nil {
		t.Fatal("expected no poll when first reply poll block is malformed")
	}
}

func TestPollCreationInsertsIntoPollsForPostsMap(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()

	alice := registerAndGetUser(t, c, "alice", "pw")
	setTrustLevel(t, c, alice.ID, 2)

	threadRes := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general", Title: "Poll map base", Body: "first post",
	})
	replyPoll := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadRes.ID,
		Body:   "[poll]\nQuestion?\nOption A\nOption B\n[/poll]",
	})
	replyNoPoll := exec(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread:  threadRes.ID,
		Body:    "plain post",
		ReplyTo: replyPoll.ID,
	})

	posts, err := c.ListPosts(threadRes.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 3 {
		t.Fatalf("expected 3 posts, got %d", len(posts))
	}

	postIDs := make([]string, 0, len(posts))
	for _, p := range posts {
		postIDs = append(postIDs, p.ID)
	}

	pollsByPost, err := c.PollsForPosts(postIDs, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pollsByPost) != 1 {
		t.Fatalf("expected 1 poll in map, got %d", len(pollsByPost))
	}
	if _, ok := pollsByPost[replyNoPoll.ID]; ok {
		t.Fatalf("did not expect map entry for non-poll post %s", replyNoPoll.ID)
	}
	if _, ok := pollsByPost[replyPoll.ID]; !ok {
		t.Fatalf("expected map entry for poll post %s", replyPoll.ID)
	}
	if pollsByPost[replyPoll.ID].Question != "Question?" {
		t.Fatalf("expected question %q, got %q", "Question?", pollsByPost[replyPoll.ID].Question)
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
		"direct_messages",
		"mail_copies",
		"mail_messages",
		"posts",
		"threads",
		"poll_votes",
		"poll_options",
		"polls",
		"post_reactions",
		"moderation_reviews",
		"content_filters",
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
	exec(t, c, admin, proto.CmdSetThreadTitle, proto.SetThreadTitlePayload{
		Thread: threadID,
		Title:  "Replay title",
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
	if beforeSnapshot.thread.Title != afterSnapshot.thread.Title {
		t.Fatalf("thread title changed by rebuild: %q vs %q", beforeSnapshot.thread.Title, afterSnapshot.thread.Title)
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
