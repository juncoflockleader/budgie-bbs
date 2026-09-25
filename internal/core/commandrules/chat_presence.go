package commandrules

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/chatmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func NormalizeChatLine(room, text string) (chatmodel.LineInput, *proto.ErrorDetail) {
	line, failure := chatmodel.NormalizeLine(room, text)
	switch failure {
	case chatmodel.LineMissing:
		return chatmodel.LineInput{}, newErrDetail(proto.ErrValidationFailed, "room and text are required", false)
	case chatmodel.LineInvalidRoom:
		return chatmodel.LineInput{}, newErrDetail(proto.ErrValidationFailed, "chat room must use letters, numbers, underscore, or hyphen", false)
	case chatmodel.LineTextTooLong:
		return chatmodel.LineInput{}, newErrDetail(proto.ErrValidationFailed, "chat text is too long", false)
	}
	return line, nil
}

func NormalizeChatRoomID(room string) string {
	return chatmodel.NormalizeRoomID(room)
}

func ValidChatRoomID(room string) bool {
	return chatmodel.ValidRoomID(room)
}

func FormatChatRoomName(roomID string) string {
	return chatmodel.FormatRoomName(roomID)
}

func DerivePresenceHints(status string) (mode, boardID, threadID string) {
	return chatmodel.DerivePresenceHints(status)
}

func ValidatePresenceText(status, sessionID, mode, boardID, threadID, location, fromHost string) *proto.ErrorDetail {
	field := chatmodel.PresenceTextTooLongField(chatmodel.PresenceText{
		Status:    status,
		SessionID: sessionID,
		Mode:      mode,
		BoardID:   boardID,
		ThreadID:  threadID,
		Location:  location,
		FromHost:  fromHost,
	})
	if field != "" {
		return newErrDetail(proto.ErrValidationFailed, field+" is too long", false)
	}
	return nil
}
