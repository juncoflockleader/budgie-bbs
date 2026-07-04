package core

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type sqlPresenceStore struct {
	db *sql.DB
}

type presenceStoreDBBinder interface {
	BindPresenceStoreDB(db *sql.DB)
}

func bindPresenceStoreDB(store PresenceStore, db *sql.DB) {
	if binder, ok := store.(presenceStoreDBBinder); ok {
		binder.BindPresenceStoreDB(db)
	}
}

func (s sqlPresenceStore) SetUserPresence(userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64) error {
	return setUserPresence(s.db, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost, ts)
}

func (s sqlPresenceStore) SetGuestPresence(sessionID, status, locationLabel, fromHost string, ts int64) error {
	return setGuestPresence(s.db, sessionID, status, locationLabel, fromHost, ts)
}

func (s sqlPresenceStore) ListOnlineUsers(viewerID, boardID string, limit, offset int) ([]SocialUser, error) {
	return projections.ListOnlineUsers(s.db, viewerID, boardID, limit, offset)
}

func (s sqlPresenceStore) ListChatOnlineUsers(viewerID, roomID string, limit, offset int) ([]SocialUser, error) {
	return projections.ListChatOnlineUsers(s.db, viewerID, roomID, limit, offset)
}

func (s sqlPresenceStore) ChatOnlineCounts() (map[string]int, error) {
	cutoff := nowMS() - 5*60*1000
	rows, err := qQuery(s.db,
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

func (s sqlPresenceStore) Stats() (PresenceStats, error) {
	cutoff := nowMS() - 5*60*1000
	stats := PresenceStats{}
	err := qQueryRow(s.db,
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
