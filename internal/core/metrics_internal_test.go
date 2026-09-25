package core

import (
	"path/filepath"
	"testing"
)

func TestOutboxStatusCounts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "outbox.db")
	c, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()

	insert := func(id, status string) {
		_, err := c.DB.Exec(
			`INSERT INTO outbox_jobs (id, kind, payload, status, attempts, next_run_at, created_at, updated_at)
			 VALUES (?, 'test', '{}', ?, 0, 0, 0, 0)`,
			id, status,
		)
		if err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("j1", "pending")
	insert("j2", "pending")
	insert("j3", "dead")

	counts, err := outboxStatusCounts(c.DB)
	if err != nil {
		t.Fatalf("outboxStatusCounts: %v", err)
	}
	if counts["pending"] != 2 {
		t.Errorf("pending: got %d, want 2", counts["pending"])
	}
	if counts["dead"] != 1 {
		t.Errorf("dead: got %d, want 1", counts["dead"])
	}
	if counts["running"] != 0 {
		t.Errorf("running: got %d, want 0", counts["running"])
	}
}
