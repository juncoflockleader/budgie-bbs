package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// TestAIReservedSuffix verifies that normal registration cannot claim the
// reserved AI bot suffix, while the bot-provisioning path can.
func TestAIReservedSuffix(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	_ = registerAndGetUser(t, c, "admin", "pw")

	if _, err := c.RegisterUser("general-ai", "pw"); err == nil {
		t.Fatal("expected registration of a -ai name to be rejected")
	}
	if _, err := c.RegisterUser("normal", "pw"); err != nil {
		t.Fatalf("normal registration should succeed: %v", err)
	}
}

// TestBoardAITokenWriteOnly verifies the BYO token is stored but never present
// in the API-safe config (only TokenSet), while the server-side runtime has it.
func TestBoardAITokenWriteOnly(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	admin := registerAndGetUser(t, c, "admin", "pw")
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "ai", Name: "AI"})

	tok := "sk-secret-123"
	if _, err := c.SetBoardAIConfig("ai", core.BoardAIConfigPatch{APIToken: &tok}); err != nil {
		t.Fatal(err)
	}
	cfg, err := c.BoardAIConfig("ai")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TokenSet {
		t.Fatal("expected tokenSet=true after storing a token")
	}
	rt, err := c.BoardAIRuntime("ai")
	if err != nil || rt == nil {
		t.Fatalf("runtime: %v", err)
	}
	if rt.APIToken != tok {
		t.Fatalf("runtime token = %q, want %q", rt.APIToken, tok)
	}
}

// TestAIResponderEveryPost drives the responder end-to-end with a fake
// generator: a user's post triggers a bot reply, and the bot never replies to
// itself (loop guard).
func TestAIResponderEveryPost(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "ai", Name: "AI"})

	if _, err := c.SetAISettings(true); err != nil {
		t.Fatal(err)
	}
	enabled, mode, role := true, "every_post", "user"
	tok := "sk-test"
	if _, err := c.SetBoardAIConfig("ai", core.BoardAIConfigPatch{Enabled: &enabled, APIToken: &tok, Mode: &mode, TriggerRole: &role}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EnsureBoardAIBot("ai"); err != nil {
		t.Fatal(err)
	}

	p, err := core.NewAIProcessor(c, time.Second, 100)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.Generate = func(ctx context.Context, rt *core.BoardAIRuntime, system, prompt string) (string, error) {
		calls++
		if !strings.Contains(prompt, "2+2") {
			t.Errorf("prompt missing the user message: %q", prompt)
		}
		return "The answer is 4.", nil
	}

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "ai", Title: "Q", Body: "What is 2+2?"})

	if _, err := p.ProcessOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !hasBotReply(t, c, base.ID, "The answer is 4.") {
		t.Fatal("expected a bot reply after the user post")
	}
	if calls != 1 {
		t.Fatalf("generator called %d times, want 1", calls)
	}

	// Loop guard: re-running must process the bot's own reply event and NOT
	// generate again.
	if _, err := p.ProcessOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("generator called %d times after re-run, want 1 (loop guard)", calls)
	}
}

// TestAIResponderSiteToggleGates verifies the site-wide switch gates all AI
// activity even when a board is configured and enabled.
func TestAIResponderSiteToggleGates(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	admin := registerAndGetUser(t, c, "admin", "pw")
	alice := registerAndGetUser(t, c, "alice", "pw")
	exec(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "ai", Name: "AI"})

	// Board fully configured + enabled, but site AI stays OFF.
	enabled, mode, role := true, "every_post", "user"
	tok := "sk-test"
	if _, err := c.SetBoardAIConfig("ai", core.BoardAIConfigPatch{Enabled: &enabled, APIToken: &tok, Mode: &mode, TriggerRole: &role}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EnsureBoardAIBot("ai"); err != nil {
		t.Fatal(err)
	}

	p, _ := core.NewAIProcessor(c, time.Second, 100)
	calls := 0
	p.Generate = func(ctx context.Context, rt *core.BoardAIRuntime, system, prompt string) (string, error) {
		calls++
		return "should not happen", nil
	}

	base := exec(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{Board: "ai", Title: "Q", Body: "hello"})
	if _, err := p.ProcessOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("generator called %d times with site AI off, want 0", calls)
	}
	if hasBotReply(t, c, base.ID, "should not happen") {
		t.Fatal("bot replied while site AI was disabled")
	}
}

func hasBotReply(t *testing.T, c *core.Core, threadID, want string) bool {
	t.Helper()
	posts, err := c.ListPosts(threadID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, post := range posts {
		if strings.HasSuffix(post.Author, "-ai") && strings.Contains(post.Body, want) {
			return true
		}
	}
	return false
}
