package loadtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCoreUsesTempSQLiteWhenPostgresDSNIsEmpty(t *testing.T) {
	c, cleanup, err := OpenCore(context.Background(), CoreConfig{
		SQLiteTempPattern: "budgie-loadtest-open-core-*.db",
	})
	if err != nil {
		t.Fatalf("OpenCore temp sqlite: %v", err)
	}
	defer cleanup()
	defer c.DB.Close()
	if err := c.DB.Ping(); err != nil {
		t.Fatalf("ping temp sqlite core: %v", err)
	}
}

func TestOpenCoreUsesExplicitSQLitePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "load-core.db")
	c, cleanup, err := OpenCore(context.Background(), CoreConfig{
		SQLitePath: path,
	})
	if err != nil {
		t.Fatalf("OpenCore explicit sqlite: %v", err)
	}
	if err := c.DB.Close(); err != nil {
		t.Fatalf("close explicit sqlite core: %v", err)
	}
	cleanup()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("explicit sqlite path should remain after cleanup: %v", err)
	}
}

func TestOpenPostgresCoreRejectsMissingDSN(t *testing.T) {
	_, cleanup, err := OpenPostgresCore(context.Background(), PostgresCoreConfig{})
	defer cleanup()
	requireErrorContains(t, err, "postgres DSN is required")
}

func TestOpenPostgresCoreRejectsInvalidSchemaBeforeDBUse(t *testing.T) {
	_, cleanup, err := OpenPostgresCore(context.Background(), PostgresCoreConfig{
		DSN:    "postgres://example/budgie",
		Schema: "bad-schema",
	})
	defer cleanup()
	requireErrorContains(t, err, "invalid schema")
}

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}
