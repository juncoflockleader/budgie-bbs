package commandevents

import "github.com/juncoflockleader/budgie-bbs/internal/proto"

const LoginWatchRelationshipKind = "login_watch"

func BoardFavoriteSet(userID, boardID, folderID string, favorite bool, position *int, ts int64) ([]string, *proto.BoardFavoriteSetPayload) {
	payload := &proto.BoardFavoriteSetPayload{UserID: userID, Board: boardID, Favorite: favorite, FolderID: folderID, Position: position, TS: ts}
	return []string{"board:" + boardID, "user:" + userID}, payload
}

func BoardZapSet(userID, boardID string, zapped bool, ts int64) ([]string, *proto.BoardZapSetPayload) {
	payload := &proto.BoardZapSetPayload{UserID: userID, Board: boardID, Zapped: zapped, TS: ts}
	return []string{"board:" + boardID, "user:" + userID}, payload
}

func BoardAutomodRuleSet(
	ruleID, boardID string,
	enabled bool,
	priority int,
	matchType, pattern string,
	threshold, windowSec int,
	action string,
	durationSec int64,
	reason, note, by string,
	ts int64,
) ([]string, *proto.BoardAutomodRuleSetPayload) {
	return []string{"board:" + boardID}, &proto.BoardAutomodRuleSetPayload{
		ID:          ruleID,
		Board:       boardID,
		Enabled:     enabled,
		Priority:    priority,
		MatchType:   matchType,
		Pattern:     pattern,
		Threshold:   threshold,
		WindowSec:   windowSec,
		Action:      action,
		DurationSec: durationSec,
		Reason:      reason,
		Note:        note,
		By:          by,
		TS:          ts,
	}
}

func BoardAutomodRuleDeleted(ruleID, boardID, by string, ts int64) ([]string, *proto.BoardAutomodRuleDeletedPayload) {
	return []string{"board:" + boardID}, &proto.BoardAutomodRuleDeletedPayload{ID: ruleID, Board: boardID, By: by, TS: ts}
}

func FavoriteFolderCreated(userID, folderID, parentID, name string, position int, ts int64) ([]string, *proto.FavoriteFolderCreatedPayload) {
	payload := &proto.FavoriteFolderCreatedPayload{ID: folderID, UserID: userID, ParentID: parentID, Name: name, Position: position, TS: ts}
	return []string{"user:" + userID}, payload
}

func FavoriteFolderUpdated(userID, folderID, parentID, name string, position int, ts int64) ([]string, *proto.FavoriteFolderUpdatedPayload) {
	payload := &proto.FavoriteFolderUpdatedPayload{ID: folderID, UserID: userID, ParentID: parentID, Name: name, Position: position, TS: ts}
	return []string{"user:" + userID}, payload
}

func FavoriteFolderDeleted(userID, folderID, parentID string, ts int64) ([]string, *proto.FavoriteFolderDeletedPayload) {
	payload := &proto.FavoriteFolderDeletedPayload{ID: folderID, UserID: userID, ParentID: parentID, TS: ts}
	return []string{"user:" + userID}, payload
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

func DirectMessageSettingsSet(userID, policy string, ts int64) ([]string, *proto.DirectMessageSettingsSetPayload) {
	payload := &proto.DirectMessageSettingsSetPayload{UserID: userID, Policy: policy, TS: ts}
	return []string{"user:" + userID}, payload
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

func ReviewResolved(reviewID, resolution, by string, ts int64) ([]string, *proto.ReviewResolvedPayload) {
	return []string{"moderation:global"}, &proto.ReviewResolvedPayload{
		ReviewID:   reviewID,
		Resolution: resolution,
		By:         by,
		TS:         ts,
	}
}
