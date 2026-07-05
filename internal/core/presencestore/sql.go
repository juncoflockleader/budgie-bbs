package presencestore

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

func ChatOnlineCounts(db *sql.DB, cutoff int64) (map[string]int, error) {
	rows, err := sqlstore.Query(db,
		`SELECT location_label, COUNT(DISTINCT user_id)
		   FROM user_presence_sessions
		  WHERE last_seen >= ?
		    AND LOWER(status) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')
		    AND LOWER(mode)='chat'
		    AND location_label<>''
		  GROUP BY location_label`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var roomID string
		var count int
		if err := rows.Scan(&roomID, &count); err != nil {
			return nil, err
		}
		counts[roomID] = count
	}
	return counts, rows.Err()
}

func OnlineStats(db *sql.DB, cutoff int64) (Stats, error) {
	stats := Stats{}
	err := sqlstore.QueryRow(db,
		`SELECT
		    (SELECT COUNT(DISTINCT user_id) FROM user_presence_sessions WHERE last_seen >= ? AND LOWER(status) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')),
		    (SELECT COUNT(*) FROM guest_presence_sessions WHERE last_seen >= ? AND LOWER(status) NOT IN ('offline', 'inactive')),
		    COALESCE((SELECT total_guest_logins FROM community_counter_totals WHERE id='default'), 0),
		    COALESCE((SELECT total_guest_logouts FROM community_counter_totals WHERE id='default'), 0)`,
		cutoff,
		cutoff,
	).Scan(&stats.OnlineUsers, &stats.OnlineGuests, &stats.TotalGuestLogins, &stats.TotalGuestLogouts)
	return stats, err
}
