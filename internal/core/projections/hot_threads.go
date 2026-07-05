package projections

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
)

func LoadHotThreadSplits(db *sql.DB) (map[string]int, error) {
	rows, err := QQuery(db, `SELECT thread_id, shards FROM hot_thread_splits ORDER BY thread_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var threadID string
		var shards int
		if err := rows.Scan(&threadID, &shards); err != nil {
			return nil, err
		}
		out[threadID] = shards
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return logmodel.NormalizeHotThreadSplits(out), nil
}

func PersistHotThreadSplit(db *sql.DB, threadID string, shards int) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("thread id is required")
	}
	if shards <= 1 {
		_, err := QExec(db, `DELETE FROM hot_thread_splits WHERE thread_id=?`, threadID)
		return err
	}
	_, err := QExec(db, `INSERT INTO hot_thread_splits (thread_id, shards, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(thread_id)
		 DO UPDATE SET shards=excluded.shards, updated_at=excluded.updated_at`,
		threadID, shards, NowMS(),
	)
	return err
}
