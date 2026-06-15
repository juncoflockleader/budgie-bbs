package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// --- Thread/post command implementations ---

func (h *Handler) createThread(actor *User, p proto.CreateThreadPayload) Reply {
	if p.Board == "" || p.Title == "" || p.Body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board, title, and body are required", false)}
	}
	pollBlock, cleanBody := extractPoll(p.Body)
	if pollBlock != nil && cleanBody != p.Body {
		if errReply := h.requireMinTrustForPoll(actor, 2, "create thread"); errReply.Err != nil {
			return errReply
		}
	}

	// Sanction check.
	if kind, ok := activeSanction(h.db, actor.ID, p.Board); ok {
		code := proto.ErrMuted
		if kind == "ban" {
			code = proto.ErrBanned
		}
		return Reply{Err: errDetail(code, "you are "+kind+"d in this board", false)}
	}
	settings, err := getBoardSettings(h.db, p.Board)
	if err != nil {
		return internalErr(err)
	}
	if settings == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}
	canModerateBoard := h.actorCanModerateBoard(actor, p.Board)
	if settings.ReadOnly && !canModerateBoard {
		return Reply{Err: errDetail(proto.ErrForbidden, "board is read-only", false)}
	}
	if (settings.MemberReadMode || settings.MemberPostMode) && !h.actorCanUseMemberBoard(actor, p.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board members only", false)}
	}
	attachments, errReply := h.normalizeAttachments(p.Attachments, settings.AttachmentsAllowed, canModerateBoard)
	if errReply.Err != nil {
		return errReply
	}
	authorName, authorID, errReply := h.postIdentity(actor, settings, p.Anonymous, canModerateBoard)
	if errReply.Err != nil {
		return errReply
	}
	signature, err := h.currentPostSignature(authorID)
	if err != nil {
		return internalErr(err)
	}
	contentFilter, err := matchContentFilter(h.db, p.Board, p.Title+"\n"+p.Body)
	if err != nil {
		return internalErr(err)
	}
	automodMatched, automodRuleID, automodAction, automodRuleReason, automodDuration, err := evaluateBoardAutomod(h.db, p.Board, p.Title+"\n"+p.Body, actor.ID)
	if err != nil {
		return internalErr(err)
	}

	ct := contentType(p.ContentType)
	ts := nowMS()
	threadID := newID("thr_")
	postID := newID("pst_")

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	// Verify board exists.
	var boardName string
	if err := qQueryRow(tx, `SELECT name FROM boards WHERE id=?`, p.Board).Scan(&boardName); err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	} else if err != nil {
		return internalErr(err)
	}

	scopes := []string{"board:" + p.Board}

	// Append thread.new
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: p.Title, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}

	threadScopes := append(scopes, "thread:"+threadID)

	// Append post.appended (the first post in the thread)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID, Body: cleanBody,
		RawBody:     p.Body,
		Signature:   signature,
		ContentType: ct, Attachments: attachments, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}

	// Update projections.
	newThread := &Thread{
		ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: p.Title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}
	if err := insertThread(tx, newThread); err != nil {
		return internalErr(err)
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID,
		Body: cleanBody, Signature: signature, ContentType: ct, CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return internalErr(err)
	}
	if err := h.insertAttachments(tx, postID, authorID, ts, attachments); err != nil {
		return internalErr(err)
	}
	if settings.RelayEnabled {
		if err := insertRelayDelivery(tx, newID("relay_"), p.Board, threadID, postID, authorID, authorName, p.Title, cleanBody, ts, pseq); err != nil {
			return internalErr(err)
		}
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return internalErr(err)
	}
	if err := ftsInsertPost(tx, postID, threadID, p.Board, authorName, cleanBody); err != nil {
		return internalErr(err)
	}
	var filterEvent *proto.Event
	filterGeneratedEvents := []*proto.Event{}
	if contentFilter != nil {
		filterEvent, filterGeneratedEvents, err = h.appendContentFilterReviewTx(tx, actor, authorName, contentFilter, postID, threadID, p.Board, !settings.MemberReadMode, ts)
		if err != nil {
			return internalErr(err)
		}
	}
	var automodEvents []*proto.Event
	if automodMatched {
		automodEvents, err = h.applyAutomodActionTx(tx, automodAction, automodReasonFor(automodRuleReason, automodRuleID), automodDuration, actor.ID, postID, threadID, p.Board, ts)
		if err != nil {
			return internalErr(err)
		}
	}
	if pollBlock != nil && cleanBody != p.Body {
		pollID := newID("pol_")
		if err := insertPoll(tx, pollID, postID, pollBlock.question, pollBlock.expiresAt, ts); err != nil {
			return internalErr(err)
		}
		for i, opt := range pollBlock.options {
			optID := newID("opt_")
			if err := insertPollOption(tx, optID, pollID, opt, i); err != nil {
				return internalErr(err)
			}
		}
	}
	if err := enqueueOutboxJob(tx, outboxPostCommitted, postCommittedJob{
		ActorID: actor.ID, ActorName: authorName, PostID: postID, ThreadID: threadID,
		BoardID: p.Board, Body: cleanBody, TS: ts, Seq: pseq,
	}, ts); err != nil {
		return internalErr(err)
	}
	if err := h.appendPostNotificationsTx(tx, actor, authorName, authorID, newThread, settings, postID, cleanBody, nil, ts); err != nil {
		return internalErr(err)
	}

	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	// Publish both events.
	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: p.Title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID, Body: cleanBody, RawBody: p.Body, Signature: signature, ContentType: ct, Attachments: attachments, TS: ts}, TS: ts})
	if filterEvent != nil {
		h.bus.Publish(filterEvent)
	}
	h.publishGeneratedEvents(filterGeneratedEvents)
	h.publishGeneratedEvents(automodEvents)

	return Reply{Result: &proto.AckResult{ID: threadID, Seq: pseq}}
}

func (h *Handler) appendPost(actor *User, p proto.AppendPostPayload) Reply {
	if p.Thread == "" || p.Body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "thread and body are required", false)}
	}
	userBody := p.Body
	pollBlock, cleanBody := extractPoll(userBody)
	pollStripped := pollBlock != nil && cleanBody != userBody
	if pollStripped {
		if errReply := h.requireMinTrustForPoll(actor, 2, "reply"); errReply.Err != nil {
			return errReply
		}
	}
	ct := contentType(p.ContentType)
	ts := nowMS()
	postID := newID("pst_")

	// All reads happen before the TX so we don't exhaust the single DB connection
	// (SetMaxOpenConns(1) means the TX holds the only connection).
	thread, err := getThread(h.db, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	settings, err := getBoardSettings(h.db, thread.Board)
	if err != nil {
		return internalErr(err)
	}
	if settings == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}
	canModerateBoard := h.actorCanModerateBoard(actor, thread.Board)
	canModerateThread := h.actorCanModerateBoardThreads(actor, thread.Board)
	if thread.Locked && !canModerateBoard {
		return Reply{Err: errDetail(proto.ErrThreadLocked, "thread is locked", false)}
	}
	if (settings.ReadOnly || settings.NoReply) && !canModerateBoard {
		return Reply{Err: errDetail(proto.ErrForbidden, "board is not accepting replies", false)}
	}
	rootNoReply, err := threadRootPostNoReply(h.db, thread.ID)
	if err != nil {
		return internalErr(err)
	}
	if rootNoReply && !canModerateThread {
		return Reply{Err: errDetail(proto.ErrForbidden, "thread starter is not accepting replies", false)}
	}
	if (settings.MemberReadMode || settings.MemberPostMode) && !h.actorCanUseMemberBoard(actor, thread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board members only", false)}
	}
	attachments, errReply := h.normalizeAttachments(p.Attachments, settings.AttachmentsAllowed, canModerateBoard)
	if errReply.Err != nil {
		return errReply
	}
	authorName, authorID, errReply := h.postIdentity(actor, settings, p.Anonymous, canModerateBoard)
	if errReply.Err != nil {
		return errReply
	}
	signature, err := h.currentPostSignature(authorID)
	if err != nil {
		return internalErr(err)
	}

	// Sanction check.
	if kind, ok := activeSanction(h.db, actor.ID, thread.Board); ok {
		code := proto.ErrMuted
		if kind == "ban" {
			code = proto.ErrBanned
		}
		return Reply{Err: errDetail(code, "you are "+kind+"d in this board", false)}
	}

	var mailBackTarget *Post
	var quoteSource *Post
	var replyNotifyTarget *Post
	// Threading depth cap: max 1 level. If replyTo is itself a reply, flatten.
	if p.ReplyTo != "" {
		parent, err := getPost(h.db, p.ReplyTo)
		if err != nil {
			return internalErr(err)
		}
		if parent == nil {
			return Reply{Err: errDetail(proto.ErrNotFound, "replyTo post not found", false)}
		}
		if parent.Thread != thread.ID {
			return Reply{Err: errDetail(proto.ErrValidationFailed, "replyTo post belongs to another thread", false)}
		}
		if p.QuotePost {
			if parent.Redacted {
				return Reply{Err: errDetail(proto.ErrConflict, "cannot quote a redacted post", false)}
			}
			quoteSource = parent
		}
		if parent.NoReply && !canModerateThread {
			return Reply{Err: errDetail(proto.ErrForbidden, "article is not accepting replies", false)}
		}
		replyNotifyTarget = parent
		if parent.MailBack {
			mailBackTarget = parent
		}
		if parent.ReplyTo != "" {
			// Already depth-1 reply — flatten to grandparent.
			p.ReplyTo = parent.ReplyTo
		}
	} else {
		if p.QuotePost {
			return Reply{Err: errDetail(proto.ErrValidationFailed, "replyTo is required for quoted replies", false)}
		}
		root, err := threadRootPost(h.db, thread.ID)
		if err != nil {
			return internalErr(err)
		}
		if root != nil && root.MailBack {
			mailBackTarget = root
		}
	}
	rawBody := userBody
	notificationBody := cleanBody
	if quoteSource != nil {
		prefix := formatQuotedReplyPrefix(quoteSource)
		cleanBody = prefix + cleanBody
		rawBody = prefix + userBody
	}
	contentFilter, err := matchContentFilter(h.db, thread.Board, rawBody)
	if err != nil {
		return internalErr(err)
	}
	automodMatched, automodRuleID, automodAction, automodRuleReason, automodDuration, err := evaluateBoardAutomod(h.db, thread.Board, rawBody, actor.ID)
	if err != nil {
		return internalErr(err)
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, scopes, &proto.PostAppendedPayload{
		ID: postID, Thread: p.Thread, Author: authorName, AuthorID: authorID, Body: cleanBody,
		RawBody:     rawBody,
		Signature:   signature,
		ContentType: ct, ReplyTo: p.ReplyTo, Attachments: attachments, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: p.Thread, Author: authorName, AuthorID: authorID,
		Body: cleanBody, Signature: signature, ContentType: ct, ReplyTo: p.ReplyTo, CreatedSeq: seq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return internalErr(err)
	}
	if err := h.insertAttachments(tx, postID, authorID, ts, attachments); err != nil {
		return internalErr(err)
	}
	if settings.RelayEnabled {
		if err := insertRelayDelivery(tx, newID("relay_"), thread.Board, p.Thread, postID, authorID, authorName, thread.Title, cleanBody, ts, seq); err != nil {
			return internalErr(err)
		}
	}
	if err := bumpThread(tx, p.Thread, seq); err != nil {
		return internalErr(err)
	}
	if err := ftsInsertPost(tx, postID, p.Thread, thread.Board, authorName, cleanBody); err != nil {
		return internalErr(err)
	}
	var filterEvent *proto.Event
	filterGeneratedEvents := []*proto.Event{}
	if contentFilter != nil {
		filterEvent, filterGeneratedEvents, err = h.appendContentFilterReviewTx(tx, actor, authorName, contentFilter, postID, p.Thread, thread.Board, !settings.MemberReadMode, ts)
		if err != nil {
			return internalErr(err)
		}
	}
	var automodEvents []*proto.Event
	if automodMatched {
		automodEvents, err = h.applyAutomodActionTx(tx, automodAction, automodReasonFor(automodRuleReason, automodRuleID), automodDuration, actor.ID, postID, p.Thread, thread.Board, ts)
		if err != nil {
			return internalErr(err)
		}
	}
	mailBackEvent, err := h.appendArticleMailBackTx(tx, actor, authorName, authorID, mailBackTarget, thread, postID, cleanBody, ts)
	if err != nil {
		return internalErr(err)
	}
	if pollStripped {
		// Create poll rows within the same TX.
		pollID := newID("pol_")
		if err := insertPoll(tx, pollID, postID, pollBlock.question, pollBlock.expiresAt, ts); err != nil {
			return internalErr(err)
		}
		for i, opt := range pollBlock.options {
			optID := newID("opt_")
			if err := insertPollOption(tx, optID, pollID, opt, i); err != nil {
				return internalErr(err)
			}
		}
	}
	if err := enqueueOutboxJob(tx, outboxPostCommitted, postCommittedJob{
		ActorID: actor.ID, ActorName: authorName, PostID: postID, ThreadID: p.Thread,
		BoardID: thread.Board, Body: cleanBody, ReplyTo: p.ReplyTo, TS: ts, Seq: seq,
	}, ts); err != nil {
		return internalErr(err)
	}
	if err := h.appendPostNotificationsTx(tx, actor, authorName, authorID, thread, settings, postID, notificationBody, replyNotifyTarget, ts); err != nil {
		return internalErr(err)
	}

	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: seq, Scopes: scopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: p.Thread, Author: authorName, AuthorID: authorID, Body: cleanBody, RawBody: rawBody, Signature: signature, ContentType: ct, ReplyTo: p.ReplyTo, Attachments: attachments, TS: ts}, TS: ts})
	if filterEvent != nil {
		h.bus.Publish(filterEvent)
	}
	if mailBackEvent != nil {
		h.bus.Publish(mailBackEvent)
	}
	h.publishGeneratedEvents(filterGeneratedEvents)
	h.publishGeneratedEvents(automodEvents)

	return Reply{Result: &proto.AckResult{ID: postID, Seq: seq}}
}

func (h *Handler) appendPostNotificationsTx(tx *sql.Tx, actor *User, authorName, authorID string, thread *Thread, settings *BoardSettings, postID, body string, replyTarget *Post, ts int64) error {
	if actor == nil || thread == nil {
		return nil
	}
	senderID := strings.TrimSpace(authorID)
	if senderID == "" {
		senderID = actor.ID
	}
	if senderID == "" {
		return nil
	}
	actorLabel := strings.TrimSpace(authorName)
	if actorLabel == "" {
		actorLabel = actor.Name
	}

	recipients := map[string]string{}
	addRecipient := func(userID, kind string) {
		userID = strings.TrimSpace(userID)
		if userID == "" || userID == senderID {
			return
		}
		if existing, ok := recipients[userID]; ok && notificationKindPriority(existing) >= notificationKindPriority(kind) {
			return
		}
		recipients[userID] = kind
	}

	for _, ref := range parseMentions(body) {
		target, err := findUserRefTx(tx, ref)
		if err != nil {
			return err
		}
		if target != nil {
			addRecipient(target.ID, "mention")
		}
	}
	if replyTarget != nil {
		addRecipient(replyTarget.AuthorID, "reply")
	}
	watchers, err := watchersOfThreadTx(tx, thread.ID, senderID)
	if err != nil {
		return err
	}
	for _, userID := range watchers {
		addRecipient(userID, "watched")
	}

	for userID, kind := range recipients {
		recipient, err := getUserTx(tx, userID)
		if err != nil {
			return err
		}
		if recipient == nil {
			continue
		}
		canReceive, err := userCanReceivePostNotificationTx(tx, recipient, thread.Board, settings)
		if err != nil {
			return err
		}
		if !canReceive {
			continue
		}
		level, err := threadPrefLevelTx(tx, recipient.ID, thread.ID)
		if err != nil {
			return err
		}
		if level == "mute" {
			continue
		}
		ignored, err := relationshipExistsTx(tx, recipient.ID, senderID, "ignore")
		if err != nil {
			return err
		}
		if ignored {
			continue
		}
		if err := insertNotificationTx(tx, newID("notif_"), recipient.ID, kind, thread.ID, postID, actorLabel, ts); err != nil {
			return err
		}
	}
	return nil
}

func notificationKindPriority(kind string) int {
	switch kind {
	case "mention":
		return 3
	case "reply":
		return 2
	case "watched":
		return 1
	default:
		return 0
	}
}

func threadPrefLevelTx(tx *sql.Tx, userID, threadID string) (string, error) {
	var level string
	err := qQueryRow(tx, `SELECT level FROM thread_prefs WHERE user_id=? AND thread_id=?`, userID, threadID).Scan(&level)
	if err == sql.ErrNoRows {
		return "normal", nil
	}
	return level, err
}

func userCanReceivePostNotificationTx(tx *sql.Tx, user *User, boardID string, settings *BoardSettings) (bool, error) {
	if user == nil {
		return false, nil
	}
	if settings == nil || !settings.MemberReadMode {
		return true, nil
	}
	if user.IsMod() {
		return true, nil
	}
	var exists int
	err := qQueryRow(tx, `SELECT 1 FROM board_moderators WHERE board_id=? AND user_id=?`, boardID, user.ID).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	err = qQueryRow(tx, `SELECT 1 FROM board_members WHERE board_id=? AND user_id=?`, boardID, user.ID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func formatQuotedReplyPrefix(source *Post) string {
	author := strings.TrimSpace(source.Author)
	if author == "" {
		author = "Unknown"
	}
	body := strings.TrimSpace(source.Body)
	if body == "" {
		body = "[empty article]"
	}
	lines := strings.Split(body, "\n")
	const maxQuoteLines = 24
	const maxQuoteBytes = 2400
	var b strings.Builder
	fmt.Fprintf(&b, "> %s wrote:\n", author)
	for i, line := range lines {
		if i >= maxQuoteLines || b.Len()+len(line)+8 > maxQuoteBytes {
			b.WriteString("> ...\n")
			break
		}
		line = strings.TrimRight(line, "\r")
		if line == "" {
			b.WriteString(">\n")
			continue
		}
		fmt.Fprintf(&b, "> %s\n", line)
	}
	b.WriteString("\n")
	return b.String()
}

func (h *Handler) repostPost(actor *User, p proto.RepostPostPayload) Reply {
	if actor == nil {
		return Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}
	p.Post = strings.TrimSpace(p.Post)
	p.Board = strings.TrimSpace(p.Board)
	p.Title = strings.TrimSpace(p.Title)
	if p.Post == "" || p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post and board are required", false)}
	}

	sourcePost, err := getPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if sourcePost == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "source post not found", false)}
	}
	if sourcePost.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot repost a redacted post", false)}
	}
	sourceThread, err := getThread(h.db, sourcePost.Thread)
	if err != nil {
		return internalErr(err)
	}
	if sourceThread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "source thread not found", false)}
	}
	sourceSettings, err := getBoardSettings(h.db, sourceThread.Board)
	if err != nil {
		return internalErr(err)
	}
	if sourceSettings != nil && sourceSettings.MemberReadMode && !h.actorCanUseMemberBoard(actor, sourceThread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "source board members only", false)}
	}

	settings, err := getBoardSettings(h.db, p.Board)
	if err != nil {
		return internalErr(err)
	}
	if settings == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "destination board not found", false)}
	}
	canModerateBoard := h.actorCanModerateBoard(actor, p.Board)
	if settings.ReadOnly && !canModerateBoard {
		return Reply{Err: errDetail(proto.ErrForbidden, "board is read-only", false)}
	}
	if (settings.MemberReadMode || settings.MemberPostMode) && !h.actorCanUseMemberBoard(actor, p.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board members only", false)}
	}
	if kind, ok := activeSanction(h.db, actor.ID, p.Board); ok {
		code := proto.ErrMuted
		if kind == "ban" {
			code = proto.ErrBanned
		}
		return Reply{Err: errDetail(code, "you are "+kind+"d in this board", false)}
	}

	title := p.Title
	if title == "" {
		title = sourceThread.Title
	}
	body := repostBody(sourcePost, sourceThread)
	authorName, authorID, errReply := h.postIdentity(actor, settings, false, canModerateBoard)
	if errReply.Err != nil {
		return errReply
	}
	signature, err := h.currentPostSignature(authorID)
	if err != nil {
		return internalErr(err)
	}
	contentFilter, err := matchContentFilter(h.db, p.Board, title+"\n"+body)
	if err != nil {
		return internalErr(err)
	}

	ct := contentType(sourcePost.ContentType)
	ts := nowMS()
	threadID := newID("thr_")
	postID := newID("pst_")

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	var boardName string
	if err := qQueryRow(tx, `SELECT name FROM boards WHERE id=?`, p.Board).Scan(&boardName); err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "destination board not found", false)}
	} else if err != nil {
		return internalErr(err)
	}

	scopes := []string{"board:" + p.Board}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: title, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}

	threadScopes := append(scopes, "thread:"+threadID)
	postPayload := &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID, Body: body,
		RawBody:        body,
		Signature:      signature,
		ContentType:    ct,
		SourcePost:     sourcePost.ID,
		SourceThread:   sourceThread.ID,
		SourceBoard:    sourceThread.Board,
		SourceAuthor:   sourcePost.Author,
		SourceAuthorID: sourcePost.AuthorID,
		SourceTitle:    sourceThread.Title,
		TS:             ts,
	}
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, postPayload)
	if err != nil {
		return internalErr(err)
	}

	if err := insertThread(tx, &Thread{
		ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return internalErr(err)
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID,
		Body: body, Signature: signature, ContentType: ct,
		SourcePost: sourcePost.ID, SourceThread: sourceThread.ID, SourceBoard: sourceThread.Board,
		SourceAuthor: sourcePost.Author, SourceAuthorID: sourcePost.AuthorID, SourceTitle: sourceThread.Title,
		CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return internalErr(err)
	}
	if settings.RelayEnabled {
		if err := insertRelayDelivery(tx, newID("relay_"), p.Board, threadID, postID, authorID, authorName, title, body, ts, pseq); err != nil {
			return internalErr(err)
		}
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return internalErr(err)
	}
	if err := ftsInsertPost(tx, postID, threadID, p.Board, authorName, body); err != nil {
		return internalErr(err)
	}
	var filterEvent *proto.Event
	filterGeneratedEvents := []*proto.Event{}
	if contentFilter != nil {
		filterEvent, filterGeneratedEvents, err = h.appendContentFilterReviewTx(tx, actor, authorName, contentFilter, postID, threadID, p.Board, !settings.MemberReadMode, ts)
		if err != nil {
			return internalErr(err)
		}
	}
	if err := enqueueOutboxJob(tx, outboxPostCommitted, postCommittedJob{
		ActorID: actor.ID, ActorName: authorName, PostID: postID, ThreadID: threadID,
		BoardID: p.Board, Body: body, TS: ts, Seq: pseq,
	}, ts); err != nil {
		return internalErr(err)
	}

	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes, Payload: postPayload, TS: ts})
	if filterEvent != nil {
		h.bus.Publish(filterEvent)
	}
	h.publishGeneratedEvents(filterGeneratedEvents)

	return Reply{Result: &proto.AckResult{ID: threadID, Seq: pseq}}
}

func repostBody(sourcePost *Post, sourceThread *Thread) string {
	return strings.TrimSpace(fmt.Sprintf(
		"Reposted from %s / %s\nOriginal author: %s\nOriginal post: %s\n\n%s",
		sourceThread.Board,
		sourceThread.Title,
		sourcePost.Author,
		sourcePost.ID,
		sourcePost.Body,
	))
}

func threadRootPostNoReply(db *sql.DB, threadID string) (bool, error) {
	var noReply int
	err := qQueryRow(db,
		`SELECT no_reply FROM posts WHERE thread=? ORDER BY created_seq LIMIT 1`,
		threadID,
	).Scan(&noReply)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return noReply != 0, err
}

func threadRootPost(db *sql.DB, threadID string) (*Post, error) {
	var postID string
	err := qQueryRow(db,
		`SELECT id FROM posts WHERE thread=? ORDER BY created_seq LIMIT 1`,
		threadID,
	).Scan(&postID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return getPost(db, postID)
}

func (h *Handler) appendArticleMailBackTx(tx *sql.Tx, actor *User, authorName, authorID string, target *Post, thread *Thread, replyPostID, replyBody string, ts int64) (*proto.Event, error) {
	if actor == nil || target == nil || !target.MailBack || target.Redacted {
		return nil, nil
	}
	authorID = strings.TrimSpace(authorID)
	if authorID == "" || strings.TrimSpace(target.AuthorID) == "" || target.AuthorID == authorID {
		return nil, nil
	}
	recipient, err := findUserRefTx(tx, target.AuthorID)
	if err != nil {
		return nil, err
	}
	if recipient == nil {
		return nil, nil
	}
	ignored, err := relationshipExistsTx(tx, recipient.ID, actor.ID, "ignore")
	if err != nil {
		return nil, err
	}
	if ignored {
		return nil, nil
	}

	subject := "Article reply: " + thread.Title
	body := formatArticleMailBackBody(thread, target, replyPostID, authorName, replyBody)
	if errReply := ensureMailQuotaTx(tx, map[string]int{recipient.ID: 1}, mailMessageSize(subject, body, nil)); errReply.Err != nil {
		if errReply.Err.Code != proto.ErrValidationFailed {
			return nil, fmt.Errorf("article mail-back quota check: %s", errReply.Err.Message)
		}
		return nil, nil
	}
	mailID := newID("mail_")
	scopes := []string{"account:" + actor.ID, "account:" + recipient.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtMailSent, scopes, &proto.MailSentPayload{
		ID: mailID, FromUserID: authorID, From: authorName, ToUserIDs: []string{recipient.ID}, To: []string{recipient.Name},
		Subject: subject, Body: body, SaveSent: false, TS: ts,
	})
	if err != nil {
		return nil, err
	}
	if err := insertMailMessage(tx, mailID, authorID, subject, body, "", ts, seq); err != nil {
		return nil, err
	}
	if err := insertMailCopy(tx, mailID, recipient.ID, "recipient", "inbox", false, false, ts); err != nil {
		return nil, err
	}
	return &proto.Event{Kind: proto.EvtMailSent, Seq: seq, Scopes: scopes,
		Payload: &proto.MailSentPayload{ID: mailID, FromUserID: authorID, From: authorName, ToUserIDs: []string{recipient.ID}, To: []string{recipient.Name}, Subject: subject, Body: body, SaveSent: false, TS: ts}, TS: ts}, nil
}

func formatArticleMailBackBody(thread *Thread, target *Post, replyPostID, authorName, replyBody string) string {
	return strings.TrimSpace(fmt.Sprintf(
		"Article reply mail-back\n\nBoard: %s\nThread: %s\nOriginal post: %s\nReply post: %s\nReply author: %s\n\n%s",
		thread.Board,
		thread.Title,
		target.ID,
		replyPostID,
		authorName,
		replyBody,
	))
}

func (h *Handler) postBoardMail(actor *User, p proto.PostBoardMailPayload) Reply {
	body := strings.TrimSpace(p.Body)
	if body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "body is required", false)}
	}
	boardID := strings.TrimSpace(p.Board)
	threadID := strings.TrimSpace(p.Thread)
	if threadID != "" {
		thread, err := getThread(h.db, threadID)
		if err != nil {
			return internalErr(err)
		}
		if thread == nil {
			return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
		}
		if boardID == "" {
			boardID = thread.Board
		} else if boardID != thread.Board {
			return Reply{Err: errDetail(proto.ErrValidationFailed, "thread does not belong to board", false)}
		}
	}
	if boardID == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	settings, err := getBoardSettings(h.db, boardID)
	if err != nil {
		return internalErr(err)
	}
	if settings == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}
	if !settings.MailInAllowed && !h.actorCanModerateBoard(actor, boardID) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board mail-in is disabled", false)}
	}
	contentType := strings.TrimSpace(p.ContentType)
	if threadID != "" {
		return h.appendPost(actor, proto.AppendPostPayload{
			Thread:      threadID,
			Body:        body,
			ContentType: contentType,
			Attachments: p.Attachments,
		})
	}
	title := strings.TrimSpace(p.Subject)
	if title == "" {
		title = "(no subject)"
	}
	return h.createThread(actor, proto.CreateThreadPayload{
		Board:       boardID,
		Title:       title,
		Body:        body,
		ContentType: contentType,
		Attachments: p.Attachments,
	})
}

func (h *Handler) currentPostSignature(authorID string) (string, error) {
	authorID = strings.TrimSpace(authorID)
	if authorID == "" {
		return "", nil
	}
	var selectedID string
	var randomEnabled int
	err := qQueryRow(h.db,
		`SELECT selected_signature_id, random_enabled FROM user_signature_settings WHERE user_id=?`,
		authorID,
	).Scan(&selectedID, &randomEnabled)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if randomEnabled != 0 {
		var count int
		if err := qQueryRow(h.db,
			`SELECT COUNT(*) FROM user_signatures
			  WHERE user_id=? AND active=1 AND TRIM(COALESCE(body,'')) <> ''`,
			authorID,
		).Scan(&count); err != nil {
			return "", err
		}
		if count > 0 {
			var signature string
			offset := int(nowMS() % int64(count))
			if err := qQueryRow(h.db,
				`SELECT COALESCE(body,'') FROM user_signatures
				  WHERE user_id=? AND active=1 AND TRIM(COALESCE(body,'')) <> ''
				  ORDER BY position, updated_at, id LIMIT 1 OFFSET ?`,
				authorID, offset,
			).Scan(&signature); err != nil {
				return "", err
			}
			return trimSignature(signature), nil
		}
	}
	if selectedID != "" {
		var signature string
		err := qQueryRow(h.db,
			`SELECT COALESCE(body,'') FROM user_signatures WHERE user_id=? AND id=? AND active=1`,
			authorID, selectedID,
		).Scan(&signature)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
		signature = trimSignature(signature)
		if signature != "" {
			return signature, nil
		}
	}
	var bankSignature string
	err = qQueryRow(h.db,
		`SELECT COALESCE(body,'') FROM user_signatures
		  WHERE user_id=? AND active=1 AND TRIM(COALESCE(body,'')) <> ''
		  ORDER BY position, updated_at, id LIMIT 1`,
		authorID,
	).Scan(&bankSignature)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	bankSignature = trimSignature(bankSignature)
	if bankSignature != "" {
		return bankSignature, nil
	}
	var signature string
	err = qQueryRow(h.db, `SELECT COALESCE(signature,'') FROM user_profiles WHERE user_id=?`, authorID).Scan(&signature)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return trimSignature(signature), nil
}

func trimSignature(signature string) string {
	signature = strings.TrimSpace(signature)
	if len(signature) > 500 {
		signature = signature[:500]
	}
	return signature
}

func (h *Handler) attachPost(actor *User, p proto.AttachPostPayload) Reply {
	if p.Post == "" || strings.TrimSpace(p.Filename) == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post and filename are required", false)}
	}
	filename := strings.TrimSpace(p.Filename)
	contentType := strings.TrimSpace(p.ContentType)
	if len(filename) > 160 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "attachment filename must be 160 characters or less", false)}
	}
	if len(contentType) > 120 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "attachment content type must be 120 characters or less", false)}
	}
	if p.SizeBytes < 0 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "attachment size cannot be negative", false)}
	}

	post, err := getPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot attach to a redacted post", false)}
	}
	thread, err := getThread(h.db, post.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	settings, err := getBoardSettings(h.db, thread.Board)
	if err != nil {
		return internalErr(err)
	}
	if settings == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}
	canModerateBoard := h.actorCanModerateBoard(actor, thread.Board)
	if !settings.AttachmentsAllowed && !canModerateBoard {
		return Reply{Err: errDetail(proto.ErrForbidden, "attachments are not enabled for this board", false)}
	}
	isAuthor := post.AuthorID == actor.ID
	if post.AuthorID == "" {
		isAuthor = post.Author == actor.Name
	}
	withinWindow := time.Now().UnixMilli()-post.CreatedAt < editWindowDur.Milliseconds()
	if !canModerateBoard && !(isAuthor && withinWindow) {
		return Reply{Err: errDetail(proto.ErrEditWindowExpired, "edit window has expired", false)}
	}
	var count int
	if err := qQueryRow(h.db, `SELECT COUNT(*) FROM post_attachments WHERE post_id=?`, p.Post).Scan(&count); err != nil {
		return internalErr(err)
	}
	if count >= 8 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "a post can have at most 8 attachments", false)}
	}

	attachmentID := strings.TrimSpace(p.ID)
	if attachmentID == "" {
		attachmentID = newID("att_")
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	payload := &proto.PostAttachmentAddedPayload{
		ID: attachmentID, Post: p.Post, Thread: thread.ID, Filename: filename,
		ContentType: contentType, SizeBytes: p.SizeBytes, AuthorID: actor.ID, TS: ts,
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAttachmentAdded, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	if err := insertPostAttachment(tx, attachmentID, p.Post, filename, contentType, p.SizeBytes, "", actor.ID, ts); err != nil {
		return internalErr(err)
	}
	if stagedBlobID := strings.TrimSpace(p.StagedBlobID); stagedBlobID != "" {
		if err := promoteStagedPostAttachmentBlob(tx, stagedBlobID, attachmentID, p.SizeBytes, contentType); err != nil {
			if errors.Is(err, projections.ErrStagedAttachmentBlobMissing) {
				return Reply{Err: errDetail(proto.ErrBlobStagingRequired, "staged attachment blob is not available yet", true)}
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
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAttachmentAdded, Seq: seq, Scopes: scopes, Payload: payload, TS: ts})
	return Reply{Result: &proto.AckResult{ID: attachmentID, Seq: seq}}
}

func (h *Handler) editPost(actor *User, p proto.EditPostPayload) Reply {
	if p.Post == "" || p.Body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post and body are required", false)}
	}
	pollBlock, _ := extractPoll(p.Body)
	if pollBlock != nil {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "editing posts with poll markup is not supported", false)}
	}
	var existingPollID string
	err := qQueryRow(h.db, `SELECT id FROM polls WHERE post_id=?`, p.Post).Scan(&existingPollID)
	if err == nil {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "editing posts that contain a poll is not supported", false)}
	}
	if err != nil && err != sql.ErrNoRows {
		return internalErr(err)
	}

	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	post, err := getPostTx(tx, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot edit a redacted post", false)}
	}

	isAuthor := post.AuthorID == actor.ID
	if post.AuthorID == "" {
		isAuthor = post.Author == actor.Name
	}
	withinWindow := time.Now().UnixMilli()-post.CreatedAt < editWindowDur.Milliseconds()
	if !actor.IsMod() && !(isAuthor && withinWindow) {
		return Reply{Err: errDetail(proto.ErrEditWindowExpired, "edit window has expired", false)}
	}

	thread, err := getThreadTx(tx, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}

	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostEdited, scopes, &proto.PostEditedPayload{
		ID: post.ID, Thread: post.Thread, NewBody: p.Body, Version: post.Version + 1, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := updatePostBody(tx, post.ID, p.Body, seq); err != nil {
		return internalErr(err)
	}
	if err := ftsUpdatePost(tx, post.ID, p.Body); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostEdited, Seq: seq, Scopes: scopes,
		Payload: &proto.PostEditedPayload{ID: post.ID, Thread: post.Thread, NewBody: p.Body, Version: post.Version + 1, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) setPostFlag(actor *User, p proto.SetPostFlagPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	if p.Marked == nil && p.Recommended == nil && p.NoReply == nil && p.TeX == nil && p.MailBack == nil {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "at least one article flag is required", false)}
	}
	post, err := getPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot flag a redacted post", false)}
	}
	thread, err := getThread(h.db, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}

	marked := post.Marked
	recommended := post.Recommended
	noReply := post.NoReply
	tex := post.TeX
	mailBack := post.MailBack
	curatorChange := false
	threadModerationChange := false
	authorMetadataChange := false
	if p.Marked != nil {
		curatorChange = curatorChange || *p.Marked != post.Marked
		marked = *p.Marked
	}
	if p.Recommended != nil {
		curatorChange = curatorChange || *p.Recommended != post.Recommended
		recommended = *p.Recommended
	}
	if p.NoReply != nil {
		threadModerationChange = *p.NoReply != post.NoReply
		noReply = *p.NoReply
	}
	if p.TeX != nil {
		authorMetadataChange = authorMetadataChange || *p.TeX != post.TeX
		tex = *p.TeX
	}
	if p.MailBack != nil {
		authorMetadataChange = authorMetadataChange || *p.MailBack != post.MailBack
		mailBack = *p.MailBack
	}
	if curatorChange && !h.actorCanCurateBoard(actor, thread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board curator permission required", false)}
	}
	if threadModerationChange && !h.actorCanModerateBoardThreads(actor, thread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board thread moderation permission required", false)}
	}
	if authorMetadataChange && (actor == nil || (actor.ID != post.AuthorID && !h.actorCanModerateBoardThreads(actor, thread.Board))) {
		return Reply{Err: errDetail(proto.ErrForbidden, "post author or board thread moderation permission required", false)}
	}
	if !curatorChange && !threadModerationChange && !authorMetadataChange {
		return Reply{Result: &proto.AckResult{ID: post.ID}}
	}

	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostFlagsSet, scopes, &proto.PostFlagsSetPayload{
		ID: post.ID, Thread: post.Thread, Marked: marked, Recommended: recommended, NoReply: noReply, TeX: tex, MailBack: mailBack, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := setPostFlags(tx, post.ID, marked, recommended, noReply, tex, mailBack, seq); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostFlagsSet, Seq: seq, Scopes: scopes,
		Payload: &proto.PostFlagsSetPayload{ID: post.ID, Thread: post.Thread, Marked: marked, Recommended: recommended, NoReply: noReply, TeX: tex, MailBack: mailBack, By: actor.Name, TS: ts}, TS: ts})
	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) redactPost(actor *User, p proto.RedactPostPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	post, err := getPostTx(tx, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "post is already redacted", false)}
	}

	isAuthor := post.AuthorID == actor.ID
	if post.AuthorID == "" {
		isAuthor = post.Author == actor.Name
	}
	withinWindow := time.Now().UnixMilli()-post.CreatedAt < editWindowDur.Milliseconds()
	thread, err := getThreadTx(tx, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}
	canModeratePosts := h.actorCanModerateBoardPostsTx(tx, actor, thread.Board)
	if !canModeratePosts && !(isAuthor && withinWindow) {
		return Reply{Err: errDetail(proto.ErrForbidden, "insufficient permissions to redact this post", false)}
	}
	deletionKind := "recycle"
	if !canModeratePosts && isAuthor && withinWindow {
		deletionKind = "junk"
	}

	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostRedacted, scopes, &proto.PostRedactedPayload{
		ID: post.ID, Thread: post.Thread, By: actor.ID, Reason: p.Reason, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := markPostRedacted(tx, post.ID, seq); err != nil {
		return internalErr(err)
	}
	if err := recordPostDeletion(tx, post.ID, post.Thread, thread.Board, actor.ID, actor.Name, p.Reason, deletionKind, ts, seq); err != nil {
		return internalErr(err)
	}
	if err := ftsDeletePost(tx, post.ID); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostRedacted, Seq: seq, Scopes: scopes,
		Payload: &proto.PostRedactedPayload{ID: post.ID, Thread: post.Thread, By: actor.Name, Reason: p.Reason, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) restorePost(actor *User, p proto.RestorePostPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	post, err := getPostTx(tx, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if !post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "post is not redacted", false)}
	}

	thread, err := getThreadTx(tx, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}
	if !h.actorCanModerateBoardPostsTx(tx, actor, thread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board post moderation permission required", false)}
	}

	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostRestored, scopes, &proto.PostRestoredPayload{
		ID: post.ID, Thread: post.Thread, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := markPostRestored(tx, post.ID, seq); err != nil {
		return internalErr(err)
	}
	if err := clearPostDeletion(tx, post.ID); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostRestored, Seq: seq, Scopes: scopes,
		Payload: &proto.PostRestoredPayload{ID: post.ID, Thread: post.Thread, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) redactPostRange(actor *User, p proto.RedactPostRangePayload) Reply {
	boardID := strings.TrimSpace(p.Board)
	if boardID == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	postIDs, errReply := normalizePostRangeIDs(p.Posts)
	if errReply.Err != nil {
		return errReply
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if errReply := ensureRangeBoardAccessTx(tx, h, actor, boardID); errReply.Err != nil {
		return errReply
	}

	published := make([]proto.Event, 0, len(postIDs))
	var lastSeq int64
	for _, postID := range postIDs {
		post, thread, errReply := loadRangePostTx(tx, postID, boardID)
		if errReply.Err != nil {
			return errReply
		}
		if post.Redacted {
			return Reply{Err: errDetail(proto.ErrConflict, "post is already redacted: "+postID, false)}
		}
		scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
		seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostRedacted, scopes, &proto.PostRedactedPayload{
			ID: post.ID, Thread: post.Thread, By: actor.ID, Reason: p.Reason, TS: ts,
		})
		if err != nil {
			return internalErr(err)
		}
		if err := markPostRedacted(tx, post.ID, seq); err != nil {
			return internalErr(err)
		}
		if err := recordPostDeletion(tx, post.ID, post.Thread, thread.Board, actor.ID, actor.Name, p.Reason, "recycle", ts, seq); err != nil {
			return internalErr(err)
		}
		if err := ftsDeletePost(tx, post.ID); err != nil {
			return internalErr(err)
		}
		lastSeq = seq
		published = append(published, proto.Event{Kind: proto.EvtPostRedacted, Seq: seq, Scopes: scopes,
			Payload: &proto.PostRedactedPayload{ID: post.ID, Thread: post.Thread, By: actor.Name, Reason: p.Reason, TS: ts}, TS: ts})
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	for i := range published {
		h.bus.Publish(&published[i])
	}
	return Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%d", len(postIDs)), Seq: lastSeq}}
}

func (h *Handler) restorePostRange(actor *User, p proto.RestorePostRangePayload) Reply {
	boardID := strings.TrimSpace(p.Board)
	if boardID == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	postIDs, errReply := normalizePostRangeIDs(p.Posts)
	if errReply.Err != nil {
		return errReply
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if errReply := ensureRangeBoardAccessTx(tx, h, actor, boardID); errReply.Err != nil {
		return errReply
	}

	published := make([]proto.Event, 0, len(postIDs))
	var lastSeq int64
	for _, postID := range postIDs {
		post, thread, errReply := loadRangePostTx(tx, postID, boardID)
		if errReply.Err != nil {
			return errReply
		}
		if !post.Redacted {
			return Reply{Err: errDetail(proto.ErrConflict, "post is not redacted: "+postID, false)}
		}
		scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
		seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostRestored, scopes, &proto.PostRestoredPayload{
			ID: post.ID, Thread: post.Thread, By: actor.ID, TS: ts,
		})
		if err != nil {
			return internalErr(err)
		}
		if err := markPostRestored(tx, post.ID, seq); err != nil {
			return internalErr(err)
		}
		if err := clearPostDeletion(tx, post.ID); err != nil {
			return internalErr(err)
		}
		lastSeq = seq
		published = append(published, proto.Event{Kind: proto.EvtPostRestored, Seq: seq, Scopes: scopes,
			Payload: &proto.PostRestoredPayload{ID: post.ID, Thread: post.Thread, By: actor.Name, TS: ts}, TS: ts})
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	for i := range published {
		h.bus.Publish(&published[i])
	}
	return Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%d", len(postIDs)), Seq: lastSeq}}
}

func (h *Handler) clearBoardJunk(actor *User, p proto.ClearBoardJunkPayload) Reply {
	boardID := strings.TrimSpace(p.Board)
	if boardID == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if errReply := ensureRangeBoardAccessTx(tx, h, actor, boardID); errReply.Err != nil {
		return errReply
	}
	postIDs, errReply := boardJunkIDsTx(tx, boardID, p.Posts)
	if errReply.Err != nil {
		return errReply
	}

	published := make([]proto.Event, 0, len(postIDs))
	var lastSeq int64
	for _, postID := range postIDs {
		var threadID string
		err := qQueryRow(tx,
			`SELECT thread_id FROM post_deletions WHERE post_id=? AND board_id=? AND kind='junk'`,
			postID, boardID,
		).Scan(&threadID)
		if err == sql.ErrNoRows {
			return Reply{Err: errDetail(proto.ErrNotFound, "junk post not found: "+postID, false)}
		}
		if err != nil {
			return internalErr(err)
		}
		scopes := []string{"thread:" + threadID, "board:" + boardID}
		seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostDeletionCleared, scopes, &proto.PostDeletionClearedPayload{
			ID: postID, Thread: threadID, Board: boardID, Kind: "junk", By: actor.ID, TS: ts,
		})
		if err != nil {
			return internalErr(err)
		}
		if err := clearPostDeletion(tx, postID); err != nil {
			return internalErr(err)
		}
		lastSeq = seq
		published = append(published, proto.Event{Kind: proto.EvtPostDeletionCleared, Seq: seq, Scopes: scopes,
			Payload: &proto.PostDeletionClearedPayload{ID: postID, Thread: threadID, Board: boardID, Kind: "junk", By: actor.Name, TS: ts}, TS: ts})
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	for i := range published {
		h.bus.Publish(&published[i])
	}
	return Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%d", len(postIDs)), Seq: lastSeq}}
}

func ensureRangeBoardAccessTx(tx *sql.Tx, h *Handler, actor *User, boardID string) Reply {
	var exists int
	if err := qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, boardID).Scan(&exists); err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	} else if err != nil {
		return internalErr(err)
	}
	if !h.actorCanModerateBoardPostsTx(tx, actor, boardID) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board post moderation permission required", false)}
	}
	return Reply{}
}

func loadRangePostTx(tx *sql.Tx, postID, boardID string) (*Post, *Thread, Reply) {
	post, err := getPostTx(tx, postID)
	if err != nil {
		return nil, nil, internalErr(err)
	}
	if post == nil {
		return nil, nil, Reply{Err: errDetail(proto.ErrNotFound, "post not found: "+postID, false)}
	}
	thread, err := getThreadTx(tx, post.Thread)
	if err != nil {
		return nil, nil, internalErr(err)
	}
	if thread == nil || thread.Board != boardID {
		return nil, nil, Reply{Err: errDetail(proto.ErrNotFound, "post not found in board: "+postID, false)}
	}
	return post, thread, Reply{}
}

func boardJunkIDsTx(tx *sql.Tx, boardID string, requested []string) ([]string, Reply) {
	if len(requested) > 0 {
		return normalizePostRangeIDs(requested)
	}
	rows, err := projections.QQuery(tx,
		`SELECT post_id FROM post_deletions WHERE board_id=? AND kind='junk' ORDER BY deleted_at DESC, seq DESC`,
		boardID,
	)
	if err != nil {
		return nil, internalErr(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var postID string
		if err := rows.Scan(&postID); err != nil {
			return nil, internalErr(err)
		}
		out = append(out, postID)
	}
	if err := rows.Err(); err != nil {
		return nil, internalErr(err)
	}
	return out, Reply{}
}

func normalizePostRangeIDs(input []string) ([]string, Reply) {
	if len(input) == 0 {
		return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "posts are required", false)}
	}
	if len(input) > 100 {
		return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "post range can include at most 100 items", false)}
	}
	out := make([]string, 0, len(input))
	seen := map[string]bool{}
	for _, raw := range input {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "post id cannot be empty", false)}
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, Reply{}
}

func (h *Handler) setThreadTitle(actor *User, p proto.SetThreadTitlePayload) Reply {
	if p.Thread == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "thread is required", false)}
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "title is required", false)}
	}
	if len(title) > 160 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "title must be 160 characters or less", false)}
	}
	if actor == nil {
		return Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}

	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	thread, err := getThreadTx(tx, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	if thread.Title == title {
		return Reply{Result: &proto.AckResult{ID: thread.ID}}
	}

	canModerateThread := h.actorCanModerateBoardThreadsTx(tx, actor, thread.Board)
	isAuthor := thread.AuthorID == actor.ID
	if thread.AuthorID == "" {
		isAuthor = thread.Author == actor.Name
	}
	withinWindow := time.Now().UnixMilli()-thread.CreatedAt < editWindowDur.Milliseconds()
	if !canModerateThread {
		if !isAuthor {
			return Reply{Err: errDetail(proto.ErrForbidden, "thread author or board thread moderation permission required", false)}
		}
		if !withinWindow {
			return Reply{Err: errDetail(proto.ErrEditWindowExpired, "edit window has expired", false)}
		}
	}

	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadTitleSet, scopes, &proto.ThreadTitleSetPayload{
		Thread: thread.ID, Title: title, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := setThreadTitle(tx, thread.ID, title, ts); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadTitleSet, Seq: seq, Scopes: scopes,
		Payload: &proto.ThreadTitleSetPayload{Thread: thread.ID, Title: title, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: thread.ID, Seq: seq}}
}

func (h *Handler) lockThread(actor *User, p proto.LockThreadPayload) Reply {
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	thread, err := getThreadTx(tx, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	if !h.actorCanModerateBoardThreadsTx(tx, actor, thread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board thread moderation permission required", false)}
	}

	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadLocked, scopes, &proto.ThreadLockedPayload{
		Thread: thread.ID, Locked: p.Locked, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := setThreadLocked(tx, thread.ID, p.Locked); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadLocked, Seq: seq, Scopes: scopes,
		Payload: &proto.ThreadLockedPayload{Thread: thread.ID, Locked: p.Locked, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: thread.ID, Seq: seq}}
}

func (h *Handler) moveThread(actor *User, p proto.MoveThreadPayload) Reply {
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	thread, err := getThreadTx(tx, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	if !h.actorCanModerateBoardThreadsTx(tx, actor, thread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board thread moderation permission required", false)}
	}

	var destName string
	if err := qQueryRow(tx, `SELECT name FROM boards WHERE id=?`, p.ToBoard).Scan(&destName); err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "destination board not found", false)}
	} else if err != nil {
		return internalErr(err)
	}

	scopes := []string{"board:" + thread.Board, "board:" + p.ToBoard}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadMoved, scopes, &proto.ThreadMovedPayload{
		Thread: thread.ID, FromBoard: thread.Board, ToBoard: p.ToBoard, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := moveThreadBoard(tx, thread.ID, p.ToBoard); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadMoved, Seq: seq, Scopes: scopes,
		Payload: &proto.ThreadMovedPayload{Thread: thread.ID, FromBoard: thread.Board, ToBoard: p.ToBoard, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: thread.ID, Seq: seq}}
}

func (h *Handler) postIdentity(actor *User, settings *BoardSettings, anonymous bool, canModerateBoard bool) (string, string, Reply) {
	if !anonymous {
		return actor.Name, actor.ID, Reply{}
	}
	if settings == nil || (!settings.AnonymousAllowed && !canModerateBoard) {
		return "", "", Reply{Err: errDetail(proto.ErrForbidden, "anonymous posting is not enabled for this board", false)}
	}
	return "Anonymous", "", Reply{}
}

func (h *Handler) normalizeAttachments(input []proto.AttachmentPayload, allowed bool, canModerateBoard bool) ([]proto.AttachmentPayload, Reply) {
	if len(input) == 0 {
		return nil, Reply{}
	}
	if !allowed && !canModerateBoard {
		return nil, Reply{Err: errDetail(proto.ErrForbidden, "attachments are not enabled for this board", false)}
	}
	if len(input) > 8 {
		return nil, Reply{Err: errDetail(proto.ErrValidationFailed, "a post can have at most 8 attachments", false)}
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
			ID:          newID("att_"),
			Filename:    filename,
			ContentType: contentType,
			SizeBytes:   item.SizeBytes,
			URL:         url,
		})
	}
	return out, Reply{}
}

func (h *Handler) insertAttachments(tx *sql.Tx, postID, authorID string, ts int64, attachments []proto.AttachmentPayload) error {
	for _, item := range attachments {
		if err := insertPostAttachment(tx, item.ID, postID, item.Filename, item.ContentType, item.SizeBytes, item.URL, authorID, ts); err != nil {
			return err
		}
	}
	return nil
}
