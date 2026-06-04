package handler

import (
	"database/sql"
	"strings"
	"time"

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
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: p.Title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
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

	return Reply{Result: &proto.AckResult{ID: threadID, Seq: pseq}}
}

func (h *Handler) appendPost(actor *User, p proto.AppendPostPayload) Reply {
	if p.Thread == "" || p.Body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "thread and body are required", false)}
	}
	pollBlock, cleanBody := extractPoll(p.Body)
	if pollBlock != nil && cleanBody != p.Body {
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
	if thread.Locked && !canModerateBoard {
		return Reply{Err: errDetail(proto.ErrThreadLocked, "thread is locked", false)}
	}
	if (settings.ReadOnly || settings.NoReply) && !canModerateBoard {
		return Reply{Err: errDetail(proto.ErrForbidden, "board is not accepting replies", false)}
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
	contentFilter, err := matchContentFilter(h.db, thread.Board, p.Body)
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

	// Threading depth cap: max 1 level. If replyTo is itself a reply, flatten.
	if p.ReplyTo != "" {
		parent, err := getPost(h.db, p.ReplyTo)
		if err != nil {
			return internalErr(err)
		}
		if parent == nil {
			return Reply{Err: errDetail(proto.ErrNotFound, "replyTo post not found", false)}
		}
		if parent.ReplyTo != "" {
			// Already depth-1 reply — flatten to grandparent.
			p.ReplyTo = parent.ReplyTo
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, scopes, &proto.PostAppendedPayload{
		ID: postID, Thread: p.Thread, Author: authorName, AuthorID: authorID, Body: cleanBody,
		RawBody:     p.Body,
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
	if pollBlock != nil && cleanBody != p.Body {
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

	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: seq, Scopes: scopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: p.Thread, Author: authorName, AuthorID: authorID, Body: cleanBody, RawBody: p.Body, Signature: signature, ContentType: ct, ReplyTo: p.ReplyTo, Attachments: attachments, TS: ts}, TS: ts})
	if filterEvent != nil {
		h.bus.Publish(filterEvent)
	}
	h.publishGeneratedEvents(filterGeneratedEvents)

	return Reply{Result: &proto.AckResult{ID: postID, Seq: seq}}
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
	if !h.actorCanModerateBoardPostsTx(tx, actor, thread.Board) && !(isAuthor && withinWindow) {
		return Reply{Err: errDetail(proto.ErrForbidden, "insufficient permissions to redact this post", false)}
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
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostRestored, Seq: seq, Scopes: scopes,
		Payload: &proto.PostRestoredPayload{ID: post.ID, Thread: post.Thread, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
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
