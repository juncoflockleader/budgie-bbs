package core

import (
	"database/sql"
	"sync/atomic"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

var asyncPostSearchCommands atomic.Bool
var asyncCommunityStatHistorySnapshots atomic.Bool

func setAsyncPostSearchCommands(enabled bool) {
	asyncPostSearchCommands.Store(enabled)
}

func setAsyncCommunityStatHistorySnapshots(enabled bool) {
	asyncCommunityStatHistorySnapshots.Store(enabled)
}

func commandFtsDeletePost(tx *sql.Tx, postID string) error {
	if asyncPostSearchCommands.Load() {
		return nil
	}
	return projections.FtsDeletePost(tx, postID)
}

func commandFtsInsertPost(tx *sql.Tx, postID, threadID, boardID, author, body string) error {
	if asyncPostSearchCommands.Load() {
		return nil
	}
	return projections.FtsInsertPost(tx, postID, threadID, boardID, author, body)
}

func commandFtsUpdatePost(tx *sql.Tx, postID, newBody string) error {
	if asyncPostSearchCommands.Load() {
		return nil
	}
	return projections.FtsUpdatePost(tx, postID, newBody)
}

func upsertBoardAutomodRule(tx *sql.Tx, p *proto.BoardAutomodRuleSetPayload) error {
	return projections.UpsertBoardAutomodRule(tx, projections.BoardAutomodRule{
		ID:          p.ID,
		Board:       p.Board,
		Enabled:     p.Enabled,
		Priority:    p.Priority,
		MatchType:   p.MatchType,
		Pattern:     p.Pattern,
		Threshold:   p.Threshold,
		WindowSec:   p.WindowSec,
		Action:      p.Action,
		DurationSec: p.DurationSec,
		Reason:      p.Reason,
		Note:        p.Note,
		CreatedBy:   p.By,
		CreatedAt:   p.TS,
		UpdatedBy:   p.By,
		UpdatedAt:   p.TS,
	})
}

func insertAutomodAuditLog(tx *sql.Tx, p *proto.BoardAutomodTriggeredPayload) error {
	return projections.InsertAutomodAuditLog(tx, projections.BoardAutomodActivity{
		ID:           p.ID,
		Board:        p.Board,
		RuleID:       p.RuleID,
		MatchType:    p.MatchType,
		Action:       p.Action,
		TargetUserID: p.TargetUser,
		PostID:       p.PostID,
		ThreadID:     p.ThreadID,
		Reason:       p.Reason,
		TS:           p.TS,
	})
}

func insertNotification(db *sql.DB, id, userID, kind, threadID, postID, actor string, ts int64) error {
	return projections.InsertNotification(db, id, userID, kind, threadID, postID, actor, ts)
}

func insertNotificationTx(tx *sql.Tx, id, userID, kind, threadID, postID, actor string, ts int64) error {
	return projections.InsertNotification(tx, id, userID, kind, threadID, postID, actor, ts)
}

func promoteStagedPostAttachmentBlob(tx *sql.Tx, stagingID, attachmentID string, expectedSize int64, contentType string) error {
	return projections.PromoteStagedAttachmentBlob(tx, projections.StagedBlobPostAttachment, stagingID, attachmentID, expectedSize, contentType)
}

func promoteStagedMailAttachmentBlob(tx *sql.Tx, stagingID, attachmentID string, expectedSize int64, contentType string) error {
	return projections.PromoteStagedAttachmentBlob(tx, projections.StagedBlobMailAttachment, stagingID, attachmentID, expectedSize, contentType)
}

func setUserPresence(db *sql.DB, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64) error {
	if asyncCommunityStatHistorySnapshots.Load() {
		if err := projections.SetUserPresenceWithoutCommunityStatHistory(db, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost, ts); err != nil {
			return err
		}
		return recordCommunityStatSnapshot(db, ts)
	}
	return projections.SetUserPresence(db, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost, ts)
}

func setGuestPresence(db *sql.DB, sessionID, status, locationLabel, fromHost string, ts int64) error {
	if asyncCommunityStatHistorySnapshots.Load() {
		if err := projections.SetGuestPresenceWithoutCommunityStatHistory(db, sessionID, status, locationLabel, fromHost, ts); err != nil {
			return err
		}
		return recordCommunityStatSnapshot(db, ts)
	}
	return projections.SetGuestPresence(db, sessionID, status, locationLabel, fromHost, ts)
}

func recordCommunityStatSnapshot(db *sql.DB, ts int64) error {
	if asyncCommunityStatHistorySnapshots.Load() {
		return enqueueCommunityStatSnapshot(db, ts)
	}
	return projections.UpsertCommunityStatHistoryFromCurrent(db, ts)
}

func userIgnores(db *sql.DB, userID, targetUserID string) (bool, error) {
	return projections.UserRelationshipExists(db, userID, targetUserID, "ignore")
}

func getMailGroupID(db *sql.DB, ownerID, groupRef string) (string, error) {
	return projections.GetMailGroupID(db, ownerID, groupRef)
}

func recordLogout(db *sql.DB) error {
	if asyncCommunityStatHistorySnapshots.Load() {
		ts := projections.NowMS()
		if err := projections.RecordLogoutAtWithoutCommunityStatHistory(db, ts); err != nil {
			return err
		}
		return recordCommunityStatSnapshot(db, ts)
	}
	return projections.RecordLogout(db)
}

func importFavoriteTree(db *sql.DB, userID string, tree *FavoriteTree, replace bool) error {
	return projections.ImportFavoriteTree(db, userID, tree, replace, newID)
}

func userTrustLevel(db *sql.DB, userID string) (int, error) {
	return projections.UserTrustLevel(db, userID)
}

func watchersOfThreadTx(tx *sql.Tx, threadID, excludeUserID string) ([]string, error) {
	return projections.WatchersOfThread(tx, threadID, excludeUserID)
}
