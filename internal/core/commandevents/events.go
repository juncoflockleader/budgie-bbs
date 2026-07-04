package commandevents

import "github.com/juncoflockleader/budgie-bbs/internal/proto"

const LoginWatchRelationshipKind = "login_watch"

func BoardFavoriteSet(userID, boardID, folderID string, favorite bool, position *int, ts int64) ([]string, *proto.BoardFavoriteSetPayload) {
	payload := &proto.BoardFavoriteSetPayload{UserID: userID, Board: boardID, Favorite: favorite, FolderID: folderID, Position: position, TS: ts}
	return []string{"board:" + boardID, "user:" + userID}, payload
}

func DirectMessageSent(messageID, fromUserID, fromName, toUserID, toName, body string, ts int64) ([]string, *proto.DirectMessageSentPayload) {
	return proto.DirectMessageEventScopes(fromUserID, toUserID),
		proto.NewDirectMessageSentPayload(messageID, fromUserID, fromName, toUserID, toName, body, ts)
}

func DirectMessageRead(messageID, userID, fromUserID, toUserID string, ts int64) ([]string, *proto.DirectMessageReadPayload) {
	payload := &proto.DirectMessageReadPayload{MessageID: messageID, UserID: userID, ReadAt: ts, TS: ts}
	return proto.DirectMessageEventScopes(fromUserID, toUserID), payload
}

func DirectMessageDeleted(
	messageID, userID, fromUserID, toUserID string,
	senderDeleted, recipientDeleted bool,
	ts int64,
) ([]string, *proto.DirectMessageDeletedPayload) {
	payload := &proto.DirectMessageDeletedPayload{MessageID: messageID, UserID: userID, SenderDeleted: senderDeleted, RecipientDeleted: recipientDeleted, TS: ts}
	return proto.DirectMessageEventScopes(fromUserID, toUserID), payload
}

func UserRelationshipSet(userID, targetUserID, kind, note string, active bool, ts int64) ([]string, *proto.UserRelationshipSetPayload) {
	payload := &proto.UserRelationshipSetPayload{UserID: userID, TargetUserID: targetUserID, Kind: kind, Active: active, Note: note, TS: ts}
	return []string{"user:" + userID, "user:" + targetUserID}, payload
}

func UserBlessed(fromUserID, fromName, toUserID, toName, blessingID, message string, ts int64) ([]string, *proto.UserBlessedPayload) {
	return []string{"user:" + fromUserID, "user:" + toUserID, "blessing:" + blessingID}, &proto.UserBlessedPayload{
		ID:         blessingID,
		FromUserID: fromUserID,
		From:       fromName,
		ToUserID:   toUserID,
		To:         toName,
		Message:    message,
		TS:         ts,
	}
}
