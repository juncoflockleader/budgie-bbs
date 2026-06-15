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

func activeSanction(db *sql.DB, userID, scope string) (string, bool) {
	return projections.ActiveSanction(db, userID, scope)
}

func matchContentFilter(db *sql.DB, boardID, text string) (*ContentFilter, error) {
	return projections.MatchContentFilter(db, boardID, text)
}

func bumpThread(tx *sql.Tx, threadID string, seq int64) error {
	return projections.BumpThread(tx, threadID, seq)
}

func castVote(tx *sql.Tx, pollID, optionID, userID string, ts int64) error {
	return projections.CastVote(tx, pollID, optionID, userID, ts)
}

func checkProcessed(db *sql.DB, partitionKind, partitionKey, actorID, cid, commandHash string) (string, bool, bool) {
	return projections.CheckProcessed(db, partitionKind, partitionKey, actorID, cid, commandHash)
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

func deletePollVote(tx *sql.Tx, pollID, userID string) error {
	return projections.DeletePollVote(tx, pollID, userID)
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

func rebuildResidentFeedPosts(tx *sql.Tx) (int64, error) {
	return projections.RebuildResidentFeedPosts(tx)
}

func rebuildLatestFeedPosts(tx *sql.Tx) (int64, error) {
	return projections.RebuildLatestFeedPosts(tx)
}

func rebuildCommunityStatsSnapshot(tx *sql.Tx) (int64, error) {
	return projections.RebuildCommunityStatsSnapshot(tx)
}

func rebuildBoardSummaryStats(tx *sql.Tx) (int64, error) {
	return projections.RebuildBoardSummaryStats(tx)
}

func rebuildUnreadThreadSummaryStats(tx *sql.Tx) (int64, error) {
	return projections.RebuildUnreadThreadSummaryStats(tx)
}

func rebuildBoardRankingStats(tx *sql.Tx) (int64, error) {
	return projections.RebuildBoardRankingStats(tx)
}

func rebuildThreadRankingStats(tx *sql.Tx) (int64, error) {
	return projections.RebuildThreadRankingStats(tx)
}

func rebuildReplyRankingPosts(tx *sql.Tx) (int64, error) {
	return projections.RebuildReplyRankingPosts(tx)
}

func rebuildUserRankingStats(tx *sql.Tx) (int64, error) {
	return projections.RebuildUserRankingStats(tx)
}

func rebuildBlessingRankingStats(tx *sql.Tx) (int64, error) {
	return projections.RebuildBlessingRankingStats(tx)
}

func rebuildArchiveRankingStats(tx *sql.Tx) (int64, error) {
	return projections.RebuildArchiveRankingStats(tx)
}

func upsertResidentFeedPost(tx *sql.Tx, postID string) (bool, error) {
	return projections.UpsertResidentFeedPost(tx, postID)
}

func deleteResidentFeedPost(tx *sql.Tx, postID string) (bool, error) {
	return projections.DeleteResidentFeedPost(tx, postID)
}

func moveResidentFeedThread(tx *sql.Tx, threadID, toBoard string) (bool, error) {
	return projections.MoveResidentFeedThread(tx, threadID, toBoard)
}

func upsertLatestFeedPost(tx *sql.Tx, postID string) (bool, error) {
	return projections.UpsertLatestFeedPost(tx, postID)
}

func deleteLatestFeedPost(tx *sql.Tx, postID string) (bool, error) {
	return projections.DeleteLatestFeedPost(tx, postID)
}

func moveLatestFeedThread(tx *sql.Tx, threadID, toBoard string) (bool, error) {
	return projections.MoveLatestFeedThread(tx, threadID, toBoard)
}

func commandFtsDeletePost(tx *sql.Tx, postID string) error {
	if asyncPostSearchCommands.Load() {
		return nil
	}
	return ftsDeletePost(tx, postID)
}

func commandFtsInsertPost(tx *sql.Tx, postID, threadID, boardID, author, body string) error {
	if asyncPostSearchCommands.Load() {
		return nil
	}
	return ftsInsertPost(tx, postID, threadID, boardID, author, body)
}

func commandFtsUpdatePost(tx *sql.Tx, postID, newBody string) error {
	if asyncPostSearchCommands.Load() {
		return nil
	}
	return ftsUpdatePost(tx, postID, newBody)
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

func setPostFlags(tx *sql.Tx, postID string, marked, recommended, noReply, tex, mailBack bool, seq int64) error {
	return projections.SetPostFlags(tx, postID, marked, recommended, noReply, tex, mailBack, seq)
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

func insertBoard(tx *sql.Tx, id, name, description, parentID string, position int) error {
	return projections.InsertBoard(tx, id, name, description, parentID, position)
}

func insertModerationReview(tx *sql.Tx, id, kind, targetID, targetKind, reporter, reason string, ts int64) error {
	return projections.InsertModerationReview(tx, id, kind, targetID, targetKind, reporter, reason, ts)
}

func upsertContentFilter(tx *sql.Tx, id, pattern, scope string, active bool, createdBy string, ts int64) error {
	return projections.UpsertContentFilter(tx, id, pattern, scope, active, createdBy, ts)
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

func deleteBoardAutomodRule(tx *sql.Tx, board, id string) error {
	return projections.DeleteBoardAutomodRule(tx, board, id)
}

func listBoardAutomodRules(db *sql.DB, boardID string) ([]BoardAutomodRule, error) {
	return projections.ListBoardAutomodRules(db, boardID)
}

func insertNotification(db *sql.DB, id, userID, kind, threadID, postID, actor string, ts int64) error {
	return projections.InsertNotification(db, id, userID, kind, threadID, postID, actor, ts)
}

func insertNotificationTx(tx *sql.Tx, id, userID, kind, threadID, postID, actor string, ts int64) error {
	return projections.InsertNotification(tx, id, userID, kind, threadID, postID, actor, ts)
}

func insertPoll(tx *sql.Tx, id, postID, question string, expiresAt, ts int64) error {
	return projections.InsertPoll(tx, id, postID, question, expiresAt, ts)
}

func insertPollOption(tx *sql.Tx, id, pollID, text string, position int) error {
	return projections.InsertPollOption(tx, id, pollID, text, position)
}

func insertPostAttachment(tx *sql.Tx, id, postID, filename, contentType string, sizeBytes int64, url, createdBy string, createdAt int64) error {
	return projections.InsertPostAttachment(tx, id, postID, filename, contentType, sizeBytes, url, createdBy, createdAt)
}

func promoteStagedPostAttachmentBlob(tx *sql.Tx, stagingID, attachmentID string, expectedSize int64, contentType string) error {
	return projections.PromoteStagedAttachmentBlob(tx, projections.StagedBlobPostAttachment, stagingID, attachmentID, expectedSize, contentType)
}

func storeAttachmentBlob(db *sql.DB, attachmentID string, data []byte, contentType string) error {
	return projections.StoreAttachmentBlob(db, attachmentID, data, contentType)
}

func insertMailAttachment(tx *sql.Tx, id, mailID, filename, contentType string, sizeBytes int64, url, createdBy string, createdAt int64) error {
	return projections.InsertMailAttachment(tx, id, mailID, filename, contentType, sizeBytes, url, createdBy, createdAt)
}

func promoteStagedMailAttachmentBlob(tx *sql.Tx, stagingID, attachmentID string, expectedSize int64, contentType string) error {
	return projections.PromoteStagedAttachmentBlob(tx, projections.StagedBlobMailAttachment, stagingID, attachmentID, expectedSize, contentType)
}

func storeMailAttachmentBlob(db *sql.DB, attachmentID string, data []byte, contentType string) error {
	return projections.StoreMailAttachmentBlob(db, attachmentID, data, contentType)
}

func getPostAttachment(db *sql.DB, attachmentID string) (*PostAttachment, error) {
	return projections.GetPostAttachment(db, attachmentID)
}

func getAttachmentBlob(db *sql.DB, attachmentID string) ([]byte, string, error) {
	return projections.GetAttachmentBlob(db, attachmentID)
}

func getMailAttachment(db *sql.DB, attachmentID string) (*MailAttachment, error) {
	return projections.GetMailAttachment(db, attachmentID)
}

func getMailAttachmentBlob(db *sql.DB, attachmentID string) ([]byte, string, error) {
	return projections.GetMailAttachmentBlob(db, attachmentID)
}

func insertPost(tx *sql.Tx, p *Post) error {
	return projections.InsertPost(tx, p)
}

func insertSanction(tx *sql.Tx, id, userID, kind, scope string, expiresAt int64, by, reason string, seq int64) error {
	return projections.InsertSanction(tx, id, userID, kind, scope, expiresAt, by, reason, seq)
}

func clearUserSanctions(tx *sql.Tx, userID, kind, scope string) (int64, error) {
	return projections.ClearUserSanctions(tx, userID, kind, scope)
}

func insertThread(tx *sql.Tx, t *Thread) error {
	return projections.InsertThread(tx, t)
}

func insertMailMessage(tx *sql.Tx, id, fromUserID, subject, body, parentID string, createdAt, seq int64) error {
	return projections.InsertMailMessage(tx, id, fromUserID, subject, body, parentID, createdAt, seq)
}

func insertMailCopy(tx *sql.Tx, messageID, userID, role, mailbox string, read, kept bool, updatedAt int64) error {
	return projections.InsertMailCopy(tx, messageID, userID, role, mailbox, read, kept, updatedAt)
}

func updateMailCopy(db *sql.DB, userID, messageID string, mailbox *string, read, kept *bool) (bool, error) {
	return projections.UpdateMailCopy(db, userID, messageID, mailbox, read, kept)
}

func trashMailCopy(db *sql.DB, userID, messageID string) (bool, error) {
	return projections.TrashMailCopy(db, userID, messageID)
}

func insertDirectMessage(tx *sql.Tx, id, conversationID, fromUserID, toUserID, body string, createdAt, seq int64) error {
	return projections.InsertDirectMessage(tx, id, conversationID, fromUserID, toUserID, body, createdAt, seq)
}

func insertBlessing(tx *sql.Tx, blessing *Blessing) error {
	return projections.InsertBlessing(tx, blessing)
}

func markDirectMessageRead(db *sql.DB, userID, messageID string) (bool, error) {
	return projections.MarkDirectMessageRead(db, userID, messageID)
}

func deleteDirectMessage(db *sql.DB, userID, messageID string) (bool, error) {
	return projections.DeleteDirectMessage(db, userID, messageID)
}

func setUserRelationship(db *sql.DB, userID, targetUserID, kind, note string, active bool) error {
	return projections.SetUserRelationship(db, userID, targetUserID, kind, note, active)
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

func insertChatLine(db *sql.DB, id, roomID, roomName, userID, userName, body string, ts int64) error {
	return projections.InsertChatLine(db, id, roomID, roomName, userID, userName, body, ts)
}

func userIgnores(db *sql.DB, userID, targetUserID string) (bool, error) {
	return projections.UserIgnores(db, userID, targetUserID)
}

func listBoards(db *sql.DB) ([]Board, error) {
	return projections.ListBoards(db)
}

func getCommunityStats(db *sql.DB) (*CommunityStats, error) {
	return projections.GetCommunityStats(db)
}

func communityStatsSnapshotRowCount(db *sql.DB) (int, error) {
	return projections.CommunityStatsSnapshotRowCount(db)
}

func getCommunityStatsSnapshot(db *sql.DB) (*CommunityStats, error) {
	return projections.GetCommunityStatsSnapshot(db)
}

func listCommunityStatHistory(db *sql.DB, limit, offset int) ([]CommunityStatHistory, error) {
	return projections.ListCommunityStatHistory(db, limit, offset)
}

func listBoardRankings(db *sql.DB, viewerID string, includePrivate bool, limit, offset int) ([]BoardRanking, error) {
	return projections.ListBoardRankings(db, viewerID, includePrivate, limit, offset)
}

func boardRankingStatsRowCount(db *sql.DB) (int, error) {
	return projections.BoardRankingStatsRowCount(db)
}

func listBoardRankingsMaterialized(db *sql.DB, viewerID string, includePrivate bool, limit, offset int) ([]BoardRanking, error) {
	return projections.ListBoardRankingsMaterialized(db, viewerID, includePrivate, limit, offset)
}

func listThreadRankings(db *sql.DB, viewerID string, includePrivate bool, boardID string, limit, offset int) ([]ThreadRanking, error) {
	return projections.ListThreadRankings(db, viewerID, includePrivate, boardID, limit, offset)
}

func threadRankingStatsRowCount(db *sql.DB) (int, error) {
	return projections.ThreadRankingStatsRowCount(db)
}

func listThreadRankingsMaterialized(db *sql.DB, viewerID string, includePrivate bool, boardID string, limit, offset int) ([]ThreadRanking, error) {
	return projections.ListThreadRankingsMaterialized(db, viewerID, includePrivate, boardID, limit, offset)
}

func listReplyRankings(db *sql.DB, viewerID string, includePrivate bool, limit, offset int) ([]ReplyRanking, error) {
	return projections.ListReplyRankings(db, viewerID, includePrivate, limit, offset)
}

func replyRankingPostsRowCount(db *sql.DB) (int, error) {
	return projections.ReplyRankingPostsRowCount(db)
}

func listReplyRankingsMaterialized(db *sql.DB, viewerID string, includePrivate bool, limit, offset int) ([]ReplyRanking, error) {
	return projections.ListReplyRankingsMaterialized(db, viewerID, includePrivate, limit, offset)
}

func listUserRankings(db *sql.DB, limit, offset int) ([]UserRanking, error) {
	return projections.ListUserRankings(db, limit, offset)
}

func userRankingStatsRowCount(db *sql.DB) (int, error) {
	return projections.UserRankingStatsRowCount(db)
}

func listUserRankingsMaterialized(db *sql.DB, limit, offset int) ([]UserRanking, error) {
	return projections.ListUserRankingsMaterialized(db, limit, offset)
}

func listBlessingRankings(db *sql.DB, limit, offset int) ([]BlessingRanking, error) {
	return projections.ListBlessingRankings(db, limit, offset)
}

func blessingRankingStatsRowCount(db *sql.DB) (int, error) {
	return projections.BlessingRankingStatsRowCount(db)
}

func listBlessingRankingsMaterialized(db *sql.DB, limit, offset int) ([]BlessingRanking, error) {
	return projections.ListBlessingRankingsMaterialized(db, limit, offset)
}

func listBlessings(db *sql.DB, limit, offset int) ([]Blessing, error) {
	return projections.ListBlessings(db, limit, offset)
}

func listBoardModeratorTerms(db *sql.DB, boardID string, limit, offset int) ([]BoardModeratorTerm, error) {
	return projections.ListBoardModeratorTerms(db, boardID, limit, offset)
}

func listArchiveRankings(db *sql.DB, viewerID string, includePrivate bool, kind string, limit, offset int) ([]ArchiveRanking, error) {
	return projections.ListArchiveRankings(db, viewerID, includePrivate, kind, limit, offset)
}

func archiveRankingStatsRowCount(db *sql.DB) (int, error) {
	return projections.ArchiveRankingStatsRowCount(db)
}

func listArchiveRankingsMaterialized(db *sql.DB, viewerID string, includePrivate bool, kind string, limit, offset int) ([]ArchiveRanking, error) {
	return projections.ListArchiveRankingsMaterialized(db, viewerID, includePrivate, kind, limit, offset)
}

func listBoardSummaries(db *sql.DB, userID string, unreadOnly bool, opts ...BoardSummaryOptions) ([]BoardSummary, error) {
	return projections.ListBoardSummaries(db, userID, unreadOnly, opts...)
}

func boardSummaryStatsRowCount(db *sql.DB) (int, error) {
	return projections.BoardSummaryStatsRowCount(db)
}

func listBoardSummariesMaterialized(db *sql.DB, userID string, unreadOnly bool, opts ...BoardSummaryOptions) ([]BoardSummary, error) {
	return projections.ListBoardSummariesMaterialized(db, userID, unreadOnly, opts...)
}

func getBoardSettings(db *sql.DB, boardID string) (*BoardSettings, error) {
	return projections.GetBoardSettings(db, boardID)
}

func getBoardMemberRequirements(db *sql.DB, boardID string) (*BoardMemberRequirements, error) {
	return projections.GetBoardMemberRequirements(db, boardID)
}

func getBoardInfo(db *sql.DB, boardID string) (*BoardInfo, error) {
	return projections.GetBoardInfo(db, boardID)
}

func listBoardMembers(db *sql.DB, boardID string) ([]BoardMember, error) {
	return projections.ListBoardMembers(db, boardID)
}

func userIsBoardMember(db *sql.DB, boardID, userID string) (bool, error) {
	return projections.UserIsBoardMember(db, boardID, userID)
}

func latestBoardMemberApplicationStatus(db *sql.DB, boardID, userID string) (string, error) {
	return projections.LatestBoardMemberApplicationStatus(db, boardID, userID)
}

func getBoardMemberApplication(db *sql.DB, applicationID string) (*BoardMemberApplication, error) {
	return projections.GetBoardMemberApplication(db, applicationID)
}

func listBoardMemberApplications(db *sql.DB, boardID, status, userID string, limit, offset int) ([]BoardMemberApplication, error) {
	return projections.ListBoardMemberApplications(db, boardID, status, userID, limit, offset)
}

func listDigestEntries(db *sql.DB, boardID, kind, path string, limit, offset int) ([]DigestEntry, error) {
	return projections.ListDigestEntries(db, boardID, kind, path, limit, offset)
}

func listDigestPathTree(db *sql.DB, boardID, kind string) ([]DigestPathNode, error) {
	return projections.ListDigestPathTree(db, boardID, kind)
}

func listSiteDigestEntries(db *sql.DB, viewerID string, includePrivate bool, kind, path string, limit, offset int) ([]DigestEntry, error) {
	return projections.ListSiteDigestEntries(db, viewerID, includePrivate, kind, path, limit, offset)
}

func searchDigestEntries(db *sql.DB, viewerID string, includePrivate bool, boardID, kind, path, query string, limit, offset int) ([]DigestEntry, error) {
	return projections.SearchDigestEntries(db, viewerID, includePrivate, boardID, kind, path, query, limit, offset)
}

func getDigestExport(db *sql.DB, entryID string) (*DigestExport, error) {
	return projections.GetDigestExport(db, entryID)
}

func listMail(db *sql.DB, userID, mailbox string, limit, offset int, unreadOnly bool) ([]MailItem, error) {
	return projections.ListMail(db, userID, mailbox, limit, offset, unreadOnly)
}

func listMailThread(db *sql.DB, userID, messageID string, limit, offset int) ([]MailItem, error) {
	return projections.ListMailThread(db, userID, messageID, limit, offset)
}

func listMailByAuthor(db *sql.DB, userID, messageID string, limit, offset int) ([]MailItem, error) {
	return projections.ListMailByAuthor(db, userID, messageID, limit, offset)
}

func getMail(db *sql.DB, userID, messageID string) (*MailItem, error) {
	return projections.GetMail(db, userID, messageID)
}

func countUnreadMail(db *sql.DB, userID string) (int, error) {
	return projections.CountUnreadMail(db, userID)
}

func getMailUsage(db *sql.DB, userID string) (*MailUsage, error) {
	return projections.GetMailUsage(db, userID)
}

func listRelayDeliveries(db *sql.DB, status string, limit, offset int) ([]RelayDelivery, error) {
	return projections.ListRelayDeliveries(db, status, limit, offset)
}

func listMailGroups(db *sql.DB, ownerID string) ([]MailGroup, error) {
	return projections.ListMailGroups(db, ownerID)
}

func getMailGroupID(db *sql.DB, ownerID, groupRef string) (string, error) {
	return projections.GetMailGroupID(db, ownerID, groupRef)
}

func listMailGroupMembers(db *sql.DB, ownerID, groupRef string) ([]MailGroupMember, error) {
	return projections.ListMailGroupMembers(db, ownerID, groupRef)
}

func listFriendUserIDs(db *sql.DB, ownerID string) ([]string, error) {
	return projections.ListFriendUserIDs(db, ownerID)
}

func listLoginWatchers(db *sql.DB, targetUserID string) ([]string, error) {
	return projections.ListLoginWatchers(db, targetUserID)
}

func listDirectMessageConversations(db *sql.DB, userID string, limit, offset int) ([]DirectMessageConversation, error) {
	return projections.ListDirectMessageConversations(db, userID, limit, offset)
}

func listDirectMessages(db *sql.DB, userID, otherUserID string, limit, offset int) ([]DirectMessage, error) {
	return projections.ListDirectMessages(db, userID, otherUserID, limit, offset)
}

func countUnreadDirectMessages(db *sql.DB, userID string) (int, error) {
	return projections.CountUnreadDirectMessages(db, userID)
}

func getDirectMessageSettings(db *sql.DB, userID string) (*DirectMessageSettings, error) {
	return projections.GetDirectMessageSettings(db, userID)
}

func listSocialUsers(db *sql.DB, userID, list string, onlineOnly bool) ([]SocialUser, error) {
	return projections.ListSocialUsers(db, userID, list, onlineOnly)
}

func listOnlineUsers(db *sql.DB, viewerID, boardID string, limit, offset int) ([]SocialUser, error) {
	return projections.ListOnlineUsers(db, viewerID, boardID, limit, offset)
}

func listChatRooms(db *sql.DB) ([]ChatRoom, error) {
	return projections.ListChatRooms(db)
}

func listChatLines(db *sql.DB, roomID string, limit int) ([]ChatLine, error) {
	return projections.ListChatLines(db, roomID, limit)
}

func listChatOnlineUsers(db *sql.DB, viewerID, roomID string, limit, offset int) ([]SocialUser, error) {
	return projections.ListChatOnlineUsers(db, viewerID, roomID, limit, offset)
}

func listFavoriteBoards(db *sql.DB, userID string) ([]Board, error) {
	return projections.ListFavoriteBoards(db, userID)
}

func listFavoriteTree(db *sql.DB, userID string) (*FavoriteTree, error) {
	return projections.ListFavoriteTree(db, userID)
}

func listCategories(db *sql.DB) ([]Category, error) {
	return projections.ListCategories(db)
}

func listModerationReviews(db *sql.DB, status string, limit, offset int) ([]ModerationReview, error) {
	return projections.ListModerationReviews(db, status, limit, offset)
}

func listContentFilters(db *sql.DB, scope string, includeInactive bool, limit, offset int) ([]ContentFilter, error) {
	return projections.ListContentFilters(db, scope, includeInactive, limit, offset)
}

func listNotifications(db *sql.DB, userID string, limit, offset int, unreadOnly bool) ([]Notification, error) {
	return projections.ListNotifications(db, userID, limit, offset, unreadOnly)
}

func listPosts(db *sql.DB, threadID string, limit, offset int) ([]Post, error) {
	return projections.ListPosts(db, threadID, limit, offset)
}

func listPostAttachments(db *sql.DB, postID string) ([]PostAttachment, error) {
	return projections.ListPostAttachments(db, postID)
}

func listReplyTreePosts(db *sql.DB, rootPostID string, limit, offset int) ([]Post, error) {
	return projections.ListReplyTreePosts(db, rootPostID, limit, offset)
}

func listPostsByAuthor(db *sql.DB, name string, limit, offset int) ([]Post, error) {
	return projections.ListPostsByAuthor(db, name, limit, offset)
}

func listReadablePostsByAuthor(db *sql.DB, viewerID string, includePrivate bool, name string, limit, offset int) ([]Post, error) {
	return projections.ListReadablePostsByAuthor(db, viewerID, includePrivate, name, limit, offset)
}

func listResidentBoardPosts(db *sql.DB, userID string, limit, offset int) ([]Post, error) {
	return projections.ListResidentBoardPosts(db, userID, limit, offset)
}

func residentFeedMaterializedRowCount(db *sql.DB) (int, error) {
	return projections.ResidentFeedMaterializedRowCount(db)
}

func listResidentBoardPostsMaterialized(db *sql.DB, userID string, limit, offset int) ([]Post, error) {
	return projections.ListResidentBoardPostsMaterialized(db, userID, limit, offset)
}

func listLatestFeedPosts(db *sql.DB, viewerID string, includePrivate bool, limit, offset int) ([]Post, error) {
	return projections.ListLatestFeedPosts(db, viewerID, includePrivate, limit, offset)
}

func latestFeedMaterializedRowCount(db *sql.DB) (int, error) {
	return projections.LatestFeedMaterializedRowCount(db)
}

func listLatestFeedPostsMaterialized(db *sql.DB, viewerID string, includePrivate bool, limit, offset int) ([]Post, error) {
	return projections.ListLatestFeedPostsMaterialized(db, viewerID, includePrivate, limit, offset)
}

func listBoardDeletedPosts(db *sql.DB, boardID, kind string, limit, offset int) ([]PostDeletion, error) {
	return projections.ListBoardDeletedPosts(db, boardID, kind, limit, offset)
}

func listPubkeyTitlesByUserName(db *sql.DB, username string) ([]string, error) {
	return projections.ListPubkeyTitlesByUserName(db, username)
}

func listThreads(db *sql.DB, boardID string, limit, offset int) ([]Thread, error) {
	return projections.ListThreads(db, boardID, limit, offset)
}

func listThreadSummaries(db *sql.DB, userID, boardID string, limit, offset int, unreadOnly bool) ([]ThreadSummary, error) {
	return projections.ListThreadSummaries(db, userID, boardID, limit, offset, unreadOnly)
}

func listThreadSummariesFiltered(db *sql.DB, userID, boardID, titleQuery, authorQuery string, limit, offset int, unreadOnly bool) ([]ThreadSummary, error) {
	return projections.ListThreadSummariesFiltered(db, userID, boardID, titleQuery, authorQuery, limit, offset, unreadOnly)
}

func listUnreadThreadSummaries(db *sql.DB, userID string, includePrivate bool, favoritesOnly bool, folderID string, limit, offset int) ([]ThreadSummary, error) {
	return projections.ListUnreadThreadSummaries(db, userID, includePrivate, favoritesOnly, folderID, limit, offset)
}

func unreadThreadSummaryStatsRowCount(db *sql.DB) (int, error) {
	return projections.UnreadThreadSummaryStatsRowCount(db)
}

func listUnreadThreadSummariesMaterialized(db *sql.DB, userID string, includePrivate bool, favoritesOnly bool, folderID string, limit, offset int) ([]ThreadSummary, error) {
	return projections.ListUnreadThreadSummariesMaterialized(db, userID, includePrivate, favoritesOnly, folderID, limit, offset)
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

func deleteNotification(db *sql.DB, id, userID string) error {
	return projections.DeleteNotification(db, id, userID)
}

func deleteReadNotifications(db *sql.DB, userID string) error {
	return projections.DeleteReadNotifications(db, userID)
}

func deleteAllNotifications(db *sql.DB, userID string) error {
	return projections.DeleteAllNotifications(db, userID)
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

func recordPostDeletion(tx *sql.Tx, postID, threadID, boardID, deletedByID, deletedByName, reason, kind string, deletedAt, seq int64) error {
	return projections.RecordPostDeletion(tx, postID, threadID, boardID, deletedByID, deletedByName, reason, kind, deletedAt, seq)
}

func clearPostDeletion(tx *sql.Tx, postID string) error {
	return projections.ClearPostDeletion(tx, postID)
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

func recordLogin(db *sql.DB, userID string) error {
	return projections.RecordLogin(db, userID)
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

func recordPostCreated(db *sql.DB, userID string) (int, int, error) {
	return projections.RecordPostCreated(db, userID)
}

func recordProcessed(tx *sql.Tx, partitionKind, partitionKey, actorID, cid, commandHash, resultJSON string) error {
	return projections.RecordProcessed(tx, partitionKind, partitionKey, actorID, cid, commandHash, resultJSON)
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

func searchReadablePosts(db *sql.DB, viewerID string, includePrivate bool, query, boardID string, limit int) ([]Post, error) {
	return projections.SearchReadablePosts(db, viewerID, includePrivate, query, boardID, limit)
}

func setThreadLocked(tx *sql.Tx, threadID string, locked bool) error {
	return projections.SetThreadLocked(tx, threadID, locked)
}

func setThreadTitle(tx *sql.Tx, threadID, title string, ts int64) error {
	return projections.SetThreadTitle(tx, threadID, title, ts)
}

func setThreadPref(db *sql.DB, userID, threadID, level string) error {
	return projections.SetThreadPref(db, userID, threadID, level)
}

func setBoardFavorite(db *sql.DB, userID, boardID, folderID string, position *int, favorite bool) error {
	return projections.SetBoardFavorite(db, userID, boardID, folderID, position, favorite)
}

func setBoardZap(db *sql.DB, userID, boardID string, zapped bool) error {
	return projections.SetBoardZap(db, userID, boardID, zapped)
}

func createFavoriteFolder(db *sql.DB, userID, folderID, parentID, name string, position *int) error {
	return projections.CreateFavoriteFolder(db, userID, folderID, parentID, name, position)
}

func updateFavoriteFolder(db *sql.DB, userID, folderID, name string, parentID *string, position *int) error {
	return projections.UpdateFavoriteFolder(db, userID, folderID, name, parentID, position)
}

func deleteFavoriteFolder(db *sql.DB, userID, folderID string) error {
	return projections.DeleteFavoriteFolder(db, userID, folderID)
}

func moveBoardFavorite(db *sql.DB, userID, boardID, folderID string, position *int) error {
	return projections.MoveBoardFavorite(db, userID, boardID, folderID, position)
}

func importFavoriteTree(db *sql.DB, userID string, tree *FavoriteTree, replace bool) error {
	return projections.ImportFavoriteTree(db, userID, tree, replace, newID)
}

func setBoardSettings(db *sql.DB, boardID string, patch BoardSettingsPatch) error {
	return projections.SetBoardSettings(db, boardID, patch)
}

func setRecommendedBoard(db *sql.DB, boardID, note, curatedBy string, position *int, recommended bool) error {
	return projections.SetRecommendedBoard(db, boardID, note, curatedBy, position, recommended)
}

func setBoardMemberRequirements(db *sql.DB, boardID string, patch BoardMemberRequirementsPatch) error {
	return projections.SetBoardMemberRequirements(db, boardID, patch)
}

func setBoardModerator(db *sql.DB, boardID, userID, actorID string, moderator bool, position *int) error {
	return projections.SetBoardModerator(db, boardID, userID, actorID, moderator, position)
}

func setBoardMember(db *sql.DB, boardID, userID string, member bool, patch BoardMemberPatch) error {
	return projections.SetBoardMember(db, boardID, userID, member, patch)
}

func insertBoardMemberApplication(db *sql.DB, id, boardID, userID, note string) error {
	return projections.InsertBoardMemberApplication(db, id, boardID, userID, note)
}

func reviewBoardMemberApplication(db *sql.DB, applicationID, reviewerID, status, title, reviewNote string) error {
	return projections.ReviewBoardMemberApplication(db, applicationID, reviewerID, status, title, reviewNote)
}

func upsertDigestEntry(db *sql.DB, id, boardID, targetKind, targetID, kind, title, path, note, createdBy string) (string, error) {
	return projections.UpsertDigestEntry(db, id, boardID, targetKind, targetID, kind, title, path, note, createdBy)
}

func upsertDigestEntryTx(tx *sql.Tx, id, boardID, targetKind, targetID, kind, title, path, note, createdBy string, ts int64) (string, error) {
	return projections.UpsertDigestEntryTx(tx, id, boardID, targetKind, targetID, kind, title, path, note, createdBy, ts)
}

func removeDigestEntry(db *sql.DB, id string) error {
	return projections.RemoveDigestEntry(db, id)
}

func removeDigestEntryTx(tx *sql.Tx, id string) error {
	return projections.RemoveDigestEntryTx(tx, id)
}

func removeDigestEntryFinalTx(tx *sql.Tx, id, boardID, kind, removedBy string, ts int64) error {
	return projections.RemoveDigestEntryFinalTx(tx, id, boardID, kind, removedBy, ts)
}

func updateDigestEntry(db *sql.DB, id, title, path, note string) error {
	return projections.UpdateDigestEntry(db, id, title, path, note)
}

func updateDigestEntryTx(tx *sql.Tx, id, title, path, note string, ts int64) error {
	return projections.UpdateDigestEntryTx(tx, id, title, path, note, ts)
}

func setDigestEntryBody(db *sql.DB, id, body string, edited bool) error {
	return projections.SetDigestEntryBody(db, id, body, edited)
}

func setDigestEntryBodyTx(tx *sql.Tx, id, body string, edited bool, ts int64) error {
	return projections.SetDigestEntryBodyTx(tx, id, body, edited, ts)
}

func upsertDigestDirectory(db *sql.DB, id, boardID, kind, path, createdBy string) (string, error) {
	return projections.UpsertDigestDirectory(db, id, boardID, kind, path, createdBy)
}

func upsertDigestDirectoryTx(tx *sql.Tx, id, boardID, kind, path, createdBy string, ts int64) (string, error) {
	return projections.UpsertDigestDirectoryTx(tx, id, boardID, kind, path, createdBy, ts)
}

func countDigestPathEntries(db *sql.DB, boardID, kind, path string) (int, error) {
	return projections.CountDigestPathEntries(db, boardID, kind, path)
}

func countDigestPathDirectories(db *sql.DB, boardID, kind, path string) (int, error) {
	return projections.CountDigestPathDirectories(db, boardID, kind, path)
}

func moveDigestPath(db *sql.DB, boardID, kind, fromPath, toPath string) (int, error) {
	return projections.MoveDigestPath(db, boardID, kind, fromPath, toPath)
}

func moveDigestPathTx(tx *sql.Tx, boardID, kind, fromPath, toPath string, ts int64) (int, error) {
	return projections.MoveDigestPathTx(tx, boardID, kind, fromPath, toPath, ts)
}

func moveDigestPathFinalTx(tx *sql.Tx, eventID, boardID, kind, fromPath, toPath, actorID string, ts int64) (int, error) {
	return projections.MoveDigestPathFinalTx(tx, eventID, boardID, kind, fromPath, toPath, actorID, ts)
}

func copyDigestPath(db *sql.DB, boardID, kind, fromPath, toPath, createdBy string, entryIDs, directoryIDs []string) (int, error) {
	return projections.CopyDigestPath(db, boardID, kind, fromPath, toPath, createdBy, entryIDs, directoryIDs)
}

func copyDigestPathTx(tx *sql.Tx, boardID, kind, fromPath, toPath, createdBy string, entryIDs, directoryIDs []string, ts int64) (int, error) {
	return projections.CopyDigestPathTx(tx, boardID, kind, fromPath, toPath, createdBy, entryIDs, directoryIDs, ts)
}

func deleteDigestPath(db *sql.DB, boardID, kind, path string) (int, error) {
	return projections.DeleteDigestPath(db, boardID, kind, path)
}

func deleteDigestPathTx(tx *sql.Tx, boardID, kind, path string) (int, error) {
	return projections.DeleteDigestPathTx(tx, boardID, kind, path)
}

func deleteDigestPathFinalTx(tx *sql.Tx, eventID, boardID, kind, path, actorID string, ts int64) (int, error) {
	return projections.DeleteDigestPathFinalTx(tx, eventID, boardID, kind, path, actorID, ts)
}

func markBoardRead(db *sql.DB, userID, boardID string) error {
	return projections.MarkBoardRead(db, userID, boardID)
}

func restoreBoardRead(db *sql.DB, userID, boardID string) error {
	return projections.RestoreBoardRead(db, userID, boardID)
}

func markFavoriteFolderRead(db *sql.DB, userID, folderID string) error {
	return projections.MarkFavoriteFolderRead(db, userID, folderID)
}

func restoreFavoriteFolderRead(db *sql.DB, userID, folderID string) error {
	return projections.RestoreFavoriteFolderRead(db, userID, folderID)
}

func markThreadRead(db *sql.DB, userID, threadID string) error {
	return projections.MarkThreadRead(db, userID, threadID)
}

func restoreThreadRead(db *sql.DB, userID, threadID string) error {
	return projections.RestoreThreadRead(db, userID, threadID)
}

func markPostRead(db *sql.DB, userID, postID string) error {
	return projections.MarkPostRead(db, userID, postID)
}

func setMailGroup(db *sql.DB, ownerID, groupID, name string, memberIDs []string) error {
	return projections.SetMailGroup(db, ownerID, groupID, name, memberIDs)
}

func deleteMailGroup(db *sql.DB, ownerID, groupID string) (bool, error) {
	return projections.DeleteMailGroup(db, ownerID, groupID)
}

func setDirectMessageSettings(db *sql.DB, userID, policy string) error {
	return projections.SetDirectMessageSettings(db, userID, policy)
}

func setUserRole(tx *sql.Tx, userID, role string) error {
	return projections.SetUserRole(tx, userID, role)
}

func transferUserID(db *sql.DB, userID, newName string) (*User, error) {
	return projections.TransferUserID(db, userID, newName)
}

func trustInfo(db *sql.DB, userID string) (*TrustLevelInfo, error) {
	return projections.TrustInfo(db, userID)
}

func updatePostBody(tx *sql.Tx, postID string, body string, seq int64) error {
	return projections.UpdatePostBody(tx, postID, body, seq)
}

func updateUserProfile(db *sql.DB, userID, displayName, title, bio, avatar, signature, plan, homepage string) error {
	return projections.UpdateUserProfile(db, userID, displayName, title, bio, avatar, signature, plan, homepage)
}

func getUserPrivateProfile(db *sql.DB, userID string) (*UserPrivateProfile, error) {
	return projections.GetUserPrivateProfile(db, userID)
}

func updateUserPrivateProfile(db *sql.DB, profile *UserPrivateProfile) error {
	return projections.UpdateUserPrivateProfile(db, profile)
}

func getAccountRegistrationSettings(db *sql.DB) (*AccountRegistrationSettings, error) {
	return projections.GetAccountRegistrationSettings(db)
}

func setAccountRegistrationSettings(db *sql.DB, requireApproval bool) (*AccountRegistrationSettings, error) {
	return projections.SetAccountRegistrationSettings(db, requireApproval)
}

func listAccountRegistrations(db *sql.DB, status string, limit, offset int) ([]AccountRegistration, error) {
	return projections.ListAccountRegistrations(db, status, limit, offset)
}

func reviewAccountRegistration(db *sql.DB, userID, reviewerID, decision, reason string) (*AccountRegistration, error) {
	return projections.ReviewAccountRegistration(db, userID, reviewerID, decision, reason)
}

func createPasswordRecoveryRequest(db *sql.DB, id, userID, submittedName, submittedEmail, note string) (*PasswordRecoveryRequest, error) {
	return projections.CreatePasswordRecoveryRequest(db, id, userID, submittedName, submittedEmail, note)
}

func listPasswordRecoveryRequests(db *sql.DB, status string, limit, offset int) ([]PasswordRecoveryRequest, error) {
	return projections.ListPasswordRecoveryRequests(db, status, limit, offset)
}

func reviewPasswordRecoveryRequest(db *sql.DB, id, reviewerID, decision, passwordHash, note string) (*PasswordRecoveryRequest, error) {
	return projections.ReviewPasswordRecoveryRequest(db, id, reviewerID, decision, passwordHash, note)
}

func listUserPersonalFiles(db *sql.DB, userID string, includePrivate bool) ([]UserPersonalFile, error) {
	return projections.ListUserPersonalFiles(db, userID, includePrivate)
}

func getUserPersonalFile(db *sql.DB, userID, name string, includePrivate bool) (*UserPersonalFile, error) {
	return projections.GetUserPersonalFile(db, userID, name, includePrivate)
}

func saveUserPersonalFile(db *sql.DB, userID, name, body string, public bool) (*UserPersonalFile, error) {
	return projections.SaveUserPersonalFile(db, userID, name, body, public)
}

func deleteUserPersonalFile(db *sql.DB, userID, name string) error {
	return projections.DeleteUserPersonalFile(db, userID, name)
}

func recountUserSignatures(db *sql.DB, userID string) (*UserSignatureRecount, error) {
	return projections.RecountUserSignatures(db, userID)
}

func insertRelayDelivery(tx *sql.Tx, id, boardID, threadID, postID, authorID, authorName, title, body string, createdAt, seq int64) error {
	return projections.InsertRelayDelivery(tx, id, boardID, threadID, postID, authorID, authorName, title, body, createdAt, seq)
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

func watchersOfThreadTx(tx *sql.Tx, threadID, excludeUserID string) ([]string, error) {
	return projections.WatchersOfThread(tx, threadID, excludeUserID)
}
