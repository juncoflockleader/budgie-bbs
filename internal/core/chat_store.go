package core

import "database/sql"

type sqlChatStore struct {
	db *sql.DB
}

func (s sqlChatStore) InsertChatLine(id, roomID, roomName, userID, userName, body string, ts int64) error {
	return insertChatLine(s.db, id, roomID, roomName, userID, userName, body, ts)
}

func (s sqlChatStore) ListChatRooms() ([]ChatRoom, error) {
	return listChatRooms(s.db)
}

func (s sqlChatStore) ListChatLines(roomID string, limit int) ([]ChatLine, error) {
	return listChatLines(s.db, roomID, limit)
}
