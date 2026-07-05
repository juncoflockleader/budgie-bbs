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

func (c *Core) ListChatRooms() ([]projections.ChatRoom, error) {
	var rooms []projections.ChatRoom
	var err error
	if c != nil && c.chatStore != nil {
		rooms, err = c.chatStore.ListChatRooms()
	} else {
		rooms, err = projections.ListChatRooms(c.DB)
	}
	if err != nil {
		return nil, err
	}
	if c != nil && c.presenceStore != nil {
		counts, err := c.presenceStore.ChatOnlineCounts()
		if err != nil {
			return nil, err
		}
		for i := range rooms {
			rooms[i].OnlineUsers = counts[rooms[i].ID]
		}
	}
	return rooms, nil
}

func (c *Core) ListChatLines(roomID string, limit int) ([]projections.ChatLine, error) {
	if c != nil && c.chatStore != nil {
		return c.chatStore.ListChatLines(roomID, limit)
	}
	return projections.ListChatLines(c.DB, roomID, limit)
}
