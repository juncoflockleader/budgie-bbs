package core

import (
	"path/filepath"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
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

func TestCommandLogReceiptSamples(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "receipts.db")
	c, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()

	now := nowMS()
	insert := func(partition, cid, status string, updatedAt int64) {
		_, err := c.DB.Exec(
			`INSERT INTO command_log_receipts (
			    partition_kind, partition_key, actor_id, cid, command_offset, status, error_json, updated_at
			) VALUES ('thread', ?, 'usr_test', ?, 1, ?, '{}', ?)`,
			partition, cid, status, updatedAt,
		)
		if err != nil {
			t.Fatalf("insert %s: %v", cid, err)
		}
	}
	insert("thr_a", "cmd_retry_old", CommandStatusRetrying, now-7_500)
	insert("thr_b", "cmd_retry_new", CommandStatusRetrying, now-1_000)
	insert("thr_c", "cmd_failed", CommandStatusFailed, now-3_000)
	insert("thr_d", "cmd_custom", "custom", now-500)
	insert("thr_e", "cmd_applied", CommandStatusApplied, now-250)

	samples, err := commandLogReceiptSamples(c.DB, now)
	if err != nil {
		t.Fatalf("commandLogReceiptSamples: %v", err)
	}

	assertSampleValue(t, samples, "budgie_command_log_receipts", CommandStatusRetrying, 2)
	assertSampleValue(t, samples, "budgie_command_log_receipt_oldest_age_ms", CommandStatusRetrying, 7_500)
	assertSampleValue(t, samples, "budgie_command_log_receipts", CommandStatusFailed, 1)
	assertSampleValue(t, samples, "budgie_command_log_receipt_oldest_age_ms", CommandStatusFailed, 3_000)
	assertSampleValue(t, samples, "budgie_command_log_receipts", "custom", 1)
	assertSampleValue(t, samples, "budgie_command_log_receipt_oldest_age_ms", "custom", 500)
	assertSampleValue(t, samples, "budgie_command_log_receipts", CommandStatusApplied, 1)
	assertSampleValue(t, samples, "budgie_command_log_receipt_oldest_age_ms", CommandStatusApplied, 250)
}

func TestCommandLogReceiptSamplesReportsKnownZeroStatuses(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "receipts-empty.db")
	c, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()

	samples, err := commandLogReceiptSamples(c.DB, 10_000)
	if err != nil {
		t.Fatalf("commandLogReceiptSamples: %v", err)
	}

	assertSampleValue(t, samples, "budgie_command_log_receipts", CommandStatusRetrying, 0)
	assertSampleValue(t, samples, "budgie_command_log_receipt_oldest_age_ms", CommandStatusRetrying, 0)
	assertSampleValue(t, samples, "budgie_command_log_receipts", CommandStatusFailed, 0)
	assertSampleValue(t, samples, "budgie_command_log_receipt_oldest_age_ms", CommandStatusFailed, 0)
	assertSampleValue(t, samples, "budgie_command_log_receipts", CommandStatusApplied, 0)
	assertSampleValue(t, samples, "budgie_command_log_receipt_oldest_age_ms", CommandStatusApplied, 0)
}

func TestAttachmentBlobStagingSamplesAndPrune(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "staged-blobs.db")
	c, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()

	now := nowMS()
	if err := c.StagePostAttachmentBlob("att_fresh", "usr_test", []byte("fresh"), "text/plain"); err != nil {
		t.Fatalf("stage fresh post blob: %v", err)
	}
	if _, err := c.DB.Exec(
		`UPDATE attachment_blob_staging SET created_at=?, expires_at=? WHERE id='att_fresh'`,
		now-1_500, now+60_000,
	); err != nil {
		t.Fatalf("age fresh staged blob: %v", err)
	}
	if err := c.StageMailAttachmentBlob("matt_expired", "usr_test", []byte("expired"), "text/plain"); err != nil {
		t.Fatalf("stage expired mail blob: %v", err)
	}
	if _, err := c.DB.Exec(
		`UPDATE attachment_blob_staging SET created_at=?, expires_at=? WHERE id='matt_expired'`,
		now-5_000, now-1,
	); err != nil {
		t.Fatalf("expire staged mail blob: %v", err)
	}

	samples, err := attachmentBlobStagingSamples(c.DB, now)
	if err != nil {
		t.Fatalf("attachmentBlobStagingSamples: %v", err)
	}
	assertLabeledSampleValue(t, samples, "budgie_attachment_blob_staging_blobs", map[string]string{"kind": "post_attachment", "state": "total"}, 1)
	assertLabeledSampleValue(t, samples, "budgie_attachment_blob_staging_blobs", map[string]string{"kind": "mail_attachment", "state": "expired"}, 1)
	assertLabeledSampleValue(t, samples, "budgie_attachment_blob_staging_bytes", map[string]string{"kind": "mail_attachment", "state": "expired"}, float64(len("expired")))
	assertLabeledSampleValue(t, samples, "budgie_attachment_blob_staging_oldest_age_ms", map[string]string{"kind": "mail_attachment"}, 5_000)

	deleted, err := c.PruneExpiredAttachmentBlobStaging(10)
	if err != nil {
		t.Fatalf("PruneExpiredAttachmentBlobStaging: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	samples, err = attachmentBlobStagingSamples(c.DB, now)
	if err != nil {
		t.Fatalf("attachmentBlobStagingSamples after prune: %v", err)
	}
	assertLabeledSampleValue(t, samples, "budgie_attachment_blob_staging_blobs", map[string]string{"kind": "mail_attachment", "state": "total"}, 0)
	assertLabeledSampleValue(t, samples, "budgie_attachment_blob_staging_blobs", map[string]string{"kind": "post_attachment", "state": "total"}, 1)
}

func assertSampleValue(t *testing.T, samples []metrics.Sample, name, status string, want float64) {
	t.Helper()
	for _, sample := range samples {
		if sample.Name == name && sample.Labels["status"] == status {
			if sample.Value != want {
				t.Fatalf("%s{%s}: got %v, want %v", name, status, sample.Value, want)
			}
			return
		}
	}
	t.Fatalf("missing %s{%s}", name, status)
}

func assertLabeledSampleValue(t *testing.T, samples []metrics.Sample, name string, labels map[string]string, want float64) {
	t.Helper()
	for _, sample := range samples {
		if sample.Name != name || !labelsMatch(sample.Labels, labels) {
			continue
		}
		if sample.Value != float64(want) {
			t.Fatalf("%s%v: got %v, want %v", name, labels, sample.Value, want)
		}
		return
	}
	t.Fatalf("missing %s%v", name, labels)
}

func labelsMatch(got, want map[string]string) bool {
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
