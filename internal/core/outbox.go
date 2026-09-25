package core

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const outboxPostCommitted = "post.committed"
const outboxMaxAttempts = 16

type outboxJob struct {
	ID       string
	Kind     string
	Payload  string
	Attempts int
}

type postCommittedJob struct {
	ActorID   string `json:"actorId"`
	ActorName string `json:"actorName"`
	PostID    string `json:"postId"`
	ThreadID  string `json:"threadId"`
	BoardID   string `json:"boardId"`
	Body      string `json:"body"`
	ReplyTo   string `json:"replyTo,omitempty"`
	TS        int64  `json:"ts"`
	Seq       int64  `json:"seq"`
}

func enqueueOutboxJob(tx *sql.Tx, kind string, payload any, ts int64) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = qExec(tx,
		`INSERT INTO outbox_jobs (id, kind, payload, status, attempts, next_run_at, created_at, updated_at)
		 VALUES (?, ?, ?, 'pending', 0, ?, ?, ?)`,
		newID("job_"), kind, string(raw), ts, ts, ts,
	)
	return err
}

// outboxStatusCounts returns the number of outbox jobs grouped by status
// (pending, running, done, dead). Used by the metrics collector.
func outboxStatusCounts(db *sql.DB) (map[string]int64, error) {
	rows, err := qQuery(db, `SELECT status, COUNT(*) FROM outbox_jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

func runOutboxWorker(ctx context.Context, db *sql.DB, bus Bus) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for {
				job, err := claimOutboxJob(db)
				if err != nil || job == nil {
					break
				}
				if err := processOutboxJob(db, bus, job); err != nil {
					_ = failOutboxJob(db, job.ID, err)
					continue
				}
				_ = completeOutboxJob(db, job.ID)
			}
		}
	}
}

func claimOutboxJob(db *sql.DB) (*outboxJob, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	now := nowMS()
	j := &outboxJob{}
	err = qQueryRow(tx,
		`SELECT id, kind, payload, attempts FROM outbox_jobs
		 WHERE status='pending' AND next_run_at <= ?
		 ORDER BY created_at LIMIT 1`,
		now,
	).Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.Attempts++
	res, err := qExec(tx,
		`UPDATE outbox_jobs SET status='running', attempts=attempts+1, updated_at=?
		 WHERE id=? AND status='pending'`,
		now, j.ID,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return j, nil
}

func completeOutboxJob(db *sql.DB, id string) error {
	_, err := qExec(db, `UPDATE outbox_jobs SET status='done', updated_at=? WHERE id=?`, nowMS(), id)
	return err
}

func failOutboxJob(db *sql.DB, id string, cause error) error {
	backoff := int64(5 * time.Second / time.Millisecond)
	now := nowMS()
	var attempts int
	err := qQueryRow(db, `SELECT attempts FROM outbox_jobs WHERE id=?`, id).Scan(&attempts)
	if err != nil {
		return err
	}
	status := "pending"
	nextRun := now + backoff
	if attempts >= outboxMaxAttempts {
		status = "dead"
		nextRun = now
	}
	_, err = qExec(db,
		`UPDATE outbox_jobs
		 SET status=?, next_run_at=?, last_error=?, updated_at=?
		 WHERE id=?`,
		status, nextRun, cause.Error(), now, id,
	)
	return err
}

func deterministicID(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "|")))
	return "det_" + hex.EncodeToString(h[:8])
}

func notificationID(postID, threadID, actorID, userID, kind string) string {
	return deterministicID("notification", postID, threadID, actorID, userID, kind)
}

func processOutboxJob(db *sql.DB, bus Bus, job *outboxJob) error {
	switch job.Kind {
	case outboxPostCommitted:
		var payload postCommittedJob
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return err
		}
		return processPostCommittedJob(db, bus, payload)
	default:
		return fmt.Errorf("unknown outbox job kind %q", job.Kind)
	}
}

func processPostCommittedJob(db *sql.DB, bus Bus, p postCommittedJob) error {
	oldTL, newTL, err := recordPostCreated(db, p.ActorID)
	if err != nil {
		return err
	}
	if newTL != oldTL {
		if err := appendTrustLevelChanged(db, bus, p.ActorID, p.ActorName, oldTL, newTL, p.TS); err != nil {
			return err
		}
	}

	for _, username := range parseMentions(p.Body) {
		u, err := getUserByName(db, username)
		if err != nil || u == nil || u.ID == p.ActorID {
			continue
		}
		id := notificationID(p.PostID, p.ThreadID, p.ActorID, u.ID, "mention")
		if err := insertNotification(db, id, u.ID, "mention", p.ThreadID, p.PostID, p.ActorName, p.TS); err != nil {
			return err
		}
	}

	if p.ReplyTo != "" {
		parent, err := getPost(db, p.ReplyTo)
		if err != nil {
			return err
		}
		if parent != nil {
			parentAuthorID := parent.AuthorID
			if parentAuthorID == "" {
				parentAuthorID = parent.Author
			}
			if parentAuthorID != p.ActorID {
				id := notificationID(p.PostID, p.ThreadID, p.ActorID, parentAuthorID, "reply")
				if err := insertNotification(db, id, parentAuthorID, "reply", p.ThreadID, p.PostID, p.ActorName, p.TS); err != nil {
					return err
				}
			}
		}
	}

	watchers, err := watchersOfThread(db, p.ThreadID, p.ActorID)
	if err != nil {
		return err
	}
	for _, watcherID := range watchers {
		id := notificationID(p.PostID, p.ThreadID, p.ActorID, watcherID, "watched")
		if err := insertNotification(db, id, watcherID, "watched", p.ThreadID, p.PostID, p.ActorName, p.TS); err != nil {
			return err
		}
	}
	return nil
}

func appendTrustLevelChanged(db *sql.DB, bus Bus, userID, userName string, oldTL, newTL int, ts int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	scopes := []string{"account:" + userID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtTrustLevelChanged, scopes,
		&proto.TrustLevelChangedPayload{User: userID, OldLevel: oldTL, NewLevel: newTL, TS: ts})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	bus.Publish(&proto.Event{Kind: proto.EvtTrustLevelChanged, Seq: seq, Scopes: scopes,
		Payload: &proto.TrustLevelChangedPayload{User: userName, OldLevel: oldTL, NewLevel: newTL, TS: ts}, TS: ts})
	return nil
}
