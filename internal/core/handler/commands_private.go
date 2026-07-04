package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type sqlQueryable interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

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
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, mailboxAdvisoryKey(k)); err != nil {
			return err
		}
	}
	return nil
}

func mailboxAdvisoryKey(mailbox string) int64 {
	hsh := fnv.New64a()
	_, _ = hsh.Write([]byte("mailbox:" + mailbox))
	return int64(hsh.Sum64())
}

// maxMailRecipientsPerSend bounds the fan-out of a single non-admin mail send.
const maxMailRecipientsPerSend = 50

func (h *Handler) sendMail(actor *User, p proto.SendMailPayload) Reply {
	recipientRefs, errReply := ExpandMailRecipients(h.db, actor, p)
	if errReply.Err != nil {
		return errReply
	}
	var msg string
	p, msg = proto.NormalizeSendMailContentPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	body := p.Body
	subject := p.Subject
	attachments, errReply := NormalizeMailAttachments(p.Attachments, func(int) string {
		return newID("matt_")
	})
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

	recipients, errReply := ResolveMailRecipients(tx, actor, recipientRefs, p.ToAll)
	if errReply.Err != nil {
		return errReply
	}
	copyCounts := MailCopyCounts(recipients, actor.ID, saveSent)
	if err := h.lockMailboxes(tx, copyCounts); err != nil {
		return internalErr(err)
	}
	if errReply := EnsureMailQuota(tx, copyCounts, proto.MailMessageSize(subject, body, attachments)); errReply.Err != nil {
		return errReply
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

func (h *Handler) forwardMail(actor *User, p proto.ForwardMailPayload) Reply {
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
	return h.sendMail(actor, ForwardMailSendPayload(p, source))
}

func (h *Handler) postMailToBoard(actor *User, p proto.PostMailToBoardPayload) Reply {
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
	title, body := PostMailToBoardContent(p, source)
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

func (h *Handler) sendDigestEntryMail(actor *User, p proto.SendDigestEntryMailPayload) Reply {
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
	if settings != nil && settings.MemberReadMode && !h.actorCanUseMemberBoard(actor, export.Entry.BoardID) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board members only", false)}
	}
	return h.sendMail(actor, DigestEntryMailSendPayload(p, export))
}

func ForwardMailSendPayload(p proto.ForwardMailPayload, source *projections.MailItem) proto.SendMailPayload {
	return proto.SendMailPayload{
		To:        p.To,
		ToGroups:  p.ToGroups,
		ToFriends: p.ToFriends,
		ToAll:     p.ToAll,
		Subject:   proto.NormalizeForwardMailSubject(p.Subject, source.Subject),
		Body:      proto.FormatForwardMailBody(p.Note, source.FromName, source.ToNames, source.Subject, projections.MailAttachmentFilenames(source.Attachments), source.Body),
		SaveSent:  p.SaveSent,
	}
}

func PostMailToBoardContent(p proto.PostMailToBoardPayload, source *projections.MailItem) (title, body string) {
	return proto.PostMailToBoardTitle(p.Subject, source.Subject),
		proto.FormatMailBoardBody(p.Note, source.FromName, source.ToNames, source.Subject, projections.MailAttachmentFilenames(source.Attachments), source.Body)
}

func DigestEntryMailSendPayload(p proto.SendDigestEntryMailPayload, export *projections.DigestExport) proto.SendMailPayload {
	body := projections.FormatDigestExportText(export)
	if note := p.Note; note != "" {
		body = note + "\n\n" + body
	}
	return proto.SendMailPayload{
		To:        p.To,
		ToGroups:  p.ToGroups,
		ToFriends: p.ToFriends,
		ToAll:     p.ToAll,
		Subject:   proto.DigestEntryMailSubject(p.Subject, export.Entry.Title),
		Body:      body,
		SaveSent:  p.SaveSent,
	}
}

func MailPostAuthorSendPayload(actor *User, p proto.MailPostAuthorPayload, thread *Thread, post *Post) (proto.SendMailPayload, Reply) {
	recipient := strings.TrimSpace(post.AuthorID)
	if recipient == "" {
		recipient = strings.TrimSpace(post.Author)
	}
	if recipient == "" || strings.EqualFold(strings.TrimSpace(post.Author), "anonymous") {
		return proto.SendMailPayload{}, Reply{Err: errDetail(proto.ErrValidationFailed, "anonymous article author cannot receive mail", false)}
	}
	return proto.SendMailPayload{
		To:       []string{recipient},
		Subject:  proto.MailPostAuthorSubject(p.Subject, thread.Title),
		Body:     proto.FormatPostAuthorMailBody(thread.Board, thread.Title, post.CreatedSeq, post.ID, post.Author, actor.Name, p.Body, post.Body),
		SaveSent: p.SaveSent,
	}, Reply{}
}

func (h *Handler) mailPostAuthor(actor *User, p proto.MailPostAuthorPayload) Reply {
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
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot mail author from a redacted post", false)}
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
	if settings != nil && settings.MemberReadMode && !h.actorCanUseMemberBoard(actor, thread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board members only", false)}
	}
	sendPayload, errReply := MailPostAuthorSendPayload(actor, p, thread, post)
	if errReply.Err != nil {
		return errReply
	}
	return h.sendMail(actor, sendPayload)
}

func (h *Handler) appendSysmailSystemPostTx(tx *sql.Tx, actor *User, mailID, subject, mailBody string, recipientCount int, ts int64) ([]*proto.Event, error) {
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

func (h *Handler) setMailGroup(actor *User, p proto.SetMailGroupPayload) Reply {
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

	groupID, errReply := ResolveMailGroupID(tx, actor.ID, p.Group, name, func() string {
		return newID("mgrp_")
	})
	if errReply.Err != nil {
		return errReply
	}
	conflictID, err := projections.MailGroupIDByName(tx, actor.ID, name)
	if err != nil {
		return internalErr(err)
	}
	if conflictID != "" && conflictID != groupID {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "mail group name already exists", false)}
	}

	memberIDs, errReply := ResolveUniqueMailGroupMembers(tx, p.Members, actor.ID)
	if errReply.Err != nil {
		return errReply
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().SetMailGroup(h.db, actor.ID, groupID, name, memberIDs); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: groupID}}
}

func ResolveMailGroupID(queryable sqlQueryable, ownerID, groupRef, name string, idForNewGroup func() string) (string, Reply) {
	if groupRef != "" {
		groupID, err := projections.GetMailGroupID(queryable, ownerID, groupRef)
		if err != nil {
			return "", internalErr(err)
		}
		if groupID == "" {
			return "", Reply{Err: errDetail(proto.ErrNotFound, "mail group not found", false)}
		}
		return groupID, Reply{}
	}
	groupID, err := projections.MailGroupIDByName(queryable, ownerID, name)
	if err != nil {
		return "", internalErr(err)
	}
	if groupID != "" {
		return groupID, Reply{}
	}
	return idForNewGroup(), Reply{}
}

func (h *Handler) deleteMailGroup(actor *User, p proto.DeleteMailGroupPayload) Reply {
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

func (h *Handler) attachMail(actor *User, p proto.AttachMailPayload) Reply {
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

	fromUserID, found, err := projections.MailSenderID(tx, mailID)
	if err != nil {
		return internalErr(err)
	}
	if !found {
		return Reply{Err: errDetail(proto.ErrNotFound, "mail not found", false)}
	}
	if fromUserID != actor.ID {
		return Reply{Err: errDetail(proto.ErrForbidden, "only the sender can attach files to this mail", false)}
	}
	count, err := projections.MailAttachmentCount(tx, mailID)
	if err != nil {
		return internalErr(err)
	}
	if msg := proto.ValidateMailAttachmentCount(count + 1); msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	copyCounts, err := projections.ActiveMailCopyCounts(tx, mailID)
	if err == nil {
		err = h.lockMailboxes(tx, copyCounts)
	}
	if err != nil {
		return internalErr(err)
	}
	if errReply := EnsureMailQuota(tx, copyCounts, p.SizeBytes); errReply.Err != nil {
		return errReply
	}
	scopes, err := projections.MailAccountScopes(tx, mailID, actor.ID)
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

func (h *Handler) updateMail(actor *User, p proto.UpdateMailPayload) Reply {
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
			if errReply := EnsureMailQuota(h.db, map[string]int{actor.ID: restoreCount}, size); errReply.Err != nil {
				return errReply
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

func (h *Handler) deleteMail(actor *User, p proto.DeleteMailPayload) Reply {
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

func (h *Handler) deleteMailRange(actor *User, p proto.DeleteMailRangePayload) Reply {
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

func (h *Handler) sendDirectMessage(actor *User, p proto.SendDirectMessagePayload) Reply {
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

	target, reply := ResolveDirectMessageRecipient(tx, actor, p.To)
	if reply.Err != nil {
		return reply
	}
	id := newID("dm_")
	payload := proto.NewDirectMessageSentPayload(id, actor.ID, actor.Name, target.ID, target.Name, p.Body, ts)
	scopes := proto.DirectMessageEventScopes(actor.ID, target.ID)
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

func (h *Handler) setDirectMessageSettings(actor *User, p proto.SetDirectMessageSettingsPayload) Reply {
	policy, msg := proto.NormalizeDirectMessagePolicy(p.Policy)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if err := currentRuntime().SetDirectMessageSettings(h.db, actor.ID, policy); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: actor.ID}}
}

func (h *Handler) markDirectMessageRead(actor *User, p proto.MarkDirectMessageReadPayload) Reply {
	return h.updateDirectMessage(p.Message, func(messageID string) (bool, error) {
		return currentRuntime().MarkDirectMessageRead(h.db, actor.ID, messageID)
	})
}

func (h *Handler) deleteDirectMessage(actor *User, p proto.DeleteDirectMessagePayload) Reply {
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

func NormalizeMailAttachments(input []proto.AttachmentPayload, idFor func(int) string) ([]proto.AttachmentPayload, Reply) {
	if len(input) == 0 {
		return nil, Reply{}
	}
	attachments, msg := proto.NormalizeMailAttachments(input)
	if msg != "" {
		return nil, Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return proto.WithAttachmentIDs(attachments, idFor), Reply{}
}

func insertMailAttachments(tx *sql.Tx, mailID, authorID string, ts int64, attachments []proto.AttachmentPayload) error {
	for _, item := range attachments {
		if err := currentRuntime().InsertMailAttachment(tx, item.ID, mailID, item.Filename, item.ContentType, item.SizeBytes, item.URL, authorID, ts); err != nil {
			return err
		}
	}
	return nil
}

func MailCopyCounts(recipients []*User, senderID string, saveSent bool) map[string]int {
	copyCounts := map[string]int{}
	for _, recipient := range recipients {
		if recipient == nil {
			continue
		}
		copyCounts[recipient.ID]++
	}
	if saveSent {
		copyCounts[senderID]++
	}
	return copyCounts
}

func ResolveMailRecipients(queryable sqlQueryable, actor *User, refs []string, mailAll bool) ([]*User, Reply) {
	if actor == nil {
		return nil, Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}
	recipients := []*User{}
	seen := map[string]bool{}
	for _, ref := range refs {
		target, err := projections.FindUserRef(queryable, ref)
		if err != nil {
			return nil, internalErr(err)
		}
		if target == nil {
			return nil, Reply{Err: errDetail(proto.ErrNotFound, "recipient not found: "+strings.TrimSpace(ref), false)}
		}
		if target.ID != actor.ID && !mailAll {
			ignored, err := projections.UserRelationshipExists(queryable, target.ID, actor.ID, "ignore")
			if err != nil {
				return nil, internalErr(err)
			}
			if ignored {
				return nil, Reply{Err: errDetail(proto.ErrForbidden, "recipient does not accept mail from this user", false)}
			}
		}
		if !seen[target.ID] {
			seen[target.ID] = true
			recipients = append(recipients, target)
		}
	}
	if len(recipients) == 0 {
		return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "at least one recipient is required", false)}
	}
	return recipients, Reply{}
}

func ResolveDirectMessageRecipient(queryable sqlQueryable, actor *User, ref string) (*User, Reply) {
	if actor == nil {
		return nil, Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}
	target, err := projections.FindUserRef(queryable, ref)
	if err != nil {
		return nil, internalErr(err)
	}
	if target == nil {
		return nil, Reply{Err: errDetail(proto.ErrNotFound, "recipient not found", false)}
	}
	if target.ID == actor.ID {
		return target, Reply{}
	}
	ignored, err := projections.UserRelationshipExists(queryable, target.ID, actor.ID, "ignore")
	if err != nil {
		return nil, internalErr(err)
	}
	if ignored {
		return nil, Reply{Err: errDetail(proto.ErrForbidden, "recipient does not accept messages from this user", false)}
	}
	allowed, err := projections.DirectMessageAllowed(queryable, target.ID, actor.ID)
	if err != nil {
		return nil, internalErr(err)
	}
	if !allowed {
		return nil, Reply{Err: errDetail(proto.ErrForbidden, "recipient only accepts messages from friends", false)}
	}
	return target, Reply{}
}

func EnsureMailQuota(queryable sqlQueryable, copyCounts map[string]int, addedPerCopy int64) Reply {
	if addedPerCopy <= 0 {
		return Reply{}
	}
	for userID, copies := range copyCounts {
		if strings.TrimSpace(userID) == "" || copies <= 0 {
			continue
		}
		added := addedPerCopy * int64(copies)
		ok, err := projections.MailQuotaAllows(queryable, userID, added)
		if err != nil {
			return internalErr(err)
		}
		if !ok {
			return Reply{Err: errDetail(proto.ErrValidationFailed, "mail quota exceeded for user "+userID, false)}
		}
	}
	return Reply{}
}

func ExpandMailRecipients(db *sql.DB, actor *User, p proto.SendMailPayload) ([]string, Reply) {
	if actor == nil {
		return nil, Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}
	ownerID := actor.ID
	refs := []string{}
	if p.ToAll {
		if !actor.IsAdmin() {
			return nil, Reply{Err: errDetail(proto.ErrForbidden, "admin role required for mail-all", false)}
		}
		allUserIDs, err := projections.ListMailAllRecipientIDs(db, actor.ID)
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
		if proto.IsFriendMailGroupRef(groupRef) {
			friendIDs, err := currentRuntime().ListFriendUserIDs(db, ownerID)
			if err != nil {
				return nil, internalErr(err)
			}
			refs = append(refs, friendIDs...)
			continue
		}
		groupID, err := currentRuntime().GetMailGroupID(db, ownerID, groupRef)
		if err != nil {
			return nil, internalErr(err)
		}
		if groupID == "" {
			return nil, Reply{Err: errDetail(proto.ErrNotFound, "mail group not found: "+groupRef, false)}
		}
		members, err := currentRuntime().ListMailGroupMembers(db, ownerID, groupID)
		if err != nil {
			return nil, internalErr(err)
		}
		for _, member := range members {
			refs = append(refs, member.UserID)
		}
	}
	if p.ToFriends {
		friendIDs, err := currentRuntime().ListFriendUserIDs(db, ownerID)
		if err != nil {
			return nil, internalErr(err)
		}
		refs = append(refs, friendIDs...)
	}
	if len(refs) == 0 {
		return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "at least one recipient is required", false)}
	}
	if !p.ToAll && len(refs) > maxMailRecipientsPerSend {
		return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "too many recipients in one message", false)}
	}
	return refs, Reply{}
}

func ResolveUniqueMailGroupMembers(queryable sqlQueryable, refs []string, ownerID string) ([]string, Reply) {
	ids, missingRef, includesOwner, err := projections.ResolveMailGroupMemberIDs(queryable, refs, ownerID)
	if err != nil {
		return nil, internalErr(err)
	}
	if missingRef != "" {
		return nil, Reply{Err: errDetail(proto.ErrNotFound, "user not found: "+missingRef, false)}
	}
	if includesOwner {
		return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "mail group cannot include yourself", false)}
	}
	return ids, Reply{}
}

type errString string

func (e errString) Error() string { return string(e) }
