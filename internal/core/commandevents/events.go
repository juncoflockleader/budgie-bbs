package commandevents

import "github.com/juncoflockleader/budgie-bbs/internal/proto"

const LoginWatchRelationshipKind = "login_watch"

func CommunityStatsSnapshotRecorded(payload *proto.CommunityStatsSnapshotRecordedPayload) ([]string, *proto.CommunityStatsSnapshotRecordedPayload) {
	return nil, payload
}

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

func BoardCreated(boardID, name, description, parentID string, position int, by string, ts int64) ([]string, *proto.BoardCreatedPayload) {
	return []string{"board:" + boardID}, &proto.BoardCreatedPayload{
		ID:          boardID,
		Name:        name,
		Description: description,
		ParentID:    parentID,
		Position:    position,
		By:          by,
		TS:          ts,
	}
}

type BoardSettingsSetSpec proto.BoardSettingsSetPayload

func BoardSettingsSet(spec BoardSettingsSetSpec) ([]string, *proto.BoardSettingsSetPayload) {
	payload := proto.BoardSettingsSetPayload(spec)
	return []string{"board:" + payload.Board}, &payload
}

type BoardMemberRequirementsSetSpec proto.BoardMemberRequirementsSetPayload

func BoardMemberRequirementsSet(spec BoardMemberRequirementsSetSpec) ([]string, *proto.BoardMemberRequirementsSetPayload) {
	payload := proto.BoardMemberRequirementsSetPayload(spec)
	return []string{"board:" + payload.Board}, &payload
}

func BoardRecommendedSet(boardID string, recommended bool, note string, position int, curatedBy string, ts int64) ([]string, *proto.BoardRecommendedSetPayload) {
	return []string{"board:" + boardID}, &proto.BoardRecommendedSetPayload{
		Board:       boardID,
		Recommended: recommended,
		Note:        note,
		Position:    position,
		CuratedBy:   curatedBy,
		TS:          ts,
	}
}

func BoardModeratorSet(boardID, userID string, moderator bool, position int, by string, ts int64) ([]string, *proto.BoardModeratorSetPayload) {
	return []string{"board:" + boardID, "user:" + userID}, &proto.BoardModeratorSetPayload{
		Board:     boardID,
		User:      userID,
		Moderator: moderator,
		Position:  position,
		By:        by,
		TS:        ts,
	}
}

type BoardMemberSetSpec proto.BoardMemberSetPayload

func BoardMemberSet(spec BoardMemberSetSpec) ([]string, *proto.BoardMemberSetPayload) {
	payload := proto.BoardMemberSetPayload(spec)
	return []string{"board:" + payload.Board, "user:" + payload.User}, &payload
}

func BoardMemberApplicationSubmitted(applicationID, boardID, userID, note string, ts int64) ([]string, *proto.BoardMemberApplicationSubmittedPayload) {
	return []string{"board:" + boardID, "user:" + userID}, &proto.BoardMemberApplicationSubmittedPayload{
		ID:    applicationID,
		Board: boardID,
		User:  userID,
		Note:  note,
		TS:    ts,
	}
}

func BoardMemberApplicationReviewed(applicationID, boardID, userID, status, title, reviewer, note string, ts int64) ([]string, *proto.BoardMemberApplicationReviewedPayload) {
	return []string{"board:" + boardID, "user:" + userID}, &proto.BoardMemberApplicationReviewedPayload{
		Application: applicationID,
		Board:       boardID,
		User:        userID,
		Status:      status,
		Title:       title,
		Reviewer:    reviewer,
		ReviewNote:  note,
		TS:          ts,
	}
}

func PostFlagged(reviewID, kind, postID, threadID, reporter, reason string, ts int64) ([]string, *proto.PostFlaggedPayload) {
	return []string{"moderation:global"}, &proto.PostFlaggedPayload{
		ReviewID: reviewID,
		Kind:     kind,
		PostID:   postID,
		Thread:   threadID,
		Reporter: reporter,
		Reason:   reason,
		TS:       ts,
	}
}

func ContentFilterSet(filterID, pattern, scope string, active bool, by string, ts int64) ([]string, *proto.ContentFilterSetPayload) {
	scopes := []string{"moderation:global"}
	if scope != proto.DefaultContentFilterScope {
		scopes = append(scopes, "board:"+scope)
	}
	return scopes, &proto.ContentFilterSetPayload{
		ID:      filterID,
		Pattern: pattern,
		Scope:   scope,
		Active:  active,
		By:      by,
		TS:      ts,
	}
}

// UserSanctioned keeps delivery scopes keyed by accountUserID while allowing
// the payload User field to preserve the caller's durable/live representation.
func UserSanctioned(accountUserID, payloadUser, kind, scope string, durationSec int64, by, reason string, ts int64) ([]string, *proto.UserSanctionedPayload) {
	return []string{"account:" + accountUserID}, &proto.UserSanctionedPayload{
		User:        payloadUser,
		Kind:        kind,
		Scope:       scope,
		DurationSec: durationSec,
		By:          by,
		Reason:      reason,
		TS:          ts,
	}
}

// UserSanctionCleared mirrors UserSanctioned's account-scope/payload split.
func UserSanctionCleared(accountUserID, payloadUser, kind, scope, by, reason string, ts int64) ([]string, *proto.UserSanctionClearedPayload) {
	return []string{"account:" + accountUserID}, &proto.UserSanctionClearedPayload{
		User:   payloadUser,
		Kind:   kind,
		Scope:  scope,
		By:     by,
		Reason: reason,
		TS:     ts,
	}
}

func ThreadTitleSet(threadID, boardID, title, by string, ts int64) ([]string, *proto.ThreadTitleSetPayload) {
	return []string{"board:" + boardID, "thread:" + threadID}, &proto.ThreadTitleSetPayload{
		Thread: threadID,
		Title:  title,
		By:     by,
		TS:     ts,
	}
}

func ThreadLocked(threadID, boardID string, locked bool, by string, ts int64) ([]string, *proto.ThreadLockedPayload) {
	return []string{"board:" + boardID, "thread:" + threadID}, &proto.ThreadLockedPayload{
		Thread: threadID,
		Locked: locked,
		By:     by,
		TS:     ts,
	}
}

func ThreadMoved(threadID, fromBoardID, toBoardID, by string, ts int64) ([]string, *proto.ThreadMovedPayload) {
	return []string{"board:" + fromBoardID, "board:" + toBoardID}, &proto.ThreadMovedPayload{
		Thread:    threadID,
		FromBoard: fromBoardID,
		ToBoard:   toBoardID,
		By:        by,
		TS:        ts,
	}
}

func ThreadNew(threadID, boardID, authorName, authorID, title string, ts int64) ([]string, *proto.ThreadNewPayload) {
	return []string{"board:" + boardID}, &proto.ThreadNewPayload{
		ID:       threadID,
		Board:    boardID,
		Author:   authorName,
		AuthorID: authorID,
		Title:    title,
		TS:       ts,
	}
}

type PostAppendedSpec proto.PostAppendedPayload

func PostAppended(boardID string, spec PostAppendedSpec) ([]string, *proto.PostAppendedPayload) {
	payload := proto.PostAppendedPayload(spec)
	return []string{"board:" + boardID, "thread:" + payload.Thread}, &payload
}

func PostRedacted(postID, threadID, boardID, by, reason, deletionKind string, ts int64) ([]string, *proto.PostRedactedPayload) {
	return []string{"thread:" + threadID, "board:" + boardID}, &proto.PostRedactedPayload{
		ID:           postID,
		Thread:       threadID,
		By:           by,
		Reason:       reason,
		DeletionKind: deletionKind,
		TS:           ts,
	}
}

func PostRestored(postID, threadID, boardID, by string, ts int64) ([]string, *proto.PostRestoredPayload) {
	return []string{"thread:" + threadID, "board:" + boardID}, &proto.PostRestoredPayload{
		ID:     postID,
		Thread: threadID,
		By:     by,
		TS:     ts,
	}
}

func PostDeletionCleared(postID, threadID, boardID, kind, by string, ts int64) ([]string, *proto.PostDeletionClearedPayload) {
	return []string{"thread:" + threadID, "board:" + boardID}, &proto.PostDeletionClearedPayload{
		ID:     postID,
		Thread: threadID,
		Board:  boardID,
		Kind:   kind,
		By:     by,
		TS:     ts,
	}
}

func PostAttachmentAdded(attachmentID, postID, threadID, boardID, filename, contentType string, sizeBytes int64, authorID, stagedBlobID string, ts int64) ([]string, *proto.PostAttachmentAddedPayload) {
	return []string{"board:" + boardID, "thread:" + threadID}, &proto.PostAttachmentAddedPayload{
		ID:           attachmentID,
		Post:         postID,
		Thread:       threadID,
		Filename:     filename,
		ContentType:  contentType,
		SizeBytes:    sizeBytes,
		AuthorID:     authorID,
		StagedBlobID: stagedBlobID,
		TS:           ts,
	}
}

func PostEdited(postID, threadID, boardID, body string, version int, ts int64) ([]string, *proto.PostEditedPayload) {
	return []string{"thread:" + threadID, "board:" + boardID}, &proto.PostEditedPayload{
		ID:      postID,
		Thread:  threadID,
		NewBody: body,
		Version: version,
		TS:      ts,
	}
}

func PostFlagsSet(postID, threadID, boardID string, marked, recommended, noReply, tex, mailBack bool, by string, ts int64) ([]string, *proto.PostFlagsSetPayload) {
	return []string{"thread:" + threadID, "board:" + boardID}, &proto.PostFlagsSetPayload{
		ID:          postID,
		Thread:      threadID,
		Marked:      marked,
		Recommended: recommended,
		NoReply:     noReply,
		TeX:         tex,
		MailBack:    mailBack,
		By:          by,
		TS:          ts,
	}
}

func PostPurged(postID, threadID, boardID, by, reason string, ts int64) ([]string, *proto.PostPurgedPayload) {
	return []string{"thread:" + threadID, "board:" + boardID}, &proto.PostPurgedPayload{
		ID:     postID,
		Thread: threadID,
		By:     by,
		Reason: reason,
		TS:     ts,
	}
}

func PostReacted(postID, threadID, boardID, user, emoji string, reactionCount int, ts int64) ([]string, *proto.PostReactedPayload) {
	return []string{"thread:" + threadID, "board:" + boardID}, &proto.PostReactedPayload{
		PostID:        postID,
		Thread:        threadID,
		User:          user,
		Emoji:         emoji,
		ReactionCount: reactionCount,
		TS:            ts,
	}
}

func PostUnreacted(postID, threadID, boardID, user, emoji string, reactionCount int, ts int64) ([]string, *proto.PostUnreactedPayload) {
	return []string{"thread:" + threadID, "board:" + boardID}, &proto.PostUnreactedPayload{
		PostID:        postID,
		Thread:        threadID,
		User:          user,
		Emoji:         emoji,
		ReactionCount: reactionCount,
		TS:            ts,
	}
}

func PollVoted(pollID, optionID, threadID, boardID, user string, ts int64) ([]string, *proto.PollVotedPayload) {
	return []string{"thread:" + threadID, "board:" + boardID}, &proto.PollVotedPayload{
		Poll:   pollID,
		Option: optionID,
		User:   user,
		TS:     ts,
	}
}

type DigestEntryUpsertedSpec proto.DigestEntryUpsertedPayload

func DigestEntryUpserted(spec DigestEntryUpsertedSpec) ([]string, *proto.DigestEntryUpsertedPayload) {
	payload := proto.DigestEntryUpsertedPayload(spec)
	return proto.DigestEventScopes(payload.Board), &payload
}

type DigestEntryUpdatedSpec proto.DigestEntryUpdatedPayload

func DigestEntryUpdated(spec DigestEntryUpdatedSpec) ([]string, *proto.DigestEntryUpdatedPayload) {
	payload := proto.DigestEntryUpdatedPayload(spec)
	return proto.DigestEventScopes(payload.Board), &payload
}

func DigestEntryBodySet(entryID, boardID, kind, body string, edited bool, by string, ts int64) ([]string, *proto.DigestEntryBodySetPayload) {
	return proto.DigestEventScopes(boardID), &proto.DigestEntryBodySetPayload{
		ID:     entryID,
		Board:  boardID,
		Kind:   kind,
		Body:   body,
		Edited: edited,
		By:     by,
		TS:     ts,
	}
}

func DigestEntryRemoved(entryID, boardID, kind, by string, ts int64) ([]string, *proto.DigestEntryRemovedPayload) {
	return proto.DigestEventScopes(boardID), &proto.DigestEntryRemovedPayload{ID: entryID, Board: boardID, Kind: kind, By: by, TS: ts}
}

func DigestDirectorySet(directoryID, boardID, kind, path, createdBy string, ts int64) ([]string, *proto.DigestDirectorySetPayload) {
	return proto.DigestEventScopes(boardID), &proto.DigestDirectorySetPayload{
		ID:        directoryID,
		Board:     boardID,
		Kind:      kind,
		Path:      path,
		CreatedBy: createdBy,
		TS:        ts,
	}
}

func DigestPathMoved(boardID, kind, fromPath, toPath string, count int, by string, ts int64) ([]string, *proto.DigestPathMovedPayload) {
	return proto.DigestEventScopes(boardID), &proto.DigestPathMovedPayload{
		Board:    boardID,
		Kind:     kind,
		FromPath: fromPath,
		ToPath:   toPath,
		Count:    count,
		By:       by,
		TS:       ts,
	}
}

func DigestPathCopied(boardID, kind, fromPath, toPath string, entryIDs, directoryIDs []string, count int, createdBy string, ts int64) ([]string, *proto.DigestPathCopiedPayload) {
	return proto.DigestEventScopes(boardID), &proto.DigestPathCopiedPayload{
		Board:        boardID,
		Kind:         kind,
		FromPath:     fromPath,
		ToPath:       toPath,
		EntryIDs:     entryIDs,
		DirectoryIDs: directoryIDs,
		Count:        count,
		CreatedBy:    createdBy,
		TS:           ts,
	}
}

func DigestPathDeleted(boardID, kind, path string, count int, by string, ts int64) ([]string, *proto.DigestPathDeletedPayload) {
	return proto.DigestEventScopes(boardID), &proto.DigestPathDeletedPayload{
		Board: boardID,
		Kind:  kind,
		Path:  path,
		Count: count,
		By:    by,
		TS:    ts,
	}
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

func FavoriteTreeImported(userID string, folders []proto.FavoriteTreeImportedFolderPayload, boards []proto.FavoriteTreeImportedBoardPayload, replace bool, ts int64) ([]string, *proto.FavoriteTreeImportedPayload) {
	payload := &proto.FavoriteTreeImportedPayload{UserID: userID, Folders: folders, Boards: boards, Replace: replace, TS: ts}
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

func MailGroupSet(groupID, ownerID, name string, memberIDs []string, ts int64) ([]string, *proto.MailGroupSetPayload) {
	return []string{"account:" + ownerID, "mail:" + groupID}, &proto.MailGroupSetPayload{
		ID:        groupID,
		OwnerID:   ownerID,
		Name:      name,
		MemberIDs: memberIDs,
		TS:        ts,
	}
}

func MailGroupDeleted(groupID, ownerID string, ts int64) ([]string, *proto.MailGroupDeletedPayload) {
	return []string{"account:" + ownerID, "mail:" + groupID}, &proto.MailGroupDeletedPayload{ID: groupID, OwnerID: ownerID, TS: ts}
}

func MailAttachmentAdded(scopes []string, attachmentID, mailID, filename, contentType string, sizeBytes int64, authorID, authorName, stagedBlobID string, ts int64) ([]string, *proto.MailAttachmentAddedPayload) {
	return scopes, &proto.MailAttachmentAddedPayload{
		ID:           attachmentID,
		Mail:         mailID,
		Filename:     filename,
		ContentType:  contentType,
		SizeBytes:    sizeBytes,
		AuthorID:     authorID,
		Author:       authorName,
		StagedBlobID: stagedBlobID,
		TS:           ts,
	}
}

func MailCopyUpdated(fromUserID, userID, mailID string, mailbox *string, read, kept *bool, ts int64) ([]string, *proto.MailCopyUpdatedPayload) {
	scopes := []string{"account:" + fromUserID}
	if userID != fromUserID {
		scopes = append(scopes, "account:"+userID)
	}
	scopes = append(scopes, "mail:"+mailID)
	return scopes, &proto.MailCopyUpdatedPayload{Mail: mailID, UserID: userID, Mailbox: mailbox, Read: read, Kept: kept, TS: ts}
}

func UserRelationshipSet(userID, targetUserID, kind, note string, active bool, ts int64) ([]string, *proto.UserRelationshipSetPayload) {
	payload := &proto.UserRelationshipSetPayload{UserID: userID, TargetUserID: targetUserID, Kind: kind, Active: active, Note: note, TS: ts}
	return []string{"user:" + userID, "user:" + targetUserID}, payload
}

func NotificationCreated(notificationID, userID, kind, threadID, postID, actor string, ts int64) ([]string, *proto.NotificationCreatedPayload) {
	return []string{"user:" + userID}, &proto.NotificationCreatedPayload{
		ID:       notificationID,
		UserID:   userID,
		Kind:     kind,
		ThreadID: threadID,
		PostID:   postID,
		Actor:    actor,
		TS:       ts,
	}
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
