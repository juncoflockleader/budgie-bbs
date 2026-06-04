package handler

import (
	"database/sql"
	"sort"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func (h *Handler) sendMail(actor *User, p proto.SendMailPayload) Reply {
	recipientRefs, errReply := h.expandMailRecipients(actor, p)
	if errReply.Err != nil {
		return errReply
	}
	if len(recipientRefs) == 0 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "at least one recipient is required", false)}
	}
	body := strings.TrimSpace(p.Body)
	if body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "body is required", false)}
	}
	subject := strings.TrimSpace(p.Subject)
	if subject == "" {
		subject = "(no subject)"
	}
	attachments, errReply := h.normalizeMailAttachments(p.Attachments)
	if errReply.Err != nil {
		return errReply
	}
	saveSent := true
	if p.SaveSent != nil {
		saveSent = *p.SaveSent
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	recipients := []*User{}
	seen := map[string]bool{}
	for _, ref := range recipientRefs {
		target, err := findUserRefTx(tx, ref)
		if err != nil {
			return internalErr(err)
		}
		if target == nil {
			return Reply{Err: errDetail(proto.ErrNotFound, "recipient not found: "+strings.TrimSpace(ref), false)}
		}
		if target.ID != actor.ID && !p.ToAll {
			ignored, err := relationshipExistsTx(tx, target.ID, actor.ID, "ignore")
			if err != nil {
				return internalErr(err)
			}
			if ignored {
				return Reply{Err: errDetail(proto.ErrForbidden, "recipient does not accept mail from this user", false)}
			}
		}
		if !seen[target.ID] {
			seen[target.ID] = true
			recipients = append(recipients, target)
		}
	}
	if len(recipients) == 0 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "at least one recipient is required", false)}
	}
	copyCounts := map[string]int{}
	for _, r := range recipients {
		copyCounts[r.ID]++
	}
	if saveSent {
		copyCounts[actor.ID]++
	}
	if errReply := ensureMailQuotaTx(tx, copyCounts, mailMessageSize(subject, body, attachments)); errReply.Err != nil {
		return errReply
	}
	parentID := strings.TrimSpace(p.ReplyTo)
	if parentID != "" {
		ok, err := actorHasMailCopyTx(tx, actor.ID, parentID)
		if err != nil {
			return internalErr(err)
		}
		if !ok {
			return Reply{Err: errDetail(proto.ErrNotFound, "reply target not found", false)}
		}
	}

	id := newID("mail_")
	toIDs := make([]string, 0, len(recipients))
	toNames := make([]string, 0, len(recipients))
	scopes := []string{"account:" + actor.ID}
	for _, r := range recipients {
		toIDs = append(toIDs, r.ID)
		toNames = append(toNames, r.Name)
		if r.ID != actor.ID {
			scopes = append(scopes, "account:"+r.ID)
		}
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtMailSent, scopes, &proto.MailSentPayload{
		ID: id, FromUserID: actor.ID, From: actor.Name, ToUserIDs: toIDs, To: toNames,
		Subject: subject, Body: body, ParentID: parentID, SaveSent: saveSent, Attachments: attachments, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := insertMailMessage(tx, id, actor.ID, subject, body, parentID, ts, seq); err != nil {
		return internalErr(err)
	}
	if err := insertMailAttachments(tx, id, actor.ID, ts, attachments); err != nil {
		return internalErr(err)
	}
	for _, r := range recipients {
		if err := insertMailCopy(tx, id, r.ID, "recipient", "inbox", false, false, ts); err != nil {
			return internalErr(err)
		}
	}
	if saveSent {
		if err := insertMailCopy(tx, id, actor.ID, "sender", "sent", true, false, ts); err != nil {
			return internalErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtMailSent, Seq: seq, Scopes: scopes,
		Payload: &proto.MailSentPayload{ID: id, FromUserID: actor.ID, From: actor.Name, ToUserIDs: toIDs, To: toNames, Subject: subject, Body: body, ParentID: parentID, SaveSent: saveSent, Attachments: attachments, TS: ts}, TS: ts})
	return Reply{Result: &proto.AckResult{ID: id, Seq: seq}}
}

func (h *Handler) sendDigestEntryMail(actor *User, p proto.SendDigestEntryMailPayload) Reply {
	entryID := strings.TrimSpace(p.Entry)
	if entryID == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "entry is required", false)}
	}
	export, err := getDigestExport(h.db, entryID)
	if err != nil {
		return internalErr(err)
	}
	if export == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "digest entry not found", false)}
	}
	settings, err := getBoardSettings(h.db, export.Entry.BoardID)
	if err != nil {
		return internalErr(err)
	}
	if settings != nil && settings.MemberReadMode && !h.actorCanUseMemberBoard(actor, export.Entry.BoardID) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board members only", false)}
	}
	subject := strings.TrimSpace(p.Subject)
	if subject == "" {
		subject = "Archive: " + export.Entry.Title
	}
	body := projections.FormatDigestExportText(export)
	if note := strings.TrimSpace(p.Note); note != "" {
		body = note + "\n\n" + body
	}
	return h.sendMail(actor, proto.SendMailPayload{
		To:        p.To,
		ToGroups:  p.ToGroups,
		ToFriends: p.ToFriends,
		ToAll:     p.ToAll,
		Subject:   subject,
		Body:      body,
		SaveSent:  p.SaveSent,
	})
}

func (h *Handler) setMailGroup(actor *User, p proto.SetMailGroupPayload) Reply {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "name is required", false)}
	}
	if len(name) > 80 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "mail group name is too long", false)}
	}
	if len(p.Members) > 200 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "mail group may contain at most 200 members", false)}
	}
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	groupID := strings.TrimSpace(p.Group)
	if groupID != "" {
		err := qQueryRow(tx,
			`SELECT id FROM mail_groups WHERE user_id=? AND (id=? OR name=?) LIMIT 1`,
			actor.ID, groupID, groupID,
		).Scan(&groupID)
		if err == sql.ErrNoRows {
			return Reply{Err: errDetail(proto.ErrNotFound, "mail group not found", false)}
		}
		if err != nil {
			return internalErr(err)
		}
	} else {
		err := qQueryRow(tx,
			`SELECT id FROM mail_groups WHERE user_id=? AND name=? LIMIT 1`,
			actor.ID, name,
		).Scan(&groupID)
		if err == sql.ErrNoRows {
			groupID = newID("mgrp_")
		} else if err != nil {
			return internalErr(err)
		}
	}
	var conflictID string
	err = qQueryRow(tx,
		`SELECT id FROM mail_groups WHERE user_id=? AND name=? LIMIT 1`,
		actor.ID, name,
	).Scan(&conflictID)
	if err != nil && err != sql.ErrNoRows {
		return internalErr(err)
	}
	if conflictID != "" && conflictID != groupID {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "mail group name already exists", false)}
	}

	memberIDs, errReply := resolveUniqueUsersTx(tx, p.Members, actor.ID)
	if errReply.Err != nil {
		return errReply
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	if err := setMailGroup(h.db, actor.ID, groupID, name, memberIDs); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: groupID}}
}

func (h *Handler) deleteMailGroup(actor *User, p proto.DeleteMailGroupPayload) Reply {
	groupRef := strings.TrimSpace(p.Group)
	if groupRef == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "group is required", false)}
	}
	groupID, err := getMailGroupID(h.db, actor.ID, groupRef)
	if err != nil {
		return internalErr(err)
	}
	if groupID == "" {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail group not found", false)}
	}
	deleted, err := deleteMailGroup(h.db, actor.ID, groupID)
	if err != nil {
		return internalErr(err)
	}
	if !deleted {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail group not found", false)}
	}
	return Reply{Result: &proto.AckResult{ID: groupID}}
}

func (h *Handler) attachMail(actor *User, p proto.AttachMailPayload) Reply {
	mailID := strings.TrimSpace(p.Mail)
	if mailID == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "mail is required", false)}
	}
	filename := strings.TrimSpace(p.Filename)
	if filename == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "attachment filename is required", false)}
	}
	if len(filename) > 160 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "attachment filename must be 160 characters or less", false)}
	}
	contentType := strings.TrimSpace(p.ContentType)
	if len(contentType) > 120 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "attachment content type must be 120 characters or less", false)}
	}
	if p.SizeBytes < 0 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "attachment size cannot be negative", false)}
	}
	attachmentID := strings.TrimSpace(p.ID)
	if attachmentID == "" {
		attachmentID = newID("matt_")
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	fromUserID, found, err := mailSenderTx(tx, mailID)
	if err != nil {
		return internalErr(err)
	}
	if !found {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail not found", false)}
	}
	if fromUserID != actor.ID {
		return Reply{Err: errDetail(proto.ErrForbidden, "only the sender can attach files to this mail", false)}
	}
	var count int
	if err := qQueryRow(tx, `SELECT COUNT(*) FROM mail_attachments WHERE message_id=?`, mailID).Scan(&count); err != nil {
		return internalErr(err)
	}
	if count >= 8 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "mail can have at most 8 attachments", false)}
	}
	copyCounts, err := activeMailCopyCountsTx(tx, mailID)
	if err != nil {
		return internalErr(err)
	}
	if errReply := ensureMailQuotaTx(tx, copyCounts, p.SizeBytes); errReply.Err != nil {
		return errReply
	}
	scopes, err := mailAccountScopesTx(tx, mailID, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	payload := &proto.MailAttachmentAddedPayload{
		ID: attachmentID, Mail: mailID, Filename: filename, ContentType: contentType,
		SizeBytes: p.SizeBytes, AuthorID: actor.ID, Author: actor.Name, TS: ts,
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtMailAttachmentAdded, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	if err := insertMailAttachment(tx, attachmentID, mailID, filename, contentType, p.SizeBytes, "", actor.ID, ts); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtMailAttachmentAdded, Seq: seq, Scopes: scopes, Payload: payload, TS: ts})
	return Reply{Result: &proto.AckResult{ID: attachmentID, Seq: seq}}
}

func (h *Handler) updateMail(actor *User, p proto.UpdateMailPayload) Reply {
	if strings.TrimSpace(p.Mail) == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "mail is required", false)}
	}
	mailbox := p.Mailbox
	if mailbox != nil {
		normalized, err := normalizeMailbox(*mailbox)
		if err != nil {
			return Reply{Err: errDetail(proto.ErrValidationFailed, err.Error(), false)}
		}
		mailbox = &normalized
	}
	if mailbox == nil && p.Read == nil && p.Kept == nil {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "mailbox, read, or kept is required", false)}
	}
	if mailbox != nil && *mailbox != "trash" {
		restoreCount, err := trashedMailCopyCount(h.db, actor.ID, p.Mail)
		if err != nil {
			return internalErr(err)
		}
		if restoreCount > 0 {
			size, err := mailStoredSize(h.db, p.Mail)
			if err != nil {
				return internalErr(err)
			}
			if errReply := ensureMailQuota(h.db, map[string]int{actor.ID: restoreCount}, size); errReply.Err != nil {
				return errReply
			}
		}
	}
	updated, err := updateMailCopy(h.db, actor.ID, p.Mail, mailbox, p.Read, p.Kept)
	if err != nil {
		return internalErr(err)
	}
	if !updated {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail not found", false)}
	}
	return Reply{Result: &proto.AckResult{ID: p.Mail}}
}

func (h *Handler) deleteMail(actor *User, p proto.DeleteMailPayload) Reply {
	if strings.TrimSpace(p.Mail) == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "mail is required", false)}
	}
	updated, err := trashMailCopy(h.db, actor.ID, p.Mail)
	if err != nil {
		return internalErr(err)
	}
	if !updated {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail not found", false)}
	}
	return Reply{Result: &proto.AckResult{ID: p.Mail}}
}

func (h *Handler) sendDirectMessage(actor *User, p proto.SendDirectMessagePayload) Reply {
	body := strings.TrimSpace(p.Body)
	if strings.TrimSpace(p.To) == "" || body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "to and body are required", false)}
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	target, err := findUserRefTx(tx, p.To)
	if err != nil {
		return internalErr(err)
	}
	if target == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "recipient not found", false)}
	}
	if target.ID != actor.ID {
		ignored, err := relationshipExistsTx(tx, target.ID, actor.ID, "ignore")
		if err != nil {
			return internalErr(err)
		}
		if ignored {
			return Reply{Err: errDetail(proto.ErrForbidden, "recipient does not accept messages from this user", false)}
		}
		allowed, err := directMessageAllowedTx(tx, target.ID, actor.ID)
		if err != nil {
			return internalErr(err)
		}
		if !allowed {
			return Reply{Err: errDetail(proto.ErrForbidden, "recipient only accepts messages from friends", false)}
		}
	}
	id := newID("dm_")
	conversationID := directConversationID(actor.ID, target.ID)
	scopes := []string{"account:" + actor.ID}
	if target.ID != actor.ID {
		scopes = append(scopes, "account:"+target.ID)
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtDirectMessageSent, scopes, &proto.DirectMessageSentPayload{
		ID: id, ConversationID: conversationID, FromUserID: actor.ID, From: actor.Name,
		ToUserID: target.ID, To: target.Name, Body: body, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := insertDirectMessage(tx, id, conversationID, actor.ID, target.ID, body, ts, seq); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtDirectMessageSent, Seq: seq, Scopes: scopes,
		Payload: &proto.DirectMessageSentPayload{ID: id, ConversationID: conversationID, FromUserID: actor.ID, From: actor.Name, ToUserID: target.ID, To: target.Name, Body: body, TS: ts}, TS: ts})
	return Reply{Result: &proto.AckResult{ID: id, Seq: seq}}
}

func (h *Handler) setDirectMessageSettings(actor *User, p proto.SetDirectMessageSettingsPayload) Reply {
	policy, errReply := normalizeDirectMessagePolicy(p.Policy)
	if errReply.Err != nil {
		return errReply
	}
	if err := setDirectMessageSettings(h.db, actor.ID, policy); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: actor.ID}}
}

func (h *Handler) markDirectMessageRead(actor *User, p proto.MarkDirectMessageReadPayload) Reply {
	if strings.TrimSpace(p.Message) == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "message is required", false)}
	}
	updated, err := markDirectMessageRead(h.db, actor.ID, p.Message)
	if err != nil {
		return internalErr(err)
	}
	if !updated {
		return Reply{Err: errDetail(proto.ErrNotFound, "message not found", false)}
	}
	return Reply{Result: &proto.AckResult{ID: p.Message}}
}

func (h *Handler) deleteDirectMessage(actor *User, p proto.DeleteDirectMessagePayload) Reply {
	if strings.TrimSpace(p.Message) == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "message is required", false)}
	}
	updated, err := deleteDirectMessage(h.db, actor.ID, p.Message)
	if err != nil {
		return internalErr(err)
	}
	if !updated {
		return Reply{Err: errDetail(proto.ErrNotFound, "message not found", false)}
	}
	return Reply{Result: &proto.AckResult{ID: p.Message}}
}

func findUserRefTx(tx *sql.Tx, ref string) (*User, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	u := &User{}
	err := qQueryRow(tx,
		`SELECT id, name, role, password, created FROM users WHERE id=? OR name=? ORDER BY CASE WHEN id=? THEN 0 ELSE 1 END LIMIT 1`,
		ref, ref, ref,
	).Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func actorHasMailCopyTx(tx *sql.Tx, userID, messageID string) (bool, error) {
	var found int
	err := qQueryRow(tx, `SELECT 1 FROM mail_copies WHERE user_id=? AND message_id=? LIMIT 1`, userID, messageID).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func relationshipExistsTx(tx *sql.Tx, userID, targetUserID, kind string) (bool, error) {
	var found int
	err := qQueryRow(tx,
		`SELECT 1 FROM user_relationships WHERE user_id=? AND target_user_id=? AND kind=? LIMIT 1`,
		userID, targetUserID, kind,
	).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (h *Handler) normalizeMailAttachments(input []proto.AttachmentPayload) ([]proto.AttachmentPayload, Reply) {
	if len(input) == 0 {
		return nil, Reply{}
	}
	if len(input) > 8 {
		return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "mail can have at most 8 attachments", false)}
	}
	out := make([]proto.AttachmentPayload, 0, len(input))
	for _, item := range input {
		filename := strings.TrimSpace(item.Filename)
		contentType := strings.TrimSpace(item.ContentType)
		url := strings.TrimSpace(item.URL)
		if filename == "" {
			return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "attachment filename is required", false)}
		}
		if len(filename) > 160 {
			return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "attachment filename must be 160 characters or less", false)}
		}
		if len(contentType) > 120 {
			return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "attachment content type must be 120 characters or less", false)}
		}
		if len(url) > 500 {
			return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "attachment URL must be 500 characters or less", false)}
		}
		if item.SizeBytes < 0 {
			return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "attachment size cannot be negative", false)}
		}
		out = append(out, proto.AttachmentPayload{
			ID:          newID("matt_"),
			Filename:    filename,
			ContentType: contentType,
			SizeBytes:   item.SizeBytes,
			URL:         url,
		})
	}
	return out, Reply{}
}

func insertMailAttachments(tx *sql.Tx, mailID, authorID string, ts int64, attachments []proto.AttachmentPayload) error {
	for _, item := range attachments {
		if err := insertMailAttachment(tx, item.ID, mailID, item.Filename, item.ContentType, item.SizeBytes, item.URL, authorID, ts); err != nil {
			return err
		}
	}
	return nil
}

func mailMessageSize(subject, body string, attachments []proto.AttachmentPayload) int64 {
	size := int64(len(subject) + len(body))
	for _, item := range attachments {
		size += item.SizeBytes
	}
	if size < 0 {
		return 0
	}
	return size
}

func ensureMailQuotaTx(tx *sql.Tx, copyCounts map[string]int, addedPerCopy int64) Reply {
	return ensureMailQuota(tx, copyCounts, addedPerCopy)
}

func ensureMailQuota(queryable interface {
	QueryRow(query string, args ...any) *sql.Row
}, copyCounts map[string]int, addedPerCopy int64) Reply {
	if addedPerCopy <= 0 {
		return Reply{}
	}
	for userID, copies := range copyCounts {
		if strings.TrimSpace(userID) == "" || copies <= 0 {
			continue
		}
		used, err := mailUsedBytes(queryable, userID)
		if err != nil {
			return internalErr(err)
		}
		added := addedPerCopy * int64(copies)
		if used+added > projections.DefaultMailQuotaBytes {
			return Reply{Err: errDetail(proto.ErrValidationFailed, "mail quota exceeded for user "+userID, false)}
		}
	}
	return Reply{}
}

func mailUsedBytes(queryable interface {
	QueryRow(query string, args ...any) *sql.Row
}, userID string) (int64, error) {
	var used sql.NullInt64
	err := qQueryRow(queryable,
		`SELECT COALESCE(SUM(LENGTH(m.subject) + LENGTH(m.body) +
		        COALESCE((SELECT SUM(size_bytes) FROM mail_attachments a WHERE a.message_id=m.id), 0)), 0)
		   FROM mail_copies c
		   JOIN mail_messages m ON m.id = c.message_id
		  WHERE c.user_id=? AND c.mailbox <> 'trash'`,
		userID,
	).Scan(&used)
	if err != nil {
		return 0, err
	}
	return used.Int64, nil
}

func mailStoredSize(queryable interface {
	QueryRow(query string, args ...any) *sql.Row
}, mailID string) (int64, error) {
	var size sql.NullInt64
	err := qQueryRow(queryable,
		`SELECT LENGTH(subject) + LENGTH(body) +
		        COALESCE((SELECT SUM(size_bytes) FROM mail_attachments a WHERE a.message_id=mail_messages.id), 0)
		   FROM mail_messages
		  WHERE id=?`,
		mailID,
	).Scan(&size)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return size.Int64, nil
}

func trashedMailCopyCount(queryable interface {
	QueryRow(query string, args ...any) *sql.Row
}, userID, mailID string) (int, error) {
	var count int
	err := qQueryRow(queryable, `SELECT COUNT(*) FROM mail_copies WHERE user_id=? AND message_id=? AND mailbox='trash'`, userID, mailID).Scan(&count)
	return count, err
}

func activeMailCopyCountsTx(tx *sql.Tx, mailID string) (map[string]int, error) {
	rows, err := tx.Query(`SELECT user_id, COUNT(*) FROM mail_copies WHERE message_id=? AND mailbox <> 'trash' GROUP BY user_id`, mailID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var userID string
		var copies int
		if err := rows.Scan(&userID, &copies); err != nil {
			return nil, err
		}
		out[userID] = copies
	}
	return out, rows.Err()
}

func mailSenderTx(tx *sql.Tx, mailID string) (string, bool, error) {
	var senderID string
	err := qQueryRow(tx, `SELECT from_user_id FROM mail_messages WHERE id=?`, mailID).Scan(&senderID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return senderID, err == nil, err
}

func mailAccountScopesTx(tx *sql.Tx, mailID, actorID string) ([]string, error) {
	rows, err := tx.Query(`SELECT DISTINCT user_id FROM mail_copies WHERE message_id=?`, mailID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	scopes := []string{}
	add := func(userID string) {
		userID = strings.TrimSpace(userID)
		if userID == "" || seen[userID] {
			return
		}
		seen[userID] = true
		scopes = append(scopes, "account:"+userID)
	}
	add(actorID)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		add(userID)
	}
	return scopes, rows.Err()
}

func (h *Handler) expandMailRecipients(actor *User, p proto.SendMailPayload) ([]string, Reply) {
	if actor == nil {
		return nil, Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}
	ownerID := actor.ID
	refs := []string{}
	if p.ToAll {
		if !actor.IsAdmin() {
			return nil, Reply{Err: errDetail(proto.ErrForbidden, "admin role required for mail-all", false)}
		}
		allUserIDs, err := h.listMailAllRecipients(actor.ID)
		if err != nil {
			return nil, internalErr(err)
		}
		refs = append(refs, allUserIDs...)
	}
	for _, ref := range p.To {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if groupRef, ok := strings.CutPrefix(ref, "group:"); ok {
			p.ToGroups = append(p.ToGroups, strings.TrimSpace(groupRef))
			continue
		}
		refs = append(refs, ref)
	}
	for _, groupRef := range p.ToGroups {
		groupRef = strings.TrimSpace(groupRef)
		if groupRef == "" {
			continue
		}
		if isFriendMailGroupRef(groupRef) {
			friendIDs, err := listFriendUserIDs(h.db, ownerID)
			if err != nil {
				return nil, internalErr(err)
			}
			refs = append(refs, friendIDs...)
			continue
		}
		groupID, err := getMailGroupID(h.db, ownerID, groupRef)
		if err != nil {
			return nil, internalErr(err)
		}
		if groupID == "" {
			return nil, Reply{Err: errDetail(proto.ErrNotFound, "mail group not found: "+groupRef, false)}
		}
		members, err := listMailGroupMembers(h.db, ownerID, groupID)
		if err != nil {
			return nil, internalErr(err)
		}
		for _, member := range members {
			refs = append(refs, member.UserID)
		}
	}
	if p.ToFriends {
		friendIDs, err := listFriendUserIDs(h.db, ownerID)
		if err != nil {
			return nil, internalErr(err)
		}
		refs = append(refs, friendIDs...)
	}
	return refs, Reply{}
}

func isFriendMailGroupRef(ref string) bool {
	switch strings.ToLower(strings.TrimSpace(ref)) {
	case "friend", "friends", "@friends", "friend-list", "friends-list":
		return true
	default:
		return false
	}
}

func (h *Handler) listMailAllRecipients(actorID string) ([]string, error) {
	rows, err := h.db.Query(`SELECT id FROM users WHERE id<>? ORDER BY name`, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		out = append(out, userID)
	}
	return out, rows.Err()
}

func resolveUniqueUsersTx(tx *sql.Tx, refs []string, ownerID string) ([]string, Reply) {
	out := []string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		target, err := findUserRefTx(tx, ref)
		if err != nil {
			return nil, internalErr(err)
		}
		if target == nil {
			return nil, Reply{Err: errDetail(proto.ErrNotFound, "user not found: "+strings.TrimSpace(ref), false)}
		}
		if target.ID == ownerID {
			return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "mail group cannot include yourself", false)}
		}
		if !seen[target.ID] {
			seen[target.ID] = true
			out = append(out, target.ID)
		}
	}
	return out, Reply{}
}

func directMessageAllowedTx(tx *sql.Tx, recipientID, senderID string) (bool, error) {
	var policy string
	err := qQueryRow(tx, `SELECT policy FROM direct_message_settings WHERE user_id=?`, recipientID).Scan(&policy)
	if err == sql.ErrNoRows {
		policy = "all"
	} else if err != nil {
		return false, err
	}
	switch policy {
	case "", "all":
		return true, nil
	case "none":
		return false, nil
	case "friends":
		return relationshipExistsTx(tx, recipientID, senderID, "friend")
	default:
		return true, nil
	}
}

func normalizeDirectMessagePolicy(policy string) (string, Reply) {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", "all", "everyone":
		return "all", Reply{}
	case "friend", "friends", "friends-only", "friend_only":
		return "friends", Reply{}
	case "none", "off", "disabled", "block":
		return "none", Reply{}
	default:
		return "", Reply{Err: errDetail(proto.ErrValidationFailed, `policy must be "all", "friends", or "none"`, false)}
	}
}

func normalizeMailbox(mailbox string) (string, error) {
	mailbox = strings.TrimSpace(strings.ToLower(mailbox))
	switch mailbox {
	case "deleted", "delete":
		mailbox = "trash"
	}
	if mailbox == "" {
		return "", errString("mailbox is required")
	}
	if len(mailbox) > 64 {
		return "", errString("mailbox is too long")
	}
	for _, r := range mailbox {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return "", errString("mailbox may contain only lowercase letters, numbers, hyphens, and underscores")
	}
	return mailbox, nil
}

func directConversationID(a, b string) string {
	parts := []string{a, b}
	sort.Strings(parts)
	return parts[0] + ":" + parts[1]
}

type errString string

func (e errString) Error() string { return string(e) }
