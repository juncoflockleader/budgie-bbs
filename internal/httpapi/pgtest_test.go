package httpapi_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
)

// testRebind converts ?-placeholders to $N when the suite runs against
// Postgres, so HTTP test helpers that touch c.DB directly stay backend-agnostic.
func testRebind(query string) string {
	if core.SQLFlavor() != core.PostgresFlavor() {
		return query
	}
	var b strings.Builder
	n := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			b.WriteByte('$')
			b.WriteString(fmt.Sprintf("%d", n))
			n++
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

// newHTTPTestCore returns a running Core for HTTP tests. By default it uses a
// temporary SQLite database; if BUDGIE_TEST_POSTGRES_DSN is set it provisions a
// unique Postgres schema per test (full isolation), so the HTTP suite doubles
// as a Postgres integration test alongside the core suite.
func newHTTPTestCore(t *testing.T) *core.Core {
	t.Helper()
	if dsn := os.Getenv("BUDGIE_TEST_POSTGRES_DSN"); dsn != "" {
		return newHTTPTestCorePostgres(t, dsn)
	}

	f, err := os.CreateTemp("", "budgie-httpapi-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	dbPath := f.Name()
	f.Close()
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	c, err := core.New(dbPath)
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	c.StartOutboxWorker(ctx)
	t.Cleanup(func() {
		cancel()
		_ = c.DB.Close()
	})
	return c
}

func newHTTPTestCorePostgres(t *testing.T, baseDSN string) *core.Core {
	t.Helper()

	admin, err := core.OpenPostgres(baseDSN)
	if err != nil {
		t.Fatalf("open admin postgres: %v", err)
	}
	schema, cleanup, err := runconfig.PreparePostgresSchema(context.Background(), admin, "", "budgie_http_test", false, t.Logf)
	if err != nil {
		admin.Close()
		t.Fatalf("prepare schema: %v", err)
	}

	c, err := core.NewPostgres(runconfig.WithSearchPath(baseDSN, schema))
	if err != nil {
		cleanup()
		admin.Close()
		t.Fatalf("new postgres core: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	c.StartOutboxWorker(ctx)
	t.Cleanup(func() {
		cancel()
		if c.DB != nil {
			_ = c.DB.Close()
		}
		cleanup()
		admin.Close()
	})
	return c
}

func drainHTTPOutbox(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var pending int
		if err := db.QueryRow(testRebind(`SELECT COUNT(*) FROM outbox_jobs WHERE status IN ('pending', 'running')`)).Scan(&pending); err != nil {
			t.Fatalf("drain outbox: %v", err)
		}
		if pending == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("drain outbox: timed out waiting for jobs")
}
