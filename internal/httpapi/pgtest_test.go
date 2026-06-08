package httpapi_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	_ "github.com/lib/pq"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
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

var pgHTTPSchemaSeq atomic.Int64

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
	t.Cleanup(func() {
		cancel()
		_ = c.DB.Close()
	})
	return c
}

func newHTTPTestCorePostgres(t *testing.T, baseDSN string) *core.Core {
	t.Helper()
	schema := fmt.Sprintf("budgie_http_test_%d_%d", os.Getpid(), pgHTTPSchemaSeq.Add(1))

	admin, err := sql.Open("postgres", baseDSN)
	if err != nil {
		t.Fatalf("open admin postgres: %v", err)
	}
	if _, err := admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE"); err != nil {
		admin.Close()
		t.Fatalf("reset schema: %v", err)
	}
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	sep := "?"
	if strings.Contains(baseDSN, "?") {
		sep = "&"
	}
	c, err := core.NewPostgres(baseDSN + sep + "search_path=" + schema)
	if err != nil {
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		admin.Close()
		t.Fatalf("new postgres core: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	t.Cleanup(func() {
		cancel()
		if c.DB != nil {
			_ = c.DB.Close()
		}
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		admin.Close()
	})
	return c
}
