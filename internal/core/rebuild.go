package core

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const sysmailSystemBoardID = "sysmail"

func rebuildProjectionsFromEventLog(db *sql.DB, fromSeq int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	if err := clearProjectionTables(tx); err != nil {
		return err
	}

	rows, err := qQuery(tx,
		`SELECT seq, kind, payload FROM events WHERE seq > ? ORDER BY seq`,
		fromSeq,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			seq     int64
			kind    string
			rawJSON string
		)
		if err := rows.Scan(&seq, &kind, &rawJSON); err != nil {
			return err
		}
		payload, err := unmarshalPayload(proto.EventKind(kind), []byte(rawJSON))
		if err != nil {
			return fmt.Errorf("event %d unmarshal %s: %w", seq, kind, err)
		}
		if err := rebuildProjectionEvent(tx, seq, payload); err != nil {
			return fmt.Errorf("replay event %d (%s): %w", seq, kind, err)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	return tx.Commit()
}

func clearProjectionTables(tx *sql.Tx) error {
	tables := []string{
		"posts_fts",
		"relay_deliveries",
		"post_attachments",
		"direct_messages",
		"mail_copies",
		"mail_attachment_blobs",
		"mail_attachments",
		"mail_messages",
		"posts",
		"threads",
		"poll_votes",
		"poll_options",
		"polls",
		"post_reactions",
		"blessings",
		"moderation_reviews",
		"content_filters",
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

func rebuildProjectionEvent(tx *sql.Tx, seq int64, payload any) error {
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

		boardID := ""
		if thread, err := getThreadTx(tx, evt.Thread); err == nil && thread != nil {
			boardID = thread.Board
		}
		if err := ftsInsertPost(tx, evt.ID, evt.Thread, boardID, evt.Author, postBody); err != nil {
			return err
		}
		if err := rebuildPollForPost(tx, evt.ID, sourceBody, evt.TS); err != nil {
			return err
		}
		if err := recordPostActivityFromEvent(tx, evt.AuthorID, seq, evt.TS); err != nil {
			return err
		}
	case *proto.PostAttachmentAddedPayload:
		if err := insertPostAttachment(tx, evt.ID, evt.Post, strings.TrimSpace(evt.Filename), strings.TrimSpace(evt.ContentType), evt.SizeBytes, "", evt.AuthorID, evt.TS); err != nil {
			return err
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
		if _, err := qExec(tx,
			`UPDATE posts SET marked=?, recommended=?, no_reply=?, updated_seq=?, updated_at=? WHERE id=?`,
			marked, recommended, noReply, seq, evt.TS, evt.ID,
		); err != nil {
			return err
		}
	case *proto.PostRedactedPayload:
		if _, err := qExec(tx,
			`UPDATE posts SET redacted=1, updated_seq=?, updated_at=? WHERE id=?`,
			seq, evt.TS, evt.ID,
		); err != nil {
			return err
		}
	case *proto.PostRestoredPayload:
		if _, err := qExec(tx,
			`UPDATE posts SET redacted=0, updated_seq=?, updated_at=? WHERE id=?`,
			seq, evt.TS, evt.ID,
		); err != nil {
			return err
		}
	case *proto.PostPurgedPayload:
		if _, err := qExec(tx,
			`UPDATE posts SET body='', redacted=1, updated_seq=?, updated_at=? WHERE id=?`,
			seq, evt.TS, evt.ID,
		); err != nil {
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
	case *proto.DirectMessageSentPayload:
		if err := insertDirectMessage(tx, evt.ID, evt.ConversationID, evt.FromUserID, evt.ToUserID, evt.Body, evt.TS, seq); err != nil {
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
		if _, err := qExec(tx,
			`DELETE FROM post_reactions WHERE post_id=? AND user_id=?`,
			evt.PostID, evt.User,
		); err != nil {
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
		if _, err := qExec(tx,
			`INSERT INTO poll_votes (poll_id, option_id, user_id, ts) VALUES (?,?,?,?)
			 ON CONFLICT(poll_id,user_id) DO UPDATE SET option_id=excluded.option_id, ts=excluded.ts`,
			evt.Poll, evt.Option, evt.User, evt.TS,
		); err != nil {
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
			 VALUES (?,0,0,?,"")
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

func ensureSysmailBoardSettingsProjection(tx *sql.Tx, ts int64) error {
	_, err := qExec(tx,
		`INSERT INTO board_settings (
		    board_id, anonymous_allowed, read_only, no_reply, attachments_allowed,
		    mail_in_allowed, relay_enabled, member_read_mode, member_post_mode, updated_at
		 ) VALUES (?, 0, 1, 1, 0, 0, 0, 1, 1, ?)
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
		    updated_at=excluded.updated_at`,
		sysmailSystemBoardID,
		ts,
	)
	return err
}

func recordReactionReceivedTx(tx *sql.Tx, postAuthorID string) error {
	_, err := qExec(tx, `
		INSERT INTO user_activity (user_id, reactions_recv) VALUES (?,1)
		ON CONFLICT(user_id) DO UPDATE SET reactions_recv = reactions_recv + 1`,
		postAuthorID,
	)
	return err
}

func recordReactionRemovedTx(tx *sql.Tx, postAuthorID string) error {
	_, err := qExec(tx, `
		UPDATE user_activity SET reactions_recv = MAX(0, reactions_recv - 1) WHERE user_id=?`,
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
	var existing string
	err := qQueryRow(tx, `SELECT user_id FROM user_activity WHERE user_id=?`, actorID).Scan(&existing)
	if err == sql.ErrNoRows {
		if _, e := qExec(tx,
			`INSERT INTO user_activity (user_id, posts_created, days_visited, last_visit_day, reactions_recv, trust_level)
			 VALUES (?, 1, 1, ?, 0, 0)`,
			actorID, day,
		); e != nil {
			return e
		}
		_, err = qExec(tx, `UPDATE user_activity SET trust_level=? WHERE user_id=?`, 0, actorID)
		return err
	}
	if err != nil {
		return err
	}
	var postsCreated, daysVisited, trustLevel int
	if err := qQueryRow(tx, `SELECT posts_created, days_visited, trust_level FROM user_activity WHERE user_id=?`, actorID).
		Scan(&postsCreated, &daysVisited, &trustLevel); err != nil {
		return err
	}
	newPosts := postsCreated + 1
	newDays := daysVisited
	if lastVisit, err := func() (string, error) {
		var dayAt string
		if err := qQueryRow(tx, `SELECT last_visit_day FROM user_activity WHERE user_id=?`, actorID).Scan(&dayAt); err != nil {
			return "", err
		}
		return dayAt, nil
	}(); err == nil {
		if lastVisit != day {
			newDays++
		}
	} else {
		return err
	}

	newTrust := computeTrustLevel(newPosts, newDays, trustLevel)
	_, err = qExec(tx,
		`UPDATE user_activity
		 SET posts_created=?, days_visited=?, last_visit_day=?, trust_level=?
		 WHERE user_id=?`,
		newPosts, newDays, day, newTrust, actorID,
	)
	return err
}
