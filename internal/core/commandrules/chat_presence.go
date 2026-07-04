package commandrules

import (
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type ChatLineInput struct {
	RoomID   string
	RoomName string
	Text     string
}

func NormalizeChatLine(room, text string) (ChatLineInput, *proto.ErrorDetail) {
	roomID := NormalizeChatRoomID(room)
	text = strings.TrimSpace(text)
	if roomID == "" || text == "" {
		return ChatLineInput{}, newErrDetail(proto.ErrValidationFailed, "room and text are required", false)
	}
	if !ValidChatRoomID(roomID) {
		return ChatLineInput{}, newErrDetail(proto.ErrValidationFailed, "chat room must use letters, numbers, underscore, or hyphen", false)
	}
	if len([]rune(text)) > 1000 {
		return ChatLineInput{}, newErrDetail(proto.ErrValidationFailed, "chat text is too long", false)
	}
	return ChatLineInput{RoomID: roomID, RoomName: FormatChatRoomName(roomID), Text: text}, nil
}

func NormalizeChatRoomID(room string) string {
	room = strings.ToLower(strings.TrimSpace(room))
	if room == "" {
		return "lobby"
	}
	return room
}

func ValidChatRoomID(room string) bool {
	if len(room) > 40 {
		return false
	}
	for _, ch := range room {
		if ch >= 'a' && ch <= 'z' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func FormatChatRoomName(roomID string) string {
	if roomID == "lobby" {
		return "Lobby"
	}
	parts := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(roomID))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	if len(parts) == 0 {
		return roomID
	}
	return strings.Join(parts, " ")
}

func DerivePresenceHints(status string) (mode, boardID, threadID string) {
	parts := strings.Split(status, ":")
	if len(parts) < 2 {
		return "", "", ""
	}
	mode = strings.TrimSpace(parts[0])
	switch strings.TrimSpace(parts[1]) {
	case "board":
		if len(parts) >= 3 {
			boardID = strings.TrimSpace(parts[2])
		}
	case "thread":
		if len(parts) >= 3 {
			threadID = strings.TrimSpace(parts[2])
		}
	default:
		boardID = strings.TrimSpace(parts[1])
	}
	return mode, boardID, threadID
}

func ValidatePresenceText(status, sessionID, mode, boardID, threadID, location, fromHost string) *proto.ErrorDetail {
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"status", status, 120},
		{"sessionId", sessionID, 80},
		{"mode", mode, 40},
		{"board", boardID, 80},
		{"thread", threadID, 120},
		{"location", location, 160},
		{"fromHost", fromHost, 160},
	} {
		if len(field.value) > field.limit {
			return newErrDetail(proto.ErrValidationFailed, field.name+" is too long", false)
		}
	}
	return nil
}
