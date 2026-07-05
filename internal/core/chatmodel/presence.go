package chatmodel

import "strings"

type LineInput struct {
	RoomID   string
	RoomName string
	Text     string
}

type LineFailure string

const (
	LineOK          LineFailure = ""
	LineMissing     LineFailure = "missing"
	LineInvalidRoom LineFailure = "invalid_room"
	LineTextTooLong LineFailure = "text_too_long"
)

func NormalizeLine(room, text string) (LineInput, LineFailure) {
	roomID := NormalizeRoomID(room)
	text = strings.TrimSpace(text)
	if roomID == "" || text == "" {
		return LineInput{}, LineMissing
	}
	if !ValidRoomID(roomID) {
		return LineInput{}, LineInvalidRoom
	}
	if len([]rune(text)) > 1000 {
		return LineInput{}, LineTextTooLong
	}
	return LineInput{RoomID: roomID, RoomName: FormatRoomName(roomID), Text: text}, LineOK
}

func NormalizeRoomID(room string) string {
	room = strings.ToLower(strings.TrimSpace(room))
	if room == "" {
		return "lobby"
	}
	return room
}

func ValidRoomID(room string) bool {
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

func FormatRoomName(roomID string) string {
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

type PresenceText struct {
	Status    string
	SessionID string
	Mode      string
	BoardID   string
	ThreadID  string
	Location  string
	FromHost  string
}

func PresenceTextTooLongField(text PresenceText) string {
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"status", text.Status, 120},
		{"sessionId", text.SessionID, 80},
		{"mode", text.Mode, 40},
		{"board", text.BoardID, 80},
		{"thread", text.ThreadID, 120},
		{"location", text.Location, 160},
		{"fromHost", text.FromHost, 160},
	} {
		if len(field.value) > field.limit {
			return field.name
		}
	}
	return ""
}
