package core

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

const memoryChatRoomHistoryLimit = 200

// MemoryChatStore is a test-only non-SQL ChatStore fixture. It keeps bounded
// recent chat history per room, matching the SQL store's 200-line retention
// policy. Production backends are sql and nats-kv (see -chat-store).
type MemoryChatStore struct {
	mu    sync.Mutex
	rooms map[string]projections.ChatRoom
	lines map[string][]projections.ChatLine
}

func NewMemoryChatStore() *MemoryChatStore {
	return &MemoryChatStore{
		rooms: map[string]projections.ChatRoom{
			"lobby": {
				ID:   "lobby",
				Name: "Lobby",
			},
		},
		lines: map[string][]projections.ChatLine{},
	}
}

func (s *MemoryChatStore) InsertChatLine(id, roomID, roomName, userID, userName, body string, ts int64) error {
	if s == nil {
		return sql.ErrConnDone
	}
	id = strings.TrimSpace(id)
	roomID = strings.TrimSpace(roomID)
	roomName = strings.TrimSpace(roomName)
	userID = strings.TrimSpace(userID)
	userName = strings.TrimSpace(userName)
	body = strings.TrimSpace(body)
	if id == "" || roomID == "" || userID == "" || body == "" {
		return fmt.Errorf("chat line id, room, user, and body are required")
	}
	if roomName == "" {
		roomName = roomID
	}
	if ts <= 0 {
		ts = nowMS()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	room := s.rooms[roomID]
	if room.ID == "" {
		room.ID = roomID
		room.Name = roomName
		room.CreatedBy = userID
		room.CreatedAt = ts
	}
	if room.Name == "" {
		room.Name = roomName
	}
	room.UpdatedAt = ts
	s.rooms[roomID] = room

	line := projections.ChatLine{
		ID:        id,
		Room:      roomID,
		UserID:    userID,
		User:      userName,
		Text:      body,
		CreatedAt: ts,
		TS:        ts,
	}
	s.lines[roomID] = append(s.lines[roomID], line)
	s.trimRoomLocked(roomID)
	return nil
}

func (s *MemoryChatStore) ListChatRooms() ([]projections.ChatRoom, error) {
	if s == nil {
		return nil, sql.ErrConnDone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]projections.ChatRoom, 0, len(s.rooms))
	for id, room := range s.rooms {
		room.LineCount = len(s.lines[id])
		out = append(out, room)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == "lobby" || out[j].ID == "lobby" {
			return out[i].ID == "lobby"
		}
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *MemoryChatStore) ListChatLines(roomID string, limit int) ([]projections.ChatLine, error) {
	if s == nil {
		return nil, sql.ErrConnDone
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		roomID = "lobby"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lines := append([]projections.ChatLine(nil), s.lines[roomID]...)
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].CreatedAt != lines[j].CreatedAt {
			return lines[i].CreatedAt > lines[j].CreatedAt
		}
		return lines[i].ID > lines[j].ID
	})
	if limit < len(lines) {
		lines = lines[:limit]
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].CreatedAt != lines[j].CreatedAt {
			return lines[i].CreatedAt < lines[j].CreatedAt
		}
		return lines[i].ID < lines[j].ID
	})
	return lines, nil
}

func (s *MemoryChatStore) trimRoomLocked(roomID string) {
	lines := s.lines[roomID]
	if len(lines) <= memoryChatRoomHistoryLimit {
		return
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].CreatedAt != lines[j].CreatedAt {
			return lines[i].CreatedAt > lines[j].CreatedAt
		}
		return lines[i].ID > lines[j].ID
	})
	lines = lines[:memoryChatRoomHistoryLimit]
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].CreatedAt != lines[j].CreatedAt {
			return lines[i].CreatedAt < lines[j].CreatedAt
		}
		return lines[i].ID < lines[j].ID
	})
	s.lines[roomID] = lines
}
