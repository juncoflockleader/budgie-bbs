package handler

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func nowMS() int64 {
	return currentRuntime().NowMS()
}

func newID(prefix string) string {
	return currentRuntime().NewID(prefix)
}

func checkProcessed(db *sql.DB, actorID, cid, commandHash string) (string, bool, bool) {
	return currentRuntime().CheckProcessed(db, actorID, cid, commandHash)
}

func qQueryRow(queryable interface {
	QueryRow(query string, args ...any) *sql.Row
}, query string, args ...any) *sql.Row {
	return currentRuntime().QQueryRow(queryable, query, args...)
}

func activeSanction(db *sql.DB, userID, scope string) (string, bool) {
	return currentRuntime().ActiveSanction(db, userID, scope)
}

func appendEvent(tx *sql.Tx, id string, kind proto.EventKind, scopes []string, payload any) (int64, error) {
	return currentRuntime().AppendEvent(tx, id, kind, scopes, payload)
}

func getThread(db *sql.DB, id string) (*Thread, error) {
	return currentRuntime().GetThread(db, id)
}

func getPost(db *sql.DB, id string) (*Post, error) {
	return currentRuntime().GetPost(db, id)
}

func getMail(db *sql.DB, userID, messageID string) (*MailItem, error) {
	return currentRuntime().GetMail(db, userID, messageID)
}

func getUserTx(tx *sql.Tx, id string) (*User, error) {
	return currentRuntime().GetUserTx(tx, id)
}

// GetUserTx exposes user lookup via a transaction for callers outside
// this package that still need command-time projection reads.
func GetUserTx(tx *sql.Tx, id string) (*User, error) {
	return getUserTx(tx, id)
}

func getThreadTx(tx *sql.Tx, id string) (*Thread, error) {
	return currentRuntime().GetThreadTx(tx, id)
}

// GetThreadTx exposes thread lookup via a transaction for callers outside
// this package that still need command-time projection reads.
func GetThreadTx(tx *sql.Tx, id string) (*Thread, error) {
	return getThreadTx(tx, id)
}

func getPostTx(tx *sql.Tx, id string) (*Post, error) {
	return currentRuntime().GetPostTx(tx, id)
}

// GetPostTx exposes post lookup via a transaction for callers outside
// this package that still need command-time projection reads.
func GetPostTx(tx *sql.Tx, id string) (*Post, error) {
	return getPostTx(tx, id)
}

func getPollWithVotes(db *sql.DB, pollID, viewerUserID string) (*Poll, error) {
	return currentRuntime().GetPollWithVotes(db, pollID, viewerUserID)
}

func insertThread(tx *sql.Tx, t *Thread) error {
	return currentRuntime().InsertThread(tx, t)
}

func insertPost(tx *sql.Tx, p *Post) error {
	return currentRuntime().InsertPost(tx, p)
}

func bumpThread(tx *sql.Tx, threadID string, seq int64) error {
	return currentRuntime().BumpThread(tx, threadID, seq)
}

func ftsInsertPost(tx *sql.Tx, postID, threadID, boardID, author, body string) error {
	return currentRuntime().FtsInsertPost(tx, postID, threadID, boardID, author, body)
}

func ftsUpdatePost(tx *sql.Tx, postID, newBody string) error {
	return currentRuntime().FtsUpdatePost(tx, postID, newBody)
}

func ftsDeletePost(tx *sql.Tx, postID string) error {
	return currentRuntime().FtsDeletePost(tx, postID)
}

func insertPoll(tx *sql.Tx, id, postID, question string, expiresAt, ts int64) error {
	return currentRuntime().InsertPoll(tx, id, postID, question, expiresAt, ts)
}

func insertPollOption(tx *sql.Tx, id, pollID, text string, position int) error {
	return currentRuntime().InsertPollOption(tx, id, pollID, text, position)
}

func insertPostAttachment(tx *sql.Tx, id, postID, filename, contentType string, sizeBytes int64, url, createdBy string, createdAt int64) error {
	return currentRuntime().InsertPostAttachment(tx, id, postID, filename, contentType, sizeBytes, url, createdBy, createdAt)
}

func insertMailAttachment(tx *sql.Tx, id, mailID, filename, contentType string, sizeBytes int64, url, createdBy string, createdAt int64) error {
	return currentRuntime().InsertMailAttachment(tx, id, mailID, filename, contentType, sizeBytes, url, createdBy, createdAt)
}

func insertRelayDelivery(tx *sql.Tx, id, boardID, threadID, postID, authorID, authorName, title, body string, createdAt, seq int64) error {
	return currentRuntime().InsertRelayDelivery(tx, id, boardID, threadID, postID, authorID, authorName, title, body, createdAt, seq)
}

func enqueueOutboxJob(tx *sql.Tx, kind string, payload any, ts int64) error {
	return currentRuntime().EnqueueOutboxJob(tx, kind, payload, ts)
}

func upsertReaction(tx *sql.Tx, postID, userID, emoji string, ts int64) error {
	return currentRuntime().UpsertReaction(tx, postID, userID, emoji, ts)
}

func reactionCountTx(tx *sql.Tx, postID string) (int, error) {
	return currentRuntime().ReactionCountTx(tx, postID)
}

func userReacted(db *sql.DB, postID, userID string) (bool, error) {
	return currentRuntime().UserReacted(db, postID, userID)
}

func deleteReaction(tx *sql.Tx, postID, userID string) error {
	return currentRuntime().DeleteReaction(tx, postID, userID)
}

func castVote(tx *sql.Tx, pollID, optionID, userID string, ts int64) error {
	return currentRuntime().CastVote(tx, pollID, optionID, userID, ts)
}

func markPostRedacted(tx *sql.Tx, postID string, seq int64) error {
	return currentRuntime().MarkPostRedacted(tx, postID, seq)
}

func markPostRestored(tx *sql.Tx, postID string, seq int64) error {
	return currentRuntime().MarkPostRestored(tx, postID, seq)
}

func markPostPurged(tx *sql.Tx, postID string, seq int64) error {
	return currentRuntime().MarkPostPurged(tx, postID, seq)
}

func setPostFlags(tx *sql.Tx, postID string, marked, recommended, noReply, tex, mailBack bool, seq int64) error {
	return currentRuntime().SetPostFlags(tx, postID, marked, recommended, noReply, tex, mailBack, seq)
}

func setThreadLocked(tx *sql.Tx, threadID string, locked bool) error {
	return currentRuntime().SetThreadLocked(tx, threadID, locked)
}

func setThreadTitle(tx *sql.Tx, threadID, title string, ts int64) error {
	return currentRuntime().SetThreadTitle(tx, threadID, title, ts)
}

func moveThreadBoard(tx *sql.Tx, threadID, toBoard string) error {
	return currentRuntime().MoveThreadBoard(tx, threadID, toBoard)
}

func setUserRole(tx *sql.Tx, userID, role string) error {
	return currentRuntime().SetUserRole(tx, userID, role)
}

func insertBoard(tx *sql.Tx, id, name, description, parentID string, position int) error {
	return currentRuntime().InsertBoard(tx, id, name, description, parentID, position)
}

func getDigestExport(db *sql.DB, entryID string) (*DigestExport, error) {
	return currentRuntime().GetDigestExport(db, entryID)
}

func insertMailMessage(tx *sql.Tx, id, fromUserID, subject, body, parentID string, createdAt, seq int64) error {
	return currentRuntime().InsertMailMessage(tx, id, fromUserID, subject, body, parentID, createdAt, seq)
}

func insertMailCopy(tx *sql.Tx, messageID, userID, role, mailbox string, read, kept bool, updatedAt int64) error {
	return currentRuntime().InsertMailCopy(tx, messageID, userID, role, mailbox, read, kept, updatedAt)
}

func insertNotification(db *sql.DB, id, userID, kind, threadID, postID, actor string, ts int64) error {
	return currentRuntime().InsertNotification(db, id, userID, kind, threadID, postID, actor, ts)
}

func insertNotificationTx(tx *sql.Tx, id, userID, kind, threadID, postID, actor string, ts int64) error {
	return currentRuntime().InsertNotificationTx(tx, id, userID, kind, threadID, postID, actor, ts)
}

func updateMailCopy(db *sql.DB, userID, messageID string, mailbox *string, read, kept *bool) (bool, error) {
	return currentRuntime().UpdateMailCopy(db, userID, messageID, mailbox, read, kept)
}

func trashMailCopy(db *sql.DB, userID, messageID string) (bool, error) {
	return currentRuntime().TrashMailCopy(db, userID, messageID)
}

func setMailGroup(db *sql.DB, ownerID, groupID, name string, memberIDs []string) error {
	return currentRuntime().SetMailGroup(db, ownerID, groupID, name, memberIDs)
}

func deleteMailGroup(db *sql.DB, ownerID, groupID string) (bool, error) {
	return currentRuntime().DeleteMailGroup(db, ownerID, groupID)
}

func getMailGroupID(db *sql.DB, ownerID, groupRef string) (string, error) {
	return currentRuntime().GetMailGroupID(db, ownerID, groupRef)
}

func listMailGroupMembers(db *sql.DB, ownerID, groupRef string) ([]MailGroupMember, error) {
	return currentRuntime().ListMailGroupMembers(db, ownerID, groupRef)
}

func listFriendUserIDs(db *sql.DB, ownerID string) ([]string, error) {
	return currentRuntime().ListFriendUserIDs(db, ownerID)
}

func listLoginWatchers(db *sql.DB, targetUserID string) ([]string, error) {
	return currentRuntime().ListLoginWatchers(db, targetUserID)
}

func insertDirectMessage(tx *sql.Tx, id, conversationID, fromUserID, toUserID, body string, createdAt, seq int64) error {
	return currentRuntime().InsertDirectMessage(tx, id, conversationID, fromUserID, toUserID, body, createdAt, seq)
}

func insertBlessing(tx *sql.Tx, blessing *Blessing) error {
	return currentRuntime().InsertBlessing(tx, blessing)
}

func markDirectMessageRead(db *sql.DB, userID, messageID string) (bool, error) {
	return currentRuntime().MarkDirectMessageRead(db, userID, messageID)
}

func deleteDirectMessage(db *sql.DB, userID, messageID string) (bool, error) {
	return currentRuntime().DeleteDirectMessage(db, userID, messageID)
}

func getDirectMessageSettings(db *sql.DB, userID string) (*DirectMessageSettings, error) {
	return currentRuntime().GetDirectMessageSettings(db, userID)
}

func setDirectMessageSettings(db *sql.DB, userID, policy string) error {
	return currentRuntime().SetDirectMessageSettings(db, userID, policy)
}

func setUserRelationship(db *sql.DB, userID, targetUserID, kind, note string, active bool) error {
	return currentRuntime().SetUserRelationship(db, userID, targetUserID, kind, note, active)
}

func setUserPresence(db *sql.DB, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64) error {
	return currentRuntime().SetUserPresence(db, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost, ts)
}

func userIgnores(db *sql.DB, userID, targetUserID string) (bool, error) {
	return currentRuntime().UserIgnores(db, userID, targetUserID)
}

func insertModerationReview(tx *sql.Tx, id, kind, targetID, targetKind, reporter, reason string, ts int64) error {
	return currentRuntime().InsertModerationReview(tx, id, kind, targetID, targetKind, reporter, reason, ts)
}

func resolveModerationReview(tx *sql.Tx, id, actor, resolution string, ts int64) error {
	return currentRuntime().ResolveModerationReview(tx, id, actor, resolution, ts)
}

func setThreadPref(db *sql.DB, userID, threadID, level string) error {
	return currentRuntime().SetThreadPref(db, userID, threadID, level)
}

func watchersOfThreadTx(tx *sql.Tx, threadID, excludeUserID string) ([]string, error) {
	return currentRuntime().WatchersOfThreadTx(tx, threadID, excludeUserID)
}

func setBoardFavorite(db *sql.DB, userID, boardID, folderID string, position *int, favorite bool) error {
	return currentRuntime().SetBoardFavorite(db, userID, boardID, folderID, position, favorite)
}

func setBoardZap(db *sql.DB, userID, boardID string, zapped bool) error {
	return currentRuntime().SetBoardZap(db, userID, boardID, zapped)
}

func createFavoriteFolder(db *sql.DB, userID, folderID, parentID, name string, position *int) error {
	return currentRuntime().CreateFavoriteFolder(db, userID, folderID, parentID, name, position)
}

func updateFavoriteFolder(db *sql.DB, userID, folderID, name string, parentID *string, position *int) error {
	return currentRuntime().UpdateFavoriteFolder(db, userID, folderID, name, parentID, position)
}

func deleteFavoriteFolder(db *sql.DB, userID, folderID string) error {
	return currentRuntime().DeleteFavoriteFolder(db, userID, folderID)
}

func moveBoardFavorite(db *sql.DB, userID, boardID, folderID string, position *int) error {
	return currentRuntime().MoveBoardFavorite(db, userID, boardID, folderID, position)
}

func importFavoriteTree(db *sql.DB, userID string, tree *projections.FavoriteTree, replace bool) error {
	return currentRuntime().ImportFavoriteTree(db, userID, tree, replace)
}

func getBoardSettings(db *sql.DB, boardID string) (*BoardSettings, error) {
	return currentRuntime().GetBoardSettings(db, boardID)
}

func setBoardSettings(db *sql.DB, boardID string, patch BoardSettingsPatch) error {
	return currentRuntime().SetBoardSettings(db, boardID, patch)
}

func setRecommendedBoard(db *sql.DB, boardID, note, curatedBy string, position *int, recommended bool) error {
	return currentRuntime().SetRecommendedBoard(db, boardID, note, curatedBy, position, recommended)
}

func getBoardMemberRequirements(db *sql.DB, boardID string) (*BoardMemberRequirements, error) {
	return currentRuntime().GetBoardMemberRequirements(db, boardID)
}

func setBoardMemberRequirements(db *sql.DB, boardID string, patch BoardMemberRequirementsPatch) error {
	return currentRuntime().SetBoardMemberRequirements(db, boardID, patch)
}

func setBoardModerator(db *sql.DB, boardID, userID string, moderator bool, position *int) error {
	return currentRuntime().SetBoardModerator(db, boardID, userID, moderator, position)
}

func setBoardMember(db *sql.DB, boardID, userID string, member bool, patch BoardMemberPatch) error {
	return currentRuntime().SetBoardMember(db, boardID, userID, member, patch)
}

func insertBoardMemberApplication(db *sql.DB, id, boardID, userID, note string) error {
	return currentRuntime().InsertBoardMemberApplication(db, id, boardID, userID, note)
}

func reviewBoardMemberApplication(db *sql.DB, applicationID, reviewerID, status, title, reviewNote string) error {
	return currentRuntime().ReviewBoardMemberApplication(db, applicationID, reviewerID, status, title, reviewNote)
}

func upsertDigestEntry(db *sql.DB, id, boardID, targetKind, targetID, kind, title, path, note, createdBy string) (string, error) {
	return currentRuntime().UpsertDigestEntry(db, id, boardID, targetKind, targetID, kind, title, path, note, createdBy)
}

func removeDigestEntry(db *sql.DB, id string) error {
	return currentRuntime().RemoveDigestEntry(db, id)
}

func updateDigestEntry(db *sql.DB, id, title, path, note string) error {
	return currentRuntime().UpdateDigestEntry(db, id, title, path, note)
}

func setDigestEntryBody(db *sql.DB, id, body string, edited bool) error {
	return currentRuntime().SetDigestEntryBody(db, id, body, edited)
}

func upsertDigestDirectory(db *sql.DB, id, boardID, kind, path, createdBy string) (string, error) {
	return currentRuntime().UpsertDigestDirectory(db, id, boardID, kind, path, createdBy)
}

func countDigestPathEntries(db *sql.DB, boardID, kind, path string) (int, error) {
	return currentRuntime().CountDigestPathEntries(db, boardID, kind, path)
}

func countDigestPathDirectories(db *sql.DB, boardID, kind, path string) (int, error) {
	return currentRuntime().CountDigestPathDirectories(db, boardID, kind, path)
}

func moveDigestPath(db *sql.DB, boardID, kind, fromPath, toPath string) (int, error) {
	return currentRuntime().MoveDigestPath(db, boardID, kind, fromPath, toPath)
}

func copyDigestPath(db *sql.DB, boardID, kind, fromPath, toPath, createdBy string, entryIDs, directoryIDs []string) (int, error) {
	return currentRuntime().CopyDigestPath(db, boardID, kind, fromPath, toPath, createdBy, entryIDs, directoryIDs)
}

func deleteDigestPath(db *sql.DB, boardID, kind, path string) (int, error) {
	return currentRuntime().DeleteDigestPath(db, boardID, kind, path)
}

func markBoardRead(db *sql.DB, userID, boardID string) error {
	return currentRuntime().MarkBoardRead(db, userID, boardID)
}

func restoreBoardRead(db *sql.DB, userID, boardID string) error {
	return currentRuntime().RestoreBoardRead(db, userID, boardID)
}

func markFavoriteFolderRead(db *sql.DB, userID, folderID string) error {
	return currentRuntime().MarkFavoriteFolderRead(db, userID, folderID)
}

func restoreFavoriteFolderRead(db *sql.DB, userID, folderID string) error {
	return currentRuntime().RestoreFavoriteFolderRead(db, userID, folderID)
}

func markThreadRead(db *sql.DB, userID, threadID string) error {
	return currentRuntime().MarkThreadRead(db, userID, threadID)
}

func restoreThreadRead(db *sql.DB, userID, threadID string) error {
	return currentRuntime().RestoreThreadRead(db, userID, threadID)
}

func markPostRead(db *sql.DB, userID, postID string) error {
	return currentRuntime().MarkPostRead(db, userID, postID)
}

func recordProcessed(tx *sql.Tx, actorID, cid, commandHash, resultJSON string) error {
	return currentRuntime().RecordProcessed(tx, actorID, cid, commandHash, resultJSON)
}

func recordReactionReceived(db *sql.DB, postAuthorID string) error {
	return currentRuntime().RecordReactionReceived(db, postAuthorID)
}

func recordReactionRemoved(db *sql.DB, postAuthorID string) error {
	return currentRuntime().RecordReactionRemoved(db, postAuthorID)
}

func userTrustLevel(db *sql.DB, userID string) (int, error) {
	return currentRuntime().UserTrustLevel(db, userID)
}

func updatePostBody(tx *sql.Tx, postID string, body string, seq int64) error {
	return currentRuntime().UpdatePostBody(tx, postID, body, seq)
}

func insertSanction(tx *sql.Tx, id, userID, kind, scope string, expiresAt int64, by, reason string, seq int64) error {
	return currentRuntime().InsertSanction(tx, id, userID, kind, scope, expiresAt, by, reason, seq)
}

func clearUserSanctions(tx *sql.Tx, userID, kind, scope string) (int64, error) {
	return currentRuntime().ClearUserSanctions(tx, userID, kind, scope)
}

func matchContentFilter(db *sql.DB, boardID, text string) (*ContentFilter, error) {
	return currentRuntime().MatchContentFilter(db, boardID, text)
}

func upsertContentFilter(tx *sql.Tx, id, pattern, scope string, active bool, createdBy string, ts int64) error {
	return currentRuntime().UpsertContentFilter(tx, id, pattern, scope, active, createdBy, ts)
}
