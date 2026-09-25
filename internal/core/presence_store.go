package core

import (
	"database/sql"
	"time"

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

func (c *Core) applyPresenceStoreStats(stats *projections.CommunityStats) error {
	if c == nil || stats == nil || !c.presenceStoreOverride || c.presenceStore == nil {
		return nil
	}
	presenceStats, err := c.presenceStore.Stats()
	if err != nil {
		return err
	}
	stats.OnlineUsers = presenceStats.OnlineUsers
	stats.OnlineGuests = presenceStats.OnlineGuests
	stats.TotalGuestLogins = presenceStats.TotalGuestLogins
	stats.TotalGuestLogouts = presenceStats.TotalGuestLogouts
	now := nowMS()
	if stats.OnlineUsers > stats.MaxOnlineUsers {
		stats.MaxOnlineUsers = stats.OnlineUsers
		stats.MaxOnlineAt = now
	}
	if stats.OnlineGuests > stats.MaxOnlineGuests {
		stats.MaxOnlineGuests = stats.OnlineGuests
		stats.MaxOnlineGuestsAt = now
	}
	return nil
}

func (c *Core) SetGuestPresence(sessionID, status, locationLabel, fromHost string, at time.Time) error {
	ts := at.UTC().UnixMilli()
	if at.IsZero() {
		ts = time.Now().UTC().UnixMilli()
	}
	if c != nil && c.presenceStoreOverride && c.presenceStore != nil {
		return c.presenceStore.SetGuestPresence(sessionID, status, locationLabel, fromHost, ts)
	}
	return setGuestPresence(c.DB, sessionID, status, locationLabel, fromHost, ts)
}

func (c *Core) ListOnlineUsers(viewerID, boardID string, limit, offset int) ([]projections.SocialUser, error) {
	if c != nil && c.presenceStore != nil {
		return c.presenceStore.ListOnlineUsers(viewerID, boardID, limit, offset)
	}
	return projections.ListOnlineUsers(c.DB, viewerID, boardID, limit, offset)
}

func (c *Core) ListChatOnlineUsers(viewerID, roomID string, limit, offset int) ([]projections.SocialUser, error) {
	if c != nil && c.presenceStore != nil {
		return c.presenceStore.ListChatOnlineUsers(viewerID, roomID, limit, offset)
	}
	return projections.ListChatOnlineUsers(c.DB, viewerID, roomID, limit, offset)
}
