package core

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAsyncCommunityStatHistorySnapshotsUseCoalescedOutbox(t *testing.T) {
	c, err := New(filepath.Join(t.TempDir(), "community-stat-history.db"), WithAsyncCommunityStatHistory())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()

	if _, err := c.RegisterUser("alice", "pw"); err != nil {
		t.Fatalf("register user: %v", err)
	}
	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head == 0 {
		t.Fatal("head = 0, want registration to create durable events")
	}
	applied, err := c.DerivedViewAppliedSeq(DerivedViewCommunityStatHistory)
	if err != nil {
		t.Fatalf("initial stat-history watermark: %v", err)
	}
	if applied != 0 {
		t.Fatalf("initial stat-history watermark = %d, want explicit async lag from 0", applied)
	}

	ts := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	if err := c.SetGuestPresence("guest_a", "active", "web", "203.0.113.10", ts); err != nil {
		t.Fatalf("SetGuestPresence guest_a: %v", err)
	}
	if err := c.SetGuestPresence("guest_b", "active", "web", "203.0.113.11", ts.Add(30*time.Second)); err != nil {
		t.Fatalf("SetGuestPresence guest_b: %v", err)
	}

	var historyRows int
	if err := c.DB.QueryRow(`SELECT COUNT(*) FROM community_stat_history WHERE day='2026-06-11'`).Scan(&historyRows); err != nil {
		t.Fatalf("count history before worker: %v", err)
	}
	if historyRows != 0 {
		t.Fatalf("history rows before worker = %d, want no inline stat-history snapshot", historyRows)
	}
	var queued int
	if err := c.DB.QueryRow(`SELECT COUNT(*) FROM outbox_jobs WHERE kind=? AND status='pending'`, outboxCommunityStatSnapshot).Scan(&queued); err != nil {
		t.Fatalf("count queued stat-history jobs: %v", err)
	}
	if queued != 1 {
		t.Fatalf("queued stat-history jobs = %d, want one coalesced job", queued)
	}

	job, err := claimOutboxJob(c.DB)
	if err != nil {
		t.Fatalf("claim stat-history job: %v", err)
	}
	if job == nil {
		t.Fatal("claim stat-history job returned nil")
	}
	if job.Kind != outboxCommunityStatSnapshot {
		t.Fatalf("claimed job kind = %q, want %q", job.Kind, outboxCommunityStatSnapshot)
	}
	if err := processOutboxJob(c.DB, c.Bus, job); err != nil {
		t.Fatalf("process stat-history job: %v", err)
	}
	if err := completeOutboxJob(c.DB, job.ID); err != nil {
		t.Fatalf("complete stat-history job: %v", err)
	}

	if err := c.DB.QueryRow(`SELECT COUNT(*) FROM community_stat_history WHERE day='2026-06-11'`).Scan(&historyRows); err != nil {
		t.Fatalf("count history after worker: %v", err)
	}
	if historyRows != 1 {
		t.Fatalf("history rows after worker = %d, want one materialized snapshot", historyRows)
	}
	applied, err = c.DerivedViewAppliedSeq(DerivedViewCommunityStatHistory)
	if err != nil {
		t.Fatalf("final stat-history watermark: %v", err)
	}
	if applied != head {
		t.Fatalf("final stat-history watermark = %d, want head %d", applied, head)
	}
}
