package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandevents"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandrules"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// lockMailboxes serializes concurrent writers to the same mailbox so the
// recipient mail-quota check and insert are race-free. CmdSendMail/CmdForwardMail
// are partitioned by the sender, so without this two senders to one recipient
// could both pass the recipient's quota check and overshoot it. No-op on SQLite
// (its single writer already serializes); on Postgres it takes a
// transaction-scoped advisory lock per mailbox, acquired in sorted order to
// avoid deadlock between overlapping recipient sets.
func (h *Handler) lockMailboxes(tx *sql.Tx, copyCounts map[string]int) error {
	if h.lockCmd == nil { // SQLite mode: writes are already serialized
		return nil
	}
	keys := make([]string, 0, len(copyCounts))
	for k := range copyCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, commandexec.MailboxAdvisoryLockKey(k)); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) sendMail(actor *projections.User, p proto.SendMailPayload) Reply {
	recipientRefs, ruleErr := commandrules.ExpandMailRecipients(h.db, actor, p)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	var msg string
	p, msg = proto.NormalizeSendMailContentPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	body := p.Body
	subject := p.Subject
	attachments, ruleErr := commandrules.NormalizeMailAttachments(p.Attachments, func(int) string {
		return newID("matt_")
	})
	if ruleErr != nil {
		return Reply{Err: ruleErr}
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

	recipients, ruleErr := commandrules.ResolveMailRecipients(tx, actor, recipientRefs, p.ToAll)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	copyCounts := commandrules.MailCopyCounts(recipients, actor.ID, saveSent)
	if err := h.lockMailboxes(tx, copyCounts); err != nil {
		return internalErr(err)
	}
	if ruleErr := commandrules.EnsureMailQuota(tx, copyCounts, proto.MailMessageSize(subject, body, attachments)); ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	parentID := p.ReplyTo
	if parentID != "" {
		ok, err := projections.UserHasMailCopy(tx, actor.ID, parentID)
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
	eventPayload := proto.NewMailSentPayload(id, actor.ID, actor.Name, toIDs, toNames, subject, body, parentID, saveSent, attachments, ts)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtMailSent, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().InsertMailMessage(tx, id, actor.ID, subject, body, parentID, ts, seq); err != nil {
		return internalErr(err)
	}
	if err := insertMailAttachments(tx, id, actor.ID, ts, attachments); err != nil {
		return internalErr(err)
	}
	for _, r := range recipients {
		if err := currentRuntime().InsertMailCopy(tx, id, r.ID, "recipient", "inbox", false, false, ts); err != nil {
			return internalErr(err)
		}
	}
	if saveSent {
		if err := currentRuntime().InsertMailCopy(tx, id, actor.ID, "sender", "sent", true, false, ts); err != nil {
			return internalErr(err)
		}
	}
	generatedEvents := []*proto.Event{}
	if p.ToAll {
		generatedEvents, err = h.appendSysmailSystemPostTx(tx, actor, id, subject, body, len(toNames), ts)
		if err != nil {
			return internalErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.publishEvent(proto.EvtMailSent, seq, scopes, eventPayload, ts)
	h.publishGeneratedEvents(generatedEvents)
	return Reply{Result: &proto.AckResult{ID: id, Seq: seq}}
}

func (h *Handler) forwardMail(actor *projections.User, p proto.ForwardMailPayload) Reply {
	if actor == nil {
		return Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}
	var msg string
	p, msg = proto.NormalizeForwardMailPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	mailID := p.Mail
	source, err := currentRuntime().GetMail(h.db, actor.ID, mailID)
	if err != nil {
		return internalErr(err)
	}
	if source == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail not found", false)}
	}
	return h.sendMail(actor, commandrules.ForwardMailSendPayload(p, source))
}

func (h *Handler) postMailToBoard(actor *projections.User, p proto.PostMailToBoardPayload) Reply {
	if actor == nil {
		return Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}
	p, mailMsg, targetMsg := proto.NormalizePostMailToBoardPayload(p)
	mailID := p.Mail
	if mailMsg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, mailMsg, false)}
	}
	source, err := currentRuntime().GetMail(h.db, actor.ID, mailID)
	if err != nil {
		return internalErr(err)
	}
	if source == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail not found", false)}
	}
	title, body := commandrules.PostMailToBoardContent(p, source)
	threadID := p.Thread
	if threadID != "" {
		return h.appendPost(actor, proto.AppendPostPayload{
			Thread:      threadID,
			Body:        body,
			ContentType: "markup",
		})
	}
	boardID := p.Board
	if targetMsg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, targetMsg, false)}
	}
	return h.createThread(actor, proto.CreateThreadPayload{
		Board:       boardID,
		Title:       title,
		Body:        body,
		ContentType: "markup",
	})
}

func (h *Handler) sendDigestEntryMail(actor *projections.User, p proto.SendDigestEntryMailPayload) Reply {
	var msg string
	p, msg = proto.NormalizeSendDigestEntryMailPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	entryID := p.Entry
	export, err := currentRuntime().GetDigestExport(h.db, entryID)
	if err != nil {
		return internalErr(err)
	}
	if export == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "digest entry not found", false)}
	}
	settings, err := currentRuntime().GetBoardSettings(h.db, export.Entry.BoardID)
	if err != nil {
		return internalErr(err)
	}
	if errDetail := commandrules.RequireMemberBoardReadAccess(h.db, actor, export.Entry.BoardID, settings, "board members only"); errDetail != nil {
		return Reply{Err: errDetail}
	}
	return h.sendMail(actor, commandrules.DigestEntryMailSendPayload(p, export))
}

func (h *Handler) mailPostAuthor(actor *projections.User, p proto.MailPostAuthorPayload) Reply {
	if actor == nil {
		return Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}
	var msg string
	p, msg = proto.NormalizeMailPostAuthorPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	postID := p.Post
	post, err := currentRuntime().GetPost(h.db, postID)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if errDetail := commandrules.RequirePostNotRedacted(post.Redacted, "cannot mail author from a redacted post"); errDetail != nil {
		return Reply{Err: errDetail}
	}
	thread, err := currentRuntime().GetThread(h.db, post.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	settings, err := currentRuntime().GetBoardSettings(h.db, thread.Board)
	if err != nil {
		return internalErr(err)
	}
	if errDetail := commandrules.RequireMemberBoardReadAccess(h.db, actor, thread.Board, settings, "board members only"); errDetail != nil {
		return Reply{Err: errDetail}
	}
	sendPayload, ruleErr := commandrules.MailPostAuthorSendPayload(actor, p, thread, post)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	return h.sendMail(actor, sendPayload)
}

func (h *Handler) appendSysmailSystemPostTx(tx *sql.Tx, actor *projections.User, mailID, subject, mailBody string, recipientCount int, ts int64) ([]*proto.Event, error) {
	threadID := "sysmail_thr_" + mailID
	postID := "sysmail_pst_" + mailID
	exists, err := projections.ThreadExists(tx, threadID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, nil
	}

	title := "Sysop mail: " + subject
	body := proto.FormatSysmailSystemBody(mailID, subject, mailBody, actor.Name, recipientCount)
	return h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:          proto.SysmailSystemBoardID,
		BoardName:        proto.SysmailSystemBoardID,
		Description:      proto.SysmailSystemBoardDescription,
		ThreadID:         threadID,
		PostID:           postID,
		Title:            title,
		Body:             body,
		AfterEnsureBoard: func() error { return ensureSysmailBoardSettingsTx(tx, ts) },
	}, ts)
}

func ensureSysmailBoardSettingsTx(tx *sql.Tx, ts int64) error {
	_, err := projections.QExec(tx,
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
		proto.SysmailSystemBoardID,
		ts,
	)
	return err
}

func (h *Handler) setMailGroup(actor *projections.User, p proto.SetMailGroupPayload) Reply {
	var msg string
	p, msg = proto.NormalizeMailGroupPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	name := p.Name
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	groupID, memberIDs, ruleErr := commandrules.ResolveMailGroupMutation(tx, actor.ID, p, func() string {
		return newID("mgrp_")
	})
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().SetMailGroup(h.db, actor.ID, groupID, name, memberIDs); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: groupID}}
}

func (h *Handler) deleteMailGroup(actor *projections.User, p proto.DeleteMailGroupPayload) Reply {
	var msg string
	p, msg = proto.NormalizeDeleteMailGroupPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	groupRef := p.Group
	groupID, err := currentRuntime().GetMailGroupID(h.db, actor.ID, groupRef)
	if err != nil {
		return internalErr(err)
	}
	if groupID == "" {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail group not found", false)}
	}
	deleted, err := currentRuntime().DeleteMailGroup(h.db, actor.ID, groupID)
	if err != nil {
		return internalErr(err)
	}
	if !deleted {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail group not found", false)}
	}
	return Reply{Result: &proto.AckResult{ID: groupID}}
}

func (h *Handler) attachMail(actor *projections.User, p proto.AttachMailPayload) Reply {
	var msg string
	p, msg = proto.NormalizeAttachMailPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	mailID := p.Mail
	filename := p.Filename
	contentType := p.ContentType
	attachmentID := p.ID
	if attachmentID == "" {
		attachmentID = newID("matt_")
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	copyCounts, ruleErr := commandrules.ValidateMailAttachmentMutation(tx, actor, mailID)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	if err := h.lockMailboxes(tx, copyCounts); err != nil {
		return internalErr(err)
	}
	if ruleErr := commandrules.EnsureMailQuota(tx, copyCounts, p.SizeBytes); ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	scopes, err := projections.MailAccountScopes(tx, mailID, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	scopes, payload := commandevents.MailAttachmentAdded(scopes, attachmentID, mailID, filename, contentType, p.SizeBytes, actor.ID, actor.Name, "", ts)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtMailAttachmentAdded, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().InsertMailAttachment(tx, attachmentID, mailID, filename, contentType, p.SizeBytes, "", actor.ID, ts); err != nil {
		return internalErr(err)
	}
	if stagedBlobID := p.StagedBlobID; stagedBlobID != "" {
		if err := currentRuntime().PromoteStagedMailBlob(tx, stagedBlobID, attachmentID, p.SizeBytes, contentType); err != nil {
			if errors.Is(err, projections.ErrStagedAttachmentBlobMissing) {
				return Reply{Err: errDetail(proto.ErrBlobStagingRequired, "staged mail attachment blob is not available yet", true)}
			}
			if errors.Is(err, projections.ErrStagedAttachmentBlobMismatch) {
				return Reply{Err: errDetail(proto.ErrValidationFailed, err.Error(), false)}
			}
			return internalErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtMailAttachmentAdded, seq, scopes, payload, ts)
	return Reply{Result: &proto.AckResult{ID: attachmentID, Seq: seq}}
}

func (h *Handler) updateMail(actor *projections.User, p proto.UpdateMailPayload) Reply {
	var msg string
	p, msg = proto.NormalizeUpdateMailPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	mailbox := p.Mailbox
	if mailbox != nil && *mailbox != "trash" {
		restoreCount, err := projections.TrashedMailCopyCount(h.db, actor.ID, p.Mail)
		if err != nil {
			return internalErr(err)
		}
		if restoreCount > 0 {
			size, err := projections.MailStoredSize(h.db, p.Mail)
			if err != nil {
				return internalErr(err)
			}
			if ruleErr := commandrules.EnsureMailQuota(h.db, map[string]int{actor.ID: restoreCount}, size); ruleErr != nil {
				return Reply{Err: ruleErr}
			}
		}
	}
	updated, err := currentRuntime().UpdateMailCopy(h.db, actor.ID, p.Mail, mailbox, p.Read, p.Kept)
	if err != nil {
		return internalErr(err)
	}
	if !updated {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail not found", false)}
	}
	return Reply{Result: &proto.AckResult{ID: p.Mail}}
}

func (h *Handler) deleteMail(actor *projections.User, p proto.DeleteMailPayload) Reply {
	var msg string
	p, msg = proto.NormalizeDeleteMailPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	updated, err := currentRuntime().TrashMailCopy(h.db, actor.ID, p.Mail)
	if err != nil {
		return internalErr(err)
	}
	if !updated {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail not found", false)}
	}
	return Reply{Result: &proto.AckResult{ID: p.Mail}}
}

func (h *Handler) deleteMailRange(actor *projections.User, p proto.DeleteMailRangePayload) Reply {
	mailIDs, msg := proto.NormalizeMailRangeIDs(p.Mail)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	for _, mailID := range mailIDs {
		res, err := projections.QExec(tx,
			`UPDATE mail_copies
			    SET mailbox='trash', updated_at=?
			  WHERE user_id=? AND message_id=?`,
			ts, actor.ID, mailID,
		)
		if err != nil {
			return internalErr(err)
		}
		updated, err := res.RowsAffected()
		if err != nil {
			return internalErr(err)
		}
		if updated == 0 {
			return Reply{Err: errDetail(proto.ErrNotFound, "mail not found: "+mailID, false)}
		}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%d", len(mailIDs))}}
}

func (h *Handler) sendDirectMessage(actor *projections.User, p proto.SendDirectMessagePayload) Reply {
	p, msg := proto.NormalizeSendDirectMessagePayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	target, errDetail := commandrules.ResolveDirectMessageRecipient(tx, actor, p.To)
	if errDetail != nil {
		return Reply{Err: errDetail}
	}
	id := newID("dm_")
	scopes, payload := commandevents.DirectMessageSent(id, actor.ID, actor.Name, target.ID, target.Name, p.Body, ts)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtDirectMessageSent, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().InsertDirectMessage(tx, id, payload.ConversationID, actor.ID, target.ID, p.Body, ts, seq); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.publishEvent(proto.EvtDirectMessageSent, seq, scopes, payload, ts)
	return Reply{Result: &proto.AckResult{ID: id, Seq: seq}}
}

func (h *Handler) setDirectMessageSettings(actor *projections.User, p proto.SetDirectMessageSettingsPayload) Reply {
	policy, msg := proto.NormalizeDirectMessagePolicy(p.Policy)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if err := currentRuntime().SetDirectMessageSettings(h.db, actor.ID, policy); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: actor.ID}}
}

func (h *Handler) markDirectMessageRead(actor *projections.User, p proto.MarkDirectMessageReadPayload) Reply {
	return h.updateDirectMessage(p.Message, func(messageID string) (bool, error) {
		return currentRuntime().MarkDirectMessageRead(h.db, actor.ID, messageID)
	})
}

func (h *Handler) deleteDirectMessage(actor *projections.User, p proto.DeleteDirectMessagePayload) Reply {
	return h.updateDirectMessage(p.Message, func(messageID string) (bool, error) {
		return currentRuntime().DeleteDirectMessage(h.db, actor.ID, messageID)
	})
}

func (h *Handler) updateDirectMessage(rawMessageID string, update func(string) (bool, error)) Reply {
	messageID, msg := proto.NormalizeDirectMessageTarget(rawMessageID)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	updated, err := update(messageID)
	if err != nil {
		return internalErr(err)
	}
	if !updated {
		return Reply{Err: errDetail(proto.ErrNotFound, "message not found", false)}
	}
	return Reply{Result: &proto.AckResult{ID: messageID}}
}

func insertMailAttachments(tx *sql.Tx, mailID, authorID string, ts int64, attachments []proto.AttachmentPayload) error {
	for _, item := range attachments {
		if err := currentRuntime().InsertMailAttachment(tx, item.ID, mailID, item.Filename, item.ContentType, item.SizeBytes, item.URL, authorID, ts); err != nil {
			return err
		}
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }
