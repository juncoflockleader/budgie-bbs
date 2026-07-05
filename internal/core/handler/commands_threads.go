package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandrules"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// --- Thread/post command implementations ---

func (h *Handler) createThread(actor *User, p proto.CreateThreadPayload) Reply {
	p, msg := proto.NormalizeCreateThreadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	pollBlock, cleanBody := extractPoll(p.Body)
	if pollBlock != nil && cleanBody != p.Body {
		if errDetail := commandrules.RequireMinTrustForPoll(h.db, actor, 2, "create thread", currentRuntime().UserTrustLevel); errDetail != nil {
			return Reply{Err: errDetail}
		}
	}

	// Sanction check.
	if kind, ok := currentRuntime().ActiveSanction(h.db, actor.ID, p.Board); ok {
		return Reply{Err: commandrules.ActiveBoardSanctionError(kind)}
	}
	settings, err := currentRuntime().GetBoardSettings(h.db, p.Board)
	if err != nil {
		return internalErr(err)
	}
	if settings == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}
	canModerateBoard := commandrules.ActorCanModerateBoard(h.db, actor, p.Board)
	if errDetail := commandrules.RequireThreadCreationBoardAccess(h.db, actor, p.Board, settings, canModerateBoard); errDetail != nil {
		return Reply{Err: errDetail}
	}
	attachments, ruleErr := commandrules.NormalizePostAttachments(p.Attachments, settings.AttachmentsAllowed, canModerateBoard, func(int) string {
		return newID("att_")
	})
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	authorName, authorID, ruleErr := commandrules.PostIdentity(actor, settings, p.Anonymous, canModerateBoard)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	signature, err := h.currentPostSignature(authorID)
	if err != nil {
		return internalErr(err)
	}
	contentFilter, err := currentRuntime().MatchContentFilter(h.db, p.Board, p.Title+"\n"+p.Body)
	if err != nil {
		return internalErr(err)
	}
	automodMatched, automodRuleID, automodMatchType, automodAction, automodRuleReason, automodDuration, err := currentRuntime().EvaluateBoardAutomod(h.db, p.Board, p.Title+"\n"+p.Body, actor.ID)
	if err != nil {
		return internalErr(err)
	}

	ct := proto.NormalizePostContentType(p.ContentType)
	ts := nowMS()
	threadID := newID("thr_")
	postID := newID("pst_")

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	// Verify board exists.
	if _, found, err := projections.BoardName(tx, p.Board); err != nil {
		return internalErr(err)
	} else if !found {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}

	scopes := []string{"board:" + p.Board}

	// Append thread.new
	threadPayload := &proto.ThreadNewPayload{
		ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: p.Title, TS: ts,
	}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, threadPayload)
	if err != nil {
		return internalErr(err)
	}

	threadScopes := append(scopes, "thread:"+threadID)

	// Append post.appended (the first post in the thread)
	postPayload := &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID, Body: cleanBody,
		RawBody:     p.Body,
		Signature:   signature,
		ContentType: ct, Attachments: attachments, TS: ts,
	}
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, postPayload)
	if err != nil {
		return internalErr(err)
	}

	// Update projections.
	newThread := &Thread{
		ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: p.Title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}
	if err := currentRuntime().InsertThread(tx, newThread); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().InsertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID,
		Body: cleanBody, Signature: signature, ContentType: ct, CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return internalErr(err)
	}
	if err := h.insertAttachments(tx, postID, authorID, ts, attachments); err != nil {
		return internalErr(err)
	}
	if settings.RelayEnabled {
		if err := currentRuntime().InsertRelayDelivery(tx, newID("relay_"), p.Board, threadID, postID, authorID, authorName, p.Title, cleanBody, ts, pseq); err != nil {
			return internalErr(err)
		}
	}
	if err := currentRuntime().BumpThread(tx, threadID, pseq); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().FtsInsertPost(tx, postID, threadID, p.Board, authorName, cleanBody); err != nil {
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
		automodEvents, err = h.applyAutomodActionTx(tx, automodRuleID, automodMatchType, automodAction, automodReasonFor(automodRuleReason, automodRuleID), automodDuration, actor.ID, postID, threadID, p.Board, ts)
		if err != nil {
			return internalErr(err)
		}
	}
	if pollBlock != nil && cleanBody != p.Body {
		pollID := newID("pol_")
		if err := currentRuntime().InsertPoll(tx, pollID, postID, pollBlock.question, pollBlock.expiresAt, ts); err != nil {
			return internalErr(err)
		}
		for i, opt := range pollBlock.options {
			optID := newID("opt_")
			if err := currentRuntime().InsertPollOption(tx, optID, pollID, opt, i); err != nil {
				return internalErr(err)
			}
		}
	}
	if err := currentRuntime().EnqueueOutboxJob(tx, outboxPostCommitted, postCommittedJob{
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
	h.publishEvent(proto.EvtThreadNew, tseq, scopes, threadPayload, ts)
	h.publishEvent(proto.EvtPostAppended, pseq, threadScopes, postPayload, ts)
	if filterEvent != nil {
		h.bus.Publish(filterEvent)
	}
	h.publishGeneratedEvents(filterGeneratedEvents)
	h.publishGeneratedEvents(automodEvents)

	return Reply{Result: &proto.AckResult{ID: threadID, Seq: pseq}}
}

func (h *Handler) appendPost(actor *User, p proto.AppendPostPayload) Reply {
	p, msg := proto.NormalizeAppendPostPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	userBody := p.Body
	pollBlock, cleanBody := extractPoll(userBody)
	pollStripped := pollBlock != nil && cleanBody != userBody
	if pollStripped {
		if errDetail := commandrules.RequireMinTrustForPoll(h.db, actor, 2, "reply", currentRuntime().UserTrustLevel); errDetail != nil {
			return Reply{Err: errDetail}
		}
	}
	ct := proto.NormalizePostContentType(p.ContentType)
	ts := nowMS()
	postID := newID("pst_")

	// All reads happen before the TX so we don't exhaust the single DB connection
	// (SetMaxOpenConns(1) means the TX holds the only connection).
	thread, err := currentRuntime().GetThread(h.db, p.Thread)
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
	if settings == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}
	canModerateBoard := commandrules.ActorCanModerateBoard(h.db, actor, thread.Board)
	canModerateThread := commandrules.ActorCanModerateBoardThreads(h.db, actor, thread.Board)
	if thread.Locked && !canModerateBoard {
		return Reply{Err: errDetail(proto.ErrThreadLocked, "thread is locked", false)}
	}
	if errDetail := commandrules.RequireReplyBoardAccess(h.db, actor, thread.Board, settings, canModerateBoard); errDetail != nil {
		return Reply{Err: errDetail}
	}
	rootReplyGuards, err := projections.ThreadRootReplyGuardsForThread(h.db, thread.ID)
	if err != nil {
		return internalErr(err)
	}
	if rootReplyGuards.NoReply && !canModerateThread {
		return Reply{Err: errDetail(proto.ErrForbidden, "thread starter is not accepting replies", false)}
	}
	attachments, ruleErr := commandrules.NormalizePostAttachments(p.Attachments, settings.AttachmentsAllowed, canModerateBoard, func(int) string {
		return newID("att_")
	})
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	authorName, authorID, ruleErr := commandrules.PostIdentity(actor, settings, p.Anonymous, canModerateBoard)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	signature, err := h.currentPostSignature(authorID)
	if err != nil {
		return internalErr(err)
	}

	// Sanction check.
	if kind, ok := currentRuntime().ActiveSanction(h.db, actor.ID, thread.Board); ok {
		return Reply{Err: commandrules.ActiveBoardSanctionError(kind)}
	}

	var mailBackTarget *Post
	var quoteSource *Post
	var replyNotifyTarget *Post
	var parent *Post
	if p.ReplyTo != "" {
		parent, err = currentRuntime().GetPost(h.db, p.ReplyTo)
		if err != nil {
			return internalErr(err)
		}
	}
	replyPlan, ruleErr := commandrules.PlanReplyTarget(p.ReplyTo, parent, thread.ID, p.QuotePost, canModerateThread)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	p.ReplyTo = replyPlan.EffectiveReplyTo
	mailBackTarget = replyPlan.MailBackTarget
	quoteSource = replyPlan.QuoteSource
	replyNotifyTarget = replyPlan.ReplyNotifyTarget
	if replyPlan.NeedsRootMailBack {
		root, err := projections.ThreadRootPost(h.db, thread.ID)
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
		prefix := proto.FormatQuotedReplyPrefix(quoteSource.Author, quoteSource.Body)
		cleanBody = prefix + cleanBody
		rawBody = prefix + userBody
	}
	contentFilter, err := currentRuntime().MatchContentFilter(h.db, thread.Board, rawBody)
	if err != nil {
		return internalErr(err)
	}
	automodMatched, automodRuleID, automodMatchType, automodAction, automodRuleReason, automodDuration, err := currentRuntime().EvaluateBoardAutomod(h.db, thread.Board, rawBody, actor.ID)
	if err != nil {
		return internalErr(err)
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	eventPayload := &proto.PostAppendedPayload{
		ID: postID, Thread: p.Thread, Author: authorName, AuthorID: authorID, Body: cleanBody,
		RawBody:     rawBody,
		Signature:   signature,
		ContentType: ct, ReplyTo: p.ReplyTo, Attachments: attachments, TS: ts,
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().InsertPost(tx, &Post{
		ID: postID, Thread: p.Thread, Author: authorName, AuthorID: authorID,
		Body: cleanBody, Signature: signature, ContentType: ct, ReplyTo: p.ReplyTo, CreatedSeq: seq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return internalErr(err)
	}
	if err := h.insertAttachments(tx, postID, authorID, ts, attachments); err != nil {
		return internalErr(err)
	}
	if settings.RelayEnabled {
		if err := currentRuntime().InsertRelayDelivery(tx, newID("relay_"), thread.Board, p.Thread, postID, authorID, authorName, thread.Title, cleanBody, ts, seq); err != nil {
			return internalErr(err)
		}
	}
	if err := currentRuntime().BumpThread(tx, p.Thread, seq); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().FtsInsertPost(tx, postID, p.Thread, thread.Board, authorName, cleanBody); err != nil {
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
		automodEvents, err = h.applyAutomodActionTx(tx, automodRuleID, automodMatchType, automodAction, automodReasonFor(automodRuleReason, automodRuleID), automodDuration, actor.ID, postID, p.Thread, thread.Board, ts)
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
		if err := currentRuntime().InsertPoll(tx, pollID, postID, pollBlock.question, pollBlock.expiresAt, ts); err != nil {
			return internalErr(err)
		}
		for i, opt := range pollBlock.options {
			optID := newID("opt_")
			if err := currentRuntime().InsertPollOption(tx, optID, pollID, opt, i); err != nil {
				return internalErr(err)
			}
		}
	}
	if err := currentRuntime().EnqueueOutboxJob(tx, outboxPostCommitted, postCommittedJob{
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

	h.publishEvent(proto.EvtPostAppended, seq, scopes, eventPayload, ts)
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
		if existing, ok := recipients[userID]; ok {
			kind = commandrules.PreferredPostNotificationKind(existing, kind)
		}
		recipients[userID] = kind
	}

	for _, ref := range parseMentions(body) {
		target, err := projections.FindUserRef(tx, ref)
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
	watchers, err := currentRuntime().WatchersOfThreadTx(tx, thread.ID, senderID)
	if err != nil {
		return err
	}
	for _, userID := range watchers {
		addRecipient(userID, "watched")
	}

	for userID, kind := range recipients {
		recipient, err := currentRuntime().GetUserTx(tx, userID)
		if err != nil {
			return err
		}
		if recipient == nil {
			continue
		}
		canReceive, err := commandrules.UserCanReceivePostNotification(tx, recipient, thread.Board, settings)
		if err != nil {
			return err
		}
		if !canReceive {
			continue
		}
		level, err := commandrules.ThreadPrefLevel(tx, recipient.ID, thread.ID)
		if err != nil {
			return err
		}
		if level == "mute" {
			continue
		}
		ignored, err := projections.UserRelationshipExists(tx, recipient.ID, senderID, "ignore")
		if err != nil {
			return err
		}
		if ignored {
			continue
		}
		if err := currentRuntime().InsertNotificationTx(tx, newID("notif_"), recipient.ID, kind, thread.ID, postID, actorLabel, ts); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) repostPost(actor *User, p proto.RepostPostPayload) Reply {
	if actor == nil {
		return Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}
	p, msg := proto.NormalizeRepostPostPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}

	sourcePost, err := currentRuntime().GetPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if sourcePost == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "source post not found", false)}
	}
	if sourcePost.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot repost a redacted post", false)}
	}
	sourceThread, err := currentRuntime().GetThread(h.db, sourcePost.Thread)
	if err != nil {
		return internalErr(err)
	}
	if sourceThread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "source thread not found", false)}
	}
	sourceSettings, err := currentRuntime().GetBoardSettings(h.db, sourceThread.Board)
	if err != nil {
		return internalErr(err)
	}
	if errDetail := commandrules.RequireMemberBoardReadAccess(h.db, actor, sourceThread.Board, sourceSettings, "source board members only"); errDetail != nil {
		return Reply{Err: errDetail}
	}

	settings, err := currentRuntime().GetBoardSettings(h.db, p.Board)
	if err != nil {
		return internalErr(err)
	}
	if settings == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "destination board not found", false)}
	}
	canModerateBoard := commandrules.ActorCanModerateBoard(h.db, actor, p.Board)
	if errDetail := commandrules.RequireThreadCreationBoardAccess(h.db, actor, p.Board, settings, canModerateBoard); errDetail != nil {
		return Reply{Err: errDetail}
	}
	if kind, ok := currentRuntime().ActiveSanction(h.db, actor.ID, p.Board); ok {
		return Reply{Err: commandrules.ActiveBoardSanctionError(kind)}
	}

	title := p.Title
	if title == "" {
		title = sourceThread.Title
	}
	body := proto.FormatRepostBody(sourceThread.Board, sourceThread.Title, sourcePost.Author, sourcePost.ID, sourcePost.Body)
	authorName, authorID, ruleErr := commandrules.PostIdentity(actor, settings, false, canModerateBoard)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	signature, err := h.currentPostSignature(authorID)
	if err != nil {
		return internalErr(err)
	}
	contentFilter, err := currentRuntime().MatchContentFilter(h.db, p.Board, title+"\n"+body)
	if err != nil {
		return internalErr(err)
	}
	automodMatched, automodRuleID, automodMatchType, automodAction, automodRuleReason, automodDuration, err := currentRuntime().EvaluateBoardAutomod(h.db, p.Board, title+"\n"+body, actor.ID)
	if err != nil {
		return internalErr(err)
	}

	ct := proto.NormalizePostContentType(sourcePost.ContentType)
	ts := nowMS()
	threadID := newID("thr_")
	postID := newID("pst_")

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if _, found, err := projections.BoardName(tx, p.Board); err != nil {
		return internalErr(err)
	} else if !found {
		return Reply{Err: errDetail(proto.ErrNotFound, "destination board not found", false)}
	}

	scopes := []string{"board:" + p.Board}
	threadPayload := &proto.ThreadNewPayload{
		ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: title, TS: ts,
	}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, threadPayload)
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

	if err := currentRuntime().InsertThread(tx, &Thread{
		ID: threadID, Board: p.Board, Author: authorName, AuthorID: authorID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().InsertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID,
		Body: body, Signature: signature, ContentType: ct,
		SourcePost: sourcePost.ID, SourceThread: sourceThread.ID, SourceBoard: sourceThread.Board,
		SourceAuthor: sourcePost.Author, SourceAuthorID: sourcePost.AuthorID, SourceTitle: sourceThread.Title,
		CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return internalErr(err)
	}
	if settings.RelayEnabled {
		if err := currentRuntime().InsertRelayDelivery(tx, newID("relay_"), p.Board, threadID, postID, authorID, authorName, title, body, ts, pseq); err != nil {
			return internalErr(err)
		}
	}
	if err := currentRuntime().BumpThread(tx, threadID, pseq); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().FtsInsertPost(tx, postID, threadID, p.Board, authorName, body); err != nil {
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
		automodEvents, err = h.applyAutomodActionTx(tx, automodRuleID, automodMatchType, automodAction, automodReasonFor(automodRuleReason, automodRuleID), automodDuration, actor.ID, postID, threadID, p.Board, ts)
		if err != nil {
			return internalErr(err)
		}
	}
	if err := currentRuntime().EnqueueOutboxJob(tx, outboxPostCommitted, postCommittedJob{
		ActorID: actor.ID, ActorName: authorName, PostID: postID, ThreadID: threadID,
		BoardID: p.Board, Body: body, TS: ts, Seq: pseq,
	}, ts); err != nil {
		return internalErr(err)
	}

	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.publishEvent(proto.EvtThreadNew, tseq, scopes, threadPayload, ts)
	h.publishEvent(proto.EvtPostAppended, pseq, threadScopes, postPayload, ts)
	if filterEvent != nil {
		h.bus.Publish(filterEvent)
	}
	h.publishGeneratedEvents(filterGeneratedEvents)
	h.publishGeneratedEvents(automodEvents)

	return Reply{Result: &proto.AckResult{ID: threadID, Seq: pseq}}
}

func (h *Handler) appendArticleMailBackTx(tx *sql.Tx, actor *User, authorName, authorID string, target *Post, thread *Thread, replyPostID, replyBody string, ts int64) (*proto.Event, error) {
	if actor == nil || target == nil || !target.MailBack || target.Redacted {
		return nil, nil
	}
	authorID = strings.TrimSpace(authorID)
	if authorID == "" || strings.TrimSpace(target.AuthorID) == "" || target.AuthorID == authorID {
		return nil, nil
	}
	recipient, err := projections.FindUserRef(tx, target.AuthorID)
	if err != nil {
		return nil, err
	}
	if recipient == nil {
		return nil, nil
	}
	ignored, err := projections.UserRelationshipExists(tx, recipient.ID, actor.ID, "ignore")
	if err != nil {
		return nil, err
	}
	if ignored {
		return nil, nil
	}

	subject := "Article reply: " + thread.Title
	body := proto.FormatArticleMailBackBody(thread.Board, thread.Title, target.ID, replyPostID, authorName, replyBody)
	if ruleErr := commandrules.EnsureMailQuota(tx, map[string]int{recipient.ID: 1}, proto.MailMessageSize(subject, body, nil)); ruleErr != nil {
		if ruleErr.Code != proto.ErrValidationFailed {
			return nil, fmt.Errorf("article mail-back quota check: %s", ruleErr.Message)
		}
		return nil, nil
	}
	mailID := newID("mail_")
	scopes := []string{"account:" + actor.ID, "account:" + recipient.ID}
	eventPayload := proto.NewMailSentPayload(mailID, authorID, authorName, []string{recipient.ID}, []string{recipient.Name}, subject, body, "", false, nil, ts)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtMailSent, scopes, eventPayload)
	if err != nil {
		return nil, err
	}
	if err := currentRuntime().InsertMailMessage(tx, mailID, authorID, subject, body, "", ts, seq); err != nil {
		return nil, err
	}
	if err := currentRuntime().InsertMailCopy(tx, mailID, recipient.ID, "recipient", "inbox", false, false, ts); err != nil {
		return nil, err
	}
	return &proto.Event{Kind: proto.EvtMailSent, Seq: seq, Scopes: scopes, Payload: eventPayload, TS: ts}, nil
}

func (h *Handler) postBoardMail(actor *User, p proto.PostBoardMailPayload) Reply {
	p, msg := proto.NormalizePostBoardMailPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	boardID := p.Board
	threadID := p.Thread
	if threadID != "" {
		thread, err := currentRuntime().GetThread(h.db, threadID)
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
	settings, err := currentRuntime().GetBoardSettings(h.db, boardID)
	if err != nil {
		return internalErr(err)
	}
	if settings == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}
	if !settings.MailInAllowed && !commandrules.ActorCanModerateBoard(h.db, actor, boardID) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board mail-in is disabled", false)}
	}
	if threadID != "" {
		return h.appendPost(actor, proto.AppendPostPayload{
			Thread:      threadID,
			Body:        p.Body,
			ContentType: p.ContentType,
			Attachments: p.Attachments,
		})
	}
	title := p.Subject
	if title == "" {
		title = "(no subject)"
	}
	return h.createThread(actor, proto.CreateThreadPayload{
		Board:       boardID,
		Title:       title,
		Body:        p.Body,
		ContentType: p.ContentType,
		Attachments: p.Attachments,
	})
}

func (h *Handler) currentPostSignature(authorID string) (string, error) {
	return projections.CurrentPostSignature(h.db, authorID, func(count int) int {
		return int(nowMS() % int64(count))
	})
}

func (h *Handler) attachPost(actor *User, p proto.AttachPostPayload) Reply {
	var msg string
	p, msg = proto.NormalizeAttachPostPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	filename := p.Filename
	contentType := p.ContentType

	post, err := currentRuntime().GetPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot attach to a redacted post", false)}
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
	if settings == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}
	canModerateBoard := commandrules.ActorCanModerateBoard(h.db, actor, thread.Board)
	if !settings.AttachmentsAllowed && !canModerateBoard {
		return Reply{Err: errDetail(proto.ErrForbidden, "attachments are not enabled for this board", false)}
	}
	isAuthor := commandrules.ActorAuthoredBy(actor, post.AuthorID, post.Author)
	withinWindow := commandrules.WithinAuthorEditWindow(time.Now().UnixMilli(), post.CreatedAt, editWindowDur.Milliseconds())
	if !canModerateBoard && !(isAuthor && withinWindow) {
		return Reply{Err: commandrules.AuthorEditWindowExpiredError()}
	}
	count, err := projections.PostAttachmentCount(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if msg := proto.ValidatePostAttachmentCount(count + 1); msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}

	attachmentID := p.ID
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
	if err := currentRuntime().InsertPostAttachment(tx, attachmentID, p.Post, filename, contentType, p.SizeBytes, "", actor.ID, ts); err != nil {
		return internalErr(err)
	}
	if stagedBlobID := p.StagedBlobID; stagedBlobID != "" {
		if err := currentRuntime().PromoteStagedPostBlob(tx, stagedBlobID, attachmentID, p.SizeBytes, contentType); err != nil {
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
	h.publishEvent(proto.EvtPostAttachmentAdded, seq, scopes, payload, ts)
	return Reply{Result: &proto.AckResult{ID: attachmentID, Seq: seq}}
}

func (h *Handler) editPost(actor *User, p proto.EditPostPayload) Reply {
	p, msg := proto.NormalizeEditPostPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	pollBlock, _ := extractPoll(p.Body)
	if pollBlock != nil {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "editing posts with poll markup is not supported", false)}
	}
	if _, found, err := projections.PollIDForPost(h.db, p.Post); err != nil {
		return internalErr(err)
	} else if found {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "editing posts that contain a poll is not supported", false)}
	}

	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	post, err := currentRuntime().GetPostTx(tx, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot edit a redacted post", false)}
	}

	isAuthor := commandrules.ActorAuthoredBy(actor, post.AuthorID, post.Author)
	withinWindow := commandrules.WithinAuthorEditWindow(time.Now().UnixMilli(), post.CreatedAt, editWindowDur.Milliseconds())
	if !actor.IsMod() && !(isAuthor && withinWindow) {
		return Reply{Err: commandrules.AuthorEditWindowExpiredError()}
	}

	thread, err := currentRuntime().GetThreadTx(tx, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}

	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	eventPayload := &proto.PostEditedPayload{
		ID: post.ID, Thread: post.Thread, NewBody: p.Body, Version: post.Version + 1, TS: ts,
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostEdited, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().UpdatePostBody(tx, post.ID, p.Body, seq); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().FtsUpdatePost(tx, post.ID, p.Body); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.publishEvent(proto.EvtPostEdited, seq, scopes, eventPayload, ts)

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) setPostFlag(actor *User, p proto.SetPostFlagPayload) Reply {
	p, msg := proto.NormalizeSetPostFlagPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	post, err := currentRuntime().GetPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot flag a redacted post", false)}
	}
	thread, err := currentRuntime().GetThread(h.db, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}

	flagPlan := commandrules.PlanPostFlagUpdate(post, p)
	canCurate := !flagPlan.CuratorChange || commandrules.ActorCanCurateBoard(h.db, actor, thread.Board)
	canModerateThread := false
	if flagPlan.ThreadModerationChange || flagPlan.AuthorMetadataChange {
		canModerateThread = commandrules.ActorCanModerateBoardThreads(h.db, actor, thread.Board)
	}
	if errDetail := commandrules.RequirePostFlagPermissions(flagPlan, actor, post, canCurate, canModerateThread); errDetail != nil {
		return Reply{Err: errDetail}
	}
	if !flagPlan.HasChanges() {
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
		ID: post.ID, Thread: post.Thread, Marked: flagPlan.Marked, Recommended: flagPlan.Recommended, NoReply: flagPlan.NoReply, TeX: flagPlan.TeX, MailBack: flagPlan.MailBack, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().SetPostFlags(tx, post.ID, flagPlan.Marked, flagPlan.Recommended, flagPlan.NoReply, flagPlan.TeX, flagPlan.MailBack, seq); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostFlagsSet, Seq: seq, Scopes: scopes,
		Payload: &proto.PostFlagsSetPayload{ID: post.ID, Thread: post.Thread, Marked: flagPlan.Marked, Recommended: flagPlan.Recommended, NoReply: flagPlan.NoReply, TeX: flagPlan.TeX, MailBack: flagPlan.MailBack, By: actor.Name, TS: ts}, TS: ts})
	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) redactPost(actor *User, p proto.RedactPostPayload) Reply {
	p, msg := proto.NormalizeRedactPostPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	post, err := currentRuntime().GetPostTx(tx, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "post is already redacted", false)}
	}

	isAuthor := commandrules.ActorAuthoredBy(actor, post.AuthorID, post.Author)
	withinWindow := commandrules.WithinAuthorEditWindow(time.Now().UnixMilli(), post.CreatedAt, editWindowDur.Milliseconds())
	thread, err := currentRuntime().GetThreadTx(tx, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}
	canModeratePosts := commandrules.ActorCanModerateBoardPosts(tx, actor, thread.Board)
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
	if err := currentRuntime().MarkPostRedacted(tx, post.ID, seq); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().RecordPostDeletion(tx, post.ID, post.Thread, thread.Board, actor.ID, actor.Name, p.Reason, deletionKind, ts, seq); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().FtsDeletePost(tx, post.ID); err != nil {
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
	p, msg := proto.NormalizeRestorePostPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	post, err := currentRuntime().GetPostTx(tx, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if !post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "post is not redacted", false)}
	}

	thread, err := currentRuntime().GetThreadTx(tx, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}
	if !commandrules.ActorCanModerateBoardPosts(tx, actor, thread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board post moderation permission required", false)}
	}

	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostRestored, scopes, &proto.PostRestoredPayload{
		ID: post.ID, Thread: post.Thread, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().MarkPostRestored(tx, post.ID, seq); err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().ClearPostDeletion(tx, post.ID); err != nil {
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
	p, msg := proto.NormalizeRedactPostRangePayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	boardID := p.Board
	postIDs := p.Posts
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if ruleErr := commandrules.EnsureRangeBoardAccess(tx, actor, boardID); ruleErr != nil {
		return Reply{Err: ruleErr}
	}

	published := make([]proto.Event, 0, len(postIDs))
	var lastSeq int64
	for _, postID := range postIDs {
		post, thread, ruleErr := loadRangePostTx(tx, postID, boardID)
		if ruleErr != nil {
			return Reply{Err: ruleErr}
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
		if err := currentRuntime().MarkPostRedacted(tx, post.ID, seq); err != nil {
			return internalErr(err)
		}
		if err := currentRuntime().RecordPostDeletion(tx, post.ID, post.Thread, thread.Board, actor.ID, actor.Name, p.Reason, "recycle", ts, seq); err != nil {
			return internalErr(err)
		}
		if err := currentRuntime().FtsDeletePost(tx, post.ID); err != nil {
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
	p, msg := proto.NormalizeRestorePostRangePayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	boardID := p.Board
	postIDs := p.Posts
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if ruleErr := commandrules.EnsureRangeBoardAccess(tx, actor, boardID); ruleErr != nil {
		return Reply{Err: ruleErr}
	}

	published := make([]proto.Event, 0, len(postIDs))
	var lastSeq int64
	for _, postID := range postIDs {
		post, thread, ruleErr := loadRangePostTx(tx, postID, boardID)
		if ruleErr != nil {
			return Reply{Err: ruleErr}
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
		if err := currentRuntime().MarkPostRestored(tx, post.ID, seq); err != nil {
			return internalErr(err)
		}
		if err := currentRuntime().ClearPostDeletion(tx, post.ID); err != nil {
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
	p, msg := proto.NormalizeClearBoardJunkPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	boardID := p.Board
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if ruleErr := commandrules.EnsureRangeBoardAccess(tx, actor, boardID); ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	postIDs, ruleErr := commandrules.BoardJunkPostIDs(tx, boardID, p.Posts)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}

	published := make([]proto.Event, 0, len(postIDs))
	var lastSeq int64
	for _, postID := range postIDs {
		threadID, ruleErr := commandrules.JunkPostThreadID(tx, postID, boardID)
		if ruleErr != nil {
			return Reply{Err: ruleErr}
		}
		scopes := []string{"thread:" + threadID, "board:" + boardID}
		seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostDeletionCleared, scopes, &proto.PostDeletionClearedPayload{
			ID: postID, Thread: threadID, Board: boardID, Kind: "junk", By: actor.ID, TS: ts,
		})
		if err != nil {
			return internalErr(err)
		}
		if err := currentRuntime().ClearPostDeletion(tx, postID); err != nil {
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

func loadRangePostTx(tx *sql.Tx, postID, boardID string) (*Post, *Thread, *proto.ErrorDetail) {
	return commandrules.LoadRangePost(postID, boardID, func(id string) (*Post, error) {
		return currentRuntime().GetPostTx(tx, id)
	}, func(id string) (*Thread, error) {
		return currentRuntime().GetThreadTx(tx, id)
	})
}

func (h *Handler) setThreadTitle(actor *User, p proto.SetThreadTitlePayload) Reply {
	p, msg := proto.NormalizeSetThreadTitlePayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
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

	thread, err := currentRuntime().GetThreadTx(tx, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	if thread.Title == p.Title {
		return Reply{Result: &proto.AckResult{ID: thread.ID}}
	}

	canModerateThread := commandrules.ActorCanModerateBoardThreads(tx, actor, thread.Board)
	isAuthor := commandrules.ActorAuthoredBy(actor, thread.AuthorID, thread.Author)
	withinWindow := commandrules.WithinAuthorEditWindow(time.Now().UnixMilli(), thread.CreatedAt, editWindowDur.Milliseconds())
	if !canModerateThread {
		if !isAuthor {
			return Reply{Err: errDetail(proto.ErrForbidden, "thread author or board thread moderation permission required", false)}
		}
		if !withinWindow {
			return Reply{Err: commandrules.AuthorEditWindowExpiredError()}
		}
	}

	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadTitleSet, scopes, &proto.ThreadTitleSetPayload{
		Thread: thread.ID, Title: p.Title, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().SetThreadTitle(tx, thread.ID, p.Title, ts); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadTitleSet, Seq: seq, Scopes: scopes,
		Payload: &proto.ThreadTitleSetPayload{Thread: thread.ID, Title: p.Title, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: thread.ID, Seq: seq}}
}

func (h *Handler) lockThread(actor *User, p proto.LockThreadPayload) Reply {
	p, msg := proto.NormalizeLockThreadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	thread, err := currentRuntime().GetThreadTx(tx, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	if !commandrules.ActorCanModerateBoardThreads(tx, actor, thread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board thread moderation permission required", false)}
	}

	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadLocked, scopes, &proto.ThreadLockedPayload{
		Thread: thread.ID, Locked: p.Locked, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().SetThreadLocked(tx, thread.ID, p.Locked); err != nil {
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
	p, msg := proto.NormalizeMoveThreadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	thread, err := currentRuntime().GetThreadTx(tx, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	if !commandrules.ActorCanModerateBoardThreads(tx, actor, thread.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board thread moderation permission required", false)}
	}

	if _, found, err := projections.BoardName(tx, p.ToBoard); err != nil {
		return internalErr(err)
	} else if !found {
		return Reply{Err: errDetail(proto.ErrNotFound, "destination board not found", false)}
	}

	scopes := []string{"board:" + thread.Board, "board:" + p.ToBoard}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadMoved, scopes, &proto.ThreadMovedPayload{
		Thread: thread.ID, FromBoard: thread.Board, ToBoard: p.ToBoard, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().MoveThreadBoard(tx, thread.ID, p.ToBoard); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadMoved, Seq: seq, Scopes: scopes,
		Payload: &proto.ThreadMovedPayload{Thread: thread.ID, FromBoard: thread.Board, ToBoard: p.ToBoard, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: thread.ID, Seq: seq}}
}

func (h *Handler) insertAttachments(tx *sql.Tx, postID, authorID string, ts int64, attachments []proto.AttachmentPayload) error {
	for _, item := range attachments {
		if err := currentRuntime().InsertPostAttachment(tx, item.ID, postID, item.Filename, item.ContentType, item.SizeBytes, item.URL, authorID, ts); err != nil {
			return err
		}
	}
	return nil
}
