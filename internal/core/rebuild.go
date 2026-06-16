package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const sysmailSystemBoardID = "sysmail"

type projectionReplayEvent struct {
	id      string
	seq     int64
	kind    string
	scopes  []string
	payload any
}

func rebuildProjectionsFromEventLog(db *sql.DB, fromSeq int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	// Materialize the full event list before replaying. lib/pq cannot run a new
	// query on a connection while an earlier result set is still open, and each
	// rebuildProjectionEvent issues its own queries on this tx — so the cursor
	// must be fully drained and closed first. (SQLite tolerates interleaving;
	// Postgres does not.)
	rows, err := qQuery(tx,
		`SELECT id, seq, kind, payload FROM events WHERE seq > ? ORDER BY seq`,
		fromSeq,
	)
	if err != nil {
		return err
	}
	var events []projectionReplayEvent
	for rows.Next() {
		var (
			id      string
			seq     int64
			kind    string
			rawJSON string
		)
		if err := rows.Scan(&id, &seq, &kind, &rawJSON); err != nil {
			rows.Close()
			return err
		}
		payload, err := unmarshalPayload(proto.EventKind(kind), []byte(rawJSON))
		if err != nil {
			rows.Close()
			return fmt.Errorf("event %d unmarshal %s: %w", seq, kind, err)
		}
		events = append(events, projectionReplayEvent{id: id, seq: seq, kind: kind, payload: payload})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	return rebuildProjectionsInTx(tx, events)
}

func rebuildProjectionsFromEventStore(ctx context.Context, db *sql.DB, store EventStore, fromSeq int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("rebuild projections from event store: nil store")
	}
	events := []projectionReplayEvent{}
	cursor := fromSeq
	for {
		batch, err := store.Replay(ctx, cursor, nil, 500)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		nextCursor := cursor
		for _, evt := range batch {
			if evt == nil || evt.Seq <= cursor {
				continue
			}
			events = append(events, projectionReplayEvent{
				id:      evt.ID,
				seq:     evt.Seq,
				kind:    string(evt.Kind),
				scopes:  append([]string(nil), evt.Scopes...),
				payload: evt.Payload,
			})
			if evt.Seq > nextCursor {
				nextCursor = evt.Seq
			}
		}
		if nextCursor <= cursor || len(batch) < 500 {
			break
		}
		cursor = nextCursor
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint
	return rebuildProjectionsInTx(tx, events)
}

func rebuildProjectionsInTx(tx *sql.Tx, events []projectionReplayEvent) error {
	unordered, err := snapshotUnorderedTraffic(tx)
	if err != nil {
		return err
	}
	if err := clearProjectionTables(tx); err != nil {
		return err
	}

	for _, e := range events {
		if err := rebuildProjectionEvent(tx, e.id, e.seq, e.payload, e.scopes); err != nil {
			return fmt.Errorf("replay event %d (%s): %w", e.seq, e.kind, err)
		}
	}
	if err := restoreUnorderedTraffic(tx, unordered); err != nil {
		return err
	}
	if err := recomputeReactionReceivedActivity(tx); err != nil {
		return err
	}
	if _, err := rebuildCommunityStatsSnapshot(tx); err != nil {
		return err
	}
	if _, err := rebuildBoardSummaryStats(tx); err != nil {
		return err
	}
	if _, err := rebuildUnreadThreadSummaryStats(tx); err != nil {
		return err
	}
	if _, err := rebuildBoardRankingStats(tx); err != nil {
		return err
	}
	if _, err := rebuildThreadRankingStats(tx); err != nil {
		return err
	}
	if _, err := rebuildReplyRankingPosts(tx); err != nil {
		return err
	}
	if _, err := rebuildUserRankingStats(tx); err != nil {
		return err
	}
	if _, err := rebuildBlessingRankingStats(tx); err != nil {
		return err
	}
	if _, err := rebuildArchiveRankingStats(tx); err != nil {
		return err
	}

	return tx.Commit()
}

type unorderedTrafficSnapshot struct {
	reactions []unorderedReaction
	votes     []unorderedPollVote
}

type unorderedReaction struct {
	postID string
	userID string
	emoji  string
	ts     int64
}

type unorderedPollVote struct {
	pollID         string
	optionID       string
	userID         string
	ts             int64
	pollPostID     string
	optionText     string
	optionPosition int
}

func snapshotUnorderedTraffic(tx *sql.Tx) (unorderedTrafficSnapshot, error) {
	var snapshot unorderedTrafficSnapshot
	reactionRows, err := qQuery(tx, `SELECT post_id, user_id, emoji, ts FROM post_reactions ORDER BY post_id, user_id`)
	if err != nil {
		return snapshot, fmt.Errorf("snapshot unordered reactions: %w", err)
	}
	for reactionRows.Next() {
		var row unorderedReaction
		if err := reactionRows.Scan(&row.postID, &row.userID, &row.emoji, &row.ts); err != nil {
			reactionRows.Close()
			return snapshot, fmt.Errorf("snapshot unordered reactions: %w", err)
		}
		snapshot.reactions = append(snapshot.reactions, row)
	}
	if err := reactionRows.Err(); err != nil {
		reactionRows.Close()
		return snapshot, fmt.Errorf("snapshot unordered reactions: %w", err)
	}
	reactionRows.Close()

	voteRows, err := qQuery(tx,
		`SELECT pv.poll_id, pv.option_id, pv.user_id, pv.ts, p.post_id, po.text, po.position
		   FROM poll_votes pv
		   JOIN polls p ON p.id=pv.poll_id
		   JOIN poll_options po ON po.id=pv.option_id AND po.poll_id=pv.poll_id
		  ORDER BY pv.poll_id, pv.user_id`,
	)
	if err != nil {
		return snapshot, fmt.Errorf("snapshot unordered poll votes: %w", err)
	}
	for voteRows.Next() {
		var row unorderedPollVote
		if err := voteRows.Scan(&row.pollID, &row.optionID, &row.userID, &row.ts, &row.pollPostID, &row.optionText, &row.optionPosition); err != nil {
			voteRows.Close()
			return snapshot, fmt.Errorf("snapshot unordered poll votes: %w", err)
		}
		snapshot.votes = append(snapshot.votes, row)
	}
	if err := voteRows.Err(); err != nil {
		voteRows.Close()
		return snapshot, fmt.Errorf("snapshot unordered poll votes: %w", err)
	}
	voteRows.Close()
	return snapshot, nil
}

func restoreUnorderedTraffic(tx *sql.Tx, snapshot unorderedTrafficSnapshot) error {
	for _, row := range snapshot.reactions {
		if !restoredPostReactionTargetExists(tx, row.postID, row.userID) {
			continue
		}
		if err := upsertReaction(tx, row.postID, row.userID, row.emoji, row.ts); err != nil {
			return fmt.Errorf("restore unordered reaction %s/%s: %w", row.postID, row.userID, err)
		}
	}
	for _, row := range snapshot.votes {
		pollID, optionID, ok, err := resolveRestoredPollVote(tx, row)
		if err != nil {
			return fmt.Errorf("restore unordered poll vote %s/%s: %w", row.pollID, row.userID, err)
		}
		if !ok {
			continue
		}
		if !restoredUserExists(tx, row.userID) {
			continue
		}
		if err := castVote(tx, pollID, optionID, row.userID, row.ts); err != nil {
			return fmt.Errorf("restore unordered poll vote %s/%s: %w", row.pollID, row.userID, err)
		}
	}
	return nil
}

func restoredPostReactionTargetExists(tx *sql.Tx, postID, userID string) bool {
	var exists int
	err := qQueryRow(tx,
		`SELECT 1
		   FROM posts p
		   JOIN users u ON u.id=?
		  WHERE p.id=?`,
		userID, postID,
	).Scan(&exists)
	return err == nil
}

func restoredUserExists(tx *sql.Tx, userID string) bool {
	var exists int
	err := qQueryRow(tx, `SELECT 1 FROM users WHERE id=?`, userID).Scan(&exists)
	return err == nil
}

func resolveRestoredPollVote(tx *sql.Tx, row unorderedPollVote) (string, string, bool, error) {
	var pollID, optionID string
	err := qQueryRow(tx,
		`SELECT p.id, po.id
		   FROM polls p
		   JOIN poll_options po ON po.poll_id=p.id
		  WHERE p.id=? AND po.id=?
		  LIMIT 1`,
		row.pollID, row.optionID,
	).Scan(&pollID, &optionID)
	if err == nil {
		return pollID, optionID, true, nil
	}
	if err != sql.ErrNoRows {
		return "", "", false, err
	}
	err = qQueryRow(tx,
		`SELECT p.id, po.id
		   FROM polls p
		   JOIN poll_options po ON po.poll_id=p.id
		  WHERE p.post_id=? AND po.text=? AND po.position=?
		  LIMIT 1`,
		row.pollPostID, row.optionText, row.optionPosition,
	).Scan(&pollID, &optionID)
	if err == nil {
		return pollID, optionID, true, nil
	}
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	return "", "", false, err
}

func recomputeReactionReceivedActivity(tx *sql.Tx) error {
	if _, err := qExec(tx, `UPDATE user_activity SET reactions_recv=0`); err != nil {
		return err
	}
	_, err := qExec(tx,
		`INSERT INTO user_activity (user_id, reactions_recv)
		 SELECT p.author_id, COUNT(*)
		   FROM post_reactions pr
		   JOIN posts p ON p.id=pr.post_id
		  WHERE p.author_id <> ''
		    AND p.author_id <> pr.user_id
		  GROUP BY p.author_id
		 ON CONFLICT(user_id) DO UPDATE SET reactions_recv=excluded.reactions_recv`,
	)
	return err
}

func clearProjectionTables(tx *sql.Tx) error {
	// Ordered children-first so foreign keys are satisfied. Postgres enforces
	// FKs strictly; deleting a parent (e.g. posts) before its children
	// (polls, poll_options, poll_votes) would violate the constraint.
	tables := []string{
		"poll_vote_count_shards",
		"poll_votes",
		"poll_options",
		"polls",
		"post_reaction_count_shards",
		"post_reactions",
		"counter_checkpoints",
		"digest_directories",
		"digest_path_mutations",
		"digest_entry_removals",
		"digest_entries",
		"posts_fts",
		"latest_feed_posts",
		"resident_feed_posts",
		"community_stats_snapshot",
		"board_summary_stats",
		"unread_thread_summary_stats",
		"board_ranking_stats",
		"thread_ranking_stats",
		"reply_ranking_posts",
		"user_ranking_stats",
		"blessing_ranking_stats",
		"archive_ranking_stats",
		"relay_deliveries",
		"post_attachments",
		"post_deletions",
		"posts",
		"threads",
		"direct_messages",
		"mail_copies",
		"mail_attachment_blobs",
		"mail_attachments",
		"mail_group_deletions",
		"mail_messages",
		"blessings",
		"moderation_reviews",
		"content_filters",
		"board_automod_rules",
		"automod_audit_log",
		"user_sanctions",
		"user_activity",
	}
	for _, table := range tables {
		if _, err := qExec(tx, `DELETE FROM `+table); err != nil {
			return fmt.Errorf("clear table %s: %w", table, err)
		}
	}
	return nil
}

type projectionApplyContext struct {
	relayEnabledByBoard map[string]bool
}

func rebuildProjectionEvent(tx *sql.Tx, eventID string, seq int64, payload any, scopes []string) error {
	return rebuildProjectionEventWithContext(tx, nil, eventID, seq, payload, scopes)
}

func rebuildProjectionEventWithContext(tx *sql.Tx, applyCtx *projectionApplyContext, eventID string, seq int64, payload any, scopes []string) error {
	switch evt := payload.(type) {
	case *proto.ThreadNewPayload:
		authorID := evt.AuthorID
		if strings.TrimSpace(authorID) == "" {
			if u := loadUserIDByName(tx, evt.Author); u != "" {
				authorID = u
			}
		}
		if err := insertThread(tx, &Thread{
			ID:        evt.ID,
			Board:     evt.Board,
			Author:    evt.Author,
			AuthorID:  authorID,
			Title:     evt.Title,
			LastSeq:   seq,
			CreatedTS: evt.TS,
			CreatedAt: evt.TS,
			UpdatedAt: evt.TS,
		}); err != nil {
			return err
		}
	case *proto.PostAppendedPayload:
		authorID := evt.AuthorID
		if strings.TrimSpace(authorID) == "" {
			if u := loadUserIDByName(tx, evt.Author); u != "" {
				authorID = u
			}
		}
		sourceBody := evt.Body
		if strings.TrimSpace(evt.RawBody) != "" {
			sourceBody = evt.RawBody
		}
		postBody := evt.Body
		pollBlock, cleanBody := extractPoll(sourceBody)
		if pollBlock != nil && cleanBody != sourceBody {
			postBody = cleanBody
		}
		if err := insertPost(tx, &Post{
			ID:             evt.ID,
			Thread:         evt.Thread,
			Author:         evt.Author,
			AuthorID:       authorID,
			Body:           postBody,
			Signature:      strings.TrimSpace(evt.Signature),
			ContentType:    evt.ContentType,
			ReplyTo:        evt.ReplyTo,
			TeX:            evt.TeX,
			MailBack:       evt.MailBack,
			SourcePost:     strings.TrimSpace(evt.SourcePost),
			SourceThread:   strings.TrimSpace(evt.SourceThread),
			SourceBoard:    strings.TrimSpace(evt.SourceBoard),
			SourceAuthor:   strings.TrimSpace(evt.SourceAuthor),
			SourceAuthorID: strings.TrimSpace(evt.SourceAuthorID),
			SourceTitle:    strings.TrimSpace(evt.SourceTitle),
			CreatedSeq:     seq,
			CreatedAt:      evt.TS,
			UpdatedAt:      evt.TS,
		}); err != nil {
			return err
		}
		for i, att := range evt.Attachments {
			attID := strings.TrimSpace(att.ID)
			if attID == "" {
				attID = fmt.Sprintf("%s_att_%d", evt.ID, i)
			}
			filename := strings.TrimSpace(att.Filename)
			if filename == "" {
				continue
			}
			if err := insertPostAttachment(tx, attID, evt.ID, filename, strings.TrimSpace(att.ContentType), att.SizeBytes, strings.TrimSpace(att.URL), authorID, evt.TS); err != nil {
				return err
			}
		}

		if err := upsertPostMeta(tx, seq, evt.Thread, evt.TS); err != nil {
			return err
		}
		boardID := projectionBoardFromScopes(scopes)
		if boardID != "" {
			if err := upsertProjectedResidentFeedPost(tx, evt.ID, evt.Thread, boardID, seq); err != nil {
				return err
			}
			if err := upsertProjectedLatestFeedPost(tx, evt.ID, evt.Thread, boardID, seq); err != nil {
				return err
			}
		} else {
			if _, err := upsertResidentFeedPost(tx, evt.ID); err != nil {
				return err
			}
			if _, err := upsertLatestFeedPost(tx, evt.ID); err != nil {
				return err
			}
			if loadedThread, err := getThreadTx(tx, evt.Thread); err == nil && loadedThread != nil {
				boardID = loadedThread.Board
			}
		}
		if err := ftsInsertPost(tx, evt.ID, evt.Thread, boardID, evt.Author, postBody); err != nil {
			return err
		}
		if err := appendRelayDeliveryFromProjectedPost(tx, applyCtx, evt, authorID, postBody, boardID, seq); err != nil {
			return err
		}
		if err := rebuildPollForPost(tx, evt.ID, sourceBody, evt.TS); err != nil {
			return err
		}
		if err := recordPostActivityFromEvent(tx, postCommittedActorIDFromEvent(tx, evt), seq, evt.TS); err != nil {
			return err
		}
	case *proto.PostAttachmentAddedPayload:
		if err := insertPostAttachment(tx, evt.ID, evt.Post, strings.TrimSpace(evt.Filename), strings.TrimSpace(evt.ContentType), evt.SizeBytes, "", evt.AuthorID, evt.TS); err != nil {
			return err
		}
		if stagedBlobID := strings.TrimSpace(evt.StagedBlobID); stagedBlobID != "" {
			if err := promoteStagedPostAttachmentBlob(tx, stagedBlobID, evt.ID, evt.SizeBytes, strings.TrimSpace(evt.ContentType)); err != nil {
				if errors.Is(err, projections.ErrStagedAttachmentBlobMissing) {
					ok, promotedErr := nativePromotedPostAttachmentBlobMatches(tx, evt.ID, evt.SizeBytes, strings.TrimSpace(evt.ContentType))
					if promotedErr != nil {
						return promotedErr
					}
					if ok {
						return nil
					}
				}
				return err
			}
		}
	case *proto.PostEditedPayload:
		if _, err := qExec(tx,
			`UPDATE posts SET body=?, version=version+1, updated_seq=?, updated_at=? WHERE id=?`,
			evt.NewBody, seq, evt.TS, evt.ID,
		); err != nil {
			return err
		}
		if err := ftsUpdatePost(tx, evt.ID, evt.NewBody); err != nil {
			return err
		}
	case *proto.PostFlagsSetPayload:
		marked := 0
		if evt.Marked {
			marked = 1
		}
		recommended := 0
		if evt.Recommended {
			recommended = 1
		}
		noReply := 0
		if evt.NoReply {
			noReply = 1
		}
		tex := 0
		if evt.TeX {
			tex = 1
		}
		mailBack := 0
		if evt.MailBack {
			mailBack = 1
		}
		if _, err := qExec(tx,
			`UPDATE posts SET marked=?, recommended=?, no_reply=?, tex=?, mail_back=?, updated_seq=?, updated_at=? WHERE id=?`,
			marked, recommended, noReply, tex, mailBack, seq, evt.TS, evt.ID,
		); err != nil {
			return err
		}
	case *proto.PostRedactedPayload:
		post, err := getPostTx(tx, evt.ID)
		if err != nil {
			return err
		}
		if post != nil {
			thread, err := getThreadTx(tx, post.Thread)
			if err != nil {
				return err
			}
			if thread != nil {
				deletedByID, deletedByName := deletedByProjectionIdentity(tx, evt.By)
				kind := strings.TrimSpace(evt.DeletionKind)
				switch kind {
				case "junk", "recycle":
				case "":
					kind = "recycle"
					if deletedByID != "" && post.AuthorID == deletedByID {
						kind = "junk"
					} else if deletedByID == "" && evt.By == post.Author {
						kind = "junk"
					}
				default:
					kind = "recycle"
				}
				if err := recordPostDeletion(tx, post.ID, post.Thread, thread.Board, deletedByID, deletedByName, evt.Reason, kind, evt.TS, seq); err != nil {
					return err
				}
			}
		}
		if _, err := qExec(tx,
			`UPDATE posts SET redacted=1, updated_seq=?, updated_at=? WHERE id=?`,
			seq, evt.TS, evt.ID,
		); err != nil {
			return err
		}
		if _, err := deleteResidentFeedPost(tx, evt.ID); err != nil {
			return err
		}
		if _, err := deleteLatestFeedPost(tx, evt.ID); err != nil {
			return err
		}
	case *proto.PostRestoredPayload:
		if err := clearPostDeletion(tx, evt.ID); err != nil {
			return err
		}
		if _, err := qExec(tx,
			`UPDATE posts SET redacted=0, updated_seq=?, updated_at=? WHERE id=?`,
			seq, evt.TS, evt.ID,
		); err != nil {
			return err
		}
		if _, err := upsertResidentFeedPost(tx, evt.ID); err != nil {
			return err
		}
		if _, err := upsertLatestFeedPost(tx, evt.ID); err != nil {
			return err
		}
	case *proto.PostDeletionClearedPayload:
		if err := clearPostDeletion(tx, evt.ID); err != nil {
			return err
		}
	case *proto.PostPurgedPayload:
		if _, err := qExec(tx,
			`UPDATE posts SET body='', redacted=1, updated_seq=?, updated_at=? WHERE id=?`,
			seq, evt.TS, evt.ID,
		); err != nil {
			return err
		}
		if err := ftsDeletePost(tx, evt.ID); err != nil {
			return err
		}
		if _, err := deleteResidentFeedPost(tx, evt.ID); err != nil {
			return err
		}
		if _, err := deleteLatestFeedPost(tx, evt.ID); err != nil {
			return err
		}
	case *proto.ThreadTitleSetPayload:
		if err := setThreadTitleWithTime(tx, evt.Thread, evt.Title, evt.TS); err != nil {
			return err
		}
	case *proto.ThreadLockedPayload:
		if err := setThreadLockedWithTime(tx, evt.Thread, evt.Locked, evt.TS); err != nil {
			return err
		}
	case *proto.ThreadMovedPayload:
		if _, err := qExec(tx,
			`UPDATE threads SET board=?, updated_at=? WHERE id=?`,
			evt.ToBoard, evt.TS, evt.Thread,
		); err != nil {
			return err
		}
		if _, err := moveResidentFeedThread(tx, evt.Thread, evt.ToBoard); err != nil {
			return err
		}
		if _, err := moveLatestFeedThread(tx, evt.Thread, evt.ToBoard); err != nil {
			return err
		}
	case *proto.UserSanctionedPayload:
		scope := strings.TrimSpace(evt.Scope)
		if scope == "" {
			scope = "global"
		}
		expiresAt := int64(0)
		if evt.DurationSec > 0 {
			expiresAt = evt.TS + evt.DurationSec*1000
		}
		if err := insertSanction(tx, buildRebuildID("san", seq), evt.User, evt.Kind, scope, expiresAt, evt.By, evt.Reason, seq); err != nil {
			return err
		}
	case *proto.UserSanctionClearedPayload:
		scope := strings.TrimSpace(evt.Scope)
		if scope == "" {
			scope = "global"
		}
		if _, err := clearUserSanctions(tx, evt.User, strings.TrimSpace(evt.Kind), scope); err != nil {
			return err
		}
	case *proto.RoleGrantedPayload:
		if err := setUserRole(tx, evt.User, evt.Role); err != nil {
			return err
		}
	case *proto.RoleRevokedPayload:
		if err := setUserRole(tx, evt.User, "user"); err != nil {
			return err
		}
	case *proto.BoardCreatedPayload:
		if err := upsertBoardProjection(tx, evt.ID, evt.Name, evt.Description, evt.ParentID, evt.Position, evt.TS); err != nil {
			return err
		}
		if evt.ID == sysmailSystemBoardID {
			if err := ensureSysmailBoardSettingsProjection(tx, evt.TS); err != nil {
				return err
			}
		}
	case *proto.BoardSettingsSetPayload:
		if err := projections.SetBoardSettingsTx(tx, projections.BoardSettings{
			BoardID:            evt.Board,
			AnonymousAllowed:   evt.AnonymousAllowed,
			ReadOnly:           evt.ReadOnly,
			NoReply:            evt.NoReply,
			AttachmentsAllowed: evt.AttachmentsAllowed,
			MailInAllowed:      evt.MailInAllowed,
			RelayEnabled:       evt.RelayEnabled,
			MemberReadMode:     evt.MemberReadMode,
			MemberPostMode:     evt.MemberPostMode,
			StatsExcluded:      evt.StatsExcluded,
			ZapAllowed:         evt.ZapAllowed,
			UpdatedAt:          evt.TS,
		}); err != nil {
			return err
		}
	case *proto.BoardMemberRequirementsSetPayload:
		if err := projections.SetBoardMemberRequirementsTx(tx, projections.BoardMemberRequirements{
			BoardID:                   evt.Board,
			MinLoginCount:             evt.MinLoginCount,
			MinPostCount:              evt.MinPostCount,
			MinTrustLevel:             evt.MinTrustLevel,
			MinScore:                  evt.MinScore,
			MinBoardPostCount:         evt.MinBoardPostCount,
			MinBoardOriginalPostCount: evt.MinBoardOriginalPostCount,
			MinBoardDigestCount:       evt.MinBoardDigestCount,
			MinBoardMarkCount:         evt.MinBoardMarkCount,
			MaxMembers:                evt.MaxMembers,
			ApprovalMode:              evt.ApprovalMode,
			UpdatedAt:                 evt.TS,
		}); err != nil {
			return err
		}
	case *proto.BoardModeratorSetPayload:
		if err := projections.SetBoardModeratorTx(tx, evt.Board, evt.User, evt.By, evt.Moderator, evt.Position, evt.TS); err != nil {
			return err
		}
	case *proto.BoardMemberSetPayload:
		if err := projections.SetBoardMemberTx(tx, evt.Board, projections.BoardMember{
			UserID:              evt.User,
			Title:               evt.Title,
			Position:            evt.Position,
			CanManageMembers:    evt.CanManageMembers,
			CanCurate:           evt.CanCurate,
			CanModeratePosts:    evt.CanModeratePosts,
			CanModerateThreads:  evt.CanModerateThreads,
			CanAnnounce:         evt.CanAnnounce,
			CanManagePolls:      evt.CanManagePolls,
			CanSetBoardSettings: evt.CanSetBoardSettings,
		}, evt.Member, evt.TS); err != nil {
			return err
		}
	case *proto.BoardMemberApplicationSubmittedPayload:
		if err := projections.InsertBoardMemberApplicationTx(tx, evt.ID, evt.Board, evt.User, evt.Note, evt.TS); err != nil {
			return err
		}
	case *proto.BoardMemberApplicationReviewedPayload:
		if err := projections.ReviewBoardMemberApplicationTx(tx, evt.Application, evt.Board, evt.User, evt.Reviewer, evt.Status, evt.Title, evt.ReviewNote, evt.TS); err != nil {
			return err
		}
	case *proto.BoardZapSetPayload:
		if err := projections.SetBoardZapTx(tx, evt.UserID, evt.Board, evt.Zapped, evt.TS); err != nil {
			return err
		}
	case *proto.BoardRecommendedSetPayload:
		if err := projections.SetRecommendedBoardTx(tx, evt.Board, evt.Note, evt.CuratedBy, evt.Position, evt.Recommended, evt.TS); err != nil {
			return err
		}
	case *proto.BoardFavoriteSetPayload:
		if err := projections.SetBoardFavoriteTx(tx, evt.UserID, evt.Board, evt.FolderID, evt.Position, evt.Favorite, evt.TS); err != nil {
			return err
		}
	case *proto.FavoriteFolderCreatedPayload:
		if err := projections.CreateFavoriteFolderTx(tx, evt.UserID, evt.ID, evt.ParentID, evt.Name, evt.Position, evt.TS); err != nil {
			return err
		}
	case *proto.FavoriteFolderUpdatedPayload:
		if err := projections.UpdateFavoriteFolderTx(tx, evt.UserID, evt.ID, evt.ParentID, evt.Name, evt.Position, evt.TS); err != nil {
			return err
		}
	case *proto.FavoriteFolderDeletedPayload:
		if err := projections.DeleteFavoriteFolderTx(tx, evt.UserID, evt.ID, evt.ParentID, evt.TS); err != nil {
			return err
		}
	case *proto.FavoriteTreeImportedPayload:
		tree := &projections.FavoriteTree{
			Folders: make([]projections.FavoriteFolder, 0, len(evt.Folders)),
			Boards:  make([]projections.FavoriteBoardEntry, 0, len(evt.Boards)),
		}
		for _, folder := range evt.Folders {
			tree.Folders = append(tree.Folders, projections.FavoriteFolder{
				ID:       folder.ID,
				ParentID: folder.ParentID,
				Name:     folder.Name,
				Position: folder.Position,
			})
		}
		for _, board := range evt.Boards {
			tree.Boards = append(tree.Boards, projections.FavoriteBoardEntry{
				ID:       board.ID,
				FolderID: board.FolderID,
				Position: board.Position,
			})
		}
		if err := projections.ImportFavoriteTreeTx(tx, evt.UserID, tree, evt.Replace, evt.TS); err != nil {
			return err
		}
	case *proto.MailSentPayload:
		if err := insertMailMessage(tx, evt.ID, evt.FromUserID, evt.Subject, evt.Body, evt.ParentID, evt.TS, seq); err != nil {
			return err
		}
		for i, att := range evt.Attachments {
			attID := strings.TrimSpace(att.ID)
			if attID == "" {
				attID = fmt.Sprintf("%s_matt_%d", evt.ID, i)
			}
			filename := strings.TrimSpace(att.Filename)
			if filename == "" {
				continue
			}
			if err := insertMailAttachment(tx, attID, evt.ID, filename, strings.TrimSpace(att.ContentType), att.SizeBytes, strings.TrimSpace(att.URL), evt.FromUserID, evt.TS); err != nil {
				return err
			}
		}

		for _, userID := range evt.ToUserIDs {
			if strings.TrimSpace(userID) == "" {
				continue
			}
			if err := insertMailCopy(tx, evt.ID, userID, "recipient", "inbox", false, false, evt.TS); err != nil {
				return err
			}
		}
		if evt.SaveSent {
			if err := insertMailCopy(tx, evt.ID, evt.FromUserID, "sender", "sent", true, false, evt.TS); err != nil {
				return err
			}
		}
	case *proto.MailAttachmentAddedPayload:
		if err := insertMailAttachment(tx, evt.ID, evt.Mail, strings.TrimSpace(evt.Filename), strings.TrimSpace(evt.ContentType), evt.SizeBytes, "", evt.AuthorID, evt.TS); err != nil {
			return err
		}
		if stagedBlobID := strings.TrimSpace(evt.StagedBlobID); stagedBlobID != "" {
			if err := promoteStagedMailAttachmentBlob(tx, stagedBlobID, evt.ID, evt.SizeBytes, strings.TrimSpace(evt.ContentType)); err != nil {
				if errors.Is(err, projections.ErrStagedAttachmentBlobMissing) {
					ok, promotedErr := nativePromotedMailAttachmentBlobMatches(tx, evt.ID, evt.SizeBytes, strings.TrimSpace(evt.ContentType))
					if promotedErr != nil {
						return promotedErr
					}
					if ok {
						return nil
					}
				}
				return err
			}
		}
	case *proto.MailGroupSetPayload:
		if err := projections.SetMailGroupTx(tx, evt.OwnerID, evt.ID, evt.Name, evt.MemberIDs, evt.TS); err != nil {
			return err
		}
	case *proto.MailGroupDeletedPayload:
		if _, err := projections.DeleteMailGroupFinalTx(tx, eventID, evt.OwnerID, evt.ID, evt.TS); err != nil {
			return err
		}
	case *proto.MailCopyUpdatedPayload:
		if _, err := projections.UpdateMailCopyTx(tx, evt.UserID, evt.Mail, evt.Mailbox, evt.Read, evt.Kept, evt.TS); err != nil {
			return err
		}
	case *proto.DirectMessageSentPayload:
		if err := insertDirectMessage(tx, evt.ID, evt.ConversationID, evt.FromUserID, evt.ToUserID, evt.Body, evt.TS, seq); err != nil {
			return err
		}
	case *proto.DirectMessageReadPayload:
		if _, err := projections.MarkDirectMessageReadTx(tx, evt.UserID, evt.MessageID, evt.ReadAt); err != nil {
			return err
		}
	case *proto.DirectMessageDeletedPayload:
		if _, err := projections.DeleteDirectMessageFlagsTx(tx, evt.MessageID, evt.SenderDeleted, evt.RecipientDeleted); err != nil {
			return err
		}
	case *proto.DirectMessageSettingsSetPayload:
		if err := projections.SetDirectMessageSettingsTx(tx, evt.UserID, evt.Policy, evt.TS); err != nil {
			return err
		}
	case *proto.UserRelationshipSetPayload:
		if err := projections.SetUserRelationshipTx(tx, evt.UserID, evt.TargetUserID, evt.Kind, evt.Note, evt.Active, evt.TS); err != nil {
			return err
		}
	case *proto.UserBlessedPayload:
		if err := insertBlessing(tx, &Blessing{
			ID:         evt.ID,
			FromUserID: evt.FromUserID,
			FromName:   evt.From,
			ToUserID:   evt.ToUserID,
			ToName:     evt.To,
			Message:    evt.Message,
			CreatedAt:  evt.TS,
			Seq:        seq,
		}); err != nil {
			return err
		}
	case *proto.NotificationCreatedPayload:
		if err := projections.InsertNotification(tx, evt.ID, evt.UserID, evt.Kind, evt.ThreadID, evt.PostID, evt.Actor, evt.TS); err != nil {
			return err
		}
	case *proto.DigestEntryUpsertedPayload:
		if _, err := upsertDigestEntryTx(tx, evt.ID, evt.Board, evt.TargetKind, evt.TargetID, evt.Kind, evt.Title, evt.Path, evt.Note, evt.CreatedBy, evt.TS); err != nil {
			return err
		}
	case *proto.DigestEntryUpdatedPayload:
		if err := updateDigestEntryTx(tx, evt.ID, evt.Title, evt.Path, evt.Note, evt.TS); err != nil {
			return err
		}
	case *proto.DigestEntryBodySetPayload:
		if err := setDigestEntryBodyTx(tx, evt.ID, evt.Body, evt.Edited, evt.TS); err != nil {
			return err
		}
	case *proto.DigestEntryRemovedPayload:
		if err := removeDigestEntryFinalTx(tx, evt.ID, evt.Board, evt.Kind, evt.By, evt.TS); err != nil {
			return err
		}
	case *proto.DigestDirectorySetPayload:
		if _, err := upsertDigestDirectoryTx(tx, evt.ID, evt.Board, evt.Kind, evt.Path, evt.CreatedBy, evt.TS); err != nil {
			return err
		}
	case *proto.DigestPathMovedPayload:
		if _, err := moveDigestPathFinalTx(tx, eventID, evt.Board, evt.Kind, evt.FromPath, evt.ToPath, evt.By, evt.TS); err != nil {
			return err
		}
	case *proto.DigestPathCopiedPayload:
		if _, err := copyDigestPathTx(tx, evt.Board, evt.Kind, evt.FromPath, evt.ToPath, evt.CreatedBy, evt.EntryIDs, evt.DirectoryIDs, evt.TS); err != nil {
			return err
		}
	case *proto.DigestPathDeletedPayload:
		if _, err := deleteDigestPathFinalTx(tx, eventID, evt.Board, evt.Kind, evt.Path, evt.By, evt.TS); err != nil {
			return err
		}
	case *proto.CommunityStatsSnapshotRecordedPayload:
		if err := projections.UpsertCommunityStatHistoryTx(tx, projections.CommunityStatHistory{
			Day:                 evt.Day,
			SnapshotAt:          evt.SnapshotAt,
			TotalUsers:          evt.TotalUsers,
			TotalBoards:         evt.TotalBoards,
			TotalThreads:        evt.TotalThreads,
			TotalPosts:          evt.TotalPosts,
			TotalReactions:      evt.TotalReactions,
			TotalMail:           evt.TotalMail,
			TotalDirectMessages: evt.TotalDirectMessages,
			TotalLogins:         evt.TotalLogins,
			TotalLogouts:        evt.TotalLogouts,
			TotalWebLogins:      evt.TotalWebLogins,
			TotalWebLogouts:     evt.TotalWebLogouts,
			TotalGuestLogins:    evt.TotalGuestLogins,
			TotalGuestLogouts:   evt.TotalGuestLogouts,
			TotalOnlineSeconds:  evt.TotalOnlineSeconds,
			OnlineUsers:         evt.OnlineUsers,
			OnlineGuests:        evt.OnlineGuests,
			MaxOnlineUsers:      evt.MaxOnlineUsers,
			MaxOnlineAt:         evt.MaxOnlineAt,
			MaxOnlineGuests:     evt.MaxOnlineGuests,
			MaxOnlineGuestsAt:   evt.MaxOnlineGuestsAt,
			HeadSeq:             evt.HeadSeq,
		}); err != nil {
			return err
		}
	case *proto.CounterCheckpointPayload:
		if err := recordCounterCheckpointTx(tx, seq, evt); err != nil {
			return err
		}
	case *proto.PostReactedPayload:
		existed := false
		if err := qQueryRow(tx,
			`SELECT 1 FROM post_reactions WHERE post_id=? AND user_id=?`, evt.PostID, evt.User,
		).Scan(new(any)); err != sql.ErrNoRows {
			existed = err == nil
			if err != nil {
				return err
			}
		}
		if err := upsertReaction(tx, evt.PostID, evt.User, evt.Emoji, evt.TS); err != nil {
			return err
		}
		if !existed {
			post, err := getPostTx(tx, evt.PostID)
			if err != nil {
				return err
			}
			if post != nil {
				author := strings.TrimSpace(post.AuthorID)
				if author == "" {
					author = post.Author
				}
				if author != evt.User && author != "" {
					if err := recordReactionReceivedTx(tx, author); err != nil {
						return err
					}
				}
			}
		}
	case *proto.PostUnreactedPayload:
		existed := false
		if err := qQueryRow(tx,
			`SELECT 1 FROM post_reactions WHERE post_id=? AND user_id=?`, evt.PostID, evt.User,
		).Scan(new(any)); err != sql.ErrNoRows {
			existed = err == nil
			if err != nil {
				return err
			}
		}
		if err := deleteReaction(tx, evt.PostID, evt.User); err != nil {
			return err
		}
		if existed {
			post, err := getPostTx(tx, evt.PostID)
			if err != nil {
				return err
			}
			if post != nil {
				author := strings.TrimSpace(post.AuthorID)
				if author == "" {
					author = post.Author
				}
				if author != evt.User && author != "" {
					if err := recordReactionRemovedTx(tx, author); err != nil {
						return err
					}
				}
			}
		}
	case *proto.PollVotedPayload:
		if err := ensurePollVoteOption(tx, evt.Poll, evt.Option); err != nil {
			return err
		}
		if err := castVote(tx, evt.Poll, evt.Option, evt.User, evt.TS); err != nil {
			return err
		}
	case *proto.ContentFilterSetPayload:
		scope := strings.TrimSpace(evt.Scope)
		if scope == "" {
			scope = "global"
		}
		if err := upsertContentFilter(tx, evt.ID, evt.Pattern, scope, evt.Active, evt.By, evt.TS); err != nil {
			return err
		}
	case *proto.BoardAutomodRuleSetPayload:
		if err := upsertBoardAutomodRule(tx, evt); err != nil {
			return err
		}
	case *proto.BoardAutomodRuleDeletedPayload:
		if err := deleteBoardAutomodRule(tx, evt.Board, evt.ID); err != nil {
			return err
		}
	case *proto.BoardAutomodTriggeredPayload:
		if err := insertAutomodAuditLog(tx, evt); err != nil {
			return err
		}
	case *proto.PostFlaggedPayload:
		kind := strings.TrimSpace(evt.Kind)
		if kind == "" {
			kind = "post_flag"
		}
		if err := insertModerationReview(tx, evt.ReviewID, kind, evt.PostID, "post", evt.Reporter, evt.Reason, evt.TS); err != nil {
			return err
		}
	case *proto.ReviewResolvedPayload:
		if err := resolveModerationReview(tx, evt.ReviewID, evt.By, evt.Resolution, evt.TS); err != nil {
			return err
		}
	case *proto.TrustLevelChangedPayload:
		if strings.TrimSpace(evt.User) == "" {
			return nil
		}
		if _, err := qExec(tx,
			`INSERT INTO user_activity (user_id, posts_created, days_visited, trust_level, last_visit_day)
			 VALUES (?,0,0,?,'')
			 ON CONFLICT(user_id)
			 DO UPDATE SET trust_level=excluded.trust_level`,
			evt.User, evt.NewLevel,
		); err != nil {
			return err
		}
	default:
		// Ignore non-projection events.
		return nil
	}

	return nil
}

func upsertPostMeta(tx *sql.Tx, seq int64, threadID string, ts int64) error {
	_, err := qExec(tx, `UPDATE threads SET post_count=post_count+1, last_seq=?, updated_at=? WHERE id=?`, seq, ts, threadID)
	return err
}

func setThreadLockedWithTime(tx *sql.Tx, threadID string, locked bool, ts int64) error {
	v := 0
	if locked {
		v = 1
	}
	_, err := qExec(tx, `UPDATE threads SET locked=?, updated_at=? WHERE id=?`, v, ts, threadID)
	return err
}

func setThreadTitleWithTime(tx *sql.Tx, threadID, title string, ts int64) error {
	_, err := qExec(tx, `UPDATE threads SET title=?, updated_at=? WHERE id=?`, title, ts, threadID)
	return err
}

func upsertBoardProjection(tx *sql.Tx, id, name, description, parentID string, position int, ts int64) error {
	_, err := qExec(tx,
		`INSERT INTO boards (id, name, description) VALUES (?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description`,
		id, name, description,
	)
	if err != nil {
		return err
	}
	_, err = qExec(tx,
		`INSERT INTO categories (id, name, description, parent_id, position, visibility, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'public', ?, ?)
		 ON CONFLICT(id)
		 DO UPDATE SET name=excluded.name, description=excluded.description, parent_id=excluded.parent_id, position=excluded.position, updated_at=excluded.updated_at`,
		id, name, description, parentID, position, ts, ts,
	)
	return err
}

func loadUserIDByName(tx *sql.Tx, name string) string {
	var userID string
	err := qQueryRow(tx, `SELECT id FROM users WHERE name=?`, name).Scan(&userID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(userID)
}

func postCommittedActorIDFromEvent(tx *sql.Tx, evt *proto.PostAppendedPayload) string {
	if evt == nil {
		return ""
	}
	if actorID := strings.TrimSpace(evt.PostCommitActorID); actorID != "" {
		return actorID
	}
	if actorID := strings.TrimSpace(evt.AuthorID); actorID != "" {
		return actorID
	}
	return loadUserIDByName(tx, evt.Author)
}

func appendRelayDeliveryFromProjectedPost(tx *sql.Tx, applyCtx *projectionApplyContext, evt *proto.PostAppendedPayload, authorID, body, boardID string, seq int64) error {
	if evt == nil {
		return nil
	}
	boardID = strings.TrimSpace(boardID)
	if boardID == "" {
		return nil
	}
	relayEnabled, err := projectedBoardRelayEnabled(tx, applyCtx, boardID)
	if err != nil {
		return err
	}
	if !relayEnabled {
		return nil
	}
	var threadTitle string
	err = qQueryRow(tx, `SELECT title FROM threads WHERE id=?`, strings.TrimSpace(evt.Thread)).Scan(&threadTitle)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	return insertRelayDelivery(
		tx,
		"relay_"+strings.TrimSpace(evt.ID),
		boardID,
		strings.TrimSpace(evt.Thread),
		strings.TrimSpace(evt.ID),
		strings.TrimSpace(authorID),
		strings.TrimSpace(evt.Author),
		strings.TrimSpace(threadTitle),
		body,
		evt.TS,
		seq,
	)
}

func projectedBoardRelayEnabled(tx *sql.Tx, applyCtx *projectionApplyContext, boardID string) (bool, error) {
	boardID = strings.TrimSpace(boardID)
	if boardID == "" {
		return false, nil
	}
	if applyCtx != nil && applyCtx.relayEnabledByBoard != nil {
		if enabled, ok := applyCtx.relayEnabledByBoard[boardID]; ok {
			return enabled, nil
		}
	}
	var relayEnabled int
	err := qQueryRow(tx, `SELECT COALESCE(relay_enabled, 0) FROM board_settings WHERE board_id=?`, boardID).Scan(&relayEnabled)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	enabled := err == nil && relayEnabled != 0
	if applyCtx != nil {
		if applyCtx.relayEnabledByBoard == nil {
			applyCtx.relayEnabledByBoard = map[string]bool{}
		}
		applyCtx.relayEnabledByBoard[boardID] = enabled
	}
	return enabled, nil
}

func projectionBoardFromScopes(scopes []string) string {
	var boardID string
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if !strings.HasPrefix(scope, "board:") {
			continue
		}
		candidate := strings.TrimSpace(strings.TrimPrefix(scope, "board:"))
		if candidate == "" {
			continue
		}
		if boardID != "" && boardID != candidate {
			return ""
		}
		boardID = candidate
	}
	return boardID
}

func upsertProjectedResidentFeedPost(tx *sql.Tx, postID, threadID, boardID string, seq int64) error {
	_, err := qExec(tx,
		`INSERT INTO resident_feed_posts (post_id, thread_id, board_id, created_seq, updated_seq)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(post_id) DO UPDATE SET
		    thread_id=excluded.thread_id,
		    board_id=excluded.board_id,
		    created_seq=excluded.created_seq,
		    updated_seq=excluded.updated_seq`,
		postID, threadID, boardID, seq, seq,
	)
	return err
}

func upsertProjectedLatestFeedPost(tx *sql.Tx, postID, threadID, boardID string, seq int64) error {
	_, err := qExec(tx,
		`INSERT INTO latest_feed_posts (post_id, thread_id, board_id, created_seq, updated_seq)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(post_id) DO UPDATE SET
		    thread_id=excluded.thread_id,
		    board_id=excluded.board_id,
		    created_seq=excluded.created_seq,
		    updated_seq=excluded.updated_seq`,
		postID, threadID, boardID, seq, seq,
	)
	return err
}

func deletedByProjectionIdentity(tx *sql.Tx, by string) (string, string) {
	by = strings.TrimSpace(by)
	if by == "" {
		return "", ""
	}
	var name string
	err := qQueryRow(tx, `SELECT name FROM users WHERE id=?`, by).Scan(&name)
	if err == nil {
		return by, strings.TrimSpace(name)
	}
	if userID := loadUserIDByName(tx, by); userID != "" {
		return userID, by
	}
	return by, by
}

func ensureSysmailBoardSettingsProjection(tx *sql.Tx, ts int64) error {
	_, err := qExec(tx,
		`INSERT INTO board_settings (
		    board_id, anonymous_allowed, read_only, no_reply, attachments_allowed,
		    mail_in_allowed, relay_enabled, member_read_mode, member_post_mode, stats_excluded, updated_at
		 ) VALUES (?, 0, 1, 1, 0, 0, 0, 1, 1, 1, ?)
		 ON CONFLICT(board_id)
		 DO UPDATE SET
		    anonymous_allowed=0,
		    read_only=1,
		    no_reply=1,
		    attachments_allowed=0,
		    mail_in_allowed=0,
		    relay_enabled=0,
		    member_read_mode=1,
		    member_post_mode=1,
		    stats_excluded=1,
		    updated_at=excluded.updated_at`,
		sysmailSystemBoardID,
		ts,
	)
	return err
}

func recordReactionReceivedTx(tx *sql.Tx, postAuthorID string) error {
	_, err := qExec(tx, `
		INSERT INTO user_activity (user_id, reactions_recv) VALUES (?,1)
		ON CONFLICT(user_id) DO UPDATE SET reactions_recv = user_activity.reactions_recv + 1`,
		postAuthorID,
	)
	return err
}

func recordReactionRemovedTx(tx *sql.Tx, postAuthorID string) error {
	_, err := qExec(tx, `
		UPDATE user_activity SET reactions_recv = CASE WHEN reactions_recv > 0 THEN reactions_recv - 1 ELSE 0 END WHERE user_id=?`,
		postAuthorID,
	)
	return err
}

func rebuildPollForPost(tx *sql.Tx, postID, body string, ts int64) error {
	pollBlock, _ := extractPoll(body)
	if pollBlock == nil {
		return nil
	}
	if len(pollBlock.options) < 2 {
		return nil
	}

	pollID := deterministicID("poll", postID, strings.Join(pollBlock.options, "|"), pollBlock.question)
	if _, err := qExec(tx,
		`INSERT INTO polls (id, post_id, question, expires_at, ts) VALUES (?,?,?,?,?)`,
		pollID, postID, pollBlock.question, pollBlock.expiresAt, ts,
	); err != nil {
		return err
	}

	for i, option := range pollBlock.options {
		optionID := deterministicID("poll_option", pollID, option, fmt.Sprintf("%d", i))
		if _, err := qExec(tx,
			`INSERT INTO poll_options (id, poll_id, text, position) VALUES (?,?,?,?)`,
			optionID, pollID, option, i,
		); err != nil {
			return err
		}
	}
	return nil
}

func ensurePollVoteOption(tx *sql.Tx, pollID, optionID string) error {
	if optionID == "" {
		return nil
	}
	var has int
	if err := qQueryRow(tx, `SELECT 1 FROM poll_options WHERE id=? AND poll_id=?`, optionID, pollID).Scan(&has); err != sql.ErrNoRows {
		if err == nil {
			return nil
		}
		return err
	}
	_, err := qExec(tx,
		`INSERT OR IGNORE INTO poll_options (id, poll_id, text, position) VALUES (?, ?, '[legacy option]', 0)`,
		optionID, pollID,
	)
	return err
}

func buildRebuildID(prefix string, seq int64) string {
	return prefix + "_" + fmt.Sprintf("%d", seq)
}

func recordPostActivityFromEvent(tx *sql.Tx, actorID string, seq, ts int64) error {
	if strings.TrimSpace(actorID) == "" {
		return nil
	}
	day := time.UnixMilli(ts).UTC().Format("2006-01-02")
	_, err := qExec(tx,
		`INSERT INTO user_activity (user_id, posts_created, days_visited, last_visit_day, reactions_recv, trust_level)
		 VALUES (?, 1, 1, ?, 0, 1)
		 ON CONFLICT(user_id) DO UPDATE SET
		   posts_created=user_activity.posts_created + 1,
		   days_visited=user_activity.days_visited + CASE
		     WHEN user_activity.last_visit_day != excluded.last_visit_day THEN 1
		     ELSE 0
		   END,
		   last_visit_day=excluded.last_visit_day,
		   trust_level=CASE
		     WHEN user_activity.trust_level >= 4 THEN user_activity.trust_level
		     WHEN (user_activity.days_visited + CASE
		             WHEN user_activity.last_visit_day != excluded.last_visit_day THEN 1
		             ELSE 0
		           END) >= 100 AND user_activity.posts_created + 1 >= 50 THEN 3
		     WHEN (user_activity.days_visited + CASE
		             WHEN user_activity.last_visit_day != excluded.last_visit_day THEN 1
		             ELSE 0
		           END) >= 30 AND user_activity.posts_created + 1 >= 15 THEN 2
		     WHEN user_activity.posts_created + 1 >= 1 THEN 1
		     ELSE 0
		   END`,
		actorID, day,
	)
	return err
}
