package core

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/presencestore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type sqlPresenceStore struct {
	db *sql.DB
}

type presenceStoreDBBinder interface {
	BindPresenceStoreDB(db *sql.DB)
}

func bindPresenceStoreDB(store presencestore.Store, db *sql.DB) {
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

func (s sqlPresenceStore) ListOnlineUsers(viewerID, boardID string, limit, offset int) ([]projections.SocialUser, error) {
	return projections.ListOnlineUsers(s.db, viewerID, boardID, limit, offset)
}

func (s sqlPresenceStore) ListChatOnlineUsers(viewerID, roomID string, limit, offset int) ([]projections.SocialUser, error) {
	return projections.ListChatOnlineUsers(s.db, viewerID, roomID, limit, offset)
}

func (s sqlPresenceStore) ChatOnlineCounts() (map[string]int, error) {
	return presencestore.ChatOnlineCounts(s.db, nowMS()-5*60*1000)
}

func (s sqlPresenceStore) Stats() (presencestore.Stats, error) {
	return presencestore.OnlineStats(s.db, nowMS()-5*60*1000)
}
