package handler

import (
	"database/sql"

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

func setThreadLocked(tx *sql.Tx, threadID string, locked bool) error {
	return currentRuntime().SetThreadLocked(tx, threadID, locked)
}

func moveThreadBoard(tx *sql.Tx, threadID, toBoard string) error {
	return currentRuntime().MoveThreadBoard(tx, threadID, toBoard)
}

func setUserRole(tx *sql.Tx, userID, role string) error {
	return currentRuntime().SetUserRole(tx, userID, role)
}

func insertBoard(tx *sql.Tx, id, name, description string) error {
	return currentRuntime().InsertBoard(tx, id, name, description)
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
