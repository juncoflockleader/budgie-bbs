package commandrules

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func PreferredPostNotificationKind(existing, candidate string) string {
	if postNotificationKindPriority(existing) >= postNotificationKindPriority(candidate) {
		return existing
	}
	return candidate
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
		return false, nil
	}
	if settings == nil || !settings.MemberReadMode {
		return true, nil
	}
	return projections.ActorCanUseMemberBoard(queryable, user, boardID)
}

func postNotificationKindPriority(kind string) int {
	switch kind {
	case "mention":
		return 3
	case "reply":
		return 2
	case "watched":
		return 1
	default:
		return 0
	}
}
