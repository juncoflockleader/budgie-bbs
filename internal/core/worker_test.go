package core

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// TestLeaderElection verifies that, among multiple worker nodes sharing one
// Postgres, exactly one holds leadership at a time and that leadership fails
// over when the current leader goes away. Postgres-only (the leader lock is a
// pg_advisory_lock); skipped on SQLite.
func TestLeaderElection(t *testing.T) {
	baseDSN := os.Getenv("BUDGIE_TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Skip("set BUDGIE_TEST_POSTGRES_DSN to run the leader-election test")
	}

	dsn := withSchema(t, baseDSN, "budgie_leader_test")

	// Speed up the election so the test is quick.
	old := leaderPollInterval
	leaderPollInterval = 150 * time.Millisecond
	t.Cleanup(func() { leaderPollInterval = old })

	a, err := NewPostgres(dsn)
	if err != nil {
		t.Fatalf("core A: %v", err)
	}
	t.Cleanup(func() { _ = a.DB.Close() })
	b, err := NewPostgres(dsn)
	if err != nil {
		t.Fatalf("core B: %v", err)
	}
	t.Cleanup(func() { _ = b.DB.Close() })

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()

	a.StartBackgroundWorker(ctxA, false)
	b.StartBackgroundWorker(ctxB, false)

	// Exactly one becomes leader.
	if !waitFor(2*time.Second, func() bool { return a.IsBackgroundLeader() != b.IsBackgroundLeader() }) {
		t.Fatalf("expected exactly one leader, got a=%v b=%v", a.IsBackgroundLeader(), b.IsBackgroundLeader())
	}
	if a.IsBackgroundLeader() && b.IsBackgroundLeader() {
		t.Fatal("both nodes claimed leadership")
	}

	// Remove the current leader; the follower must take over.
	var cancelLeader context.CancelFunc
	var follower *Core
	if a.IsBackgroundLeader() {
		cancelLeader, follower = cancelA, b
	} else {
		cancelLeader, follower = cancelB, a
	}
	cancelLeader()

	if !waitFor(3*time.Second, follower.IsBackgroundLeader) {
		t.Fatal("follower did not take over leadership after the leader stepped down")
	}
}

func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// withSchema provisions a unique Postgres schema and returns a DSN bound to it
// via search_path; the schema is dropped on cleanup.
func withSchema(t *testing.T, baseDSN, prefix string) string {
	t.Helper()
	schema := fmt.Sprintf("%s_%d", prefix, os.Getpid())

	admin, err := OpenPostgres(baseDSN)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	if _, err := admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE"); err != nil {
		admin.Close()
		t.Fatalf("reset schema: %v", err)
	}
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		admin.Close()
	})

	sep := "?"
	if strings.Contains(baseDSN, "?") {
		sep = "&"
	}
	return baseDSN + sep + "search_path=" + schema
}
