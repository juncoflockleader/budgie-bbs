package tui

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"net"
	"os"
	"sync"
	"testing"

	charmssh "github.com/charmbracelet/ssh"
	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	gossh "golang.org/x/crypto/ssh"
)

func TestPasswordAuthAuthenticatesUserAndRecordsLogin(t *testing.T) {
	c := newTestCore(t)
	user, err := c.RegisterUser("alice", "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}

	srv := New(c, 0, "")
	ctx := newTestSSHContext("alice", "203.0.113.7:49152")
	if !srv.authPassword(ctx, "correct-horse-battery-staple") {
		t.Fatalf("expected password auth to succeed")
	}

	got, _ := ctx.Value(userKey{}).(*projections.User)
	if got == nil || got.ID != user.ID || got.Name != "alice" {
		t.Fatalf("expected authenticated alice in context, got %+v", got)
	}
	if got := loginCount(t, c, user.ID); got != 1 {
		t.Fatalf("expected ssh password auth to record one login, got %d", got)
	}
}

func TestPasswordAuthRejectsBadCredentials(t *testing.T) {
	c := newTestCore(t)
	user, err := c.RegisterUser("alice", "correct-password")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}

	srv := New(c, 0, "")
	ctx := newTestSSHContext("alice", "203.0.113.7:49152")
	if srv.authPassword(ctx, "wrong-password") {
		t.Fatalf("expected bad password auth to fail")
	}
	if got := ctx.Value(userKey{}); got != nil {
		t.Fatalf("expected no user in context after failed auth, got %+v", got)
	}
	if got := loginCount(t, c, user.ID); got != 0 {
		t.Fatalf("expected failed auth not to record a login, got %d", got)
	}
}

func TestUnknownPubkeyDefersToPasswordForNamedUsers(t *testing.T) {
	c := newTestCore(t)
	srv := New(c, 0, "")
	key := newTestPublicKey(t)

	ctx := newTestSSHContext("alice", "203.0.113.7:49152")
	if srv.authPublicKey(ctx, key) {
		t.Fatalf("expected unknown key for named user to be rejected so password auth can run")
	}
	if got := ctx.Value(userKey{}); got != nil {
		t.Fatalf("expected no user in context after unknown named-user key, got %+v", got)
	}
}

func TestUnknownPubkeyStillAllowsExplicitGuest(t *testing.T) {
	c := newTestCore(t)
	srv := New(c, 0, "")
	key := newTestPublicKey(t)

	ctx := newTestSSHContext("guest", "203.0.113.7:49152")
	if !srv.authPublicKey(ctx, key) {
		t.Fatalf("expected unknown key for guest user to be accepted")
	}
	got, _ := ctx.Value(userKey{}).(*projections.User)
	if got == nil || got.ID != "guest" || got.Name != "guest" {
		t.Fatalf("expected guest in context, got %+v", got)
	}
}

func TestPubkeyAuthRejectsDeactivatedAccount(t *testing.T) {
	c := newTestCore(t)
	user, err := c.RegisterUser("carol", "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	key := newTestPublicKey(t)
	fp := gossh.FingerprintSHA256(key)
	if err := c.AddPubkey(user.ID, fp); err != nil {
		t.Fatalf("add pubkey: %v", err)
	}

	srv := New(c, 0, "")

	// Sanity: the key authenticates while the account is active.
	ctx := newTestSSHContext("carol", "203.0.113.7:49152")
	if !srv.authPublicKey(ctx, key) {
		t.Fatalf("expected pubkey auth to succeed for an active account")
	}

	// Deactivating the account must make the same key stop working — otherwise a
	// registered key bypasses the deactivation gate the password path enforces.
	if err := c.DeactivateAccount(user.ID, "correct-horse-battery-staple", "leaving"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	ctx2 := newTestSSHContext("carol", "203.0.113.7:49152")
	if srv.authPublicKey(ctx2, key) {
		t.Fatalf("expected pubkey auth to be rejected for a deactivated account")
	}
	if got := ctx2.Value(userKey{}); got != nil {
		t.Fatalf("expected no user in context after rejected pubkey auth, got %+v", got)
	}
}

func newTestCore(t *testing.T) *core.Core {
	t.Helper()
	f, err := os.CreateTemp("", "budgie_tui_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	c, err := core.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.DB.Close() })
	return c
}

func loginCount(t *testing.T, c *core.Core, userID string) int {
	t.Helper()
	var count int
	err := c.DB.QueryRow(`SELECT login_count FROM user_activity WHERE user_id=?`, userID).Scan(&count)
	if err == sql.ErrNoRows {
		return 0
	}
	if err != nil {
		t.Fatalf("query login count: %v", err)
	}
	return count
}

func newTestPublicKey(t *testing.T) gossh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type testSSHContext struct {
	context.Context
	sync.Mutex

	user   string
	remote net.Addr
	values map[any]any
}

func newTestSSHContext(user, remote string) *testSSHContext {
	return &testSSHContext{
		Context: context.Background(),
		user:    user,
		remote:  testAddr(remote),
		values:  make(map[any]any),
	}
}

func (c *testSSHContext) User() string { return c.user }

func (c *testSSHContext) SessionID() string { return "test-session" }

func (c *testSSHContext) ClientVersion() string { return "SSH-2.0-test-client" }

func (c *testSSHContext) ServerVersion() string { return "SSH-2.0-test-server" }

func (c *testSSHContext) RemoteAddr() net.Addr { return c.remote }

func (c *testSSHContext) LocalAddr() net.Addr { return testAddr("127.0.0.1:2222") }

func (c *testSSHContext) Permissions() *charmssh.Permissions { return &charmssh.Permissions{} }

func (c *testSSHContext) SetValue(key, value any) { c.values[key] = value }

func (c *testSSHContext) Value(key any) any {
	if value, ok := c.values[key]; ok {
		return value
	}
	return c.Context.Value(key)
}

type testAddr string

func (a testAddr) Network() string { return "tcp" }

func (a testAddr) String() string { return string(a) }
