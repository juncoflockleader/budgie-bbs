package chatstore

import "github.com/juncoflockleader/budgie-bbs/internal/core/projections"

// Store owns high-volume unordered chat history. SQL remains the default
// bounded-history store, but handlers depend on this boundary so live chat can
// move to a broker or KV-backed side store without entering the ordered log.
type Store interface {
	InsertChatLine(id, roomID, roomName, userID, userName, body string, ts int64) error
	ListChatRooms() ([]projections.ChatRoom, error)
	ListChatLines(roomID string, limit int) ([]projections.ChatLine, error)
}
