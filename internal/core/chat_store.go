package core

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type sqlChatStore struct {
	db *sql.DB
}

func (s sqlChatStore) InsertChatLine(id, roomID, roomName, userID, userName, body string, ts int64) error {
	return projections.InsertChatLine(s.db, id, roomID, roomName, userID, userName, body, ts)
}

func (s sqlChatStore) ListChatRooms() ([]projections.ChatRoom, error) {
	return projections.ListChatRooms(s.db)
}

func (s sqlChatStore) ListChatLines(roomID string, limit int) ([]projections.ChatLine, error) {
	return projections.ListChatLines(s.db, roomID, limit)
}
