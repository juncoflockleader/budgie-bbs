package core

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func activeSanction(db *sql.DB, userID, scope string) (string, bool) {
	return projections.ActiveSanction(db, userID, scope)
}

func bumpThread(tx *sql.Tx, threadID string, seq int64) error {
	return projections.BumpThread(tx, threadID, seq)
}

func castVote(tx *sql.Tx, pollID, optionID, userID string, ts int64) error {
	return projections.CastVote(tx, pollID, optionID, userID, ts)
}

func checkProcessed(db *sql.DB, actorID, cid, commandHash string) (string, bool, bool) {
	return projections.CheckProcessed(db, actorID, cid, commandHash)
}

func computeTrustLevel(postsCreated, daysVisited, currentLevel int) int {
	return projections.ComputeTrustLevel(postsCreated, daysVisited, currentLevel)
}

func countUnreadNotifications(db *sql.DB, userID string) (int, error) {
	return projections.CountUnreadNotifications(db, userID)
}

func countUsers(db *sql.DB) (int, error) {
	return projections.CountUsers(db)
}

func deleteReaction(tx *sql.Tx, postID, userID string) error {
	return projections.DeleteReaction(tx, postID, userID)
}

func ensureActivity(db *sql.DB, userID string) error {
	return projections.EnsureActivity(db, userID)
}

func extractPubkeyTitle(raw string) string {
	return projections.ExtractPubkeyTitle(raw)
}

func ftsDeletePost(tx *sql.Tx, postID string) error {
	return projections.FtsDeletePost(tx, postID)
}

func ftsInsertPost(tx *sql.Tx, postID, threadID, boardID, author, body string) error {
	return projections.FtsInsertPost(tx, postID, threadID, boardID, author, body)
}

func ftsUpdatePost(tx *sql.Tx, postID, newBody string) error {
	return projections.FtsUpdatePost(tx, postID, newBody)
}

func getBoard(db *sql.DB, id string) (*Board, error) {
	return projections.GetBoard(db, id)
}

func getPollByPostID(db *sql.DB, postID string) (*Poll, error) {
	return projections.GetPollByPostID(db, postID)
}

func getPollWithVotes(db *sql.DB, pollID, viewerUserID string) (*Poll, error) {
	return projections.GetPollWithVotes(db, pollID, viewerUserID)
}

func getPost(db *sql.DB, id string) (*Post, error) {
	return projections.GetPost(db, id)
}

func getThread(db *sql.DB, id string) (*Thread, error) {
	return projections.GetThread(db, id)
}

func getUserByID(db *sql.DB, id string) (*User, error) {
	return projections.GetUserByID(db, id)
}

func getUserByName(db *sql.DB, name string) (*User, error) {
	return projections.GetUserByName(db, name)
}

func getUserByPubkey(db *sql.DB, pubkey string) (*User, error) {
	return projections.GetUserByPubkey(db, pubkey)
}

func getUserProfileByName(db *sql.DB, name string) (*UserProfile, error) {
	return projections.GetUserProfileByName(db, name)
}

func insertBoard(tx *sql.Tx, id, name, description string) error {
	return projections.InsertBoard(tx, id, name, description)
}

func insertModerationReview(tx *sql.Tx, id, kind, targetID, targetKind, reporter, reason string, ts int64) error {
	return projections.InsertModerationReview(tx, id, kind, targetID, targetKind, reporter, reason, ts)
}

func insertNotification(db *sql.DB, id, userID, kind, threadID, postID, actor string, ts int64) error {
	return projections.InsertNotification(db, id, userID, kind, threadID, postID, actor, ts)
}

func insertPoll(tx *sql.Tx, id, postID, question string, expiresAt, ts int64) error {
	return projections.InsertPoll(tx, id, postID, question, expiresAt, ts)
}

func insertPollOption(tx *sql.Tx, id, pollID, text string, position int) error {
	return projections.InsertPollOption(tx, id, pollID, text, position)
}

func insertPost(tx *sql.Tx, p *Post) error {
	return projections.InsertPost(tx, p)
}

func insertSanction(tx *sql.Tx, id, userID, kind, scope string, expiresAt int64, by, reason string, seq int64) error {
	return projections.InsertSanction(tx, id, userID, kind, scope, expiresAt, by, reason, seq)
}

func insertThread(tx *sql.Tx, t *Thread) error {
	return projections.InsertThread(tx, t)
}

func listBoards(db *sql.DB) ([]Board, error) {
	return projections.ListBoards(db)
}

func listCategories(db *sql.DB) ([]Category, error) {
	return projections.ListCategories(db)
}

func listModerationReviews(db *sql.DB, status string, limit, offset int) ([]ModerationReview, error) {
	return projections.ListModerationReviews(db, status, limit, offset)
}

func listNotifications(db *sql.DB, userID string, limit, offset int, unreadOnly bool) ([]Notification, error) {
	return projections.ListNotifications(db, userID, limit, offset, unreadOnly)
}

func listPosts(db *sql.DB, threadID string, limit, offset int) ([]Post, error) {
	return projections.ListPosts(db, threadID, limit, offset)
}

func listPostsByAuthor(db *sql.DB, name string, limit, offset int) ([]Post, error) {
	return projections.ListPostsByAuthor(db, name, limit, offset)
}

func listPubkeyTitlesByUserName(db *sql.DB, username string) ([]string, error) {
	return projections.ListPubkeyTitlesByUserName(db, username)
}

func listThreads(db *sql.DB, boardID string, limit, offset int) ([]Thread, error) {
	return projections.ListThreads(db, boardID, limit, offset)
}

func listUserSanctions(db *sql.DB, userID string, limit, offset int) ([]UserSanction, error) {
	return projections.ListUserSanctions(db, userID, limit, offset)
}

func markAllNotificationsRead(db *sql.DB, userID string) error {
	return projections.MarkAllNotificationsRead(db, userID)
}

func markNotificationRead(db *sql.DB, id, userID string) error {
	return projections.MarkNotificationRead(db, id, userID)
}

func markPostPurged(tx *sql.Tx, postID string, seq int64) error {
	return projections.MarkPostPurged(tx, postID, seq)
}

func markPostRedacted(tx *sql.Tx, postID string, seq int64) error {
	return projections.MarkPostRedacted(tx, postID, seq)
}

func markPostRestored(tx *sql.Tx, postID string, seq int64) error {
	return projections.MarkPostRestored(tx, postID, seq)
}

func moveThreadBoard(tx *sql.Tx, threadID, toBoard string) error {
	return projections.MoveThreadBoard(tx, threadID, toBoard)
}

func pollsForPosts(db *sql.DB, postIDs []string, viewerUserID string) (map[string]*Poll, error) {
	return projections.PollsForPosts(db, postIDs, viewerUserID)
}

func reactionCount(db *sql.DB, postID string) (int, error) {
	return projections.ReactionCount(db, postID)
}

func reactionCountTx(tx *sql.Tx, postID string) (int, error) {
	return projections.ReactionCountTx(tx, postID)
}

func recomputeTrust(db *sql.DB, userID string) (int, int, error) {
	return projections.RecomputeTrust(db, userID)
}

func recordPostCreated(db *sql.DB, userID string) (int, int, error) {
	return projections.RecordPostCreated(db, userID)
}

func recordProcessed(tx *sql.Tx, actorID, cid, commandHash, resultJSON string) error {
	return projections.RecordProcessed(tx, actorID, cid, commandHash, resultJSON)
}

func recordReactionReceived(db *sql.DB, postAuthorID string) error {
	return projections.RecordReactionReceived(db, postAuthorID)
}

func recordReactionRemoved(db *sql.DB, postAuthorID string) error {
	return projections.RecordReactionRemoved(db, postAuthorID)
}

func resolveModerationReview(tx *sql.Tx, id, actor, resolution string, ts int64) error {
	return projections.ResolveModerationReview(tx, id, actor, resolution, ts)
}

func searchPosts(db *sql.DB, query, boardID string, limit int) ([]Post, error) {
	return projections.SearchPosts(db, query, boardID, limit)
}

func setThreadLocked(tx *sql.Tx, threadID string, locked bool) error {
	return projections.SetThreadLocked(tx, threadID, locked)
}

func setThreadPref(db *sql.DB, userID, threadID, level string) error {
	return projections.SetThreadPref(db, userID, threadID, level)
}

func setUserRole(tx *sql.Tx, userID, role string) error {
	return projections.SetUserRole(tx, userID, role)
}

func trustInfo(db *sql.DB, userID string) (*TrustLevelInfo, error) {
	return projections.TrustInfo(db, userID)
}

func updatePostBody(tx *sql.Tx, postID string, body string, seq int64) error {
	return projections.UpdatePostBody(tx, postID, body, seq)
}

func updateUserProfile(db *sql.DB, userID, displayName, bio, avatar string) error {
	return projections.UpdateUserProfile(db, userID, displayName, bio, avatar)
}

func upsertReaction(tx *sql.Tx, postID, userID, emoji string, ts int64) error {
	return projections.UpsertReaction(tx, postID, userID, emoji, ts)
}

func userReacted(db *sql.DB, postID, userID string) (bool, error) {
	return projections.UserReacted(db, postID, userID)
}

func userTrustLevel(db *sql.DB, userID string) (int, error) {
	return projections.UserTrustLevel(db, userID)
}

func watchersOfThread(db *sql.DB, threadID, excludeUserID string) ([]string, error) {
	return projections.WatchersOfThread(db, threadID, excludeUserID)
}
