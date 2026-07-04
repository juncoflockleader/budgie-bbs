package presencestore

import "github.com/juncoflockleader/budgie-bbs/internal/core/projections"

// Store owns high-volume session roster updates. SQL remains the default
// store, but command handlers use this boundary so active/idle presence can
// move to a TTL or broker-backed side store without joining the ordered log.
type Store interface {
	SetUserPresence(userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64) error
	SetGuestPresence(sessionID, status, locationLabel, fromHost string, ts int64) error
	ListOnlineUsers(viewerID, boardID string, limit, offset int) ([]projections.SocialUser, error)
	ListChatOnlineUsers(viewerID, roomID string, limit, offset int) ([]projections.SocialUser, error)
	ChatOnlineCounts() (map[string]int, error)
	Stats() (Stats, error)
}

type Stats struct {
	OnlineUsers       int
	OnlineGuests      int
	TotalGuestLogins  int
	TotalGuestLogouts int
}
