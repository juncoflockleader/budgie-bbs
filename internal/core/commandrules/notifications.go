package commandrules

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/notificationmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func PreferredPostNotificationKind(existing, candidate string) string {
	return notificationmodel.PreferredPostKind(existing, candidate)
}

func ThreadPrefLevel(queryable Queryable, userID, threadID string) (string, error) {
	var level string
	err := projections.QQueryRow(queryable, `SELECT level FROM thread_prefs WHERE user_id=? AND thread_id=?`, userID, threadID).Scan(&level)
	if err == sql.ErrNoRows {
		return "normal", nil
	}
	return level, err
}

func UserCanReceivePostNotification(queryable Queryable, user *projections.User, boardID string, settings *projections.BoardSettings) (bool, error) {
	if user == nil {
		return notificationmodel.CanReceiveBoardPost(false, false, false), nil
	}
	memberReadMode := settings != nil && settings.MemberReadMode
	if !memberReadMode {
		return notificationmodel.CanReceiveBoardPost(true, false, false), nil
	}
	canUseMemberBoard, err := projections.ActorCanUseMemberBoard(queryable, user, boardID)
	if err != nil {
		return false, err
	}
	return notificationmodel.CanReceiveBoardPost(true, true, canUseMemberBoard), nil
}
