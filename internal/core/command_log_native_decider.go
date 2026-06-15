package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	corehandler "github.com/juncoflockleader/budgie-bbs/internal/core/handler"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// CommandLogNativeDecisionExecutor is the first broker-native command decision
// bridge. It decides a fail-closed subset of commands without appending through
// the SQL event log or updating SQL projections; the returned events are meant
// to be finalized through CommandEventTransactionStore.
type CommandLogNativeDecisionExecutor struct {
	core          *Core
	decisionMu    sync.Mutex
	decisionCache map[nativeDecisionCacheKey]nativeCommandDecision
}

func NewCommandLogNativeDecisionExecutor(c *Core) *CommandLogNativeDecisionExecutor {
	return &CommandLogNativeDecisionExecutor{core: c}
}

func (e *CommandLogNativeDecisionExecutor) ExecuteCommandLogRecord(ctx context.Context, record CommandLogRecord) Reply {
	decision, errDetail := e.decide(ctx, record)
	if errDetail != nil {
		return Reply{Err: errDetail}
	}
	e.rememberDecision(record, decision)
	return decision.reply
}

func (e *CommandLogNativeDecisionExecutor) DecideCommandLogEvents(ctx context.Context, record CommandLogRecord, reply Reply) ([]EventAppend, error) {
	if reply.Err != nil {
		return nil, nil
	}
	if decision, ok := e.takeDecision(record); ok {
		if reply.Result == nil || decision.reply.Result == nil || reply.Result.ID != decision.reply.Result.ID {
			return nil, fmt.Errorf("native command decision: reply result does not match cached deterministic decision")
		}
		return append([]EventAppend(nil), decision.events...), nil
	}
	decision, errDetail := e.decide(ctx, record)
	if errDetail != nil {
		return nil, fmt.Errorf("native command decision: %s: %s", errDetail.Code, errDetail.Message)
	}
	if reply.Result == nil || decision.reply.Result == nil || reply.Result.ID != decision.reply.Result.ID {
		return nil, fmt.Errorf("native command decision: reply result does not match deterministic decision")
	}
	return decision.events, nil
}

type nativeCommandDecision struct {
	reply  Reply
	events []EventAppend
}

type nativeDecisionCacheKey struct {
	partition LogPartition
	offset    int64
}

func nativeDecisionCacheKeyForRecord(record CommandLogRecord) (nativeDecisionCacheKey, bool) {
	partition := record.Partition.Normalize()
	if partition.Kind == "" || partition.Key == "" || record.Offset <= 0 {
		return nativeDecisionCacheKey{}, false
	}
	return nativeDecisionCacheKey{partition: partition, offset: record.Offset}, true
}

func (e *CommandLogNativeDecisionExecutor) rememberDecision(record CommandLogRecord, decision nativeCommandDecision) {
	if e == nil {
		return
	}
	key, ok := nativeDecisionCacheKeyForRecord(record)
	if !ok {
		return
	}
	e.decisionMu.Lock()
	defer e.decisionMu.Unlock()
	if e.decisionCache == nil {
		e.decisionCache = make(map[nativeDecisionCacheKey]nativeCommandDecision)
	}
	decision.events = append([]EventAppend(nil), decision.events...)
	e.decisionCache[key] = decision
}

func (e *CommandLogNativeDecisionExecutor) takeDecision(record CommandLogRecord) (nativeCommandDecision, bool) {
	if e == nil {
		return nativeCommandDecision{}, false
	}
	key, ok := nativeDecisionCacheKeyForRecord(record)
	if !ok {
		return nativeCommandDecision{}, false
	}
	e.decisionMu.Lock()
	defer e.decisionMu.Unlock()
	decision, ok := e.decisionCache[key]
	if ok {
		delete(e.decisionCache, key)
		decision.events = append([]EventAppend(nil), decision.events...)
	}
	return decision, ok
}

func (e *CommandLogNativeDecisionExecutor) decide(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if err := ctx.Err(); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("request_cancelled", err.Error(), true)
	}
	if e == nil || e.core == nil || e.core.DB == nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", "core is not initialized", true)
	}
	switch record.Command {
	case proto.CmdCreateThread:
		return e.decideCreateThread(ctx, record)
	case proto.CmdAppendPost:
		return e.decideAppendPost(ctx, record)
	case proto.CmdRepostPost:
		return e.decideRepostPost(ctx, record)
	case proto.CmdPostBoardMail:
		return e.decidePostBoardMail(ctx, record)
	case proto.CmdAttachPost:
		return e.decideAttachPost(ctx, record)
	case proto.CmdEditPost:
		return e.decideEditPost(ctx, record)
	case proto.CmdSetPostFlag:
		return e.decideSetPostFlag(ctx, record)
	case proto.CmdRedactPost:
		return e.decideRedactPost(ctx, record)
	case proto.CmdRestorePost:
		return e.decideRestorePost(ctx, record)
	case proto.CmdRedactPostRange:
		return e.decideRedactPostRange(ctx, record)
	case proto.CmdRestorePostRange:
		return e.decideRestorePostRange(ctx, record)
	case proto.CmdClearBoardJunk:
		return e.decideClearBoardJunk(ctx, record)
	case proto.CmdPurgePost:
		return e.decidePurgePost(ctx, record)
	case proto.CmdSanctionUser:
		return e.decideSanctionUser(ctx, record)
	case proto.CmdClearUserSanction:
		return e.decideClearUserSanction(ctx, record)
	case proto.CmdGrantRole:
		return e.decideGrantRole(ctx, record)
	case proto.CmdRevokeRole:
		return e.decideRevokeRole(ctx, record)
	case proto.CmdPublishStatsSnapshot:
		return e.decidePublishStatsSnapshot(ctx, record)
	case proto.CmdPublishSystemNotice:
		return e.decidePublishSystemNotice(ctx, record)
	case proto.CmdCreateBoard:
		return e.decideCreateBoardCommand(ctx, record)
	case proto.CmdSetBoardSettings:
		return e.decideSetBoardSettings(ctx, record)
	case proto.CmdSetBoardMemberRequirements:
		return e.decideSetBoardMemberRequirements(ctx, record)
	case proto.CmdSetBoardModerator:
		return e.decideSetBoardModerator(ctx, record)
	case proto.CmdSetBoardMember:
		return e.decideSetBoardMember(ctx, record)
	case proto.CmdLeaveBoardMembership:
		return e.decideLeaveBoardMembership(ctx, record)
	case proto.CmdApplyBoardMembership:
		return e.decideApplyBoardMembership(ctx, record)
	case proto.CmdReviewBoardMembership:
		return e.decideReviewBoardMembership(ctx, record)
	case proto.CmdSetRecommendedBoard:
		return e.decideSetRecommendedBoard(ctx, record)
	case proto.CmdSetThreadTitle:
		return e.decideSetThreadTitle(ctx, record)
	case proto.CmdLockThread:
		return e.decideLockThread(ctx, record)
	case proto.CmdMoveThread:
		return e.decideMoveThread(ctx, record)
	case proto.CmdSetContentFilter:
		return e.decideSetContentFilter(ctx, record)
	case proto.CmdSetBoardAutomodRule:
		return e.decideSetBoardAutomodRule(ctx, record)
	case proto.CmdDeleteBoardAutomodRule:
		return e.decideDeleteBoardAutomodRule(ctx, record)
	case proto.CmdPublishPollResult:
		return e.decidePublishPollResult(ctx, record)
	case proto.CmdFlagPost:
		return e.decideFlagPost(ctx, record)
	case proto.CmdResolveReview:
		return e.decideResolveReview(ctx, record)
	case proto.CmdBlessUser:
		return e.decideBlessUser(ctx, record)
	case proto.CmdSendMail:
		return e.decideSendMail(ctx, record)
	case proto.CmdForwardMail:
		return e.decideForwardMail(ctx, record)
	case proto.CmdPostMailToBoard:
		return e.decidePostMailToBoard(ctx, record)
	case proto.CmdMailPostAuthor:
		return e.decideMailPostAuthor(ctx, record)
	case proto.CmdSendDigestEntryMail:
		return e.decideSendDigestEntryMail(ctx, record)
	case proto.CmdCuratePost:
		return e.decideCuratePost(ctx, record)
	case proto.CmdCurateThread:
		return e.decideCurateThread(ctx, record)
	case proto.CmdRemoveDigestEntry:
		return e.decideRemoveDigestEntry(ctx, record)
	case proto.CmdUpdateDigestEntry:
		return e.decideUpdateDigestEntry(ctx, record)
	case proto.CmdSetDigestEntryBody:
		return e.decideSetDigestEntryBody(ctx, record)
	case proto.CmdCreateDigestDirectory:
		return e.decideCreateDigestDirectory(ctx, record)
	case proto.CmdMoveDigestPath:
		return e.decideMoveDigestPath(ctx, record)
	case proto.CmdCopyDigestPath:
		return e.decideCopyDigestPath(ctx, record)
	case proto.CmdDeleteDigestPath:
		return e.decideDeleteDigestPath(ctx, record)
	case proto.CmdSetMailGroup:
		return e.decideSetMailGroup(ctx, record)
	case proto.CmdDeleteMailGroup:
		return e.decideDeleteMailGroup(ctx, record)
	case proto.CmdAttachMail:
		return e.decideAttachMail(ctx, record)
	case proto.CmdUpdateMail:
		return e.decideUpdateMail(ctx, record)
	case proto.CmdDeleteMail:
		return e.decideDeleteMail(ctx, record)
	case proto.CmdDeleteMailRange:
		return e.decideDeleteMailRange(ctx, record)
	case proto.CmdSendDirectMessage:
		return e.decideSendDirectMessage(ctx, record)
	case proto.CmdMarkDirectMessageRead:
		return e.decideMarkDirectMessageRead(ctx, record)
	case proto.CmdDeleteDirectMessage:
		return e.decideDeleteDirectMessage(ctx, record)
	case proto.CmdSetDirectMessageSettings:
		return e.decideSetDirectMessageSettings(ctx, record)
	case proto.CmdSetUserRelationship:
		return e.decideSetUserRelationship(ctx, record)
	case proto.CmdSetLoginWatch:
		return e.decideSetLoginWatch(ctx, record)
	case proto.CmdSetBoardFavorite:
		return e.decideSetBoardFavorite(ctx, record)
	case proto.CmdCreateFavoriteFolder:
		return e.decideCreateFavoriteFolder(ctx, record)
	case proto.CmdUpdateFavoriteFolder:
		return e.decideUpdateFavoriteFolder(ctx, record)
	case proto.CmdDeleteFavoriteFolder:
		return e.decideDeleteFavoriteFolder(ctx, record)
	case proto.CmdMoveBoardFavorite:
		return e.decideMoveBoardFavorite(ctx, record)
	case proto.CmdImportFavoriteTree:
		return e.decideImportFavoriteTree(ctx, record)
	case proto.CmdSetBoardZap:
		return e.decideSetBoardZap(ctx, record)
	default:
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "native command decision does not support "+string(record.Command), false)
	}
}

func (e *CommandLogNativeDecisionExecutor) decideCreateThread(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.CreateThreadPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid createThread payload", false)
	}
	payload.Board = strings.TrimSpace(payload.Board)
	payload.Title = strings.TrimSpace(payload.Title)
	if payload.Board == "" || payload.Title == "" || payload.Body == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board, title, and body are required", false)
	}
	pollBlock, cleanBody := extractPoll(payload.Body)
	pollStripped := pollBlock != nil && cleanBody != payload.Body

	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: payload.Board}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, payload.Board), false)
	}
	if strings.TrimSpace(record.ActorID) == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrUnauthenticated, "command actor is required", false)
	}

	decisionCtx, err := nativeCreateThreadDecisionContext(e.core.DB, record.ActorID, payload.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	actor := decisionCtx.Actor
	if actor == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrUnauthenticated, "command actor not found", false)
	}
	if pollStripped {
		if errDetail := nativeRequireMinTrustForPoll(e.core.DB, actor, 2, "create thread"); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
	}
	if kind := decisionCtx.SanctionKind; kind != "" {
		code := proto.ErrMuted
		if kind == "ban" {
			code = proto.ErrBanned
		}
		return nativeCommandDecision{}, nativeDecisionErr(code, "you are "+kind+"d in this board", false)
	}
	settings := decisionCtx.Settings
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canModerateBoard := decisionCtx.CanModerateBoard
	if settings.ReadOnly && !canModerateBoard {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board is read-only", false)
	}
	if settings.MemberReadMode || settings.MemberPostMode {
		canUseMemberBoard, err := nativeActorCanUseMemberBoard(e.core.DB, actor, payload.Board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !canUseMemberBoard {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board members only", false)
		}
	}
	authorName, authorID, errDetail := nativePostIdentity(actor, settings, payload.Anonymous, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	attachments, errDetail := nativeNormalizeAttachments(record, payload.Attachments, settings.AttachmentsAllowed, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	contentFilter, err := matchContentFilter(e.core.DB, payload.Board, payload.Title+"\n"+payload.Body)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}

	signature, err := nativePostSignature(e.core.DB, authorID, record)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	threadID := stableCommandLogDecisionID("thr_", record, 0)
	postID := stableCommandLogDecisionID("pst_", record, 1)
	scopes := []string{"board:" + payload.Board}
	threadScopes := []string{"board:" + payload.Board, "thread:" + threadID}
	contentType := nativeContentType(payload.ContentType)
	threadPayload := &proto.ThreadNewPayload{
		ID:       threadID,
		Board:    payload.Board,
		Author:   authorName,
		AuthorID: authorID,
		Title:    payload.Title,
		TS:       ts,
	}
	postPayload := &proto.PostAppendedPayload{
		ID:          postID,
		Thread:      threadID,
		Author:      authorName,
		AuthorID:    authorID,
		Body:        cleanBody,
		RawBody:     payload.Body,
		Signature:   signature,
		ContentType: contentType,
		Attachments: attachments,
		TS:          ts,
	}
	if authorID == "" && actor.ID != "" {
		postPayload.PostCommitActorID = actor.ID
		postPayload.PostCommitActorName = authorName
	}
	events := []EventAppend{
		{
			ID:      stableCommandLogDecisionID("evt_", record, 0),
			Kind:    proto.EvtThreadNew,
			Scopes:  scopes,
			Payload: threadPayload,
			TS:      ts,
		},
		{
			ID:      stableCommandLogDecisionID("evt_", record, 1),
			Kind:    proto.EvtPostAppended,
			Scopes:  threadScopes,
			Payload: postPayload,
			TS:      ts,
		},
	}
	if filterEvents, errDetail := nativeContentFilterReviewEvents(e.core.DB, record, actor, authorName, contentFilter, postID, threadID, payload.Board, !settings.MemberReadMode, ts, len(events)); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	} else {
		events = append(events, filterEvents...)
	}
	if automodEvents, errDetail := nativeAutomodEvents(e.core.DB, record, actor.ID, postID, threadID, payload.Board, payload.Title+"\n"+payload.Body, ts, len(events)); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	} else {
		events = append(events, automodEvents...)
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: threadID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideAppendPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.AppendPostPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid appendPost payload", false)
	}
	payload.Thread = strings.TrimSpace(payload.Thread)
	if payload.Thread == "" || payload.Body == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "thread and body are required", false)
	}
	payload.ReplyTo = strings.TrimSpace(payload.ReplyTo)
	pollBlock, cleanBody := extractPoll(payload.Body)
	pollStripped := pollBlock != nil && cleanBody != payload.Body
	partition := record.Partition.Normalize()
	if !commandLogAppendPostPartitionMatchesTarget(record.Command, record.Payload, partition) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionThread, payload.Thread), false)
	}
	if strings.TrimSpace(record.ActorID) == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrUnauthenticated, "command actor is required", false)
	}

	decisionCtx, err := nativeAppendPostDecisionContext(e.core.DB, record.ActorID, payload.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	actor := decisionCtx.Actor
	if actor == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrUnauthenticated, "command actor not found", false)
	}
	if pollStripped {
		if errDetail := nativeRequireMinTrustForPoll(e.core.DB, actor, 2, "reply"); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
	}
	thread := decisionCtx.Thread
	rootReplyGuards := decisionCtx.RootReplyGuards
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	settings := decisionCtx.Settings
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canModerateBoard := decisionCtx.CanModerateBoard
	canModerateThread := decisionCtx.CanModerateThread
	if thread.Locked && !canModerateBoard {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrThreadLocked, "thread is locked", false)
	}
	if (settings.ReadOnly || settings.NoReply) && !canModerateBoard {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board is not accepting replies", false)
	}
	if settings.MemberReadMode || settings.MemberPostMode {
		canUseMemberBoard, err := nativeActorCanUseMemberBoard(e.core.DB, actor, thread.Board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !canUseMemberBoard {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board members only", false)
		}
	}
	authorName, authorID, errDetail := nativePostIdentity(actor, settings, payload.Anonymous, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	attachments, errDetail := nativeNormalizeAttachments(record, payload.Attachments, settings.AttachmentsAllowed, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if rootReplyGuards.NoReply && !canModerateThread {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "thread starter is not accepting replies", false)
	}
	effectiveReplyTo := ""
	var quoteSource *Post
	var mailBackTarget *Post
	if payload.ReplyTo != "" {
		parent, err := getPost(e.core.DB, payload.ReplyTo)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if parent == nil {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "replyTo post not found", false)
		}
		if parent.Thread != thread.ID {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "replyTo post belongs to another thread", false)
		}
		if payload.QuotePost {
			if parent.Redacted {
				return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "cannot quote a redacted post", false)
			}
			quoteSource = parent
		}
		if parent.NoReply && !canModerateThread {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "article is not accepting replies", false)
		}
		if parent.MailBack {
			mailBackTarget = parent
		}
		effectiveReplyTo = parent.ID
		if parent.ReplyTo != "" {
			effectiveReplyTo = parent.ReplyTo
		}
	} else {
		if payload.QuotePost {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "replyTo is required for quoted replies", false)
		}
		if rootReplyGuards.MailBack {
			root, err := nativeThreadRootPost(e.core.DB, thread.ID)
			if err != nil {
				return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
			}
			mailBackTarget = root
		}
	}
	rawBody := payload.Body
	var postCommitBody *string
	if quoteSource != nil {
		notificationBody := cleanBody
		prefix := nativeFormatQuotedReplyPrefix(quoteSource)
		cleanBody = prefix + cleanBody
		rawBody = prefix + payload.Body
		postCommitBody = &notificationBody
	}
	if kind := decisionCtx.SanctionKind; kind != "" {
		code := proto.ErrMuted
		if kind == "ban" {
			code = proto.ErrBanned
		}
		return nativeCommandDecision{}, nativeDecisionErr(code, "you are "+kind+"d in this board", false)
	}
	contentFilter, err := matchContentFilter(e.core.DB, thread.Board, rawBody)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}

	signature, err := nativePostSignature(e.core.DB, authorID, record)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	postID := stableCommandLogDecisionID("pst_", record, 0)
	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	contentType := nativeContentType(payload.ContentType)
	postPayload := &proto.PostAppendedPayload{
		ID:             postID,
		Thread:         thread.ID,
		Author:         authorName,
		AuthorID:       authorID,
		Body:           cleanBody,
		RawBody:        rawBody,
		PostCommitBody: postCommitBody,
		Signature:      signature,
		ContentType:    contentType,
		ReplyTo:        effectiveReplyTo,
		Attachments:    attachments,
		TS:             ts,
	}
	if authorID == "" && actor.ID != "" {
		postPayload.PostCommitActorID = actor.ID
		postPayload.PostCommitActorName = authorName
	}
	event := EventAppend{
		ID:      stableCommandLogDecisionID("evt_", record, 0),
		Kind:    proto.EvtPostAppended,
		Scopes:  scopes,
		Payload: postPayload,
		TS:      ts,
	}
	events := []EventAppend{event}
	if filterEvents, errDetail := nativeContentFilterReviewEvents(e.core.DB, record, actor, authorName, contentFilter, postID, thread.ID, thread.Board, !settings.MemberReadMode, ts, len(events)); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	} else {
		events = append(events, filterEvents...)
	}
	if automodEvents, errDetail := nativeAutomodEvents(e.core.DB, record, actor.ID, postID, thread.ID, thread.Board, rawBody, ts, len(events)); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	} else {
		events = append(events, automodEvents...)
	}
	if mailEvent, errDetail := nativeArticleMailBackEvent(e.core.DB, record, actor, authorName, authorID, thread, mailBackTarget, postID, cleanBody, ts, len(events)); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	} else if mailEvent != nil {
		events = append(events, *mailEvent)
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: postID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decidePostBoardMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.PostBoardMailPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid postBoardMail payload", false)
	}
	body := strings.TrimSpace(payload.Body)
	if body == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "body is required", false)
	}
	rawBoardID := strings.TrimSpace(payload.Board)
	threadID := strings.TrimSpace(payload.Thread)
	expectedKey := rawBoardID
	if expectedKey == "" {
		expectedKey = threadID
	}
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := rawBoardID
	var thread *Thread
	if threadID != "" {
		var err error
		thread, err = getThread(e.core.DB, threadID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if thread == nil {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
		}
		if boardID == "" {
			boardID = thread.Board
		} else if boardID != thread.Board {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "thread does not belong to board", false)
		}
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	settings, err := getBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canModerateBoard, err := nativeActorCanModerateBoard(e.core.DB, actor, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !settings.MailInAllowed && !canModerateBoard {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board mail-in is disabled", false)
	}
	contentType := strings.TrimSpace(payload.ContentType)
	if thread != nil {
		return e.decidePostBoardMailAppend(record, actor, thread, body, contentType, payload.Attachments)
	}
	title := strings.TrimSpace(payload.Subject)
	if title == "" {
		title = "(no subject)"
	}
	threadPayload := proto.CreateThreadPayload{
		Board:       boardID,
		Title:       title,
		Body:        body,
		ContentType: contentType,
		Attachments: payload.Attachments,
	}
	raw, err := json.Marshal(threadPayload)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	transformed := record
	transformed.Payload = raw
	return e.decideCreateThread(ctx, transformed)
}

func (e *CommandLogNativeDecisionExecutor) decidePostBoardMailAppend(record CommandLogRecord, actor *User, thread *Thread, body, contentType string, attachmentsIn []proto.AttachmentPayload) (nativeCommandDecision, *proto.ErrorDetail) {
	pollBlock, cleanBody := extractPoll(body)
	pollStripped := pollBlock != nil && cleanBody != body
	if pollStripped {
		if errDetail := nativeRequireMinTrustForPoll(e.core.DB, actor, 2, "reply"); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
	}
	settings, err := getBoardSettings(e.core.DB, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canModerateBoard, err := nativeActorCanModerateBoard(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	canModerateThread, err := nativeActorCanModerateBoardThreads(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread.Locked && !canModerateBoard {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrThreadLocked, "thread is locked", false)
	}
	if (settings.ReadOnly || settings.NoReply) && !canModerateBoard {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board is not accepting replies", false)
	}
	if settings.MemberReadMode || settings.MemberPostMode {
		canUseMemberBoard, err := nativeActorCanUseMemberBoard(e.core.DB, actor, thread.Board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !canUseMemberBoard {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board members only", false)
		}
	}
	authorName, authorID, errDetail := nativePostIdentity(actor, settings, false, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	attachments, errDetail := nativeNormalizeAttachments(record, attachmentsIn, settings.AttachmentsAllowed, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	rootReplyGuards, err := nativeThreadRootPostReplyGuards(e.core.DB, thread.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if rootReplyGuards.NoReply && !canModerateThread {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "thread starter is not accepting replies", false)
	}
	var mailBackTarget *Post
	if rootReplyGuards.MailBack {
		root, err := nativeThreadRootPost(e.core.DB, thread.ID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		mailBackTarget = root
	}
	if kind, ok := activeSanction(e.core.DB, actor.ID, thread.Board); ok {
		code := proto.ErrMuted
		if kind == "ban" {
			code = proto.ErrBanned
		}
		return nativeCommandDecision{}, nativeDecisionErr(code, "you are "+kind+"d in this board", false)
	}
	rawBody := body
	contentFilter, err := matchContentFilter(e.core.DB, thread.Board, rawBody)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	signature, err := nativePostSignature(e.core.DB, authorID, record)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	postID := stableCommandLogDecisionID("pst_", record, 0)
	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	postPayload := &proto.PostAppendedPayload{
		ID:          postID,
		Thread:      thread.ID,
		Author:      authorName,
		AuthorID:    authorID,
		Body:        cleanBody,
		RawBody:     rawBody,
		Signature:   signature,
		ContentType: nativeContentType(contentType),
		Attachments: attachments,
		TS:          ts,
	}
	if authorID == "" && actor.ID != "" {
		postPayload.PostCommitActorID = actor.ID
		postPayload.PostCommitActorName = authorName
	}
	event := EventAppend{
		ID:      stableCommandLogDecisionID("evt_", record, 0),
		Kind:    proto.EvtPostAppended,
		Scopes:  scopes,
		Payload: postPayload,
		TS:      ts,
	}
	events := []EventAppend{event}
	if filterEvents, errDetail := nativeContentFilterReviewEvents(e.core.DB, record, actor, authorName, contentFilter, postID, thread.ID, thread.Board, !settings.MemberReadMode, ts, len(events)); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	} else {
		events = append(events, filterEvents...)
	}
	if mailEvent, errDetail := nativeArticleMailBackEvent(e.core.DB, record, actor, authorName, authorID, thread, mailBackTarget, postID, cleanBody, ts, len(events)); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	} else if mailEvent != nil {
		events = append(events, *mailEvent)
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: postID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideRepostPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.RepostPostPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid repostPost payload", false)
	}
	payload.Post = strings.TrimSpace(payload.Post)
	payload.Board = strings.TrimSpace(payload.Board)
	payload.Title = strings.TrimSpace(payload.Title)
	if payload.Post == "" || payload.Board == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "post and board are required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: payload.Board}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, payload.Board), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	sourcePost, err := getPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if sourcePost == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "source post not found", false)
	}
	if sourcePost.Redacted {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "cannot repost a redacted post", false)
	}
	sourceThread, err := getThread(e.core.DB, sourcePost.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if sourceThread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "source thread not found", false)
	}
	sourceSettings, err := getBoardSettings(e.core.DB, sourceThread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if sourceSettings != nil && sourceSettings.MemberReadMode {
		canUseSource, err := nativeActorCanUseMemberBoard(e.core.DB, actor, sourceThread.Board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !canUseSource {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "source board members only", false)
		}
	}
	settings, err := getBoardSettings(e.core.DB, payload.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "destination board not found", false)
	}
	canModerateBoard, err := nativeActorCanModerateBoard(e.core.DB, actor, payload.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings.ReadOnly && !canModerateBoard {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board is read-only", false)
	}
	if settings.MemberReadMode || settings.MemberPostMode {
		canUseDestination, err := nativeActorCanUseMemberBoard(e.core.DB, actor, payload.Board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !canUseDestination {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board members only", false)
		}
	}
	if kind, ok := activeSanction(e.core.DB, actor.ID, payload.Board); ok {
		code := proto.ErrMuted
		if kind == "ban" {
			code = proto.ErrBanned
		}
		return nativeCommandDecision{}, nativeDecisionErr(code, "you are "+kind+"d in this board", false)
	}

	title := payload.Title
	if title == "" {
		title = sourceThread.Title
	}
	body := nativeRepostBody(sourcePost, sourceThread)
	authorName, authorID, errDetail := nativePostIdentity(actor, settings, false, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	signature, err := nativePostSignature(e.core.DB, authorID, record)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	contentFilter, err := matchContentFilter(e.core.DB, payload.Board, title+"\n"+body)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	threadID := stableCommandLogDecisionID("thr_", record, 0)
	postID := stableCommandLogDecisionID("pst_", record, 1)
	scopes := []string{"board:" + payload.Board}
	threadScopes := []string{"board:" + payload.Board, "thread:" + threadID}
	threadPayload := &proto.ThreadNewPayload{
		ID:       threadID,
		Board:    payload.Board,
		Author:   authorName,
		AuthorID: authorID,
		Title:    title,
		TS:       ts,
	}
	postPayload := &proto.PostAppendedPayload{
		ID:             postID,
		Thread:         threadID,
		Author:         authorName,
		AuthorID:       authorID,
		Body:           body,
		RawBody:        body,
		Signature:      signature,
		ContentType:    nativeContentType(sourcePost.ContentType),
		SourcePost:     sourcePost.ID,
		SourceThread:   sourceThread.ID,
		SourceBoard:    sourceThread.Board,
		SourceAuthor:   sourcePost.Author,
		SourceAuthorID: sourcePost.AuthorID,
		SourceTitle:    sourceThread.Title,
		TS:             ts,
	}
	events := []EventAppend{
		{
			ID:      stableCommandLogDecisionID("evt_", record, 0),
			Kind:    proto.EvtThreadNew,
			Scopes:  scopes,
			Payload: threadPayload,
			TS:      ts,
		},
		{
			ID:      stableCommandLogDecisionID("evt_", record, 1),
			Kind:    proto.EvtPostAppended,
			Scopes:  threadScopes,
			Payload: postPayload,
			TS:      ts,
		},
	}
	if filterEvents, errDetail := nativeContentFilterReviewEvents(e.core.DB, record, actor, authorName, contentFilter, postID, threadID, payload.Board, !settings.MemberReadMode, ts, len(events)); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	} else {
		events = append(events, filterEvents...)
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: threadID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideAttachPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.AttachPostPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid attachPost payload", false)
	}
	payload.Post = strings.TrimSpace(payload.Post)
	filename := strings.TrimSpace(payload.Filename)
	contentType := strings.TrimSpace(payload.ContentType)
	if payload.Post == "" || filename == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "post and filename are required", false)
	}
	if len(filename) > 160 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "attachment filename must be 160 characters or less", false)
	}
	if len(contentType) > 120 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "attachment content type must be 120 characters or less", false)
	}
	if payload.SizeBytes < 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "attachment size cannot be negative", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionPost, Key: payload.Post}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionPost, payload.Post), false)
	}

	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := getPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if post.Redacted {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "cannot attach to a redacted post", false)
	}
	thread, err := getThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	settings, err := getBoardSettings(e.core.DB, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	if !settings.AttachmentsAllowed {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "attachments are not enabled for this board", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	isAuthor := post.AuthorID == actor.ID
	if post.AuthorID == "" {
		isAuthor = post.Author == actor.Name
	}
	if !isAuthor || ts-post.CreatedAt >= nativeAuthorEditWindow.Milliseconds() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrEditWindowExpired, "edit window has expired", false)
	}
	var count int
	if err := qQueryRow(e.core.DB, `SELECT COUNT(*) FROM post_attachments WHERE post_id=?`, post.ID).Scan(&count); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if count >= 8 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "a post can have at most 8 attachments", false)
	}

	attachmentID := strings.TrimSpace(payload.ID)
	if attachmentID == "" {
		attachmentID = stableCommandLogDecisionID("att_", record, 0)
	}
	stagedBlobID := strings.TrimSpace(payload.StagedBlobID)
	if errDetail := nativeValidateStagedPostAttachmentBlob(e.core.DB, stagedBlobID, attachmentID, payload.SizeBytes, contentType); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtPostAttachmentAdded,
		Scopes: []string{"board:" + thread.Board, "thread:" + thread.ID},
		Payload: &proto.PostAttachmentAddedPayload{
			ID:           attachmentID,
			Post:         post.ID,
			Thread:       thread.ID,
			Filename:     filename,
			ContentType:  contentType,
			SizeBytes:    payload.SizeBytes,
			AuthorID:     actor.ID,
			StagedBlobID: stagedBlobID,
			TS:           ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: attachmentID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideEditPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.EditPostPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid editPost payload", false)
	}
	payload.Post = strings.TrimSpace(payload.Post)
	if payload.Post == "" || payload.Body == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "post and body are required", false)
	}
	if pollBlock, _ := extractPoll(payload.Body); pollBlock != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "editing posts with poll markup is not supported", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionPost, Key: payload.Post}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionPost, payload.Post), false)
	}

	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	var existingPollID string
	err := qQueryRow(e.core.DB, `SELECT id FROM polls WHERE post_id=?`, payload.Post).Scan(&existingPollID)
	if err == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "editing posts that contain a poll is not supported", false)
	}
	if err != nil && err != sql.ErrNoRows {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	post, err := getPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if post.Redacted {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "cannot edit a redacted post", false)
	}
	thread, err := getThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	isAuthor := post.AuthorID == actor.ID
	if post.AuthorID == "" {
		isAuthor = post.Author == actor.Name
	}
	if !actor.IsMod() && (!isAuthor || ts-post.CreatedAt >= nativeAuthorEditWindow.Milliseconds()) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrEditWindowExpired, "edit window has expired", false)
	}

	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtPostEdited,
		Scopes: []string{"thread:" + post.Thread, "board:" + thread.Board},
		Payload: &proto.PostEditedPayload{
			ID:      post.ID,
			Thread:  post.Thread,
			NewBody: payload.Body,
			Version: post.Version + 1,
			TS:      ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: post.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetPostFlag(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetPostFlagPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setPostFlag payload", false)
	}
	payload.Post = strings.TrimSpace(payload.Post)
	if payload.Post == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "post is required", false)
	}
	if payload.Marked == nil && payload.Recommended == nil && payload.NoReply == nil && payload.TeX == nil && payload.MailBack == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "at least one article flag is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionPost, Key: payload.Post}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionPost, payload.Post), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := getPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if post.Redacted {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "cannot flag a redacted post", false)
	}
	thread, err := getThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}

	marked := post.Marked
	recommended := post.Recommended
	noReply := post.NoReply
	tex := post.TeX
	mailBack := post.MailBack
	curatorChange := false
	threadModerationChange := false
	authorMetadataChange := false
	if payload.Marked != nil {
		curatorChange = curatorChange || *payload.Marked != post.Marked
		marked = *payload.Marked
	}
	if payload.Recommended != nil {
		curatorChange = curatorChange || *payload.Recommended != post.Recommended
		recommended = *payload.Recommended
	}
	if payload.NoReply != nil {
		threadModerationChange = *payload.NoReply != post.NoReply
		noReply = *payload.NoReply
	}
	if payload.TeX != nil {
		authorMetadataChange = authorMetadataChange || *payload.TeX != post.TeX
		tex = *payload.TeX
	}
	if payload.MailBack != nil {
		authorMetadataChange = authorMetadataChange || *payload.MailBack != post.MailBack
		mailBack = *payload.MailBack
	}
	canModerateThread := false
	if threadModerationChange || authorMetadataChange {
		canModerateThread, err = nativeActorCanModerateBoardThreads(e.core.DB, actor, thread.Board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	if curatorChange {
		canCurate, err := nativeActorCanCurateBoard(e.core.DB, actor, thread.Board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !canCurate {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board curator permission required", false)
		}
	}
	if threadModerationChange && !canModerateThread {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board thread moderation permission required", false)
	}
	if authorMetadataChange && actor.ID != post.AuthorID && !canModerateThread {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "post author or board thread moderation permission required", false)
	}
	if !curatorChange && !threadModerationChange && !authorMetadataChange {
		return nativeCommandDecision{reply: Reply{Result: &proto.AckResult{ID: post.ID}}}, nil
	}

	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtPostFlagsSet,
		Scopes: []string{"thread:" + post.Thread, "board:" + thread.Board},
		Payload: &proto.PostFlagsSetPayload{
			ID:          post.ID,
			Thread:      post.Thread,
			Marked:      marked,
			Recommended: recommended,
			NoReply:     noReply,
			TeX:         tex,
			MailBack:    mailBack,
			By:          actor.ID,
			TS:          ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: post.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideRedactPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.RedactPostPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid redactPost payload", false)
	}
	payload.Post = strings.TrimSpace(payload.Post)
	if payload.Post == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "post is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionPost, Key: payload.Post}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionPost, payload.Post), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := getPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if post.Redacted {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "post is already redacted", false)
	}
	thread, err := getThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	canModeratePosts, err := nativeActorCanModerateBoardPosts(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	isAuthor := post.AuthorID != "" && post.AuthorID == actor.ID
	if !canModeratePosts && !(isAuthor && ts-post.CreatedAt < nativeAuthorEditWindow.Milliseconds()) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "insufficient permissions to redact this post", false)
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtPostRedacted,
		Scopes: []string{"thread:" + post.Thread, "board:" + thread.Board},
		Payload: &proto.PostRedactedPayload{
			ID:     post.ID,
			Thread: post.Thread,
			By:     actor.ID,
			Reason: payload.Reason,
			TS:     ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: post.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideRestorePost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.RestorePostPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid restorePost payload", false)
	}
	payload.Post = strings.TrimSpace(payload.Post)
	if payload.Post == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "post is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionPost, Key: payload.Post}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionPost, payload.Post), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := getPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if !post.Redacted {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "post is not redacted", false)
	}
	thread, err := getThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	canModeratePosts, err := nativeActorCanModerateBoardPosts(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canModeratePosts {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board post moderation permission required", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtPostRestored,
		Scopes: []string{"thread:" + post.Thread, "board:" + thread.Board},
		Payload: &proto.PostRestoredPayload{
			ID:     post.ID,
			Thread: post.Thread,
			By:     actor.ID,
			TS:     ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: post.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideRedactPostRange(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.RedactPostRangePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid redactPostRange payload", false)
	}
	payload.Board = strings.TrimSpace(payload.Board)
	if payload.Board == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	postIDs, errDetail := nativeNormalizePostRangeIDs(payload.Posts)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: payload.Board}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, payload.Board), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := nativeEnsureRangeBoardAccess(e.core.DB, actor, payload.Board); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := make([]EventAppend, 0, len(postIDs))
	for i, postID := range postIDs {
		post, thread, errDetail := nativeLoadRangePost(e.core.DB, postID, payload.Board)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		if post.Redacted {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "post is already redacted: "+postID, false)
		}
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, i),
			Kind:   proto.EvtPostRedacted,
			Scopes: []string{"thread:" + post.Thread, "board:" + thread.Board},
			Payload: &proto.PostRedactedPayload{
				ID:           post.ID,
				Thread:       post.Thread,
				By:           actor.ID,
				Reason:       payload.Reason,
				DeletionKind: "recycle",
				TS:           ts,
			},
			TS: ts,
		})
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: strconv.Itoa(len(postIDs))}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideRestorePostRange(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.RestorePostRangePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid restorePostRange payload", false)
	}
	payload.Board = strings.TrimSpace(payload.Board)
	if payload.Board == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	postIDs, errDetail := nativeNormalizePostRangeIDs(payload.Posts)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: payload.Board}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, payload.Board), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := nativeEnsureRangeBoardAccess(e.core.DB, actor, payload.Board); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := make([]EventAppend, 0, len(postIDs))
	for i, postID := range postIDs {
		post, thread, errDetail := nativeLoadRangePost(e.core.DB, postID, payload.Board)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		if !post.Redacted {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "post is not redacted: "+postID, false)
		}
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, i),
			Kind:   proto.EvtPostRestored,
			Scopes: []string{"thread:" + post.Thread, "board:" + thread.Board},
			Payload: &proto.PostRestoredPayload{
				ID:     post.ID,
				Thread: post.Thread,
				By:     actor.ID,
				TS:     ts,
			},
			TS: ts,
		})
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: strconv.Itoa(len(postIDs))}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideClearBoardJunk(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.ClearBoardJunkPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid clearBoardJunk payload", false)
	}
	payload.Board = strings.TrimSpace(payload.Board)
	if payload.Board == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: payload.Board}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, payload.Board), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := nativeEnsureRangeBoardAccess(e.core.DB, actor, payload.Board); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	postIDs, errDetail := nativeBoardJunkIDs(e.core.DB, payload.Board, payload.Posts)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := make([]EventAppend, 0, len(postIDs))
	for i, postID := range postIDs {
		threadID, errDetail := nativeJunkPostThread(e.core.DB, postID, payload.Board)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, i),
			Kind:   proto.EvtPostDeletionCleared,
			Scopes: []string{"thread:" + threadID, "board:" + payload.Board},
			Payload: &proto.PostDeletionClearedPayload{
				ID:     postID,
				Thread: threadID,
				Board:  payload.Board,
				Kind:   "junk",
				By:     actor.ID,
				TS:     ts,
			},
			TS: ts,
		})
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: strconv.Itoa(len(postIDs))}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decidePurgePost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.PurgePostPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid purgePost payload", false)
	}
	payload.Post = strings.TrimSpace(payload.Post)
	if payload.Post == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "post is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionPost, Key: payload.Post}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionPost, payload.Post), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "admin role required", false)
	}
	post, err := getPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	thread, err := getThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", "thread not found", true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtPostPurged,
		Scopes: []string{"thread:" + post.Thread, "board:" + thread.Board},
		Payload: &proto.PostPurgedPayload{
			ID:     post.ID,
			Thread: post.Thread,
			By:     actor.ID,
			Reason: payload.Reason,
			TS:     ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: post.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetThreadTitle(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetThreadTitlePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setThreadTitle payload", false)
	}
	payload.Thread = strings.TrimSpace(payload.Thread)
	title := strings.TrimSpace(payload.Title)
	if payload.Thread == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "thread is required", false)
	}
	if title == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "title is required", false)
	}
	if len(title) > 160 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "title must be 160 characters or less", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionThread, Key: payload.Thread}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionThread, payload.Thread), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := getThread(e.core.DB, payload.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	if thread.Title == title {
		return nativeCommandDecision{reply: Reply{Result: &proto.AckResult{ID: thread.ID}}}, nil
	}
	canModerateThread, err := nativeActorCanModerateBoardThreads(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	isAuthor := thread.AuthorID == actor.ID
	if thread.AuthorID == "" {
		isAuthor = thread.Author == actor.Name
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	if !canModerateThread {
		if !isAuthor {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "thread author or board thread moderation permission required", false)
		}
		if ts-thread.CreatedAt >= nativeAuthorEditWindow.Milliseconds() {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrEditWindowExpired, "edit window has expired", false)
		}
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtThreadTitleSet,
		Scopes: []string{"board:" + thread.Board, "thread:" + thread.ID},
		Payload: &proto.ThreadTitleSetPayload{
			Thread: thread.ID,
			Title:  title,
			By:     actor.ID,
			TS:     ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: thread.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideLockThread(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.LockThreadPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid lockThread payload", false)
	}
	payload.Thread = strings.TrimSpace(payload.Thread)
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionThread, Key: payload.Thread}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionThread, payload.Thread), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := getThread(e.core.DB, payload.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	canModerateThread, err := nativeActorCanModerateBoardThreads(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canModerateThread {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board thread moderation permission required", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtThreadLocked,
		Scopes: []string{"board:" + thread.Board, "thread:" + thread.ID},
		Payload: &proto.ThreadLockedPayload{
			Thread: thread.ID,
			Locked: payload.Locked,
			By:     actor.ID,
			TS:     ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: thread.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideMoveThread(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.MoveThreadPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid moveThread payload", false)
	}
	payload.Thread = strings.TrimSpace(payload.Thread)
	payload.ToBoard = strings.TrimSpace(payload.ToBoard)
	if payload.Thread == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "thread is required", false)
	}
	if payload.ToBoard == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "destination board is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionThread, Key: payload.Thread}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionThread, payload.Thread), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := getThread(e.core.DB, payload.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	canModerateThread, err := nativeActorCanModerateBoardThreads(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canModerateThread {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board thread moderation permission required", false)
	}
	var destName string
	if err := qQueryRow(e.core.DB, `SELECT name FROM boards WHERE id=?`, payload.ToBoard).Scan(&destName); err == sql.ErrNoRows {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "destination board not found", false)
	} else if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtThreadMoved,
		Scopes: []string{"board:" + thread.Board, "board:" + payload.ToBoard},
		Payload: &proto.ThreadMovedPayload{
			Thread:    thread.ID,
			FromBoard: thread.Board,
			ToBoard:   payload.ToBoard,
			By:        actor.ID,
			TS:        ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: thread.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideFlagPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.FlagPostPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid flagPost payload", false)
	}
	payload.Post = strings.TrimSpace(payload.Post)
	if payload.Post == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "post is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionPost, Key: payload.Post}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionPost, payload.Post), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := getPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	thread, err := getThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", "thread not found", true)
	}
	publicBoard, err := nativePublicBoardForModerationLog(e.core.DB, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	reviewID := stableCommandLogDecisionID("rev_", record, 0)
	events := []EventAppend{
		{
			ID:     stableCommandLogDecisionID("evt_", record, 0),
			Kind:   proto.EvtPostFlagged,
			Scopes: []string{"thread:" + post.Thread, "board:" + thread.Board, "moderation:global"},
			Payload: &proto.PostFlaggedPayload{
				ReviewID: reviewID,
				Kind:     "post_flag",
				PostID:   post.ID,
				Thread:   post.Thread,
				Reporter: actor.ID,
				Reason:   payload.Reason,
				TS:       ts,
			},
			TS: ts,
		},
	}
	logEvents, errDetail := nativeModerationSystemLogEvents(e.core.DB, record, actor, moderationLogFlag, reviewID, post.ID, post.Thread, thread.Board, publicBoard, ts, len(events))
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events = append(events, logEvents...)
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: reviewID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideResolveReview(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.ResolveReviewPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid resolveReview payload", false)
	}
	payload.Review = strings.TrimSpace(payload.Review)
	payload.Resolution = strings.TrimSpace(payload.Resolution)
	if payload.Review == "" || payload.Resolution == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "review and resolution are required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionReview, Key: payload.Review}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionReview, payload.Review), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsMod() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "moderator role required", false)
	}
	targetPostID, targetThreadID, targetBoardID, publicBoard, errDetail := nativeModerationReviewLogTarget(e.core.DB, payload.Review)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := []EventAppend{
		{
			ID:     stableCommandLogDecisionID("evt_", record, 0),
			Kind:   proto.EvtReviewResolved,
			Scopes: []string{"moderation:global"},
			Payload: &proto.ReviewResolvedPayload{
				ReviewID:   payload.Review,
				Resolution: payload.Resolution,
				By:         actor.ID,
				TS:         ts,
			},
			TS: ts,
		},
	}
	logEvents, errDetail := nativeModerationSystemLogEvents(e.core.DB, record, actor, moderationLogResolve, payload.Review, targetPostID, targetThreadID, targetBoardID, publicBoard, ts, len(events))
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events = append(events, logEvents...)
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: payload.Review}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decidePublishPollResult(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.PublishPollResultPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid publishPollResult payload", false)
	}
	payload.Poll = strings.TrimSpace(payload.Poll)
	if payload.Poll == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "poll is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionPoll, Key: payload.Poll}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionPoll, payload.Poll), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	poll, err := getPollWithVotes(e.core.DB, payload.Poll, actor.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if poll == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "poll not found", false)
	}
	post, err := getPost(e.core.DB, poll.PostID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", "poll post not found", true)
	}
	thread, err := getThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", "poll thread not found", true)
	}
	canManagePolls, err := nativeActorCanManageBoardPolls(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canManagePolls && post.AuthorID != actor.ID && thread.AuthorID != actor.ID {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "poll author or board poll manager required", false)
	}
	settings, err := getBoardSettings(e.core.DB, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings != nil && settings.MemberReadMode {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "member-read poll results stay on the source board", false)
	}

	threadID := "vote_poll_" + poll.ID
	postID := "vote_poll_post_" + poll.ID
	var existingSeq int64
	err = qQueryRow(e.core.DB, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		return nativeCommandDecision{
			reply: Reply{Result: &proto.AckResult{ID: threadID, Seq: existingSeq}},
		}, nil
	}
	if err != sql.ErrNoRows {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}

	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	question := strings.TrimSpace(poll.Question)
	title := "Poll result"
	if question != "" {
		title = "Poll result: " + question
	}
	body := nativeFormatPollResultBody(thread, poll)
	events := make([]EventAppend, 0, 3)
	var voteBoardExists int
	err = qQueryRow(e.core.DB, `SELECT 1 FROM boards WHERE id=?`, nativeVoteSystemBoardID).Scan(&voteBoardExists)
	if err == sql.ErrNoRows {
		position, posErr := nativeBoardCategoryPosition(e.core.DB, "")
		if posErr != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", posErr.Error(), true)
		}
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, len(events)),
			Kind:   proto.EvtBoardCreated,
			Scopes: []string{"board:" + nativeVoteSystemBoardID},
			Payload: &proto.BoardCreatedPayload{
				ID:          nativeVoteSystemBoardID,
				Name:        "vote",
				Description: "Generated poll results",
				Position:    position,
				By:          actor.ID,
				TS:          ts,
			},
			TS: ts,
		})
	} else if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, len(events)),
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:" + nativeVoteSystemBoardID},
		Payload: &proto.ThreadNewPayload{
			ID:       threadID,
			Board:    nativeVoteSystemBoardID,
			Author:   actor.Name,
			AuthorID: actor.ID,
			Title:    title,
			TS:       ts,
		},
		TS: ts,
	})
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, len(events)),
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:" + nativeVoteSystemBoardID, "thread:" + threadID},
		Payload: &proto.PostAppendedPayload{
			ID:          postID,
			Thread:      threadID,
			Author:      actor.Name,
			AuthorID:    actor.ID,
			Body:        body,
			RawBody:     body,
			ContentType: "markup",
			TS:          ts,
		},
		TS: ts,
	})
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: threadID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetContentFilter(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetContentFilterPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setContentFilter payload", false)
	}
	filterID := strings.TrimSpace(payload.ID)
	if filterID == "" {
		filterID = stableCommandLogDecisionID("filter_", record, 0)
	} else if !nativeValidSlug(filterID) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "filter id must be lowercase alphanumeric, hyphens, or underscores", false)
	}
	pattern := strings.TrimSpace(payload.Pattern)
	if pattern == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "pattern is required", false)
	}
	if len(pattern) > 120 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "pattern must be 120 characters or less", false)
	}
	scope := strings.TrimSpace(payload.Scope)
	if scope == "" {
		scope = "global"
	}
	partition := record.Partition.Normalize()
	expectedPartition := LogPartition{Kind: partitionBoard, Key: scope}
	if expectedPartition.Key == "" {
		expectedPartition.Key = partitionGlobal
	}
	if partition != expectedPartition {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, expectedPartition.Kind, expectedPartition.Key), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "admin role required", false)
	}
	if scope != "global" {
		var boardName string
		if err := qQueryRow(e.core.DB, `SELECT name FROM boards WHERE id=?`, scope).Scan(&boardName); err == sql.ErrNoRows {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found for scope", false)
		} else if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	active := true
	if payload.Active != nil {
		active = *payload.Active
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	scopes := []string{"moderation:global"}
	if scope != "global" {
		scopes = append(scopes, "board:"+scope)
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtContentFilterSet,
		Scopes: scopes,
		Payload: &proto.ContentFilterSetPayload{
			ID:      filterID,
			Pattern: pattern,
			Scope:   scope,
			Active:  active,
			By:      actor.ID,
			TS:      ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: filterID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardAutomodRule(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetBoardAutomodRulePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setBoardAutomodRule payload", false)
	}
	board := strings.TrimSpace(payload.Board)
	if board == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: board}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, board), false)
	}
	if msg := proto.ValidateAutomodRule(payload); msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	action := strings.TrimSpace(payload.Action)
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	exists, err := nativeBoardExists(e.core.DB, board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	switch action {
	case "global_mute":
		if !actor.IsAdmin() {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "only admins can create global-sanction rules", false)
		}
	case "lock_thread":
		if ok, err := e.core.userCanModerateBoardCap(actor.ID, actor.Role, board, "can_moderate_threads"); err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		} else if !ok {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "thread moderation permission required", false)
		}
	default:
		if ok, err := e.core.userCanModerateBoardCap(actor.ID, actor.Role, board, "can_moderate_posts"); err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		} else if !ok {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "post moderation permission required", false)
		}
	}
	ruleID := strings.TrimSpace(payload.ID)
	if ruleID == "" {
		ruleID = stableCommandLogDecisionID("rule_", record, 0)
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardAutomodRuleSet,
		Scopes: []string{"board:" + board},
		Payload: &proto.BoardAutomodRuleSetPayload{
			ID: ruleID, Board: board, Enabled: enabled, Priority: payload.Priority,
			MatchType: strings.TrimSpace(payload.MatchType), Pattern: strings.TrimSpace(payload.Pattern),
			Threshold: payload.Threshold, WindowSec: payload.WindowSec, Action: action,
			DurationSec: payload.DurationSec, Reason: strings.TrimSpace(payload.Reason),
			Note: strings.TrimSpace(payload.Note), By: actor.ID, TS: ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: ruleID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteBoardAutomodRule(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.DeleteBoardAutomodRulePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid deleteBoardAutomodRule payload", false)
	}
	board := strings.TrimSpace(payload.Board)
	id := strings.TrimSpace(payload.ID)
	if board == "" || id == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board and id are required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: board}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, board), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if ok, err := e.core.UserCanModerateBoard(actor.ID, actor.Role, board); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	} else if !ok {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board moderation permission required", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:      stableCommandLogDecisionID("evt_", record, 0),
		Kind:    proto.EvtBoardAutomodRuleDeleted,
		Scopes:  []string{"board:" + board},
		Payload: &proto.BoardAutomodRuleDeletedPayload{ID: id, Board: board, By: actor.ID, TS: ts},
		TS:      ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: id}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSanctionUser(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SanctionUserPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid sanctionUser payload", false)
	}
	targetID := strings.TrimSpace(payload.User)
	expectedKey := targetID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsMod() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "moderator role required", false)
	}
	reason := strings.TrimSpace(payload.Reason)
	if len(reason) > 500 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "reason must be 500 characters or less", false)
	}
	if targetID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "user is required", false)
	}
	if payload.Kind != "mute" && payload.Kind != "ban" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, `kind must be "mute" or "ban"`, false)
	}
	scope := strings.TrimSpace(payload.Scope)
	if scope == "" {
		scope = "global"
	}
	target, err := getUserByID(e.core.DB, targetID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if target == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	if target.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "cannot sanction an admin", false)
	}
	if target.IsMod() && !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "only admins can sanction moderators", false)
	}
	sourceBoardName := ""
	if scope != "global" {
		if err := qQueryRow(e.core.DB, `SELECT name FROM boards WHERE id=?`, scope).Scan(&sourceBoardName); err == sql.ErrNoRows {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found for scope", false)
		} else if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := []EventAppend{
		{
			ID:     stableCommandLogDecisionID("evt_", record, 0),
			Kind:   proto.EvtUserSanctioned,
			Scopes: []string{"account:" + target.ID},
			Payload: &proto.UserSanctionedPayload{
				User:        target.ID,
				Kind:        payload.Kind,
				Scope:       scope,
				DurationSec: payload.DurationSec,
				By:          actor.ID,
				Reason:      reason,
				TS:          ts,
			},
			TS: ts,
		},
	}
	if scope != "global" {
		auditEvents, errDetail := nativeDenyPostSystemLogEvents(e.core.DB, record, actor, target, scope, sourceBoardName, payload.Kind, reason, ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, auditEvents...)
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: stableCommandLogDecisionID("san_", record, 0)}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideClearUserSanction(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.ClearUserSanctionPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid clearUserSanction payload", false)
	}
	targetRef := strings.TrimSpace(payload.User)
	expectedKey := targetRef
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsMod() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "moderator role required", false)
	}
	if targetRef == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "user is required", false)
	}
	kind := strings.TrimSpace(payload.Kind)
	if kind != "" && kind != "mute" && kind != "ban" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, `kind must be "mute", "ban", or empty`, false)
	}
	scope := strings.TrimSpace(payload.Scope)
	if scope == "" {
		scope = "global"
	}
	reason := strings.TrimSpace(payload.Reason)
	if len(reason) > 500 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "reason must be 500 characters or less", false)
	}
	target, err := nativeFindUserRef(e.core.DB, targetRef)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if target == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	if target.IsMod() && !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "only admins can clear moderator sanctions", false)
	}
	sourceBoardName := ""
	if scope != "global" {
		if err := qQueryRow(e.core.DB, `SELECT name FROM boards WHERE id=?`, scope).Scan(&sourceBoardName); err == sql.ErrNoRows {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found for scope", false)
		} else if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := []EventAppend{
		{
			ID:     stableCommandLogDecisionID("evt_", record, 0),
			Kind:   proto.EvtUserSanctionCleared,
			Scopes: []string{"account:" + target.ID},
			Payload: &proto.UserSanctionClearedPayload{
				User:   target.ID,
				Kind:   kind,
				Scope:  scope,
				By:     actor.ID,
				Reason: reason,
				TS:     ts,
			},
			TS: ts,
		},
	}
	if scope != "global" {
		auditEvents, errDetail := nativeUndenyPostSystemLogEvents(e.core.DB, record, actor, target, scope, sourceBoardName, kind, reason, ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, auditEvents...)
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: target.ID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideCreateBoardCommand(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.CreateBoardPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid createBoard payload", false)
	}
	payload.ID = strings.TrimSpace(payload.ID)
	payload.Name = strings.TrimSpace(payload.Name)
	payload.ParentID = strings.TrimSpace(payload.ParentID)
	expectedKey := payload.ID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "admin role required", false)
	}
	if payload.ID == "" || payload.Name == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "id and name are required", false)
	}
	if !nativeIsValidSlug(payload.ID) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "id must be lowercase alphanumeric, hyphens, or underscores (max 64 chars)", false)
	}
	if payload.ParentID == payload.ID {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board cannot be its own parent", false)
	}
	if payload.Position != nil && *payload.Position < 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "position cannot be negative", false)
	}
	if payload.ParentID != "" {
		found, err := nativeCategoryExists(e.core.DB, payload.ParentID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !found {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "parent category not found", false)
		}
	}
	position, err := nativeBoardCategoryPositionForCreate(e.core.DB, payload.ID, payload.ParentID, payload.Position)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	matches, err := nativeCreatedBoardMatches(e.core.DB, payload.ID, payload.Name, payload.Description, payload.ParentID, position)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !matches {
		exists, err := nativeBoardExists(e.core.DB, payload.ID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if exists {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "board already exists", false)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardCreated,
		Scopes: []string{"board:" + payload.ID},
		Payload: &proto.BoardCreatedPayload{
			ID:          payload.ID,
			Name:        payload.Name,
			Description: payload.Description,
			ParentID:    payload.ParentID,
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: payload.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardSettings(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetBoardSettingsPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setBoardSettings payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	settings, err := getBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canSet, err := nativeActorCanSetBoardSettings(e.core.DB, actor, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canSet {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board settings permission required", false)
	}
	projections.ApplyBoardSettingsPatch(settings, BoardSettingsPatch{
		AnonymousAllowed:   payload.AnonymousAllowed,
		ReadOnly:           payload.ReadOnly,
		NoReply:            payload.NoReply,
		AttachmentsAllowed: payload.AttachmentsAllowed,
		MailInAllowed:      payload.MailInAllowed,
		RelayEnabled:       payload.RelayEnabled,
		MemberReadMode:     payload.MemberReadMode,
		MemberPostMode:     payload.MemberPostMode,
		StatsExcluded:      payload.StatsExcluded,
		ZapAllowed:         payload.ZapAllowed,
	})
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := []EventAppend{{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardSettingsSet,
		Scopes: []string{"board:" + boardID},
		Payload: &proto.BoardSettingsSetPayload{
			Board:              boardID,
			AnonymousAllowed:   settings.AnonymousAllowed,
			ReadOnly:           settings.ReadOnly,
			NoReply:            settings.NoReply,
			AttachmentsAllowed: settings.AttachmentsAllowed,
			MailInAllowed:      settings.MailInAllowed,
			RelayEnabled:       settings.RelayEnabled,
			MemberReadMode:     settings.MemberReadMode,
			MemberPostMode:     settings.MemberPostMode,
			StatsExcluded:      settings.StatsExcluded,
			ZapAllowed:         settings.ZapAllowed,
			By:                 actor.ID,
			TS:                 ts,
		},
		TS: ts,
	}}
	if settingLines := nativeBoardSettingsAuditLines(payload); len(settingLines) > 0 && !settings.MemberReadMode {
		lines := []string{
			"Action: board settings changed",
			"Board: " + boardID,
			"Actor: " + actor.Name,
		}
		lines = append(lines, settingLines...)
		auditEvents, errDetail := nativeSyssecuritySystemLogEvents(e.core.DB, record, actor, "Board settings changed: "+boardID, lines, ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, auditEvents...)
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: boardID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardMemberRequirements(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetBoardMemberRequirementsPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setBoardMemberRequirements payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	req, err := getBoardMemberRequirements(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if req == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canSet, err := nativeActorCanSetBoardSettings(e.core.DB, actor, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canSet {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board settings permission required", false)
	}
	for _, field := range []struct {
		name  string
		value *int
	}{
		{"minLoginCount", payload.MinLoginCount},
		{"minPostCount", payload.MinPostCount},
		{"minTrustLevel", payload.MinTrustLevel},
		{"minScore", payload.MinScore},
		{"minBoardPostCount", payload.MinBoardPostCount},
		{"minBoardOriginalPostCount", payload.MinBoardOriginalPostCount},
		{"minBoardDigestCount", payload.MinBoardDigestCount},
		{"minBoardMarkCount", payload.MinBoardMarkCount},
		{"maxMembers", payload.MaxMembers},
	} {
		if field.value != nil && *field.value < 0 {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, field.name+" must be non-negative", false)
		}
	}
	patch := BoardMemberRequirementsPatch{
		MinLoginCount:             payload.MinLoginCount,
		MinPostCount:              payload.MinPostCount,
		MinTrustLevel:             payload.MinTrustLevel,
		MinScore:                  payload.MinScore,
		MinBoardPostCount:         payload.MinBoardPostCount,
		MinBoardOriginalPostCount: payload.MinBoardOriginalPostCount,
		MinBoardDigestCount:       payload.MinBoardDigestCount,
		MinBoardMarkCount:         payload.MinBoardMarkCount,
		MaxMembers:                payload.MaxMembers,
	}
	if payload.ApprovalMode != nil {
		mode, errDetail := nativeNormalizeBoardMemberApprovalMode(*payload.ApprovalMode)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		patch.ApprovalMode = &mode
	}
	projections.ApplyBoardMemberRequirementsPatch(req, patch)
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardMemberRequirementsSet,
		Scopes: []string{"board:" + boardID},
		Payload: &proto.BoardMemberRequirementsSetPayload{
			Board:                     boardID,
			MinLoginCount:             req.MinLoginCount,
			MinPostCount:              req.MinPostCount,
			MinTrustLevel:             req.MinTrustLevel,
			MinScore:                  req.MinScore,
			MinBoardPostCount:         req.MinBoardPostCount,
			MinBoardOriginalPostCount: req.MinBoardOriginalPostCount,
			MinBoardDigestCount:       req.MinBoardDigestCount,
			MinBoardMarkCount:         req.MinBoardMarkCount,
			MaxMembers:                req.MaxMembers,
			ApprovalMode:              req.ApprovalMode,
			By:                        actor.ID,
			TS:                        ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: boardID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardModerator(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetBoardModeratorPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setBoardModerator payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "admin role required", false)
	}
	userRef := strings.TrimSpace(payload.User)
	if boardID == "" || userRef == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board and user are required", false)
	}
	exists, err := nativeBoardExists(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	target, errDetail := nativeResolveUserRef(e.core.DB, userRef)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	position, err := nativeBoardModeratorEventPosition(e.core.DB, boardID, target.ID, actor.ID, payload.Moderator, payload.Position, ts)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	events := []EventAppend{{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardModeratorSet,
		Scopes: []string{"board:" + boardID, "user:" + target.ID},
		Payload: &proto.BoardModeratorSetPayload{
			Board:     boardID,
			User:      target.ID,
			Moderator: payload.Moderator,
			Position:  position,
			By:        actor.ID,
			TS:        ts,
		},
		TS: ts,
	}}
	emitAudit, err := nativeBoardAllowsSyssecurityAudit(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if emitAudit {
		action := "moderator removed"
		if payload.Moderator {
			action = "moderator appointed"
		}
		auditEvents, errDetail := nativeSyssecuritySystemLogEvents(e.core.DB, record, actor, "Board "+action+": "+boardID, []string{
			"Action: board " + action,
			"Board: " + boardID,
			"User: " + target.Name,
			"Actor: " + actor.Name,
		}, ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, auditEvents...)
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: boardID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardMember(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetBoardMemberPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setBoardMember payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	userRef := strings.TrimSpace(payload.User)
	if boardID == "" || userRef == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board and user are required", false)
	}
	exists, err := nativeBoardExists(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canModerateBoard, err := nativeActorCanModerateBoard(e.core.DB, actor, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	canManageMembers, err := nativeActorCanManageBoardMembers(e.core.DB, actor, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canModerateBoard && !canManageMembers {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board member manager permission required", false)
	}
	target, errDetail := nativeResolveUserRef(e.core.DB, userRef)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !canModerateBoard {
		if nativeBoardMemberPermissionsChanged(payload) {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board moderator role required to change member permissions", false)
		}
		isModerator, err := nativeIsBoardModerator(e.core.DB, target.ID, boardID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if isModerator {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board moderator role required to manage board moderators", false)
		}
		privilegedMember, err := nativeBoardMemberHasDelegatedPermissions(e.core.DB, boardID, target.ID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if privilegedMember {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board moderator role required to manage delegated board members", false)
		}
	}
	title := strings.TrimSpace(payload.Title)
	if len(title) > 80 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "member title must be 80 characters or less", false)
	}
	if payload.Position != nil && *payload.Position < 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "member position cannot be negative", false)
	}
	member, err := nativeBoardMemberFinalState(e.core.DB, boardID, target.ID, payload, title)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardMemberSet,
		Scopes: []string{"board:" + boardID, "user:" + target.ID},
		Payload: &proto.BoardMemberSetPayload{
			Board:               boardID,
			User:                target.ID,
			Member:              payload.Member,
			Title:               member.Title,
			Position:            member.Position,
			CanManageMembers:    member.CanManageMembers,
			CanCurate:           member.CanCurate,
			CanModeratePosts:    member.CanModeratePosts,
			CanModerateThreads:  member.CanModerateThreads,
			CanAnnounce:         member.CanAnnounce,
			CanManagePolls:      member.CanManagePolls,
			CanSetBoardSettings: member.CanSetBoardSettings,
			By:                  actor.ID,
			TS:                  ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: boardID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideLeaveBoardMembership(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.LeaveBoardMembershipPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid leaveBoardMembership payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	exists, err := nativeBoardExists(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardMemberSet,
		Scopes: []string{"board:" + boardID, "user:" + actor.ID},
		Payload: &proto.BoardMemberSetPayload{
			Board:  boardID,
			User:   actor.ID,
			Member: false,
			By:     actor.ID,
			TS:     ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: boardID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideApplyBoardMembership(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.ApplyBoardMembershipPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid applyBoardMembership payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	exists, err := nativeBoardExists(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	note := strings.TrimSpace(payload.Note)
	if len(note) > 500 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "application note must be 500 characters or less", false)
	}
	requirements, err := getBoardMemberRequirements(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	autoApprove := requirements != nil && requirements.ApprovalMode == "auto"
	applicationID := stableCommandLogDecisionID("bmap_", record, 0)
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	existingApplication, err := getBoardMemberApplication(e.core.DB, applicationID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if existingApplication != nil {
		if existingApplication.BoardID != boardID || existingApplication.UserID != actor.ID || existingApplication.Note != note {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "membership application id conflict", false)
		}
		switch existingApplication.Status {
		case "pending":
		case "approved":
			autoApprove = autoApprove ||
				(existingApplication.ReviewerID == actor.ID &&
					existingApplication.Title == "" &&
					existingApplication.ReviewNote == "auto-approved by board membership rules")
		default:
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "membership application is already reviewed", false)
		}
		events, errDetail := nativeBoardMembershipApplicationEvents(e.core.DB, record, actor, applicationID, boardID, actor.ID, note, autoApprove, ts)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		return nativeCommandDecision{
			reply:  Reply{Result: &proto.AckResult{ID: applicationID}},
			events: events,
		}, nil
	}
	isMember, err := projections.UserIsBoardMember(e.core.DB, boardID, actor.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if isMember {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "already a board member", false)
	}
	status, err := projections.LatestBoardMemberApplicationStatus(e.core.DB, boardID, actor.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	switch status {
	case "pending":
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "membership application already pending", false)
	case "blacklisted":
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "membership application is blocked", false)
	}
	if errDetail := nativeRequireBoardMembershipAdmission(e.core, boardID, actor.ID, requirements); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events, errDetail := nativeBoardMembershipApplicationEvents(e.core.DB, record, actor, applicationID, boardID, actor.ID, note, autoApprove, ts)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: applicationID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideReviewBoardMembership(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.ReviewBoardMembershipPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid reviewBoardMembership payload", false)
	}
	applicationID := strings.TrimSpace(payload.Application)
	rawStatus := strings.TrimSpace(payload.Status)
	if applicationID == "" || rawStatus == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "application and status are required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionReview, Key: applicationID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionReview, applicationID), false)
	}
	status, errDetail := nativeNormalizeMemberApplicationStatus(rawStatus)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	app, err := getBoardMemberApplication(e.core.DB, applicationID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if app == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "membership application not found", false)
	}
	title := strings.TrimSpace(payload.Title)
	if len(title) > 80 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "member title must be 80 characters or less", false)
	}
	note := strings.TrimSpace(payload.Note)
	if len(note) > 500 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "review note must be 500 characters or less", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	if app.Status != "pending" {
		if app.Status == status &&
			app.ReviewerID == actor.ID &&
			app.Title == title &&
			app.ReviewNote == note &&
			app.ReviewedAt == ts {
			events, errDetail := nativeBoardMembershipReviewEvents(e.core.DB, record, actor, applicationID, app.BoardID, app.UserID, status, title, note, ts)
			if errDetail != nil {
				return nativeCommandDecision{}, errDetail
			}
			return nativeCommandDecision{
				reply:  Reply{Result: &proto.AckResult{ID: applicationID}},
				events: events,
			}, nil
		}
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "membership application is already reviewed", false)
	}
	canModerateBoard, err := nativeActorCanModerateBoard(e.core.DB, actor, app.BoardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	canManageMembers, err := nativeActorCanManageBoardMembers(e.core.DB, actor, app.BoardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canModerateBoard && !canManageMembers {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board member manager permission required", false)
	}
	if !canModerateBoard && actor.ID == app.UserID {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board moderator role required to review your own application", false)
	}
	if !canModerateBoard && status == "blacklisted" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board moderator role required to blacklist membership applications", false)
	}
	if status == "approved" {
		requirements, err := getBoardMemberRequirements(e.core.DB, app.BoardID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if errDetail := nativeRequireBoardMembershipAdmission(e.core, app.BoardID, app.UserID, requirements); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
	}
	events, errDetail := nativeBoardMembershipReviewEvents(e.core.DB, record, actor, applicationID, app.BoardID, app.UserID, status, title, note, ts)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: applicationID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetRecommendedBoard(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetRecommendedBoardPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setRecommendedBoard payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "admin role required", false)
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	exists, err := nativeBoardExists(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	if payload.Position != nil && *payload.Position < 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "position cannot be negative", false)
	}
	note := strings.TrimSpace(payload.Note)
	if len(note) > 500 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "recommendation note must be 500 characters or less", false)
	}
	if payload.Recommended {
		ok, reason, err := nativeBoardCanBePubliclyRecommended(e.core.DB, boardID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !ok {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, reason, false)
		}
	}
	position := 0
	if payload.Recommended {
		var err error
		position, err = nativeRecommendedBoardPosition(e.core.DB, boardID, payload.Position)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardRecommendedSet,
		Scopes: []string{"board:" + boardID},
		Payload: &proto.BoardRecommendedSetPayload{
			Board:       boardID,
			Recommended: payload.Recommended,
			Note:        note,
			Position:    position,
			CuratedBy:   actor.ID,
			TS:          ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: boardID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideGrantRole(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.GrantRolePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid grantRole payload", false)
	}
	targetID := strings.TrimSpace(payload.User)
	expectedKey := targetID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "admin role required", false)
	}
	target, err := getUserByID(e.core.DB, targetID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if target == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := []EventAppend{
		{
			ID:     stableCommandLogDecisionID("evt_", record, 0),
			Kind:   proto.EvtRoleGranted,
			Scopes: []string{"account:" + target.ID},
			Payload: &proto.RoleGrantedPayload{
				User: target.ID,
				Role: payload.Role,
				By:   actor.ID,
				TS:   ts,
			},
			TS: ts,
		},
	}
	auditEvents, errDetail := nativeSyssecuritySystemLogEvents(e.core.DB, record, actor, "Role granted: "+target.Name, []string{
		"Action: role granted",
		"User: " + target.Name,
		"Role: " + payload.Role,
		"Actor: " + actor.Name,
	}, ts, len(events))
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events = append(events, auditEvents...)
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: target.ID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideRevokeRole(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.RevokeRolePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid revokeRole payload", false)
	}
	targetID := strings.TrimSpace(payload.User)
	expectedKey := targetID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "admin role required", false)
	}
	target, err := getUserByID(e.core.DB, targetID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if target == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := []EventAppend{
		{
			ID:     stableCommandLogDecisionID("evt_", record, 0),
			Kind:   proto.EvtRoleRevoked,
			Scopes: []string{"account:" + target.ID},
			Payload: &proto.RoleRevokedPayload{
				User: target.ID,
				Role: payload.Role,
				By:   actor.ID,
				TS:   ts,
			},
			TS: ts,
		},
	}
	auditEvents, errDetail := nativeSyssecuritySystemLogEvents(e.core.DB, record, actor, "Role revoked: "+target.Name, []string{
		"Action: role revoked",
		"User: " + target.Name,
		"Role: " + payload.Role,
		"Actor: " + actor.Name,
	}, ts, len(events))
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events = append(events, auditEvents...)
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: target.ID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decidePublishStatsSnapshot(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.PublishStatsSnapshotPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid publishStatsSnapshot payload", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionGlobal, Key: partitionGlobal}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionGlobal, partitionGlobal), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "admin role required", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	dateLabel, _, errDetail := nativeNormalizeStatsSnapshotDate(payload.Date, ts)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	plan, err := corehandler.PlanStatsSnapshotSystemPosts(e.core.DB, actor, dateLabel, ts)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	events := []EventAppend{{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtCommunityStatsSnapshotRecorded,
		Scopes: nil,
		Payload: &proto.CommunityStatsSnapshotRecordedPayload{
			Day:                 plan.Snapshot.Day,
			SnapshotAt:          plan.Snapshot.SnapshotAt,
			TotalUsers:          plan.Snapshot.TotalUsers,
			TotalBoards:         plan.Snapshot.TotalBoards,
			TotalThreads:        plan.Snapshot.TotalThreads,
			TotalPosts:          plan.Snapshot.TotalPosts,
			TotalReactions:      plan.Snapshot.TotalReactions,
			TotalMail:           plan.Snapshot.TotalMail,
			TotalDirectMessages: plan.Snapshot.TotalDirectMessages,
			TotalLogins:         plan.Snapshot.TotalLogins,
			TotalLogouts:        plan.Snapshot.TotalLogouts,
			TotalWebLogins:      plan.Snapshot.TotalWebLogins,
			TotalWebLogouts:     plan.Snapshot.TotalWebLogouts,
			TotalGuestLogins:    plan.Snapshot.TotalGuestLogins,
			TotalGuestLogouts:   plan.Snapshot.TotalGuestLogouts,
			TotalOnlineSeconds:  plan.Snapshot.TotalOnlineSeconds,
			OnlineUsers:         plan.Snapshot.OnlineUsers,
			OnlineGuests:        plan.Snapshot.OnlineGuests,
			MaxOnlineUsers:      plan.Snapshot.MaxOnlineUsers,
			MaxOnlineAt:         plan.Snapshot.MaxOnlineAt,
			MaxOnlineGuests:     plan.Snapshot.MaxOnlineGuests,
			MaxOnlineGuestsAt:   plan.Snapshot.MaxOnlineGuestsAt,
			HeadSeq:             plan.Snapshot.HeadSeq,
		},
		TS: ts,
	}}
	if len(plan.Posts) > 0 {
		exists, err := nativeBoardExists(e.core.DB, corehandler.StatsSystemBoardID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			position, err := nativeBoardCategoryPosition(e.core.DB, "")
			if err != nil {
				return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
			}
			events = append(events, EventAppend{
				ID:     stableCommandLogDecisionID("evt_", record, len(events)),
				Kind:   proto.EvtBoardCreated,
				Scopes: []string{"board:" + corehandler.StatsSystemBoardID},
				Payload: &proto.BoardCreatedPayload{
					ID:          corehandler.StatsSystemBoardID,
					Name:        "BBSLists",
					Description: "Generated community rankings and statistics",
					Position:    position,
					By:          actor.ID,
					TS:          ts,
				},
				TS: ts,
			})
		}
	}
	for _, post := range plan.Posts {
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, len(events)),
			Kind:   proto.EvtThreadNew,
			Scopes: []string{"board:" + corehandler.StatsSystemBoardID},
			Payload: &proto.ThreadNewPayload{
				ID:       post.ThreadID,
				Board:    corehandler.StatsSystemBoardID,
				Author:   actor.Name,
				AuthorID: actor.ID,
				Title:    post.Title,
				TS:       ts,
			},
			TS: ts,
		})
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, len(events)),
			Kind:   proto.EvtPostAppended,
			Scopes: []string{"board:" + corehandler.StatsSystemBoardID, "thread:" + post.ThreadID},
			Payload: &proto.PostAppendedPayload{
				ID:          post.PostID,
				Thread:      post.ThreadID,
				Author:      actor.Name,
				AuthorID:    actor.ID,
				Body:        post.Body,
				RawBody:     post.Body,
				ContentType: "markup",
				TS:          ts,
			},
			TS: ts,
		})
	}
	reply := &proto.AckResult{ID: plan.MainThreadID}
	if plan.MainExistingSeq > 0 {
		reply.Seq = plan.MainExistingSeq
	}
	return nativeCommandDecision{
		reply:  Reply{Result: reply},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decidePublishSystemNotice(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.PublishSystemNoticePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid publishSystemNotice payload", false)
	}
	rawBoard := strings.TrimSpace(payload.Board)
	expectedKey := rawBoard
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !actor.IsAdmin() {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "admin role required", false)
	}
	board, ok := nativeNormalizeSystemNoticeBoard(rawBoard)
	if !ok {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "notice board must be notepad, GiveupNotice, or bbsnet", false)
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "title is required", false)
	}
	if len(title) > 160 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "title must be 160 characters or less", false)
	}
	body := strings.TrimSpace(payload.Body)
	if body == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "body is required", false)
	}
	if len(body) > 20000 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "body must be 20000 characters or less", false)
	}
	source := strings.TrimSpace(payload.Source)
	if len(source) > 160 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "source must be 160 characters or less", false)
	}

	threadID := stableCommandLogDecisionID("notice_thr_", record, 0)
	postID := stableCommandLogDecisionID("notice_pst_", record, 0)
	var existingSeq int64
	err := qQueryRow(e.core.DB, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		return nativeCommandDecision{
			reply: Reply{Result: &proto.AckResult{ID: threadID, Seq: existingSeq}},
		}, nil
	}
	if err != sql.ErrNoRows {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}

	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	noticeBody := nativeFormatSystemNoticeBody(board, title, body, source, actor.Name)
	events := make([]EventAppend, 0, 3)
	var boardExists int
	err = qQueryRow(e.core.DB, `SELECT 1 FROM boards WHERE id=?`, board.ID).Scan(&boardExists)
	if err == sql.ErrNoRows {
		position, posErr := nativeBoardCategoryPosition(e.core.DB, "")
		if posErr != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", posErr.Error(), true)
		}
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, 0),
			Kind:   proto.EvtBoardCreated,
			Scopes: []string{"board:" + board.ID},
			Payload: &proto.BoardCreatedPayload{
				ID:          board.ID,
				Name:        board.Name,
				Description: board.Description,
				Position:    position,
				By:          actor.ID,
				TS:          ts,
			},
			TS: ts,
		})
	} else if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	scopes := []string{"board:" + board.ID}
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 1),
		Kind:   proto.EvtThreadNew,
		Scopes: scopes,
		Payload: &proto.ThreadNewPayload{
			ID:       threadID,
			Board:    board.ID,
			Author:   actor.Name,
			AuthorID: actor.ID,
			Title:    title,
			TS:       ts,
		},
		TS: ts,
	})
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 2),
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:" + board.ID, "thread:" + threadID},
		Payload: &proto.PostAppendedPayload{
			ID:          postID,
			Thread:      threadID,
			Author:      actor.Name,
			AuthorID:    actor.ID,
			Body:        noticeBody,
			RawBody:     noticeBody,
			ContentType: "markup",
			TS:          ts,
		},
		TS: ts,
	})
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: threadID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideBlessUser(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.BlessUserPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid blessUser payload", false)
	}
	targetRef := strings.TrimSpace(payload.User)
	expectedKey := targetRef
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if targetRef == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "user is required", false)
	}
	message := strings.TrimSpace(payload.Message)
	if len(message) > 500 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "blessing message must be 500 characters or less", false)
	}
	target, err := nativeFindUserRef(e.core.DB, targetRef)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if target == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	if target.ID == actor.ID {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "cannot bless yourself", false)
	}
	ignored, err := nativeRelationshipExists(e.core.DB, target.ID, actor.ID, "ignore")
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if ignored {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "target user ignores you", false)
	}

	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	blessingID := stableCommandLogDecisionID("bless_", record, 0)
	events := []EventAppend{
		{
			ID:     stableCommandLogDecisionID("evt_", record, 0),
			Kind:   proto.EvtUserBlessed,
			Scopes: []string{"user:" + actor.ID, "user:" + target.ID, "blessing:" + blessingID},
			Payload: &proto.UserBlessedPayload{
				ID:         blessingID,
				FromUserID: actor.ID,
				From:       actor.Name,
				ToUserID:   target.ID,
				To:         target.Name,
				Message:    message,
				TS:         ts,
			},
			TS: ts,
		},
	}
	auditEvents, errDetail := nativeBlessingSystemLogEvents(e.core.DB, record, actor, target, blessingID, message, ts, len(events))
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events = append(events, auditEvents...)
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: blessingID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSendMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SendMailPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid sendMail payload", false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionMail, Key: actor.ID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionMail, actor.ID), false)
	}
	return e.decideSendMailPayload(record, actor, payload)
}

func (e *CommandLogNativeDecisionExecutor) decideForwardMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.ForwardMailPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid forwardMail payload", false)
	}
	mailID := strings.TrimSpace(payload.Mail)
	expectedKey := mailID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionMail, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionMail, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if mailID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail is required", false)
	}
	source, err := getMail(e.core.DB, actor.ID, mailID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if source == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found", false)
	}
	subject := strings.TrimSpace(payload.Subject)
	if subject == "" {
		subject = nativeForwardMailSubject(source.Subject)
	}
	return e.decideSendMailPayload(record, actor, proto.SendMailPayload{
		To:        payload.To,
		ToGroups:  payload.ToGroups,
		ToFriends: payload.ToFriends,
		ToAll:     payload.ToAll,
		Subject:   subject,
		Body:      nativeFormatForwardMailBody(source, payload.Note),
		SaveSent:  payload.SaveSent,
	})
}

func (e *CommandLogNativeDecisionExecutor) decidePostMailToBoard(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.PostMailToBoardPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid postMailToBoard payload", false)
	}
	mailID := strings.TrimSpace(payload.Mail)
	boardID := strings.TrimSpace(payload.Board)
	threadID := strings.TrimSpace(payload.Thread)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = threadID
	}
	if expectedKey == "" {
		expectedKey = mailID
	}
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if mailID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail is required", false)
	}
	source, err := getMail(e.core.DB, actor.ID, mailID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if source == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found", false)
	}
	body := nativeFormatMailBoardBody(source, payload.Note)
	if threadID != "" {
		thread, err := getThread(e.core.DB, threadID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if thread == nil {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
		}
		return e.decidePostBoardMailAppend(record, actor, thread, body, "markup", nil)
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	title := strings.TrimSpace(payload.Subject)
	if title == "" {
		title = strings.TrimSpace(source.Subject)
	}
	if title == "" {
		title = "(no subject)"
	}
	threadPayload := proto.CreateThreadPayload{
		Board:       boardID,
		Title:       title,
		Body:        body,
		ContentType: "markup",
	}
	raw, err := json.Marshal(threadPayload)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	transformed := record
	transformed.Payload = raw
	return e.decideCreateThread(ctx, transformed)
}

func (e *CommandLogNativeDecisionExecutor) decideMailPostAuthor(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.MailPostAuthorPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid mailPostAuthor payload", false)
	}
	postID := strings.TrimSpace(payload.Post)
	expectedKey := postID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionPost, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionPost, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if postID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "post is required", false)
	}
	body := strings.TrimSpace(payload.Body)
	if body == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "body is required", false)
	}
	post, err := getPost(e.core.DB, postID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if post.Redacted {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "cannot mail author from a redacted post", false)
	}
	thread, err := getThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	settings, err := getBoardSettings(e.core.DB, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings != nil && settings.MemberReadMode {
		canUse, err := nativeActorCanUseMemberBoard(e.core.DB, actor, thread.Board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !canUse {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board members only", false)
		}
	}
	recipient := strings.TrimSpace(post.AuthorID)
	if recipient == "" {
		recipient = strings.TrimSpace(post.Author)
	}
	if recipient == "" || strings.EqualFold(strings.TrimSpace(post.Author), "anonymous") {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "anonymous article author cannot receive mail", false)
	}
	subject := strings.TrimSpace(payload.Subject)
	if subject == "" {
		subject = "Re: " + thread.Title
	}
	return e.decideSendMailPayload(record, actor, proto.SendMailPayload{
		To:       []string{recipient},
		Subject:  subject,
		Body:     nativeFormatPostAuthorMailBody(thread, post, actor.Name, body),
		SaveSent: payload.SaveSent,
	})
}

func (e *CommandLogNativeDecisionExecutor) decideSendDigestEntryMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SendDigestEntryMailPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid sendDigestEntryMail payload", false)
	}
	entryID := strings.TrimSpace(payload.Entry)
	expectedKey := entryID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionMail, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionMail, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if entryID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "entry is required", false)
	}
	export, err := getDigestExport(e.core.DB, entryID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if export == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "digest entry not found", false)
	}
	settings, err := getBoardSettings(e.core.DB, export.Entry.BoardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings != nil && settings.MemberReadMode {
		canUse, err := nativeActorCanUseMemberBoard(e.core.DB, actor, export.Entry.BoardID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !canUse {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board members only", false)
		}
	}
	subject := strings.TrimSpace(payload.Subject)
	if subject == "" {
		subject = "Archive: " + export.Entry.Title
	}
	body := projections.FormatDigestExportText(export)
	if note := strings.TrimSpace(payload.Note); note != "" {
		body = note + "\n\n" + body
	}
	return e.decideSendMailPayload(record, actor, proto.SendMailPayload{
		To:        payload.To,
		ToGroups:  payload.ToGroups,
		ToFriends: payload.ToFriends,
		ToAll:     payload.ToAll,
		Subject:   subject,
		Body:      body,
		SaveSent:  payload.SaveSent,
	})
}

func (e *CommandLogNativeDecisionExecutor) decideCuratePost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.CuratePostPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid curatePost payload", false)
	}
	postID := strings.TrimSpace(payload.Post)
	expectedKey := postID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionPost, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionPost, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if postID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "post is required", false)
	}
	post, err := getPost(e.core.DB, postID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if post.Redacted {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "cannot curate a redacted post", false)
	}
	thread, err := getThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	return e.decideDigestCuration(record, actor, thread, post, "post", post.ID, payload.Kind, payload.Title, payload.Path, payload.Note)
}

func (e *CommandLogNativeDecisionExecutor) decideCurateThread(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.CurateThreadPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid curateThread payload", false)
	}
	threadID := strings.TrimSpace(payload.Thread)
	expectedKey := threadID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionThread, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionThread, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if threadID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "thread is required", false)
	}
	thread, err := getThread(e.core.DB, threadID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	return e.decideDigestCuration(record, actor, thread, nil, "thread", thread.ID, payload.Kind, payload.Title, payload.Path, payload.Note)
}

func (e *CommandLogNativeDecisionExecutor) decideDigestCuration(record CommandLogRecord, actor *User, thread *Thread, post *Post, targetKind, targetID, rawKind, rawTitle, rawPath, rawNote string) (nativeCommandDecision, *proto.ErrorDetail) {
	if actor == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	kind, errDetail := nativeNormalizeDigestKind(rawKind)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	canCurate, err := nativeActorCanCurateBoardKind(e.core.DB, actor, thread.Board, kind)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canCurate {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, nativeDigestCurationPermissionMessage(kind), false)
	}
	title := strings.TrimSpace(rawTitle)
	if title == "" {
		if targetKind == "post" {
			if post == nil {
				return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
			}
			title = fmt.Sprintf("%s #%d", thread.Title, post.CreatedSeq)
		} else {
			title = thread.Title
		}
	}
	path := nativeNormalizeDigestPath(rawPath)
	note := strings.TrimSpace(rawNote)
	entryID, err := nativeDigestEntryID(e.core.DB, thread.Board, targetKind, targetID, kind, path)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if entryID == "" {
		entryID = stableCommandLogDecisionID("dig_", record, 0)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	eventPayload := &proto.DigestEntryUpsertedPayload{
		ID:         entryID,
		Board:      thread.Board,
		TargetKind: targetKind,
		TargetID:   targetID,
		Kind:       kind,
		Title:      title,
		Path:       path,
		Note:       note,
		CreatedBy:  actor.ID,
		TS:         ts,
	}
	events := []EventAppend{{
		ID:      stableCommandLogDecisionID("evt_", record, 0),
		Kind:    proto.EvtDigestEntryUpserted,
		Scopes:  nativeDigestEventScopes(thread.Board),
		Payload: eventPayload,
		TS:      ts,
	}}
	if mirror, ok := nativeDigestMirrorForKind(kind); ok {
		export, errDetail := nativeDigestExportForCuration(e.core.DB, eventPayload, thread, post)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		mirrorEvents, errDetail := nativeDigestMirrorSystemLogEvents(e.core.DB, record, actor, entryID, export, mirror, ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, mirrorEvents...)
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: entryID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideRemoveDigestEntry(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.RemoveDigestEntryPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid removeDigestEntry payload", false)
	}
	entryID := strings.TrimSpace(payload.Entry)
	if entryID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "entry is required", false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	entry, errDetail := nativeDigestEntryForCuration(e.core.DB, actor, entryID)
	if errDetail != nil {
		if errDetail.Code != proto.ErrNotFound {
			return nativeCommandDecision{}, errDetail
		}
		removed, found, err := nativeDigestEntryRemoval(e.core.DB, entryID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !found || (removed.RemovedBy != "" && removed.RemovedBy != actor.ID) {
			return nativeCommandDecision{}, errDetail
		}
		entry = nativeDigestEntryForCommand{
			ID:      removed.ID,
			BoardID: removed.BoardID,
			Kind:    removed.Kind,
		}
	}
	if errDetail := nativeValidateDigestEntryCommandPartition(record, entry); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtDigestEntryRemoved,
		Scopes: nativeDigestEventScopes(entry.BoardID),
		Payload: &proto.DigestEntryRemovedPayload{
			ID:    entry.ID,
			Board: entry.BoardID,
			Kind:  entry.Kind,
			By:    actor.ID,
			TS:    ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: entry.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideUpdateDigestEntry(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.UpdateDigestEntryPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid updateDigestEntry payload", false)
	}
	entryID := strings.TrimSpace(payload.Entry)
	if entryID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "entry is required", false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	entry, errDetail := nativeDigestEntryForCuration(e.core.DB, actor, entryID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := nativeValidateDigestEntryCommandPartition(record, entry); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	title := entry.Title
	if payload.Title != nil {
		title = strings.TrimSpace(*payload.Title)
		if title == "" {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "title is required", false)
		}
	}
	path := entry.Path
	if payload.Path != nil {
		path = nativeNormalizeDigestPath(*payload.Path)
	}
	note := entry.Note
	if payload.Note != nil {
		note = strings.TrimSpace(*payload.Note)
	}
	if path != entry.Path {
		conflict, err := nativeDigestEntryPathConflict(e.core.DB, entry, path)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if conflict {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "digest entry already exists at that path", false)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtDigestEntryUpdated,
		Scopes: nativeDigestEventScopes(entry.BoardID),
		Payload: &proto.DigestEntryUpdatedPayload{
			ID:         entry.ID,
			Board:      entry.BoardID,
			TargetKind: entry.TargetKind,
			TargetID:   entry.TargetID,
			Kind:       entry.Kind,
			Title:      title,
			Path:       path,
			Note:       note,
			By:         actor.ID,
			TS:         ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: entry.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetDigestEntryBody(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetDigestEntryBodyPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setDigestEntryBody payload", false)
	}
	entryID := strings.TrimSpace(payload.Entry)
	if entryID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "entry is required", false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	entry, errDetail := nativeDigestEntryForCuration(e.core.DB, actor, entryID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := nativeValidateDigestEntryCommandPartition(record, entry); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	body := payload.Body
	edited := !payload.Reset
	if payload.Reset {
		body = ""
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtDigestEntryBodySet,
		Scopes: nativeDigestEventScopes(entry.BoardID),
		Payload: &proto.DigestEntryBodySetPayload{
			ID:     entry.ID,
			Board:  entry.BoardID,
			Kind:   entry.Kind,
			Body:   body,
			Edited: edited,
			By:     actor.ID,
			TS:     ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: entry.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideCreateDigestDirectory(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.CreateDigestDirectoryPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid createDigestDirectory payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	settings, err := getBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	kind := strings.TrimSpace(payload.Kind)
	if kind == "" {
		kind = "archive"
	}
	kind, errDetail = nativeNormalizeDigestKind(kind)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	canCurate, err := nativeActorCanCurateBoardKind(e.core.DB, actor, boardID, kind)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canCurate {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, nativeDigestCurationPermissionMessage(kind), false)
	}
	path := nativeNormalizeDigestPath(payload.Path)
	if path == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "source path is required", false)
	}
	directoryID, err := nativeDigestDirectoryID(e.core.DB, boardID, kind, path)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if directoryID == "" {
		directoryID = stableCommandLogDecisionID("dir_", record, 0)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtDigestDirectorySet,
		Scopes: nativeDigestEventScopes(boardID),
		Payload: &proto.DigestDirectorySetPayload{
			ID:        directoryID,
			Board:     boardID,
			Kind:      kind,
			Path:      path,
			CreatedBy: actor.ID,
			TS:        ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: directoryID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideMoveDigestPath(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.MoveDigestPathPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid moveDigestPath payload", false)
	}
	boardID, kind, errDetail := e.nativeDigestPathMutationBoardKind(record, payload.Board, payload.Kind)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := e.nativeRequireDigestPathMutation(actor, boardID, kind); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	fromPath := nativeNormalizeDigestPath(payload.FromPath)
	if fromPath == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "source path is required", false)
	}
	toPath := nativeNormalizeDigestPath(payload.ToPath)
	if fromPath == toPath {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "destination path must differ from source path", false)
	}
	if toPath != "" && (toPath == fromPath || strings.HasPrefix(toPath, fromPath+"/")) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "cannot move an archive path into itself", false)
	}
	eventID := stableCommandLogDecisionID("evt_", record, 0)
	count, found, err := nativeDigestPathMutationCount(e.core.DB, eventID, "move")
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		entries, err := nativeDigestPathEntriesForCopy(e.core.DB, record, boardID, kind, fromPath)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		dirs, err := nativeDigestPathDirectoriesForCopy(e.core.DB, record, boardID, kind, fromPath)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		movingEntryIDs := make(map[string]struct{}, len(entries))
		for _, entry := range entries {
			movingEntryIDs[entry.ID] = struct{}{}
		}
		movingDirectoryIDs := make(map[string]struct{}, len(dirs))
		for _, dir := range dirs {
			movingDirectoryIDs[dir.ID] = struct{}{}
		}
		for _, entry := range entries {
			conflict, err := nativeDigestPathMoveEntryConflict(e.core.DB, entry, nativeRemapDigestPath(entry.Path, fromPath, toPath), movingEntryIDs)
			if err != nil {
				return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
			}
			if conflict {
				return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "digest path move would overwrite an existing entry", false)
			}
		}
		for _, dir := range dirs {
			conflict, err := nativeDigestPathMoveDirectoryConflict(e.core.DB, dir, nativeRemapDigestPath(dir.Path, fromPath, toPath), movingDirectoryIDs)
			if err != nil {
				return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
			}
			if conflict {
				return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "digest path move would overwrite an existing entry", false)
			}
		}
		count = len(entries) + len(dirs)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     eventID,
		Kind:   proto.EvtDigestPathMoved,
		Scopes: nativeDigestEventScopes(boardID),
		Payload: &proto.DigestPathMovedPayload{
			Board:    boardID,
			Kind:     kind,
			FromPath: fromPath,
			ToPath:   toPath,
			Count:    count,
			By:       actor.ID,
			TS:       ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%s:%s:%d", boardID, kind, count)}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideCopyDigestPath(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.CopyDigestPathPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid copyDigestPath payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	settings, err := getBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	kind := strings.TrimSpace(payload.Kind)
	if kind == "" {
		kind = "archive"
	}
	kind, errDetail = nativeNormalizeDigestKind(kind)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	canCurate, err := nativeActorCanCurateBoardKind(e.core.DB, actor, boardID, kind)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canCurate {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, nativeDigestCurationPermissionMessage(kind), false)
	}
	fromPath := nativeNormalizeDigestPath(payload.FromPath)
	if fromPath == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "source path is required", false)
	}
	toPath := nativeNormalizeDigestPath(payload.ToPath)
	if fromPath == toPath {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "destination path must differ from source path", false)
	}

	entries, err := nativeDigestPathEntriesForCopy(e.core.DB, record, boardID, kind, fromPath)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	dirs, err := nativeDigestPathDirectoriesForCopy(e.core.DB, record, boardID, kind, fromPath)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	entryIDs := make([]string, len(entries))
	for i, entry := range entries {
		entryIDs[i] = stableCommandLogDecisionID("dig_", record, i)
		conflict, err := nativeDigestPathCopyEntryConflict(e.core.DB, entry, nativeRemapDigestPath(entry.Path, fromPath, toPath), entryIDs[i])
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if conflict {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "digest path copy would overwrite an existing entry", false)
		}
	}
	directoryIDs := make([]string, len(dirs))
	for i, dir := range dirs {
		directoryIDs[i] = stableCommandLogDecisionID("dir_", record, i)
		conflict, err := nativeDigestPathCopyDirectoryConflict(e.core.DB, dir, nativeRemapDigestPath(dir.Path, fromPath, toPath), directoryIDs[i])
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if conflict {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "digest path copy would overwrite an existing entry", false)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	count := len(entries) + len(dirs)
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtDigestPathCopied,
		Scopes: nativeDigestEventScopes(boardID),
		Payload: &proto.DigestPathCopiedPayload{
			Board:        boardID,
			Kind:         kind,
			FromPath:     fromPath,
			ToPath:       toPath,
			EntryIDs:     entryIDs,
			DirectoryIDs: directoryIDs,
			Count:        count,
			CreatedBy:    actor.ID,
			TS:           ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%s:%s:%d", boardID, kind, count)}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteDigestPath(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.DeleteDigestPathPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid deleteDigestPath payload", false)
	}
	boardID, kind, errDetail := e.nativeDigestPathMutationBoardKind(record, payload.Board, payload.Kind)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := e.nativeRequireDigestPathMutation(actor, boardID, kind); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	path := nativeNormalizeDigestPath(payload.Path)
	if path == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "source path is required", false)
	}
	eventID := stableCommandLogDecisionID("evt_", record, 0)
	count, found, err := nativeDigestPathMutationCount(e.core.DB, eventID, "delete")
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		entries, err := nativeDigestPathEntriesForCopy(e.core.DB, record, boardID, kind, path)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		dirs, err := nativeDigestPathDirectoriesForCopy(e.core.DB, record, boardID, kind, path)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		count = len(entries) + len(dirs)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     eventID,
		Kind:   proto.EvtDigestPathDeleted,
		Scopes: nativeDigestEventScopes(boardID),
		Payload: &proto.DigestPathDeletedPayload{
			Board: boardID,
			Kind:  kind,
			Path:  path,
			Count: count,
			By:    actor.ID,
			TS:    ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%s:%s:%d", boardID, kind, count)}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetMailGroup(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetMailGroupPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setMailGroup payload", false)
	}
	groupRef := strings.TrimSpace(payload.Group)
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "name is required", false)
	}
	if len(name) > 80 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail group name is too long", false)
	}
	if len(payload.Members) > 200 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail group may contain at most 200 members", false)
	}
	expectedKey := groupRef
	if expectedKey == "" {
		expectedKey = strings.TrimSpace(record.ActorID)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionMail, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionMail, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	groupID, errDetail := nativeResolveMailGroupID(e.core.DB, actor.ID, groupRef, name, record)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	conflictID, err := nativeMailGroupIDByName(e.core.DB, actor.ID, name)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if conflictID != "" && conflictID != groupID {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail group name already exists", false)
	}
	memberIDs, errDetail := nativeResolveUniqueMailGroupMembers(e.core.DB, payload.Members, actor.ID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtMailGroupSet,
		Scopes: []string{"account:" + actor.ID, "mail:" + groupID},
		Payload: &proto.MailGroupSetPayload{
			ID:        groupID,
			OwnerID:   actor.ID,
			Name:      name,
			MemberIDs: memberIDs,
			TS:        ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: groupID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteMailGroup(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.DeleteMailGroupPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid deleteMailGroup payload", false)
	}
	groupRef := strings.TrimSpace(payload.Group)
	if groupRef == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "group is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionMail, Key: groupRef}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionMail, groupRef), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	eventID := stableCommandLogDecisionID("evt_", record, 0)
	groupID, found, err := nativeMailGroupDeletion(e.core.DB, eventID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		groupID, err = projections.GetMailGroupID(e.core.DB, actor.ID, groupRef)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if groupID == "" {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail group not found", false)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     eventID,
		Kind:   proto.EvtMailGroupDeleted,
		Scopes: []string{"account:" + actor.ID, "mail:" + groupID},
		Payload: &proto.MailGroupDeletedPayload{
			ID:      groupID,
			OwnerID: actor.ID,
			TS:      ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: groupID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideAttachMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.AttachMailPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid attachMail payload", false)
	}
	mailID := strings.TrimSpace(payload.Mail)
	filename := strings.TrimSpace(payload.Filename)
	contentType := strings.TrimSpace(payload.ContentType)
	if mailID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail is required", false)
	}
	if filename == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "attachment filename is required", false)
	}
	if len(filename) > 160 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "attachment filename must be 160 characters or less", false)
	}
	if len(contentType) > 120 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "attachment content type must be 120 characters or less", false)
	}
	if payload.SizeBytes < 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "attachment size cannot be negative", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionMail, Key: mailID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionMail, mailID), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	fromUserID, found, err := nativeMailSender(e.core.DB, mailID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found", false)
	}
	if fromUserID != actor.ID {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "only the sender can attach files to this mail", false)
	}
	var count int
	if err := qQueryRow(e.core.DB, `SELECT COUNT(*) FROM mail_attachments WHERE message_id=?`, mailID).Scan(&count); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if count >= 8 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail can have at most 8 attachments", false)
	}
	copyCounts, err := nativeActiveMailCopyCounts(e.core.DB, mailID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	for userID, copies := range copyCounts {
		if copies <= 0 {
			continue
		}
		ok, err := nativeMailQuotaAllows(e.core.DB, userID, payload.SizeBytes*int64(copies))
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !ok {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail quota exceeded for user "+userID, false)
		}
	}
	attachmentID := strings.TrimSpace(payload.ID)
	if attachmentID == "" {
		attachmentID = stableCommandLogDecisionID("matt_", record, 0)
	}
	stagedBlobID := strings.TrimSpace(payload.StagedBlobID)
	if errDetail := nativeValidateStagedMailAttachmentBlob(e.core.DB, stagedBlobID, attachmentID, payload.SizeBytes, contentType); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	scopes, err := nativeMailAccountScopes(e.core.DB, mailID, actor.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtMailAttachmentAdded,
		Scopes: scopes,
		Payload: &proto.MailAttachmentAddedPayload{
			ID:           attachmentID,
			Mail:         mailID,
			Filename:     filename,
			ContentType:  contentType,
			SizeBytes:    payload.SizeBytes,
			AuthorID:     actor.ID,
			Author:       actor.Name,
			StagedBlobID: stagedBlobID,
			TS:           ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: attachmentID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideUpdateMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.UpdateMailPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid updateMail payload", false)
	}
	mailID := strings.TrimSpace(payload.Mail)
	if mailID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionMail, Key: mailID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionMail, mailID), false)
	}
	var mailbox *string
	if payload.Mailbox != nil {
		normalized, errDetail := nativeNormalizeMailbox(*payload.Mailbox)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		mailbox = &normalized
	}
	if mailbox == nil && payload.Read == nil && payload.Kept == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mailbox, read, or kept is required", false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	target, found, err := nativeMailCopyUpdateTarget(e.core.DB, actor.ID, mailID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found", false)
	}
	if mailbox != nil && *mailbox != "trash" && target.trashedCopies > 0 {
		size, err := nativeMailStoredSize(e.core.DB, mailID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		ok, err := nativeMailQuotaAllows(e.core.DB, actor.ID, size*int64(target.trashedCopies))
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !ok {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail quota exceeded for user "+actor.ID, false)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	scopes := []string{"account:" + target.fromUserID}
	if actor.ID != target.fromUserID {
		scopes = append(scopes, "account:"+actor.ID)
	}
	scopes = append(scopes, "mail:"+mailID)
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtMailCopyUpdated,
		Scopes: scopes,
		Payload: &proto.MailCopyUpdatedPayload{
			Mail:    mailID,
			UserID:  actor.ID,
			Mailbox: mailbox,
			Read:    payload.Read,
			Kept:    payload.Kept,
			TS:      ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: mailID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.DeleteMailPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid deleteMail payload", false)
	}
	mailID := strings.TrimSpace(payload.Mail)
	if mailID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionMail, Key: mailID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionMail, mailID), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	target, found, err := nativeMailCopyUpdateTarget(e.core.DB, actor.ID, mailID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	scopes := []string{"account:" + target.fromUserID}
	if actor.ID != target.fromUserID {
		scopes = append(scopes, "account:"+actor.ID)
	}
	scopes = append(scopes, "mail:"+mailID)
	mailbox := "trash"
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtMailCopyUpdated,
		Scopes: scopes,
		Payload: &proto.MailCopyUpdatedPayload{
			Mail:    mailID,
			UserID:  actor.ID,
			Mailbox: &mailbox,
			TS:      ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: mailID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteMailRange(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.DeleteMailRangePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid deleteMailRange payload", false)
	}
	mailIDs, errDetail := nativeNormalizeMailRangeIDs(payload.Mail)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionMail, Key: mailIDs[0]}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionMail, mailIDs[0]), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	targets := make([]nativeMailCopyUpdateState, 0, len(mailIDs))
	for _, mailID := range mailIDs {
		target, found, err := nativeMailCopyUpdateTarget(e.core.DB, actor.ID, mailID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !found {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found: "+mailID, false)
		}
		targets = append(targets, target)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := make([]EventAppend, 0, len(mailIDs))
	for i, mailID := range mailIDs {
		target := targets[i]
		scopes := []string{"account:" + target.fromUserID}
		if actor.ID != target.fromUserID {
			scopes = append(scopes, "account:"+actor.ID)
		}
		scopes = append(scopes, "mail:"+mailID)
		mailbox := "trash"
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, i),
			Kind:   proto.EvtMailCopyUpdated,
			Scopes: scopes,
			Payload: &proto.MailCopyUpdatedPayload{
				Mail:    mailID,
				UserID:  actor.ID,
				Mailbox: &mailbox,
				TS:      ts,
			},
			TS: ts,
		})
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%d", len(mailIDs))}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSendMailPayload(record CommandLogRecord, actor *User, payload proto.SendMailPayload) (nativeCommandDecision, *proto.ErrorDetail) {
	recipientRefs, errDetail := nativeExpandMailRecipients(e.core.DB, actor, payload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if len(recipientRefs) == 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "at least one recipient is required", false)
	}
	body := strings.TrimSpace(payload.Body)
	if body == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "body is required", false)
	}
	subject := strings.TrimSpace(payload.Subject)
	if subject == "" {
		subject = "(no subject)"
	}
	attachments, errDetail := nativeNormalizeMailAttachments(record, payload.Attachments)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	saveSent := true
	if payload.SaveSent != nil {
		saveSent = *payload.SaveSent
	}
	recipients := []*User{}
	seen := map[string]bool{}
	for _, ref := range recipientRefs {
		target, err := nativeFindUserRef(e.core.DB, ref)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if target == nil {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "recipient not found: "+strings.TrimSpace(ref), false)
		}
		if target.ID != actor.ID && !payload.ToAll {
			ignored, err := nativeRelationshipExists(e.core.DB, target.ID, actor.ID, "ignore")
			if err != nil {
				return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
			}
			if ignored {
				return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "recipient does not accept mail from this user", false)
			}
		}
		if !seen[target.ID] {
			seen[target.ID] = true
			recipients = append(recipients, target)
		}
	}
	if len(recipients) == 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "at least one recipient is required", false)
	}
	addedBytes := nativeMailMessageSize(subject, body, attachments)
	copyCounts := map[string]int{}
	for _, recipient := range recipients {
		copyCounts[recipient.ID]++
	}
	if saveSent {
		copyCounts[actor.ID]++
	}
	for userID, copies := range copyCounts {
		if copies <= 0 {
			continue
		}
		ok, err := nativeMailQuotaAllows(e.core.DB, userID, addedBytes*int64(copies))
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !ok {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "mail quota exceeded for user "+userID, false)
		}
	}
	parentID := strings.TrimSpace(payload.ReplyTo)
	if parentID != "" {
		ok, err := nativeActorHasMailCopy(e.core.DB, actor.ID, parentID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !ok {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "reply target not found", false)
		}
	}

	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	mailID := stableCommandLogDecisionID("mail_", record, 0)
	toIDs := make([]string, 0, len(recipients))
	toNames := make([]string, 0, len(recipients))
	scopes := []string{"account:" + actor.ID}
	for _, recipient := range recipients {
		toIDs = append(toIDs, recipient.ID)
		toNames = append(toNames, recipient.Name)
		if recipient.ID != actor.ID {
			scopes = append(scopes, "account:"+recipient.ID)
		}
	}
	events := []EventAppend{
		{
			ID:     stableCommandLogDecisionID("evt_", record, 0),
			Kind:   proto.EvtMailSent,
			Scopes: scopes,
			Payload: &proto.MailSentPayload{
				ID:          mailID,
				FromUserID:  actor.ID,
				From:        actor.Name,
				ToUserIDs:   toIDs,
				To:          toNames,
				Subject:     subject,
				Body:        body,
				ParentID:    parentID,
				SaveSent:    saveSent,
				Attachments: attachments,
				TS:          ts,
			},
			TS: ts,
		},
	}
	if payload.ToAll {
		sysmailEvents, errDetail := nativeSysmailSystemLogEvents(e.core.DB, record, actor, mailID, subject, body, len(toNames), ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, sysmailEvents...)
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: mailID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSendDirectMessage(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SendDirectMessagePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid sendDirectMessage payload", false)
	}
	targetRef := strings.TrimSpace(payload.To)
	expectedKey := targetRef
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: expectedKey}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, expectedKey), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	body := strings.TrimSpace(payload.Body)
	if targetRef == "" || body == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "to and body are required", false)
	}
	target, err := nativeFindUserRef(e.core.DB, targetRef)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if target == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "recipient not found", false)
	}
	if target.ID != actor.ID {
		ignored, err := nativeRelationshipExists(e.core.DB, target.ID, actor.ID, "ignore")
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if ignored {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "recipient does not accept messages from this user", false)
		}
		allowed, err := nativeDirectMessageAllowed(e.core.DB, target.ID, actor.ID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !allowed {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "recipient only accepts messages from friends", false)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	messageID := stableCommandLogDecisionID("dm_", record, 0)
	conversationID := nativeDirectConversationID(actor.ID, target.ID)
	scopes := []string{"account:" + actor.ID}
	if target.ID != actor.ID {
		scopes = append(scopes, "account:"+target.ID)
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtDirectMessageSent,
		Scopes: scopes,
		Payload: &proto.DirectMessageSentPayload{
			ID:             messageID,
			ConversationID: conversationID,
			FromUserID:     actor.ID,
			From:           actor.Name,
			ToUserID:       target.ID,
			To:             target.Name,
			Body:           body,
			TS:             ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: messageID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideMarkDirectMessageRead(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.MarkDirectMessageReadPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid markDirectMessageRead payload", false)
	}
	messageID := strings.TrimSpace(payload.Message)
	if messageID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "message is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: messageID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, messageID), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	message, found, err := nativeDirectMessageReadTarget(e.core.DB, messageID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found || message.toUserID != actor.ID || message.recipientDeleted {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "message not found", false)
	}
	if message.readAt > 0 {
		return nativeCommandDecision{reply: Reply{Result: &proto.AckResult{ID: messageID}}}, nil
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	scopes := []string{"account:" + message.fromUserID}
	if message.toUserID != message.fromUserID {
		scopes = append(scopes, "account:"+message.toUserID)
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtDirectMessageRead,
		Scopes: scopes,
		Payload: &proto.DirectMessageReadPayload{
			MessageID: messageID,
			UserID:    actor.ID,
			ReadAt:    ts,
			TS:        ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: messageID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteDirectMessage(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.DeleteDirectMessagePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid deleteDirectMessage payload", false)
	}
	messageID := strings.TrimSpace(payload.Message)
	if messageID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "message is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: messageID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, messageID), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	message, found, err := nativeDirectMessageDeleteTarget(e.core.DB, messageID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	deleteSenderCopy := found && message.fromUserID == actor.ID
	deleteRecipientCopy := found && message.toUserID == actor.ID
	if !deleteSenderCopy && !deleteRecipientCopy {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "message not found", false)
	}
	if (!deleteSenderCopy || message.senderDeleted) && (!deleteRecipientCopy || message.recipientDeleted) {
		return nativeCommandDecision{reply: Reply{Result: &proto.AckResult{ID: messageID}}}, nil
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	scopes := []string{"account:" + message.fromUserID}
	if message.toUserID != message.fromUserID {
		scopes = append(scopes, "account:"+message.toUserID)
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtDirectMessageDeleted,
		Scopes: scopes,
		Payload: &proto.DirectMessageDeletedPayload{
			MessageID:        messageID,
			UserID:           actor.ID,
			SenderDeleted:    deleteSenderCopy,
			RecipientDeleted: deleteRecipientCopy,
			TS:               ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: messageID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetDirectMessageSettings(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetDirectMessageSettingsPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setDirectMessageSettings payload", false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: actor.ID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, actor.ID), false)
	}
	policy, errDetail := nativeNormalizeDirectMessagePolicy(payload.Policy)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtDirectMessageSettingsSet,
		Scopes: []string{"user:" + actor.ID},
		Payload: &proto.DirectMessageSettingsSetPayload{
			UserID: actor.ID,
			Policy: policy,
			TS:     ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: actor.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetUserRelationship(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetUserRelationshipPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setUserRelationship payload", false)
	}
	targetRef := strings.TrimSpace(payload.User)
	if targetRef == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "user is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: targetRef}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, targetRef), false)
	}
	kind := nativeNormalizeRelationshipKind(payload.Kind)
	if kind == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, `kind must be "friend" or "ignore"`, false)
	}
	note := strings.TrimSpace(payload.Note)
	if len(note) > 160 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "note is too long", false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	target, err := nativeFindUserRef(e.core.DB, targetRef)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if target == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	if target.ID == actor.ID {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "cannot create a relationship with yourself", false)
	}
	if !payload.Active {
		exists, err := nativeRelationshipExists(e.core.DB, actor.ID, target.ID, kind)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			return nativeCommandDecision{reply: Reply{Result: &proto.AckResult{ID: target.ID}}}, nil
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtUserRelationshipSet,
		Scopes: []string{"user:" + actor.ID, "user:" + target.ID},
		Payload: &proto.UserRelationshipSetPayload{
			UserID:       actor.ID,
			TargetUserID: target.ID,
			Kind:         kind,
			Active:       payload.Active,
			Note:         note,
			TS:           ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: target.ID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetLoginWatch(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetLoginWatchPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setLoginWatch payload", false)
	}
	targetRef := strings.TrimSpace(payload.User)
	if targetRef == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "user is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: targetRef}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, targetRef), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	target, err := nativeFindUserRef(e.core.DB, targetRef)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if target == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	if target.ID == actor.ID {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "cannot wait for yourself", false)
	}
	relationshipActive := payload.Active
	online := false
	if payload.Active {
		friend, err := nativeRelationshipExists(e.core.DB, actor.ID, target.ID, "friend")
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !friend {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "friend relationship required", false)
		}
		online, err = nativeUserRecentlyOnline(e.core.DB, target.ID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if online {
			relationshipActive = false
		}
	} else {
		exists, err := nativeRelationshipExists(e.core.DB, actor.ID, target.ID, "login_watch")
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			return nativeCommandDecision{reply: Reply{Result: &proto.AckResult{ID: target.ID}}}, nil
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	events := make([]EventAppend, 0, 2)
	if online {
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, 0),
			Kind:   proto.EvtNotificationCreated,
			Scopes: []string{"user:" + actor.ID},
			Payload: &proto.NotificationCreatedPayload{
				ID:     stableCommandLogDecisionID("notif_", record, 0),
				UserID: actor.ID,
				Kind:   "login",
				Actor:  target.Name,
				TS:     ts,
			},
			TS: ts,
		})
	}
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, len(events)),
		Kind:   proto.EvtUserRelationshipSet,
		Scopes: []string{"user:" + actor.ID, "user:" + target.ID},
		Payload: &proto.UserRelationshipSetPayload{
			UserID:       actor.ID,
			TargetUserID: target.ID,
			Kind:         "login_watch",
			Active:       relationshipActive,
			TS:           ts,
		},
		TS: ts,
	})
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: target.ID}},
		events: events,
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardFavorite(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetBoardFavoritePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setBoardFavorite payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: boardID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, boardID), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	settings, err := getBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	folderID := strings.TrimSpace(payload.FolderID)
	if payload.Favorite {
		exists, err := nativeFavoriteFolderExists(e.core.DB, actor.ID, folderID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardFavoriteSet,
		Scopes: []string{"board:" + boardID, "user:" + actor.ID},
		Payload: &proto.BoardFavoriteSetPayload{
			UserID:   actor.ID,
			Board:    boardID,
			Favorite: payload.Favorite,
			FolderID: folderID,
			Position: payload.Position,
			TS:       ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: boardID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideCreateFavoriteFolder(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.CreateFavoriteFolderPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid createFavoriteFolder payload", false)
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "name is required", false)
	}
	if len(name) > 80 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "folder name must be 80 characters or less", false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: actor.ID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, actor.ID), false)
	}
	parentID := strings.TrimSpace(payload.ParentID)
	exists, err := nativeFavoriteFolderExists(e.core.DB, actor.ID, parentID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
	}
	position, err := nativeFavoriteFolderTargetPosition(e.core.DB, actor.ID, parentID, payload.Position)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	folderID := stableCommandLogDecisionID("favfld_", record, 0)
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtFavoriteFolderCreated,
		Scopes: []string{"user:" + actor.ID},
		Payload: &proto.FavoriteFolderCreatedPayload{
			ID:       folderID,
			UserID:   actor.ID,
			ParentID: parentID,
			Name:     name,
			Position: position,
			TS:       ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: folderID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideUpdateFavoriteFolder(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.UpdateFavoriteFolderPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid updateFavoriteFolder payload", false)
	}
	folderID := strings.TrimSpace(payload.Folder)
	if folderID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "folder is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: folderID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, folderID), false)
	}
	name := strings.TrimSpace(payload.Name)
	if len(name) > 80 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "folder name must be 80 characters or less", false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	current, found, err := nativeFavoriteFolderState(e.core.DB, actor.ID, folderID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
	}
	if name == "" {
		name = current.name
	}
	nextParent := current.parentID
	if payload.ParentID != nil {
		nextParent = strings.TrimSpace(*payload.ParentID)
		if nextParent == folderID {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "folder cannot be its own parent", false)
		}
		exists, err := nativeFavoriteFolderExists(e.core.DB, actor.ID, nextParent)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
		}
		contains, err := nativeFavoriteFolderContains(e.core.DB, actor.ID, folderID, nextParent)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if contains {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "folder cannot move under its descendant", false)
		}
	}
	targetPosition := current.position
	if payload.Position != nil {
		if *payload.Position < 0 {
			targetPosition = 0
		} else {
			targetPosition = *payload.Position
		}
	} else if nextParent != current.parentID {
		targetPosition, err = nativeFavoriteFolderTargetPosition(e.core.DB, actor.ID, nextParent, nil)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtFavoriteFolderUpdated,
		Scopes: []string{"user:" + actor.ID},
		Payload: &proto.FavoriteFolderUpdatedPayload{
			ID:       folderID,
			UserID:   actor.ID,
			ParentID: nextParent,
			Name:     name,
			Position: targetPosition,
			TS:       ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: folderID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteFavoriteFolder(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.DeleteFavoriteFolderPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid deleteFavoriteFolder payload", false)
	}
	folderID := strings.TrimSpace(payload.Folder)
	if folderID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "folder is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: folderID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, folderID), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	current, found, err := nativeFavoriteFolderState(e.core.DB, actor.ID, folderID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtFavoriteFolderDeleted,
		Scopes: []string{"user:" + actor.ID},
		Payload: &proto.FavoriteFolderDeletedPayload{
			ID:       folderID,
			UserID:   actor.ID,
			ParentID: current.parentID,
			TS:       ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: folderID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideMoveBoardFavorite(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.MoveBoardFavoritePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid moveBoardFavorite payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: boardID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, boardID), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	settings, err := getBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	folderID := strings.TrimSpace(payload.FolderID)
	exists, err := nativeFavoriteFolderExists(e.core.DB, actor.ID, folderID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardFavoriteSet,
		Scopes: []string{"board:" + boardID, "user:" + actor.ID},
		Payload: &proto.BoardFavoriteSetPayload{
			UserID:   actor.ID,
			Board:    boardID,
			Favorite: true,
			FolderID: folderID,
			Position: payload.Position,
			TS:       ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: boardID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideImportFavoriteTree(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.ImportFavoriteTreePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid importFavoriteTree payload", false)
	}
	if len(payload.Folders) > 200 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "favorite import supports at most 200 folders", false)
	}
	if len(payload.Boards) > 500 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "favorite import supports at most 500 boards", false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionUser, Key: actor.ID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionUser, actor.ID), false)
	}

	type importFolder struct {
		sourceID string
		finalID  string
		parentID string
		name     string
		position int
	}
	folderIDMap := map[string]string{}
	sourceFolderIDs := map[string]bool{}
	folders := make([]importFolder, 0, len(payload.Folders))
	for i, folder := range payload.Folders {
		sourceID := strings.TrimSpace(folder.ID)
		if sourceID == "" {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("folder %d is missing id", i+1), false)
		}
		if sourceFolderIDs[sourceID] {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("duplicate folder id %q", sourceID), false)
		}
		name := strings.TrimSpace(folder.Name)
		if name == "" {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("folder %q is missing name", sourceID), false)
		}
		if len(name) > 80 {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("folder %q name must be 80 characters or less", sourceID), false)
		}
		sourceFolderIDs[sourceID] = true
		finalID := stableCommandLogDecisionID("favfld_", record, i)
		folderIDMap[sourceID] = finalID
		folders = append(folders, importFolder{
			sourceID: sourceID,
			finalID:  finalID,
			parentID: strings.TrimSpace(folder.ParentID),
			name:     name,
			position: folder.Position,
		})
	}
	for _, folder := range folders {
		if folder.parentID != "" && !sourceFolderIDs[folder.parentID] {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("folder %q references missing parent %q", folder.sourceID, folder.parentID), false)
		}
	}
	ordered := map[string]bool{}
	remaining := append([]importFolder(nil), folders...)
	for len(remaining) > 0 {
		progressed := false
		next := remaining[:0]
		for _, folder := range remaining {
			if folder.parentID != "" && !ordered[folder.parentID] {
				next = append(next, folder)
				continue
			}
			ordered[folder.sourceID] = true
			progressed = true
		}
		if !progressed {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "favorite folder import contains a cycle", false)
		}
		remaining = next
	}

	eventFolders := make([]proto.FavoriteTreeImportedFolderPayload, 0, len(folders))
	for _, folder := range folders {
		parentID := ""
		if folder.parentID != "" {
			parentID = folderIDMap[folder.parentID]
		}
		eventFolders = append(eventFolders, proto.FavoriteTreeImportedFolderPayload{
			ID:       folder.finalID,
			ParentID: parentID,
			Name:     folder.name,
			Position: folder.position,
		})
	}

	seenBoards := map[string]bool{}
	eventBoards := make([]proto.FavoriteTreeImportedBoardPayload, 0, len(payload.Boards))
	for _, board := range payload.Boards {
		boardID := strings.TrimSpace(board.ID)
		if boardID == "" {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "favorite board is missing id", false)
		}
		sourceFolderID := strings.TrimSpace(board.FolderID)
		if sourceFolderID != "" && !sourceFolderIDs[sourceFolderID] {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("board %q references missing folder %q", boardID, sourceFolderID), false)
		}
		exists, err := nativeBoardExists(e.core.DB, boardID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("board %q not found", boardID), false)
		}
		if seenBoards[boardID] {
			continue
		}
		seenBoards[boardID] = true
		folderID := ""
		if sourceFolderID != "" {
			folderID = folderIDMap[sourceFolderID]
		}
		eventBoards = append(eventBoards, proto.FavoriteTreeImportedBoardPayload{
			ID:       boardID,
			FolderID: folderID,
			Position: board.Position,
		})
	}

	replace := true
	if payload.Replace != nil {
		replace = *payload.Replace
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtFavoriteTreeImported,
		Scopes: []string{"user:" + actor.ID},
		Payload: &proto.FavoriteTreeImportedPayload{
			UserID:  actor.ID,
			Folders: eventFolders,
			Boards:  eventBoards,
			Replace: replace,
			TS:      ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardZap(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	if record.Offset <= 0 {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	var payload proto.SetBoardZapPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "invalid setBoardZap payload", false)
	}
	boardID := strings.TrimSpace(payload.Board)
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: boardID}) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, boardID), false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	settings, err := getBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	if payload.Zapped && !settings.ZapAllowed {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "board cannot be zapped", false)
	}
	ts := record.EnqueuedAt
	if ts <= 0 {
		ts = nowMS()
	}
	event := EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardZapSet,
		Scopes: []string{"board:" + boardID, "user:" + actor.ID},
		Payload: &proto.BoardZapSetPayload{
			UserID: actor.ID,
			Board:  boardID,
			Zapped: payload.Zapped,
			TS:     ts,
		},
		TS: ts,
	}
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: boardID}},
		events: []EventAppend{event},
	}, nil
}

func (e *CommandLogNativeDecisionExecutor) loadNativeDecisionActor(actorID string) (*User, *proto.ErrorDetail) {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "command actor is required", false)
	}
	actor, err := getUserByID(e.core.DB, actorID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "command actor not found", false)
	}
	return actor, nil
}

type nativeCreateThreadContext struct {
	Actor            *User
	Settings         *BoardSettings
	CanModerateBoard bool
	SanctionKind     string
}

func nativeCreateThreadDecisionContext(db *sql.DB, actorID, boardID string) (nativeCreateThreadContext, error) {
	var out nativeCreateThreadContext
	actorID = strings.TrimSpace(actorID)
	boardID = strings.TrimSpace(boardID)
	if actorID == "" {
		return out, nil
	}

	actor := &User{}
	settings := &BoardSettings{ZapAllowed: true}
	var (
		boardIDOut       string
		anonymousAllowed int
		readOnly         int
		noReply          int
		attachments      int
		mailIn           int
		relay            int
		memberRead       int
		memberPost       int
		statsExcluded    int
		zapAllowed       int
		canModerate      int
		sanctionKind     string
	)
	err := qQueryRow(db,
		`SELECT u.id, u.name, u.role, u.password, u.created,
		        COALESCE(NULLIF(u.registration_status,''), 'approved'), COALESCE(u.reviewed_at,0), COALESCE(u.reviewed_by,''), COALESCE(u.review_reason,''),
		        COALESCE(u.deactivated_at,0), COALESCE(u.deactivated_by,''), COALESCE(u.deactivated_reason,''),
		        COALESCE(b.id, ''),
		        COALESCE(s.anonymous_allowed, 0), COALESCE(s.read_only, 0), COALESCE(s.no_reply, 0),
		        COALESCE(s.attachments_allowed, 0), COALESCE(s.mail_in_allowed, 0), COALESCE(s.relay_enabled, 0),
		        COALESCE(s.member_read_mode, 0), COALESCE(s.member_post_mode, 0), COALESCE(s.stats_excluded, 0),
		        COALESCE(s.zap_allowed, 1), COALESCE(s.updated_at, 0),
		        CASE WHEN u.role IN ('moderator', 'admin') OR bm.user_id IS NOT NULL THEN 1 ELSE 0 END,
		        COALESCE((
		            SELECT us.kind
		              FROM user_sanctions us
		             WHERE us.user_id=u.id AND (us.scope=? OR us.scope='global')
		               AND (us.expires_at=0 OR us.expires_at>?)
		             ORDER BY CASE us.kind WHEN 'ban' THEN 0 ELSE 1 END
		             LIMIT 1
		        ), '')
		   FROM users u
		   LEFT JOIN boards b ON b.id=?
		   LEFT JOIN board_settings s ON s.board_id=b.id
		   LEFT JOIN board_moderators bm ON bm.board_id=b.id AND bm.user_id=u.id
		  WHERE u.id=?`,
		boardID, nowMS(), boardID, actorID,
	).Scan(&actor.ID, &actor.Name, &actor.Role, &actor.Password, &actor.Created,
		&actor.RegistrationStatus, &actor.ReviewedAt, &actor.ReviewedBy, &actor.ReviewReason,
		&actor.DeactivatedAt, &actor.DeactivatedBy, &actor.DeactivatedReason,
		&boardIDOut,
		&anonymousAllowed, &readOnly, &noReply, &attachments, &mailIn, &relay,
		&memberRead, &memberPost, &statsExcluded, &zapAllowed, &settings.UpdatedAt,
		&canModerate, &sanctionKind)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, err
	}

	out.Actor = actor
	if boardIDOut != "" {
		settings.BoardID = boardIDOut
		settings.AnonymousAllowed = anonymousAllowed != 0
		settings.ReadOnly = readOnly != 0
		settings.NoReply = noReply != 0
		settings.AttachmentsAllowed = attachments != 0
		settings.MailInAllowed = mailIn != 0
		settings.RelayEnabled = relay != 0
		settings.MemberReadMode = memberRead != 0
		settings.MemberPostMode = memberPost != 0
		settings.StatsExcluded = statsExcluded != 0
		settings.ZapAllowed = zapAllowed != 0
		out.Settings = settings
	}
	out.CanModerateBoard = canModerate != 0
	out.SanctionKind = strings.TrimSpace(sanctionKind)
	return out, nil
}

type nativeAppendPostContext struct {
	Actor             *User
	Thread            *Thread
	RootReplyGuards   nativeThreadRootPostReplyGuard
	Settings          *BoardSettings
	CanModerateBoard  bool
	CanModerateThread bool
	SanctionKind      string
}

func nativeAppendPostDecisionContext(db *sql.DB, actorID, threadID string) (nativeAppendPostContext, error) {
	var out nativeAppendPostContext
	actorID = strings.TrimSpace(actorID)
	threadID = strings.TrimSpace(threadID)
	if actorID == "" {
		return out, nil
	}

	actor := &User{}
	thread := &Thread{}
	settings := &BoardSettings{ZapAllowed: true}
	var (
		threadIDOut      string
		boardIDOut       string
		locked           int
		noReplyRoot      int
		mailBackRoot     int
		anonymousAllowed int
		readOnly         int
		noReply          int
		attachments      int
		mailIn           int
		relay            int
		memberRead       int
		memberPost       int
		statsExcluded    int
		zapAllowed       int
		canModerate      int
		canModerateReply int
		sanctionKind     string
	)
	err := qQueryRow(db,
		`SELECT u.id, u.name, u.role, u.password, u.created,
		        COALESCE(NULLIF(u.registration_status,''), 'approved'), COALESCE(u.reviewed_at,0), COALESCE(u.reviewed_by,''), COALESCE(u.review_reason,''),
		        COALESCE(u.deactivated_at,0), COALESCE(u.deactivated_by,''), COALESCE(u.deactivated_reason,''),
		        COALESCE(t.id, ''), COALESCE(t.board, ''), COALESCE(t.author, ''), COALESCE(t.author_id, ''),
		        COALESCE(t.title, ''), COALESCE(t.locked, 0), COALESCE(t.post_count, 0),
		        COALESCE(t.last_seq, 0), COALESCE(t.created_ts, 0), COALESCE(t.created_at, 0), COALESCE(t.updated_at, 0),
		        COALESCE((SELECT p.no_reply FROM posts p WHERE p.thread=t.id ORDER BY p.created_seq LIMIT 1), 0),
		        COALESCE((SELECT p.mail_back FROM posts p WHERE p.thread=t.id ORDER BY p.created_seq LIMIT 1), 0),
		        COALESCE(b.id, ''),
		        COALESCE(s.anonymous_allowed, 0), COALESCE(s.read_only, 0), COALESCE(s.no_reply, 0),
		        COALESCE(s.attachments_allowed, 0), COALESCE(s.mail_in_allowed, 0), COALESCE(s.relay_enabled, 0),
		        COALESCE(s.member_read_mode, 0), COALESCE(s.member_post_mode, 0), COALESCE(s.stats_excluded, 0),
		        COALESCE(s.zap_allowed, 1), COALESCE(s.updated_at, 0),
		        CASE WHEN u.role IN ('moderator', 'admin') OR bm.user_id IS NOT NULL THEN 1 ELSE 0 END,
		        CASE WHEN u.role IN ('moderator', 'admin') OR bm.user_id IS NOT NULL OR COALESCE(mem.can_moderate_threads, 0) != 0 THEN 1 ELSE 0 END,
		        COALESCE((
		            SELECT us.kind
		              FROM user_sanctions us
		             WHERE us.user_id=u.id AND (us.scope=t.board OR us.scope='global')
		               AND (us.expires_at=0 OR us.expires_at>?)
		             ORDER BY CASE us.kind WHEN 'ban' THEN 0 ELSE 1 END
		             LIMIT 1
		        ), '')
		   FROM users u
		   LEFT JOIN threads t ON t.id=?
		   LEFT JOIN boards b ON b.id=t.board
		   LEFT JOIN board_settings s ON s.board_id=b.id
		   LEFT JOIN board_moderators bm ON bm.board_id=b.id AND bm.user_id=u.id
		   LEFT JOIN board_members mem ON mem.board_id=b.id AND mem.user_id=u.id
		  WHERE u.id=?`,
		nowMS(), threadID, actorID,
	).Scan(&actor.ID, &actor.Name, &actor.Role, &actor.Password, &actor.Created,
		&actor.RegistrationStatus, &actor.ReviewedAt, &actor.ReviewedBy, &actor.ReviewReason,
		&actor.DeactivatedAt, &actor.DeactivatedBy, &actor.DeactivatedReason,
		&threadIDOut, &thread.Board, &thread.Author, &thread.AuthorID, &thread.Title, &locked,
		&thread.PostCount, &thread.LastSeq, &thread.CreatedTS, &thread.CreatedAt, &thread.UpdatedAt,
		&noReplyRoot, &mailBackRoot,
		&boardIDOut,
		&anonymousAllowed, &readOnly, &noReply, &attachments, &mailIn, &relay,
		&memberRead, &memberPost, &statsExcluded, &zapAllowed, &settings.UpdatedAt,
		&canModerate, &canModerateReply, &sanctionKind)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, err
	}

	out.Actor = actor
	if threadIDOut != "" {
		thread.ID = threadIDOut
		if thread.CreatedAt == 0 {
			thread.CreatedAt = thread.CreatedTS
		}
		if thread.UpdatedAt == 0 {
			thread.UpdatedAt = thread.CreatedAt
		}
		thread.Locked = locked != 0
		out.Thread = thread
		out.RootReplyGuards = nativeThreadRootPostReplyGuard{NoReply: noReplyRoot != 0, MailBack: mailBackRoot != 0}
	}
	if boardIDOut != "" {
		settings.BoardID = boardIDOut
		settings.AnonymousAllowed = anonymousAllowed != 0
		settings.ReadOnly = readOnly != 0
		settings.NoReply = noReply != 0
		settings.AttachmentsAllowed = attachments != 0
		settings.MailInAllowed = mailIn != 0
		settings.RelayEnabled = relay != 0
		settings.MemberReadMode = memberRead != 0
		settings.MemberPostMode = memberPost != 0
		settings.StatsExcluded = statsExcluded != 0
		settings.ZapAllowed = zapAllowed != 0
		out.Settings = settings
	}
	out.CanModerateBoard = canModerate != 0
	out.CanModerateThread = canModerateReply != 0
	out.SanctionKind = strings.TrimSpace(sanctionKind)
	return out, nil
}

func nativeDecisionErr(code, message string, retryable bool) *proto.ErrorDetail {
	return &proto.ErrorDetail{Code: code, Message: message, Retryable: retryable}
}

func nativeNormalizeStatsSnapshotDate(raw string, ts int64) (dateLabel, dateID string, errDetail *proto.ErrorDetail) {
	raw = strings.TrimSpace(raw)
	var day time.Time
	var err error
	if raw == "" {
		day = time.UnixMilli(ts).UTC()
	} else {
		day, err = time.Parse("2006-01-02", raw)
		if err != nil {
			return "", "", nativeDecisionErr(proto.ErrValidationFailed, "date must be YYYY-MM-DD", false)
		}
	}
	return day.Format("2006-01-02"), day.Format("20060102"), nil
}

func nativeNormalizeDirectMessagePolicy(policy string) (string, *proto.ErrorDetail) {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", "all", "everyone":
		return "all", nil
	case "friend", "friends", "friends-only", "friend_only":
		return "friends", nil
	case "none", "off", "disabled", "block":
		return "none", nil
	default:
		return "", nativeDecisionErr(proto.ErrValidationFailed, `policy must be "all", "friends", or "none"`, false)
	}
}

func nativeNormalizeRelationshipKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "friend", "friends", "follow", "following":
		return "friend"
	case "ignore", "ignored", "badlist":
		return "ignore"
	default:
		return ""
	}
}

func nativeFavoriteFolderExists(db *sql.DB, userID, folderID string) (bool, error) {
	if strings.TrimSpace(folderID) == "" {
		return true, nil
	}
	var exists int
	err := qQueryRow(db, `SELECT 1 FROM favorite_folders WHERE user_id=? AND id=?`, userID, folderID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

type nativeFavoriteFolderSnapshot struct {
	parentID string
	name     string
	position int
}

func nativeFavoriteFolderState(db *sql.DB, userID, folderID string) (nativeFavoriteFolderSnapshot, bool, error) {
	var snapshot nativeFavoriteFolderSnapshot
	err := qQueryRow(db,
		`SELECT parent_id, name, position FROM favorite_folders WHERE user_id=? AND id=?`,
		userID, folderID,
	).Scan(&snapshot.parentID, &snapshot.name, &snapshot.position)
	if err == sql.ErrNoRows {
		return nativeFavoriteFolderSnapshot{}, false, nil
	}
	if err != nil {
		return nativeFavoriteFolderSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func nativeFavoriteFolderContains(db *sql.DB, userID, ancestorID, folderID string) (bool, error) {
	for folderID != "" {
		if folderID == ancestorID {
			return true, nil
		}
		var parentID string
		err := qQueryRow(db, `SELECT parent_id FROM favorite_folders WHERE user_id=? AND id=?`, userID, folderID).Scan(&parentID)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		folderID = parentID
	}
	return false, nil
}

func nativeFavoriteFolderTargetPosition(db *sql.DB, userID, parentID string, position *int) (int, error) {
	if position != nil {
		if *position < 0 {
			return 0, nil
		}
		return *position, nil
	}
	var next int
	err := qQueryRow(db,
		`SELECT COALESCE(MAX(position) + 1, 0) FROM favorite_folders WHERE user_id=? AND parent_id=?`,
		userID, parentID,
	).Scan(&next)
	return next, err
}

func nativeRequireMinTrustForPoll(db *sql.DB, actor *User, minLevel int, action string) *proto.ErrorDetail {
	if actor == nil || actor.IsMod() {
		return nil
	}
	trustLevel, err := userTrustLevel(db, actor.ID)
	if err != nil {
		return nativeDecisionErr("internal_error", err.Error(), true)
	}
	if trustLevel < minLevel {
		return nativeDecisionErr(proto.ErrForbidden, action+" with poll requires trust level "+strconv.Itoa(minLevel), false)
	}
	return nil
}

func nativeNormalizePostRangeIDs(input []string) ([]string, *proto.ErrorDetail) {
	if len(input) == 0 {
		return nil, nativeDecisionErr(proto.ErrValidationFailed, "posts are required", false)
	}
	if len(input) > 100 {
		return nil, nativeDecisionErr(proto.ErrValidationFailed, "post range can include at most 100 items", false)
	}
	out := make([]string, 0, len(input))
	seen := map[string]bool{}
	for _, raw := range input {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "post id cannot be empty", false)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func nativeNormalizeMailRangeIDs(input []string) ([]string, *proto.ErrorDetail) {
	if len(input) == 0 {
		return nil, nativeDecisionErr(proto.ErrValidationFailed, "mail is required", false)
	}
	if len(input) > 100 {
		return nil, nativeDecisionErr(proto.ErrValidationFailed, "mail range can include at most 100 items", false)
	}
	out := make([]string, 0, len(input))
	seen := map[string]bool{}
	for _, raw := range input {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "mail id cannot be empty", false)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func nativeValidSlug(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func nativeEnsureRangeBoardAccess(db *sql.DB, actor *User, boardID string) *proto.ErrorDetail {
	var exists int
	err := qQueryRow(db, `SELECT 1 FROM boards WHERE id=?`, boardID).Scan(&exists)
	if err == sql.ErrNoRows {
		return nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	if err != nil {
		return nativeDecisionErr("internal_error", err.Error(), true)
	}
	canModeratePosts, err := nativeActorCanModerateBoardPosts(db, actor, boardID)
	if err != nil {
		return nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canModeratePosts {
		return nativeDecisionErr(proto.ErrForbidden, "board post moderation permission required", false)
	}
	return nil
}

func nativeLoadRangePost(db *sql.DB, postID, boardID string) (*Post, *Thread, *proto.ErrorDetail) {
	post, err := getPost(db, postID)
	if err != nil {
		return nil, nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nil, nil, nativeDecisionErr(proto.ErrNotFound, "post not found: "+postID, false)
	}
	thread, err := getThread(db, post.Thread)
	if err != nil {
		return nil, nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil || thread.Board != boardID {
		return nil, nil, nativeDecisionErr(proto.ErrNotFound, "post not found in board: "+postID, false)
	}
	return post, thread, nil
}

func nativeBoardJunkIDs(db *sql.DB, boardID string, requested []string) ([]string, *proto.ErrorDetail) {
	if len(requested) > 0 {
		return nativeNormalizePostRangeIDs(requested)
	}
	rows, err := qQuery(db,
		`SELECT post_id FROM post_deletions WHERE board_id=? AND kind='junk' ORDER BY deleted_at DESC, seq DESC`,
		boardID,
	)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var postID string
		if err := rows.Scan(&postID); err != nil {
			return nil, nativeDecisionErr("internal_error", err.Error(), true)
		}
		out = append(out, postID)
	}
	if err := rows.Err(); err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	return out, nil
}

func nativeJunkPostThread(db *sql.DB, postID, boardID string) (string, *proto.ErrorDetail) {
	var threadID string
	err := qQueryRow(db,
		`SELECT thread_id FROM post_deletions WHERE post_id=? AND board_id=? AND kind='junk'`,
		postID, boardID,
	).Scan(&threadID)
	if err == sql.ErrNoRows {
		return "", nativeDecisionErr(proto.ErrNotFound, "junk post not found: "+postID, false)
	}
	if err != nil {
		return "", nativeDecisionErr("internal_error", err.Error(), true)
	}
	return threadID, nil
}

func nativeActorCanModerateBoardThreads(db *sql.DB, actor *User, boardID string) (bool, error) {
	canModerateBoard, err := nativeActorCanModerateBoard(db, actor, boardID)
	if err != nil || canModerateBoard {
		return canModerateBoard, err
	}
	return nativeActorHasBoardMemberPermission(db, actor, boardID, "can_moderate_threads")
}

func nativeActorCanModerateBoardPosts(db *sql.DB, actor *User, boardID string) (bool, error) {
	canModerateBoard, err := nativeActorCanModerateBoard(db, actor, boardID)
	if err != nil || canModerateBoard {
		return canModerateBoard, err
	}
	return nativeActorHasBoardMemberPermission(db, actor, boardID, "can_moderate_posts")
}

func nativeActorBoardReplyPermissions(db *sql.DB, actor *User, boardID string) (bool, bool, error) {
	if actor == nil {
		return false, false, nil
	}
	if actor.IsMod() {
		return true, true, nil
	}
	var moderator, canModerateThreads int
	err := qQueryRow(db,
		`SELECT
		   CASE WHEN EXISTS (
		     SELECT 1 FROM board_moderators WHERE board_id=? AND user_id=?
		   ) THEN 1 ELSE 0 END,
		   COALESCE((
		     SELECT can_moderate_threads FROM board_members WHERE board_id=? AND user_id=?
		   ), 0)`,
		boardID, actor.ID, boardID, actor.ID,
	).Scan(&moderator, &canModerateThreads)
	if err != nil {
		return false, false, err
	}
	canModerateBoard := moderator != 0
	return canModerateBoard, canModerateBoard || canModerateThreads != 0, nil
}

func nativeActorCanCurateBoard(db *sql.DB, actor *User, boardID string) (bool, error) {
	canModerateBoard, err := nativeActorCanModerateBoard(db, actor, boardID)
	if err != nil || canModerateBoard {
		return canModerateBoard, err
	}
	return nativeActorHasBoardMemberPermission(db, actor, boardID, "can_curate")
}

func nativeActorCanAnnounceBoard(db *sql.DB, actor *User, boardID string) (bool, error) {
	canModerateBoard, err := nativeActorCanModerateBoard(db, actor, boardID)
	if err != nil || canModerateBoard {
		return canModerateBoard, err
	}
	return nativeActorHasBoardMemberPermission(db, actor, boardID, "can_announce")
}

func nativeActorCanSetBoardSettings(db *sql.DB, actor *User, boardID string) (bool, error) {
	canModerateBoard, err := nativeActorCanModerateBoard(db, actor, boardID)
	if err != nil || canModerateBoard {
		return canModerateBoard, err
	}
	return nativeActorHasBoardMemberPermission(db, actor, boardID, "can_set_board_settings")
}

func nativeActorCanManageBoardMembers(db *sql.DB, actor *User, boardID string) (bool, error) {
	canModerateBoard, err := nativeActorCanModerateBoard(db, actor, boardID)
	if err != nil || canModerateBoard {
		return canModerateBoard, err
	}
	return nativeActorHasBoardMemberPermission(db, actor, boardID, "can_manage_members")
}

func nativeActorCanCurateBoardKind(db *sql.DB, actor *User, boardID, kind string) (bool, error) {
	canCurate, err := nativeActorCanCurateBoard(db, actor, boardID)
	if err != nil || canCurate || kind != "announcement" {
		return canCurate, err
	}
	return nativeActorCanAnnounceBoard(db, actor, boardID)
}

func nativeActorCanManageBoardPolls(db *sql.DB, actor *User, boardID string) (bool, error) {
	canModerateBoard, err := nativeActorCanModerateBoard(db, actor, boardID)
	if err != nil || canModerateBoard {
		return canModerateBoard, err
	}
	return nativeActorHasBoardMemberPermission(db, actor, boardID, "can_manage_polls")
}

func nativeActorCanModerateBoard(db *sql.DB, actor *User, boardID string) (bool, error) {
	if actor == nil {
		return false, nil
	}
	if actor.IsMod() {
		return true, nil
	}
	var exists int
	err := qQueryRow(db, `SELECT 1 FROM board_moderators WHERE board_id=? AND user_id=?`, boardID, actor.ID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return exists != 0, nil
}

func nativeActorCanUseMemberBoard(db *sql.DB, actor *User, boardID string) (bool, error) {
	canModerateBoard, err := nativeActorCanModerateBoard(db, actor, boardID)
	if err != nil || canModerateBoard {
		return canModerateBoard, err
	}
	var exists int
	err = qQueryRow(db, `SELECT 1 FROM board_members WHERE board_id=? AND user_id=?`, boardID, actor.ID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return exists != 0, nil
}

func nativeActorHasBoardMemberPermission(db *sql.DB, actor *User, boardID, column string) (bool, error) {
	if actor == nil {
		return false, nil
	}
	switch column {
	case "can_announce", "can_curate", "can_manage_members", "can_moderate_posts", "can_moderate_threads", "can_manage_polls", "can_set_board_settings":
	default:
		return false, nil
	}
	var allowed int
	err := qQueryRow(db, `SELECT `+column+` FROM board_members WHERE board_id=? AND user_id=?`, boardID, actor.ID).Scan(&allowed)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return allowed != 0, nil
}

const nativeAuthorEditWindow = 24 * time.Hour

func nativeContentType(contentType string) string {
	if contentType == "ansi-art" {
		return "ansi-art"
	}
	return "markup"
}

func nativeNormalizeAttachments(record CommandLogRecord, input []proto.AttachmentPayload, allowed bool, canModerateBoard bool) ([]proto.AttachmentPayload, *proto.ErrorDetail) {
	if len(input) == 0 {
		return nil, nil
	}
	if !allowed && !canModerateBoard {
		return nil, nativeDecisionErr(proto.ErrForbidden, "attachments are not enabled for this board", false)
	}
	if len(input) > 8 {
		return nil, nativeDecisionErr(proto.ErrValidationFailed, "a post can have at most 8 attachments", false)
	}
	out := make([]proto.AttachmentPayload, 0, len(input))
	for i, item := range input {
		filename := strings.TrimSpace(item.Filename)
		contentType := strings.TrimSpace(item.ContentType)
		url := strings.TrimSpace(item.URL)
		if filename == "" {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "attachment filename is required", false)
		}
		if len(filename) > 160 {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "attachment filename must be 160 characters or less", false)
		}
		if len(contentType) > 120 {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "attachment content type must be 120 characters or less", false)
		}
		if len(url) > 500 {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "attachment URL must be 500 characters or less", false)
		}
		if item.SizeBytes < 0 {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "attachment size cannot be negative", false)
		}
		out = append(out, proto.AttachmentPayload{
			ID:          stableCommandLogDecisionID("att_", record, 100+i),
			Filename:    filename,
			ContentType: contentType,
			SizeBytes:   item.SizeBytes,
			URL:         url,
		})
	}
	return out, nil
}

func nativeFormatQuotedReplyPrefix(source *Post) string {
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

func stableCommandLogDecisionID(prefix string, record CommandLogRecord, ordinal int) string {
	partition := record.Partition.Normalize()
	h := sha256.New()
	_, _ = h.Write([]byte(partition.Kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(partition.Key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.FormatInt(record.Offset, 10)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(record.ActorID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(record.CID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(record.Command))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.Itoa(ordinal)))
	sum := h.Sum(nil)
	return prefix + hex.EncodeToString(sum[:12])
}

type nativeThreadRootPostReplyGuard struct {
	NoReply  bool
	MailBack bool
}

func nativeThreadWithRootReplyGuards(db *sql.DB, threadID string) (*Thread, nativeThreadRootPostReplyGuard, error) {
	thread := &Thread{}
	var locked, noReply, mailBack int
	err := qQueryRow(db,
		`SELECT t.id, t.board, t.author, COALESCE(t.author_id,''), t.title, t.locked, t.post_count,
		        t.last_seq, t.created_ts, t.created_at, t.updated_at,
		        COALESCE((SELECT p.no_reply FROM posts p WHERE p.thread=t.id ORDER BY p.created_seq LIMIT 1), 0),
		        COALESCE((SELECT p.mail_back FROM posts p WHERE p.thread=t.id ORDER BY p.created_seq LIMIT 1), 0)
		   FROM threads t
		  WHERE t.id=?`,
		threadID,
	).Scan(&thread.ID, &thread.Board, &thread.Author, &thread.AuthorID, &thread.Title, &locked, &thread.PostCount, &thread.LastSeq, &thread.CreatedTS, &thread.CreatedAt, &thread.UpdatedAt, &noReply, &mailBack)
	if err == sql.ErrNoRows {
		return nil, nativeThreadRootPostReplyGuard{}, nil
	}
	if err != nil {
		return nil, nativeThreadRootPostReplyGuard{}, err
	}
	if thread.CreatedAt == 0 {
		thread.CreatedAt = thread.CreatedTS
	}
	if thread.UpdatedAt == 0 {
		thread.UpdatedAt = thread.CreatedAt
	}
	thread.Locked = locked != 0
	guards := nativeThreadRootPostReplyGuard{NoReply: noReply != 0, MailBack: mailBack != 0}
	return thread, guards, nil
}

func nativeThreadRootPostReplyGuards(db *sql.DB, threadID string) (nativeThreadRootPostReplyGuard, error) {
	var noReply, mailBack int
	err := qQueryRow(db,
		`SELECT no_reply, mail_back FROM posts WHERE thread=? ORDER BY created_seq LIMIT 1`,
		threadID,
	).Scan(&noReply, &mailBack)
	if err == sql.ErrNoRows {
		return nativeThreadRootPostReplyGuard{}, nil
	}
	return nativeThreadRootPostReplyGuard{NoReply: noReply != 0, MailBack: mailBack != 0}, err
}

func nativeThreadRootPost(db *sql.DB, threadID string) (*Post, error) {
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

const (
	nativeVoteSystemBoardID           = "vote"
	nativeFilterSystemBoardID         = "Filter"
	nativeModerationSystemBoardID     = "0moderation"
	nativeSyssecuritySystemBoardID    = "syssecurity"
	nativeDenyPostSystemBoardID       = "denypost"
	nativeUndenyPostSystemBoardID     = "undenypost"
	nativeBlessingSystemBoardID       = "Blessing"
	nativeSysmailSystemBoardID        = "sysmail"
	nativeAnnouncementSystemBoardID   = "0announce"
	nativeRecommendSystemBoardID      = "Recommend"
	nativeRegistrySystemBoardID       = "Registry"
	nativeRejectRegistrySystemBoardID = "reject_registry"
	moderationLogFlag                 = "flag"
	moderationLogResolve              = "resolve"
)

type nativeDigestMirrorSystemBoard struct {
	Kind        string
	BoardID     string
	Name        string
	Description string
	ThreadID    string
	PostID      string
	Default     string
}

var nativeGeneratedSystemBoardIDSet = map[string]bool{
	"0announce":       true,
	"0moderation":     true,
	"BBSLists":        true,
	"Blessing":        true,
	"Filter":          true,
	"Goodbye":         true,
	"GiveupNotice":    true,
	"Recommend":       true,
	"Registry":        true,
	"bbsnet":          true,
	"denypost":        true,
	"newcomers":       true,
	"notepad":         true,
	"reject_registry": true,
	"sysmail":         true,
	"syssecurity":     true,
	"undenypost":      true,
	"vote":            true,
}

type nativeDigestEntryForCommand struct {
	ID         string
	BoardID    string
	TargetKind string
	TargetID   string
	Kind       string
	Title      string
	Path       string
	Note       string
}

type nativeDigestEntryRemovalRecord struct {
	ID        string
	BoardID   string
	Kind      string
	RemovedBy string
}

type nativeDigestPathEntryForCopy struct {
	ID         string
	BoardID    string
	TargetKind string
	TargetID   string
	Kind       string
	Path       string
}

type nativeDigestPathDirectoryForCopy struct {
	ID      string
	BoardID string
	Kind    string
	Path    string
}

func (e *CommandLogNativeDecisionExecutor) nativeDigestPathMutationBoardKind(record CommandLogRecord, rawBoard, rawKind string) (string, string, *proto.ErrorDetail) {
	boardID := strings.TrimSpace(rawBoard)
	expectedKey := boardID
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	partition := record.Partition.Normalize()
	if partition != (LogPartition{Kind: partitionBoard, Key: expectedKey}) {
		return "", "", nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			partition.Kind, partition.Key, partitionBoard, expectedKey), false)
	}
	if boardID == "" {
		return "", "", nativeDecisionErr(proto.ErrValidationFailed, "board is required", false)
	}
	kind := strings.TrimSpace(rawKind)
	if kind == "" {
		kind = "archive"
	}
	normalizedKind, errDetail := nativeNormalizeDigestKind(kind)
	if errDetail != nil {
		return "", "", errDetail
	}
	return boardID, normalizedKind, nil
}

func (e *CommandLogNativeDecisionExecutor) nativeRequireDigestPathMutation(actor *User, boardID, kind string) *proto.ErrorDetail {
	settings, err := getBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canCurate, err := nativeActorCanCurateBoardKind(e.core.DB, actor, boardID, kind)
	if err != nil {
		return nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canCurate {
		return nativeDecisionErr(proto.ErrForbidden, nativeDigestCurationPermissionMessage(kind), false)
	}
	return nil
}

type nativeSystemNoticeBoard struct {
	ID          string
	Name        string
	Description string
}

func nativeNormalizeSystemNoticeBoard(raw string) (nativeSystemNoticeBoard, bool) {
	board := strings.TrimSpace(raw)
	if board == "" || strings.EqualFold(board, "notepad") {
		return nativeSystemNoticeBoard{ID: "notepad", Name: "notepad", Description: "Generated public system notes"}, true
	}
	switch strings.ToLower(board) {
	case "giveupnotice", "giveup_notice":
		return nativeSystemNoticeBoard{ID: "GiveupNotice", Name: "GiveupNotice", Description: "Generated give-up-net notices"}, true
	case "bbsnet":
		return nativeSystemNoticeBoard{ID: "bbsnet", Name: "bbsnet", Description: "Generated site-hop and network notices"}, true
	default:
		return nativeSystemNoticeBoard{}, false
	}
}

func nativeFormatSystemNoticeBody(board nativeSystemNoticeBoard, title, noticeBody, source, actorName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "- Notice board: %s\n", board.Name)
	fmt.Fprintf(&b, "- Actor: %s\n", actorName)
	if source != "" {
		fmt.Fprintf(&b, "- Source: %s\n", source)
	}
	b.WriteString("\n")
	b.WriteString(noticeBody)
	if !strings.HasSuffix(noticeBody, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\nGenerated public system notice.\n")
	return b.String()
}

func nativeFormatBlessingSystemBody(fromName, toName, message string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Blessing for %s\n\n", toName)
	fmt.Fprintf(&b, "- From: %s\n", fromName)
	fmt.Fprintf(&b, "- To: %s\n\n", toName)
	if strings.TrimSpace(message) == "" {
		b.WriteString("A public blessing was sent.\n")
	} else {
		b.WriteString(strings.TrimSpace(message))
		if !strings.HasSuffix(message, "\n") {
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nGenerated public blessing record.\n")
	return b.String()
}

func nativeFormatPollResultBody(sourceThread *Thread, poll *Poll) string {
	total := 0
	if poll != nil {
		for _, option := range poll.Options {
			total += option.VoteCount
		}
	}
	var b strings.Builder
	question := ""
	if poll != nil {
		question = strings.TrimSpace(poll.Question)
	}
	if question == "" {
		question = "Untitled poll"
	}
	sourceTitle := ""
	sourceBoard := ""
	if sourceThread != nil {
		sourceTitle = sourceThread.Title
		sourceBoard = sourceThread.Board
	}
	fmt.Fprintf(&b, "# Poll result: %s\n\n", question)
	fmt.Fprintf(&b, "- Source thread: %s\n", sourceTitle)
	fmt.Fprintf(&b, "- Source board: %s\n", sourceBoard)
	fmt.Fprintf(&b, "- Total votes: %d\n\n", total)
	if poll != nil {
		for i, option := range poll.Options {
			percent := 0
			if total > 0 {
				percent = option.VoteCount * 100 / total
			}
			fmt.Fprintf(&b, "%d. %s: %d vote(s), %d%%\n", i+1, option.Text, option.VoteCount, percent)
		}
	}
	b.WriteString("\nGenerated public poll result.\n")
	return b.String()
}

func nativeBoardSettingsAuditLines(p proto.SetBoardSettingsPayload) []string {
	fields := []struct {
		name  string
		value *bool
	}{
		{"anonymousAllowed", p.AnonymousAllowed},
		{"readOnly", p.ReadOnly},
		{"noReply", p.NoReply},
		{"attachmentsAllowed", p.AttachmentsAllowed},
		{"mailInAllowed", p.MailInAllowed},
		{"relayEnabled", p.RelayEnabled},
		{"memberReadMode", p.MemberReadMode},
		{"memberPostMode", p.MemberPostMode},
		{"statsExcluded", p.StatsExcluded},
		{"zapAllowed", p.ZapAllowed},
	}
	out := []string{}
	for _, field := range fields {
		if field.value == nil {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %t", field.name, *field.value))
	}
	return out
}

func nativeNormalizeBoardMemberApprovalMode(mode string) (string, *proto.ErrorDetail) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "manual":
		return "manual", nil
	case "auto", "automatic":
		return "auto", nil
	default:
		return "", nativeDecisionErr(proto.ErrValidationFailed, `approvalMode must be "manual" or "auto"`, false)
	}
}

func nativeNormalizeMemberApplicationStatus(status string) (string, *proto.ErrorDetail) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approved", "approve":
		return "approved", nil
	case "rejected", "reject":
		return "rejected", nil
	case "blacklisted", "blacklist":
		return "blacklisted", nil
	default:
		return "", nativeDecisionErr(proto.ErrValidationFailed, `status must be "approved", "rejected", or "blacklisted"`, false)
	}
}

func nativeResolveUserRef(db *sql.DB, ref string) (*User, *proto.ErrorDetail) {
	ref = strings.TrimSpace(ref)
	var userID string
	err := qQueryRow(db, `SELECT id FROM users WHERE id=? OR name=?`, ref, ref).Scan(&userID)
	if err == sql.ErrNoRows {
		return nil, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	user, err := getUserByID(db, userID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if user == nil {
		return nil, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	return user, nil
}

func nativeBoardMemberPermissionsChanged(p proto.SetBoardMemberPayload) bool {
	return p.CanManageMembers != nil ||
		p.CanCurate != nil ||
		p.CanModeratePosts != nil ||
		p.CanModerateThreads != nil ||
		p.CanAnnounce != nil ||
		p.CanManagePolls != nil ||
		p.CanSetBoardSettings != nil
}

func nativeIsBoardModerator(db *sql.DB, userID, boardID string) (bool, error) {
	var exists int
	err := qQueryRow(db, `SELECT 1 FROM board_moderators WHERE board_id=? AND user_id=?`, boardID, userID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return exists != 0, nil
}

func nativeBoardMemberHasDelegatedPermissions(db *sql.DB, boardID, userID string) (bool, error) {
	var canManageMembers, canCurate, canModeratePosts, canModerateThreads, canAnnounce, canManagePolls, canSetBoardSettings int
	err := qQueryRow(db,
		`SELECT can_manage_members, can_curate, can_moderate_posts, can_moderate_threads, can_announce, can_manage_polls, can_set_board_settings
		   FROM board_members WHERE board_id=? AND user_id=?`,
		boardID, userID,
	).Scan(&canManageMembers, &canCurate, &canModeratePosts, &canModerateThreads, &canAnnounce, &canManagePolls, &canSetBoardSettings)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return canManageMembers != 0 ||
		canCurate != 0 ||
		canModeratePosts != 0 ||
		canModerateThreads != 0 ||
		canAnnounce != 0 ||
		canManagePolls != 0 ||
		canSetBoardSettings != 0, nil
}

func nativeBoardMemberFinalState(db *sql.DB, boardID, userID string, payload proto.SetBoardMemberPayload, title string) (BoardMember, error) {
	member := BoardMember{UserID: userID}
	if !payload.Member {
		return member, nil
	}
	var canManageMembers, canCurate, canModeratePosts, canModerateThreads, canAnnounce, canManagePolls, canSetBoardSettings int
	err := qQueryRow(db,
		`SELECT can_manage_members, can_curate, can_moderate_posts, can_moderate_threads, can_announce, can_manage_polls, can_set_board_settings, COALESCE(position, 0)
		   FROM board_members WHERE board_id=? AND user_id=?`,
		boardID, userID,
	).Scan(&canManageMembers, &canCurate, &canModeratePosts, &canModerateThreads, &canAnnounce, &canManagePolls, &canSetBoardSettings, &member.Position)
	if err == sql.ErrNoRows {
		if err := qQueryRow(db, `SELECT COALESCE(MAX(position) + 1, 0) FROM board_members WHERE board_id=?`, boardID).Scan(&member.Position); err != nil {
			return member, err
		}
	} else if err != nil {
		return member, err
	}
	member.Title = title
	member.CanManageMembers = canManageMembers != 0
	member.CanCurate = canCurate != 0
	member.CanModeratePosts = canModeratePosts != 0
	member.CanModerateThreads = canModerateThreads != 0
	member.CanAnnounce = canAnnounce != 0
	member.CanManagePolls = canManagePolls != 0
	member.CanSetBoardSettings = canSetBoardSettings != 0
	if payload.Position != nil {
		member.Position = *payload.Position
	}
	if payload.CanManageMembers != nil {
		member.CanManageMembers = *payload.CanManageMembers
	}
	if payload.CanCurate != nil {
		member.CanCurate = *payload.CanCurate
	}
	if payload.CanModeratePosts != nil {
		member.CanModeratePosts = *payload.CanModeratePosts
	}
	if payload.CanModerateThreads != nil {
		member.CanModerateThreads = *payload.CanModerateThreads
	}
	if payload.CanAnnounce != nil {
		member.CanAnnounce = *payload.CanAnnounce
	}
	if payload.CanManagePolls != nil {
		member.CanManagePolls = *payload.CanManagePolls
	}
	if payload.CanSetBoardSettings != nil {
		member.CanSetBoardSettings = *payload.CanSetBoardSettings
	}
	return member, nil
}

func nativeRequireBoardMembershipAdmission(c *Core, boardID, userID string, requirements *BoardMemberRequirements) *proto.ErrorDetail {
	if requirements == nil {
		return nil
	}
	if c == nil || c.DB == nil {
		return nativeDecisionErr("internal_error", "core is not initialized", true)
	}
	db := c.DB
	if requirements.MaxMembers > 0 {
		isMember, err := projections.UserIsBoardMember(db, boardID, userID)
		if err != nil {
			return nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !isMember {
			var currentMembers int
			if err := qQueryRow(db, `SELECT COUNT(*) FROM board_members WHERE board_id=?`, boardID).Scan(&currentMembers); err != nil {
				return nativeDecisionErr("internal_error", err.Error(), true)
			}
			if currentMembers >= requirements.MaxMembers {
				return nativeDecisionErr(proto.ErrConflict, "board membership is full", false)
			}
		}
	}
	if requirements.MinLoginCount > 0 {
		loginCount, err := nativeUserLoginCount(db, userID)
		if err != nil {
			return nativeDecisionErr("internal_error", err.Error(), true)
		}
		if loginCount < requirements.MinLoginCount {
			return nativeDecisionErr(proto.ErrForbidden, fmt.Sprintf("minimum login count is %d", requirements.MinLoginCount), false)
		}
	}
	if requirements.MinPostCount > 0 {
		postsCreated, err := nativeUserPostsCreated(db, userID)
		if err != nil {
			return nativeDecisionErr("internal_error", err.Error(), true)
		}
		if postsCreated < requirements.MinPostCount {
			return nativeDecisionErr(proto.ErrForbidden, fmt.Sprintf("minimum post count is %d", requirements.MinPostCount), false)
		}
	}
	counterStore := c.counterStore
	if counterStore == nil {
		counterStore = sqlCounterStore{db: db}
	}
	if requirements.MinScore > 0 {
		score, err := nativeUserReactionScore(db, counterStore, userID)
		if err != nil {
			return nativeDecisionErr("internal_error", err.Error(), true)
		}
		if score < requirements.MinScore {
			return nativeDecisionErr(proto.ErrForbidden, fmt.Sprintf("minimum score is %d", requirements.MinScore), false)
		}
	}
	if requirements.MinBoardPostCount > 0 {
		boardPosts, err := nativeUserBoardPostCount(db, boardID, userID)
		if err != nil {
			return nativeDecisionErr("internal_error", err.Error(), true)
		}
		if boardPosts < requirements.MinBoardPostCount {
			return nativeDecisionErr(proto.ErrForbidden, fmt.Sprintf("minimum board post count is %d", requirements.MinBoardPostCount), false)
		}
	}
	if requirements.MinBoardOriginalPostCount > 0 {
		boardOriginalPosts, err := nativeUserBoardOriginalPostCount(db, boardID, userID)
		if err != nil {
			return nativeDecisionErr("internal_error", err.Error(), true)
		}
		if boardOriginalPosts < requirements.MinBoardOriginalPostCount {
			return nativeDecisionErr(proto.ErrForbidden, fmt.Sprintf("minimum board original post count is %d", requirements.MinBoardOriginalPostCount), false)
		}
	}
	if requirements.MinBoardDigestCount > 0 {
		boardDigests, err := nativeUserBoardDigestCount(db, boardID, userID)
		if err != nil {
			return nativeDecisionErr("internal_error", err.Error(), true)
		}
		if boardDigests < requirements.MinBoardDigestCount {
			return nativeDecisionErr(proto.ErrForbidden, fmt.Sprintf("minimum board digest count is %d", requirements.MinBoardDigestCount), false)
		}
	}
	if requirements.MinBoardMarkCount > 0 {
		boardMarks, err := nativeUserBoardMarkCount(db, counterStore, boardID, userID)
		if err != nil {
			return nativeDecisionErr("internal_error", err.Error(), true)
		}
		if boardMarks < requirements.MinBoardMarkCount {
			return nativeDecisionErr(proto.ErrForbidden, fmt.Sprintf("minimum board mark count is %d", requirements.MinBoardMarkCount), false)
		}
	}
	if requirements.MinTrustLevel > 0 {
		level, err := userTrustLevel(db, userID)
		if err != nil {
			return nativeDecisionErr("internal_error", err.Error(), true)
		}
		if level < requirements.MinTrustLevel {
			return nativeDecisionErr(proto.ErrForbidden, fmt.Sprintf("minimum trust level is %d", requirements.MinTrustLevel), false)
		}
	}
	return nil
}

func nativeUserLoginCount(db *sql.DB, userID string) (int, error) {
	var loginCount int
	err := qQueryRow(db, `SELECT login_count FROM user_activity WHERE user_id=?`, userID).Scan(&loginCount)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return loginCount, err
}

func nativeUserPostsCreated(db *sql.DB, userID string) (int, error) {
	var postsCreated int
	err := qQueryRow(db, `SELECT COUNT(*) FROM posts WHERE author_id=? AND redacted=0`, userID).Scan(&postsCreated)
	return postsCreated, err
}

func nativeUserReactionScore(db *sql.DB, store CounterStore, userID string) (int, error) {
	rows, err := qQuery(db, `SELECT id FROM posts WHERE author_id=? AND redacted=0`, userID)
	if err != nil {
		return 0, err
	}
	return nativeSumCounterStoreReactions(rows, store)
}

func nativeUserBoardPostCount(db *sql.DB, boardID, userID string) (int, error) {
	var count int
	err := qQueryRow(db,
		`SELECT COUNT(*)
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		  WHERE t.board=? AND p.author_id=? AND p.redacted=0`,
		boardID, userID,
	).Scan(&count)
	return count, err
}

func nativeUserBoardOriginalPostCount(db *sql.DB, boardID, userID string) (int, error) {
	var count int
	err := qQueryRow(db,
		`SELECT COUNT(*) FROM threads WHERE board=? AND author_id=?`,
		boardID, userID,
	).Scan(&count)
	return count, err
}

func nativeUserBoardMarkCount(db *sql.DB, store CounterStore, boardID, userID string) (int, error) {
	rows, err := qQuery(db,
		`SELECT p.id
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		  WHERE t.board=? AND p.author_id=? AND p.redacted=0`,
		boardID, userID,
	)
	if err != nil {
		return 0, err
	}
	return nativeSumCounterStoreReactions(rows, store)
}

func nativeUserBoardDigestCount(db *sql.DB, boardID, userID string) (int, error) {
	var count int
	err := qQueryRow(db,
		`SELECT COUNT(*)
		   FROM digest_entries d
		   LEFT JOIN posts p ON d.target_kind='post' AND p.id=d.target_id
		   LEFT JOIN threads tt ON d.target_kind='thread' AND tt.id=d.target_id
		  WHERE d.board_id=?
		    AND (
		      (d.target_kind='post' AND p.author_id=?)
		      OR (d.target_kind='thread' AND tt.author_id=?)
		    )`,
		boardID, userID, userID,
	).Scan(&count)
	return count, err
}

func nativeSumCounterStoreReactions(rows *sql.Rows, store CounterStore) (int, error) {
	postIDs := []string{}
	total := 0
	for rows.Next() {
		var postID string
		if err := rows.Scan(&postID); err != nil {
			_ = rows.Close()
			return 0, err
		}
		postIDs = append(postIDs, postID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, postID := range postIDs {
		count, err := store.ReactionCount(postID)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func nativeBoardMembershipApplicationEvents(db *sql.DB, record CommandLogRecord, actor *User, applicationID, boardID, userID, note string, autoApprove bool, ts int64) ([]EventAppend, *proto.ErrorDetail) {
	events := []EventAppend{{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardMemberApplicationSubmitted,
		Scopes: []string{"board:" + boardID, "user:" + userID},
		Payload: &proto.BoardMemberApplicationSubmittedPayload{
			ID:    applicationID,
			Board: boardID,
			User:  userID,
			Note:  note,
			TS:    ts,
		},
		TS: ts,
	}}
	if !autoApprove {
		return events, nil
	}
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, 1),
		Kind:   proto.EvtBoardMemberApplicationReviewed,
		Scopes: []string{"board:" + boardID, "user:" + userID},
		Payload: &proto.BoardMemberApplicationReviewedPayload{
			Application: applicationID,
			Board:       boardID,
			User:        userID,
			Status:      "approved",
			Reviewer:    actor.ID,
			ReviewNote:  "auto-approved by board membership rules",
			TS:          ts,
		},
		TS: ts,
	})
	registryEvents, errDetail := nativeBoardRegistrationSystemLogEvents(db, record, actor, applicationID, "approved", boardID, userID, ts, 2)
	if errDetail != nil {
		return nil, errDetail
	}
	events = append(events, registryEvents...)
	return events, nil
}

func nativeBoardMembershipReviewEvents(db *sql.DB, record CommandLogRecord, actor *User, applicationID, boardID, userID, status, title, note string, ts int64) ([]EventAppend, *proto.ErrorDetail) {
	events := []EventAppend{{
		ID:     stableCommandLogDecisionID("evt_", record, 0),
		Kind:   proto.EvtBoardMemberApplicationReviewed,
		Scopes: []string{"board:" + boardID, "user:" + userID},
		Payload: &proto.BoardMemberApplicationReviewedPayload{
			Application: applicationID,
			Board:       boardID,
			User:        userID,
			Status:      status,
			Title:       title,
			Reviewer:    actor.ID,
			ReviewNote:  note,
			TS:          ts,
		},
		TS: ts,
	}}
	registryEvents, errDetail := nativeBoardRegistrationSystemLogEvents(db, record, actor, applicationID, status, boardID, userID, ts, 1)
	if errDetail != nil {
		return nil, errDetail
	}
	events = append(events, registryEvents...)
	return events, nil
}

func nativeBoardRegistrationSystemLogEvents(db *sql.DB, record CommandLogRecord, actor *User, applicationID, status, boardID, userID string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	switch status {
	case "approved", "rejected", "blacklisted":
	default:
		return nil, nil
	}
	settings, err := getBoardSettings(db, boardID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings != nil && settings.MemberReadMode {
		return nil, nil
	}
	boardIDOut := nativeRegistrySystemBoardID
	boardNameOut := "Registry"
	boardDescription := "Generated board registration approvals"
	if status != "approved" {
		boardIDOut = nativeRejectRegistrySystemBoardID
		boardNameOut = "reject_registry"
		boardDescription = "Generated rejected board registrations"
	}
	threadID := "registry_" + status + "_thr_" + applicationID
	postID := "registry_" + status + "_pst_" + applicationID
	var exists int
	err = qQueryRow(db, `SELECT 1 FROM threads WHERE id=?`, threadID).Scan(&exists)
	if err == nil {
		return nil, nil
	}
	if err != sql.ErrNoRows {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	var sourceBoardName string
	if err := qQueryRow(db, `SELECT name FROM boards WHERE id=?`, boardID).Scan(&sourceBoardName); err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	var applicantName string
	if err := qQueryRow(db, `SELECT name FROM users WHERE id=?`, userID).Scan(&applicantName); err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	title := "Board registration " + status + " " + applicationID
	body := fmt.Sprintf("# %s\n\n- Application: %s\n- Status: %s\n- Board: %s (%s)\n- Applicant: %s\n- Reviewer: %s\n\nApplication and review notes are kept in the board member manager queue.\n",
		title, applicationID, status, sourceBoardName, boardID, applicantName, actor.Name)

	events := make([]EventAppend, 0, 3)
	err = qQueryRow(db, `SELECT 1 FROM boards WHERE id=?`, boardIDOut).Scan(&exists)
	if err == sql.ErrNoRows {
		position, posErr := nativeBoardCategoryPosition(db, "")
		if posErr != nil {
			return nil, nativeDecisionErr("internal_error", posErr.Error(), true)
		}
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, startIndex),
			Kind:   proto.EvtBoardCreated,
			Scopes: []string{"board:" + boardIDOut},
			Payload: &proto.BoardCreatedPayload{
				ID:          boardIDOut,
				Name:        boardNameOut,
				Description: boardDescription,
				Position:    position,
				By:          actor.ID,
				TS:          ts,
			},
			TS: ts,
		})
	} else if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, startIndex+1),
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:" + boardIDOut},
		Payload: &proto.ThreadNewPayload{
			ID:       threadID,
			Board:    boardIDOut,
			Author:   actor.Name,
			AuthorID: actor.ID,
			Title:    title,
			TS:       ts,
		},
		TS: ts,
	})
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, startIndex+2),
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:" + boardIDOut, "thread:" + threadID},
		Payload: &proto.PostAppendedPayload{
			ID:          postID,
			Thread:      threadID,
			Author:      actor.Name,
			AuthorID:    actor.ID,
			Body:        body,
			RawBody:     body,
			ContentType: "markup",
			TS:          ts,
		},
		TS: ts,
	})
	return events, nil
}

func nativeBoardModeratorEventPosition(db *sql.DB, boardID, userID, actorID string, moderator bool, requested *int, ts int64) (int, error) {
	if moderator {
		if requested != nil {
			if *requested < 0 {
				return 0, nil
			}
			return *requested, nil
		}
		if position, ok, err := nativeCurrentBoardModeratorPosition(db, boardID, userID); err != nil || ok {
			return position, err
		}
		var position int
		err := qQueryRow(db, `SELECT COALESCE(MAX(position) + 1, 0) FROM board_moderators WHERE board_id=?`, boardID).Scan(&position)
		return position, err
	}
	if position, ok, err := nativeCurrentBoardModeratorPosition(db, boardID, userID); err != nil || ok {
		return position, err
	}
	var position int
	err := qQueryRow(db,
		`SELECT position
		   FROM board_moderator_terms
		  WHERE board_id=? AND user_id=? AND ended_at=? AND removed_by=?
		  ORDER BY started_at DESC
		  LIMIT 1`,
		boardID, userID, ts, actorID,
	).Scan(&position)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return position, err
}

func nativeCurrentBoardModeratorPosition(db *sql.DB, boardID, userID string) (int, bool, error) {
	var position int
	err := qQueryRow(db, `SELECT position FROM board_moderators WHERE board_id=? AND user_id=?`, boardID, userID).Scan(&position)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return position, true, nil
}

func nativeBoardAllowsSyssecurityAudit(db *sql.DB, boardID string) (bool, error) {
	if strings.TrimSpace(boardID) == "" {
		return true, nil
	}
	settings, err := getBoardSettings(db, boardID)
	if err != nil {
		return false, err
	}
	return settings == nil || !settings.MemberReadMode, nil
}

func nativeRepostBody(sourcePost *Post, sourceThread *Thread) string {
	if sourcePost == nil || sourceThread == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf(
		"Reposted from %s / %s\nOriginal author: %s\nOriginal post: %s\n\n%s",
		sourceThread.Board,
		sourceThread.Title,
		sourcePost.Author,
		sourcePost.ID,
		sourcePost.Body,
	))
}

func nativeSyssecuritySystemLogEvents(db *sql.DB, record CommandLogRecord, actor *User, title string, lines []string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Security notice"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s\n", line)
	}
	b.WriteString("\nGenerated security notices omit private notes and article content.\n")
	body := b.String()

	events := make([]EventAppend, 0, 3)
	var exists int
	err := qQueryRow(db, `SELECT 1 FROM boards WHERE id=?`, nativeSyssecuritySystemBoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, posErr := nativeBoardCategoryPosition(db, "")
		if posErr != nil {
			return nil, nativeDecisionErr("internal_error", posErr.Error(), true)
		}
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, startIndex),
			Kind:   proto.EvtBoardCreated,
			Scopes: []string{"board:" + nativeSyssecuritySystemBoardID},
			Payload: &proto.BoardCreatedPayload{
				ID:          nativeSyssecuritySystemBoardID,
				Name:        "syssecurity",
				Description: "Generated security and administration audit log",
				Position:    position,
				By:          actor.ID,
				TS:          ts,
			},
			TS: ts,
		})
	} else if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	threadID := stableCommandLogDecisionID("syssecurity_thr_", record, startIndex)
	postID := stableCommandLogDecisionID("syssecurity_pst_", record, startIndex)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, startIndex+1),
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:" + nativeSyssecuritySystemBoardID},
		Payload: &proto.ThreadNewPayload{
			ID:       threadID,
			Board:    nativeSyssecuritySystemBoardID,
			Author:   actor.Name,
			AuthorID: actor.ID,
			Title:    title,
			TS:       ts,
		},
		TS: ts,
	})
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, startIndex+2),
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:" + nativeSyssecuritySystemBoardID, "thread:" + threadID},
		Payload: &proto.PostAppendedPayload{
			ID:          postID,
			Thread:      threadID,
			Author:      actor.Name,
			AuthorID:    actor.ID,
			Body:        body,
			RawBody:     body,
			ContentType: "markup",
			TS:          ts,
		},
		TS: ts,
	})
	return events, nil
}

func nativeSysmailSystemLogEvents(db *sql.DB, record CommandLogRecord, actor *User, mailID, subject, mailBody string, recipientCount int, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	threadID := "sysmail_thr_" + mailID
	postID := "sysmail_pst_" + mailID
	var exists int
	err := qQueryRow(db, `SELECT 1 FROM threads WHERE id=?`, threadID).Scan(&exists)
	if err == nil {
		return nil, nil
	}
	if err != sql.ErrNoRows {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}

	events := make([]EventAppend, 0, 3)
	err = qQueryRow(db, `SELECT 1 FROM boards WHERE id=?`, nativeSysmailSystemBoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, posErr := nativeBoardCategoryPosition(db, "")
		if posErr != nil {
			return nil, nativeDecisionErr("internal_error", posErr.Error(), true)
		}
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, startIndex),
			Kind:   proto.EvtBoardCreated,
			Scopes: []string{"board:" + nativeSysmailSystemBoardID},
			Payload: &proto.BoardCreatedPayload{
				ID:          nativeSysmailSystemBoardID,
				Name:        "sysmail",
				Description: "Generated restricted sysop mail log",
				Position:    position,
				By:          actor.ID,
				TS:          ts,
			},
			TS: ts,
		})
	} else if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}

	title := "Sysop mail: " + subject
	body := nativeFormatSysmailSystemBody(mailID, subject, mailBody, actor.Name, recipientCount)
	threadEventIndex := startIndex + len(events)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, threadEventIndex),
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:" + nativeSysmailSystemBoardID},
		Payload: &proto.ThreadNewPayload{
			ID:       threadID,
			Board:    nativeSysmailSystemBoardID,
			Author:   actor.Name,
			AuthorID: actor.ID,
			Title:    title,
			TS:       ts,
		},
		TS: ts,
	})
	postEventIndex := startIndex + len(events)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, postEventIndex),
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:" + nativeSysmailSystemBoardID, "thread:" + threadID},
		Payload: &proto.PostAppendedPayload{
			ID:          postID,
			Thread:      threadID,
			Author:      actor.Name,
			AuthorID:    actor.ID,
			Body:        body,
			RawBody:     body,
			ContentType: "markup",
			TS:          ts,
		},
		TS: ts,
	})
	return events, nil
}

func nativeFormatSysmailSystemBody(mailID, subject, mailBody, actorName string, recipientCount int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Sysop mail: %s\n\n", subject)
	fmt.Fprintf(&b, "- Mail: %s\n", mailID)
	fmt.Fprintf(&b, "- From: %s\n", actorName)
	if recipientCount == 1 {
		b.WriteString("- Recipients: 1 user\n")
	} else {
		fmt.Fprintf(&b, "- Recipients: %d users\n", recipientCount)
	}
	b.WriteString("- Source: admin mail-all broadcast\n\n")
	b.WriteString(mailBody)
	if !strings.HasSuffix(mailBody, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\nGenerated restricted sysop mail record.\n")
	return b.String()
}

func nativeBlessingSystemLogEvents(db *sql.DB, record CommandLogRecord, actor, target *User, blessingID, message string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	if target == nil {
		return nil, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	threadID := "blessing_thr_" + blessingID
	postID := "blessing_pst_" + blessingID
	var exists int
	err := qQueryRow(db, `SELECT 1 FROM threads WHERE id=?`, threadID).Scan(&exists)
	if err == nil {
		return nil, nil
	}
	if err != sql.ErrNoRows {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}

	events := make([]EventAppend, 0, 3)
	err = qQueryRow(db, `SELECT 1 FROM boards WHERE id=?`, nativeBlessingSystemBoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, posErr := nativeBoardCategoryPosition(db, "")
		if posErr != nil {
			return nil, nativeDecisionErr("internal_error", posErr.Error(), true)
		}
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, startIndex),
			Kind:   proto.EvtBoardCreated,
			Scopes: []string{"board:" + nativeBlessingSystemBoardID},
			Payload: &proto.BoardCreatedPayload{
				ID:          nativeBlessingSystemBoardID,
				Name:        "Blessing",
				Description: "Generated blessing rituals and rankings",
				Position:    position,
				By:          actor.ID,
				TS:          ts,
			},
			TS: ts,
		})
	} else if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}

	title := "Blessing: " + actor.Name + " -> " + target.Name
	body := nativeFormatBlessingSystemBody(actor.Name, target.Name, message)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, startIndex+1),
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:" + nativeBlessingSystemBoardID},
		Payload: &proto.ThreadNewPayload{
			ID:       threadID,
			Board:    nativeBlessingSystemBoardID,
			Author:   actor.Name,
			AuthorID: actor.ID,
			Title:    title,
			TS:       ts,
		},
		TS: ts,
	})
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, startIndex+2),
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:" + nativeBlessingSystemBoardID, "thread:" + threadID},
		Payload: &proto.PostAppendedPayload{
			ID:          postID,
			Thread:      threadID,
			Author:      actor.Name,
			AuthorID:    actor.ID,
			Body:        body,
			RawBody:     body,
			ContentType: "markup",
			TS:          ts,
		},
		TS: ts,
	})
	return events, nil
}

func nativeDenyPostSystemLogEvents(db *sql.DB, record CommandLogRecord, actor, target *User, sourceBoardID, sourceBoardName, kind, reason string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	return nativeSanctionSystemLogEvents(db, record, nativeDenyPostSystemBoardID, "denypost", "Generated board posting deny records", actor, target, sourceBoardID, sourceBoardName, kind, reason, "Board posting denied", ts, startIndex)
}

func nativeUndenyPostSystemLogEvents(db *sql.DB, record CommandLogRecord, actor, target *User, sourceBoardID, sourceBoardName, kind, reason string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	return nativeSanctionSystemLogEvents(db, record, nativeUndenyPostSystemBoardID, "undenypost", "Generated board posting restore records", actor, target, sourceBoardID, sourceBoardName, kind, reason, "Board posting restored", ts, startIndex)
}

func nativeSanctionSystemLogEvents(db *sql.DB, record CommandLogRecord, systemBoardID, systemBoardName, systemBoardDescription string, actor, target *User, sourceBoardID, sourceBoardName, kind, reason, action string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	if target == nil {
		return nil, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	settings, err := getBoardSettings(db, sourceBoardID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings != nil && settings.MemberReadMode {
		return nil, nil
	}

	events := make([]EventAppend, 0, 3)
	position, err := nativeBoardCategoryUpsertPosition(db, systemBoardID, "")
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, startIndex),
		Kind:   proto.EvtBoardCreated,
		Scopes: []string{"board:" + systemBoardID},
		Payload: &proto.BoardCreatedPayload{
			ID:          systemBoardID,
			Name:        systemBoardName,
			Description: systemBoardDescription,
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		},
		TS: ts,
	})

	threadID := stableCommandLogDecisionID(systemBoardID+"_thr_", record, startIndex)
	postID := stableCommandLogDecisionID(systemBoardID+"_pst_", record, startIndex)
	title := fmt.Sprintf("%s: %s on %s", action, target.Name, sourceBoardID)
	body := nativeFormatSanctionSystemBody(action, target.Name, sourceBoardName, sourceBoardID, kind, actor.Name, reason)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, startIndex+1),
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:" + systemBoardID},
		Payload: &proto.ThreadNewPayload{
			ID:       threadID,
			Board:    systemBoardID,
			Author:   actor.Name,
			AuthorID: actor.ID,
			Title:    title,
			TS:       ts,
		},
		TS: ts,
	})
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, startIndex+2),
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:" + systemBoardID, "thread:" + threadID},
		Payload: &proto.PostAppendedPayload{
			ID:          postID,
			Thread:      threadID,
			Author:      actor.Name,
			AuthorID:    actor.ID,
			Body:        body,
			RawBody:     body,
			ContentType: "markup",
			TS:          ts,
		},
		TS: ts,
	})
	return events, nil
}

func nativeFormatSanctionSystemBody(action, targetName, sourceBoardName, sourceBoardID, kind, actorName, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", action)
	fmt.Fprintf(&b, "- Action: %s\n", strings.ToLower(action))
	fmt.Fprintf(&b, "- User: %s\n", targetName)
	fmt.Fprintf(&b, "- Board: %s (%s)\n", sourceBoardName, sourceBoardID)
	if strings.TrimSpace(kind) == "" {
		fmt.Fprintf(&b, "- Kind: all\n")
	} else {
		fmt.Fprintf(&b, "- Kind: %s\n", strings.TrimSpace(kind))
	}
	fmt.Fprintf(&b, "- Actor: %s\n", actorName)
	if strings.TrimSpace(reason) != "" {
		fmt.Fprintf(&b, "- Reason: %s\n", strings.TrimSpace(reason))
	}
	b.WriteString("\nGenerated public board-posting sanction record. Private moderation notes and article bodies are not mirrored.\n")
	return b.String()
}

func nativePublicBoardForModerationLog(db *sql.DB, boardID string) (bool, error) {
	settings, err := getBoardSettings(db, boardID)
	if err != nil {
		return false, err
	}
	return settings == nil || !settings.MemberReadMode, nil
}

func nativeModerationReviewLogTarget(db *sql.DB, reviewID string) (postID, threadID, boardID string, public bool, errDetail *proto.ErrorDetail) {
	var targetKind string
	var status string
	err := qQueryRow(db, `SELECT target_id, target_kind, status FROM moderation_reviews WHERE id=?`, reviewID).Scan(&postID, &targetKind, &status)
	if err == sql.ErrNoRows {
		return "", "", "", false, nativeDecisionErr(proto.ErrNotFound, "review not found", false)
	}
	if err != nil {
		return "", "", "", false, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if status != "open" {
		return "", "", "", false, nativeDecisionErr(proto.ErrNotFound, "review not found", false)
	}
	if targetKind != "post" {
		return postID, "", "", false, nil
	}
	post, err := getPost(db, postID)
	if err != nil {
		return postID, "", "", false, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return postID, "", "", false, nil
	}
	thread, err := getThread(db, post.Thread)
	if err != nil {
		return postID, post.Thread, "", false, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return postID, post.Thread, "", false, nil
	}
	public, err = nativePublicBoardForModerationLog(db, thread.Board)
	if err != nil {
		return postID, post.Thread, thread.Board, false, nativeDecisionErr("internal_error", err.Error(), true)
	}
	return postID, post.Thread, thread.Board, public, nil
}

func nativeModerationSystemLogEvents(db *sql.DB, record CommandLogRecord, actor *User, action, reviewID, postID, threadID, boardID string, publicBoard bool, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if !publicBoard {
		return nil, nil
	}
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	position, err := nativeBoardCategoryPosition(db, "")
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	events := []EventAppend{
		{
			ID:     stableCommandLogDecisionID("evt_", record, startIndex),
			Kind:   proto.EvtBoardCreated,
			Scopes: []string{"board:" + nativeModerationSystemBoardID},
			Payload: &proto.BoardCreatedPayload{
				ID:          nativeModerationSystemBoardID,
				Name:        "0Moderation",
				Description: "Generated moderation audit log",
				Position:    position,
				By:          actor.ID,
				TS:          ts,
			},
			TS: ts,
		},
	}
	title := "Moderation flag " + reviewID
	statusLine := "opened"
	if action == moderationLogResolve {
		title = "Moderation resolved " + reviewID
		statusLine = "resolved"
	}
	body := fmt.Sprintf("# %s\n\n- Review: %s\n- Status: %s\n- Board: %s\n- Thread: %s\n- Post: %s\n- Actor: %s\n\nSensitive report and resolution text is kept in the moderator review queue.\n",
		title, reviewID, statusLine, boardID, threadID, postID, actor.Name)
	threadIDOut := "mod_" + action + "_thr_" + reviewID
	postIDOut := "mod_" + action + "_pst_" + reviewID
	threadEventIndex := startIndex + len(events)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, threadEventIndex),
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:" + nativeModerationSystemBoardID},
		Payload: &proto.ThreadNewPayload{
			ID:       threadIDOut,
			Board:    nativeModerationSystemBoardID,
			Author:   actor.Name,
			AuthorID: actor.ID,
			Title:    title,
			TS:       ts,
		},
		TS: ts,
	})
	postEventIndex := startIndex + len(events)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, postEventIndex),
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:" + nativeModerationSystemBoardID, "thread:" + threadIDOut},
		Payload: &proto.PostAppendedPayload{
			ID:          postIDOut,
			Thread:      threadIDOut,
			Author:      actor.Name,
			AuthorID:    actor.ID,
			Body:        body,
			RawBody:     body,
			ContentType: "markup",
			TS:          ts,
		},
		TS: ts,
	})
	return events, nil
}

func nativeContentFilterReviewEvents(db *sql.DB, record CommandLogRecord, actor *User, publicAuthor string, filter *ContentFilter, postID, threadID, boardID string, publicBoard bool, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if filter == nil {
		return nil, nil
	}
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	reviewID := stableCommandLogDecisionID("rev_", record, startIndex)
	reason := "Matched content filter " + strings.TrimSpace(filter.ID)
	events := []EventAppend{
		{
			ID:     stableCommandLogDecisionID("evt_", record, startIndex),
			Kind:   proto.EvtPostFlagged,
			Scopes: []string{"thread:" + threadID, "board:" + boardID, "moderation:global"},
			Payload: &proto.PostFlaggedPayload{
				ReviewID: reviewID,
				Kind:     "content_filter",
				PostID:   postID,
				Thread:   threadID,
				Reporter: actor.ID,
				Reason:   reason,
				TS:       ts,
			},
			TS: ts,
		},
	}
	if !publicBoard {
		return events, nil
	}
	position, err := nativeBoardCategoryPosition(db, "")
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	boardEventIndex := startIndex + len(events)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, boardEventIndex),
		Kind:   proto.EvtBoardCreated,
		Scopes: []string{"board:" + nativeFilterSystemBoardID},
		Payload: &proto.BoardCreatedPayload{
			ID:          nativeFilterSystemBoardID,
			Name:        "Filter",
			Description: "Generated content filter review log",
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		},
		TS: ts,
	})

	threadIDOut := "filter_thr_" + reviewID
	postIDOut := "filter_pst_" + reviewID
	title := "Content filter review " + reviewID
	body := nativeFormatContentFilterReviewBody(title, reviewID, filter, boardID, threadID, postID, publicAuthor)
	threadEventIndex := startIndex + len(events)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, threadEventIndex),
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:" + nativeFilterSystemBoardID},
		Payload: &proto.ThreadNewPayload{
			ID:       threadIDOut,
			Board:    nativeFilterSystemBoardID,
			Author:   actor.Name,
			AuthorID: actor.ID,
			Title:    title,
			TS:       ts,
		},
		TS: ts,
	})
	postEventIndex := startIndex + len(events)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, postEventIndex),
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:" + nativeFilterSystemBoardID, "thread:" + threadIDOut},
		Payload: &proto.PostAppendedPayload{
			ID:          postIDOut,
			Thread:      threadIDOut,
			Author:      actor.Name,
			AuthorID:    actor.ID,
			Body:        body,
			RawBody:     body,
			ContentType: "markup",
			TS:          ts,
		},
		TS: ts,
	})
	return events, nil
}

func nativeBoardCategoryPosition(db *sql.DB, parentID string) (int, error) {
	var next int
	err := qQueryRow(db, `SELECT COALESCE(MAX(position) + 1, 0) FROM categories WHERE parent_id=?`, parentID).Scan(&next)
	return next, err
}

func nativeBoardCategoryPositionForCreate(db *sql.DB, boardID, parentID string, requested *int) (int, error) {
	if requested != nil {
		return *requested, nil
	}
	return nativeBoardCategoryUpsertPosition(db, boardID, parentID)
}

func nativeBoardCategoryUpsertPosition(db *sql.DB, boardID, parentID string) (int, error) {
	var position int
	err := qQueryRow(db, `SELECT position FROM categories WHERE id=?`, boardID).Scan(&position)
	if err == nil {
		return position, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	return nativeBoardCategoryPosition(db, parentID)
}

func nativeBoardExists(db *sql.DB, boardID string) (bool, error) {
	var exists int
	err := qQueryRow(db, `SELECT 1 FROM boards WHERE id=?`, boardID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func nativeBoardCanBePubliclyRecommended(db *sql.DB, boardID string) (bool, string, error) {
	if nativeGeneratedSystemBoardIDSet[boardID] {
		return false, "generated system boards cannot be recommended", nil
	}
	var visibility string
	var memberReadMode, statsExcluded int
	err := qQueryRow(
		db,
		`SELECT COALESCE(c.visibility, 'public'),
		        COALESCE(s.member_read_mode, 0),
		        COALESCE(s.stats_excluded, 0)
		   FROM boards b
		   LEFT JOIN categories c ON c.id=b.id
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  WHERE b.id=?`,
		boardID,
	).Scan(&visibility, &memberReadMode, &statsExcluded)
	if err != nil {
		return false, "", err
	}
	if strings.ToLower(strings.TrimSpace(visibility)) != "public" {
		return false, "only public directory boards can be recommended", nil
	}
	if memberReadMode != 0 {
		return false, "member-read boards cannot be publicly recommended", nil
	}
	if statsExcluded != 0 {
		return false, "stats-excluded boards cannot be publicly recommended", nil
	}
	return true, "", nil
}

func nativeRecommendedBoardPosition(db *sql.DB, boardID string, requested *int) (int, error) {
	if requested != nil {
		return *requested, nil
	}
	var position int
	err := qQueryRow(db, `SELECT position FROM recommended_boards WHERE board_id=?`, boardID).Scan(&position)
	if err == nil {
		return position, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	err = qQueryRow(db, `SELECT COALESCE(MAX(position), -10) + 10 FROM recommended_boards`).Scan(&position)
	return position, err
}

func nativeCategoryExists(db *sql.DB, categoryID string) (bool, error) {
	var exists int
	err := qQueryRow(db, `SELECT 1 FROM categories WHERE id=?`, categoryID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func nativeCreatedBoardMatches(db *sql.DB, id, name, description, parentID string, position int) (bool, error) {
	var gotName, gotDescription, gotParentID string
	var gotPosition int
	err := qQueryRow(db,
		`SELECT b.name, b.description, c.parent_id, c.position
		   FROM boards b
		   JOIN categories c ON c.id=b.id
		  WHERE b.id=?`,
		id,
	).Scan(&gotName, &gotDescription, &gotParentID, &gotPosition)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return gotName == name && gotDescription == description && gotParentID == parentID && gotPosition == position, nil
}

func nativeIsValidSlug(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func nativeNormalizeDigestKind(kind string) (string, *proto.ErrorDetail) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "digest"
	}
	switch kind {
	case "digest", "archive", "recommended", "pinned", "announcement":
		return kind, nil
	default:
		return "", nativeDecisionErr(proto.ErrValidationFailed, `kind must be "digest", "archive", "recommended", "pinned", or "announcement"`, false)
	}
}

func nativeDigestCurationPermissionMessage(kind string) string {
	if kind == "announcement" {
		return "board announcement permission required"
	}
	return "board curator permission required"
}

func nativeNormalizeDigestPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "/")
	if len(path) > 120 {
		return path[:120]
	}
	return path
}

func nativeDigestEventScopes(boardID string) []string {
	return []string{"board:" + boardID, "digest:" + boardID, "digest:global"}
}

func nativeDigestEntryID(db *sql.DB, boardID, targetKind, targetID, kind, path string) (string, error) {
	var id string
	err := qQueryRow(db,
		`SELECT id FROM digest_entries
		  WHERE board_id=? AND target_kind=? AND target_id=? AND kind=? AND path=?`,
		boardID, targetKind, targetID, kind, path,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

func nativeDigestEntryForCuration(db *sql.DB, actor *User, entryID string) (nativeDigestEntryForCommand, *proto.ErrorDetail) {
	var entry nativeDigestEntryForCommand
	err := qQueryRow(
		db,
		`SELECT id, board_id, target_kind, target_id, kind, title, path, note
		   FROM digest_entries
		  WHERE id=?`,
		entryID,
	).Scan(&entry.ID, &entry.BoardID, &entry.TargetKind, &entry.TargetID, &entry.Kind, &entry.Title, &entry.Path, &entry.Note)
	if err == sql.ErrNoRows {
		return entry, nativeDecisionErr(proto.ErrNotFound, "digest entry not found", false)
	}
	if err != nil {
		return entry, nativeDecisionErr("internal_error", err.Error(), true)
	}
	canCurate, err := nativeActorCanCurateBoardKind(db, actor, entry.BoardID, entry.Kind)
	if err != nil {
		return entry, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canCurate {
		return entry, nativeDecisionErr(proto.ErrForbidden, nativeDigestCurationPermissionMessage(entry.Kind), false)
	}
	return entry, nil
}

func nativeDigestEntryRemoval(db *sql.DB, entryID string) (nativeDigestEntryRemovalRecord, bool, error) {
	var removal nativeDigestEntryRemovalRecord
	err := qQueryRow(
		db,
		`SELECT id, board_id, kind, removed_by
		   FROM digest_entry_removals
		  WHERE id=?`,
		entryID,
	).Scan(&removal.ID, &removal.BoardID, &removal.Kind, &removal.RemovedBy)
	if err == sql.ErrNoRows {
		return removal, false, nil
	}
	if err != nil {
		return removal, false, err
	}
	return removal, true, nil
}

func nativeValidateDigestEntryCommandPartition(record CommandLogRecord, entry nativeDigestEntryForCommand) *proto.ErrorDetail {
	partition := record.Partition.Normalize()
	if partition.Kind == partitionBoard && (partition.Key == entry.ID || partition.Key == entry.BoardID) {
		return nil
	}
	return nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
		partition.Kind, partition.Key, partitionBoard, entry.ID), false)
}

func nativeDigestEntryPathConflict(db *sql.DB, entry nativeDigestEntryForCommand, path string) (bool, error) {
	var conflictID string
	err := qQueryRow(
		db,
		`SELECT id
		   FROM digest_entries
		  WHERE board_id=? AND target_kind=? AND target_id=? AND kind=? AND path=? AND id<>?
		  LIMIT 1`,
		entry.BoardID, entry.TargetKind, entry.TargetID, entry.Kind, path, entry.ID,
	).Scan(&conflictID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func nativeDigestDirectoryID(db *sql.DB, boardID, kind, path string) (string, error) {
	var id string
	err := qQueryRow(db,
		`SELECT id FROM digest_directories WHERE board_id=? AND kind=? AND path=?`,
		boardID, kind, path,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

func nativeDigestPathEntriesForCopy(db *sql.DB, record CommandLogRecord, boardID, kind, path string) ([]nativeDigestPathEntryForCopy, error) {
	rows, err := qQuery(db,
		`SELECT id, board_id, target_kind, target_id, kind, path
		   FROM digest_entries
		  WHERE board_id=? AND kind=?
		  ORDER BY path, title, id`,
		boardID, kind,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []nativeDigestPathEntryForCopy{}
	for rows.Next() {
		var entry nativeDigestPathEntryForCopy
		if err := rows.Scan(&entry.ID, &entry.BoardID, &entry.TargetKind, &entry.TargetID, &entry.Kind, &entry.Path); err != nil {
			return nil, err
		}
		if nativeDigestPathContains(path, entry.Path) {
			entries = append(entries, entry)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	generated := nativeStableDecisionIDSet("dig_", record, len(entries))
	out := entries[:0]
	for _, entry := range entries {
		if _, copiedByThisCommand := generated[entry.ID]; copiedByThisCommand {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func nativeDigestPathDirectoriesForCopy(db *sql.DB, record CommandLogRecord, boardID, kind, path string) ([]nativeDigestPathDirectoryForCopy, error) {
	rows, err := qQuery(db,
		`SELECT id, board_id, kind, path
		   FROM digest_directories
		  WHERE board_id=? AND kind=?
		  ORDER BY path, id`,
		boardID, kind,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dirs := []nativeDigestPathDirectoryForCopy{}
	for rows.Next() {
		var dir nativeDigestPathDirectoryForCopy
		if err := rows.Scan(&dir.ID, &dir.BoardID, &dir.Kind, &dir.Path); err != nil {
			return nil, err
		}
		if nativeDigestPathContains(path, dir.Path) {
			dirs = append(dirs, dir)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	generated := nativeStableDecisionIDSet("dir_", record, len(dirs))
	out := dirs[:0]
	for _, dir := range dirs {
		if _, copiedByThisCommand := generated[dir.ID]; copiedByThisCommand {
			continue
		}
		out = append(out, dir)
	}
	return out, nil
}

func nativeDigestPathMutationCount(db *sql.DB, eventID, action string) (int, bool, error) {
	var count int
	err := qQueryRow(db, `SELECT count FROM digest_path_mutations WHERE event_id=? AND action=?`, eventID, action).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return count, true, nil
}

func nativeStableDecisionIDSet(prefix string, record CommandLogRecord, count int) map[string]struct{} {
	ids := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		ids[stableCommandLogDecisionID(prefix, record, i)] = struct{}{}
	}
	return ids
}

func nativeDigestPathCopyEntryConflict(db *sql.DB, entry nativeDigestPathEntryForCopy, newPath, expectedID string) (bool, error) {
	rows, err := qQuery(db,
		`SELECT id
		   FROM digest_entries
		  WHERE board_id=? AND target_kind=? AND target_id=? AND kind=? AND path=?`,
		entry.BoardID, entry.TargetKind, entry.TargetID, entry.Kind, newPath,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return false, err
		}
		if id != expectedID {
			return true, nil
		}
	}
	return false, rows.Err()
}

func nativeDigestPathMoveEntryConflict(db *sql.DB, entry nativeDigestPathEntryForCopy, newPath string, movingIDs map[string]struct{}) (bool, error) {
	rows, err := qQuery(db,
		`SELECT id
		   FROM digest_entries
		  WHERE board_id=? AND target_kind=? AND target_id=? AND kind=? AND path=?`,
		entry.BoardID, entry.TargetKind, entry.TargetID, entry.Kind, newPath,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return false, err
		}
		if _, moving := movingIDs[id]; !moving {
			return true, nil
		}
	}
	return false, rows.Err()
}

func nativeDigestPathMoveDirectoryConflict(db *sql.DB, dir nativeDigestPathDirectoryForCopy, newPath string, movingIDs map[string]struct{}) (bool, error) {
	rows, err := qQuery(db,
		`SELECT id
		   FROM digest_directories
		  WHERE board_id=? AND kind=? AND path=?`,
		dir.BoardID, dir.Kind, newPath,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return false, err
		}
		if _, moving := movingIDs[id]; !moving {
			return true, nil
		}
	}
	return false, rows.Err()
}

func nativeDigestPathCopyDirectoryConflict(db *sql.DB, dir nativeDigestPathDirectoryForCopy, newPath, expectedID string) (bool, error) {
	rows, err := qQuery(db,
		`SELECT id
		   FROM digest_directories
		  WHERE board_id=? AND kind=? AND path=?`,
		dir.BoardID, dir.Kind, newPath,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return false, err
		}
		if id != expectedID {
			return true, nil
		}
	}
	return false, rows.Err()
}

func nativeDigestPathContains(parent, child string) bool {
	if parent == "" {
		return child == ""
	}
	return child == parent || strings.HasPrefix(child, parent+"/")
}

func nativeRemapDigestPath(path, fromPath, toPath string) string {
	if path == fromPath {
		return toPath
	}
	suffix := strings.TrimPrefix(path, fromPath+"/")
	if toPath == "" {
		return suffix
	}
	if suffix == "" {
		return toPath
	}
	return toPath + "/" + suffix
}

func nativeDigestMirrorForKind(kind string) (nativeDigestMirrorSystemBoard, bool) {
	switch kind {
	case "announcement":
		return nativeDigestMirrorSystemBoard{
			Kind:        "announcement",
			BoardID:     nativeAnnouncementSystemBoardID,
			Name:        "0Announce",
			Description: "Generated site-wide announcements",
			ThreadID:    "ann_thr_",
			PostID:      "ann_pst_",
			Default:     "Announcement",
		}, true
	case "recommended":
		return nativeDigestMirrorSystemBoard{
			Kind:        "recommended",
			BoardID:     nativeRecommendSystemBoardID,
			Name:        "Recommend",
			Description: "Generated recommended articles and homepage recommendations",
			ThreadID:    "recommend_thr_",
			PostID:      "recommend_pst_",
			Default:     "Recommended article",
		}, true
	default:
		return nativeDigestMirrorSystemBoard{}, false
	}
}

func nativeDigestExportForCuration(db *sql.DB, entry *proto.DigestEntryUpsertedPayload, thread *Thread, post *Post) (*DigestExport, *proto.ErrorDetail) {
	if entry == nil {
		return nil, nativeDecisionErr(proto.ErrValidationFailed, "digest entry is required", false)
	}
	if thread == nil {
		return nil, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	board, err := getBoard(db, entry.Board)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	boardName := ""
	if board != nil {
		boardName = board.Name
	}
	author := thread.Author
	threadID := thread.ID
	postID := ""
	body := ""
	if entry.TargetKind == "post" {
		if post == nil {
			return nil, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
		}
		author = post.Author
		postID = post.ID
		body = post.Body
	} else {
		threadBody, err := nativeDigestThreadTranscript(db, thread.ID)
		if err != nil {
			return nil, nativeDecisionErr("internal_error", err.Error(), true)
		}
		body = threadBody
	}
	var bodyEdited int
	var editedBody string
	err = qQueryRow(db, `SELECT body_edited, body FROM digest_entries WHERE id=?`, entry.ID).Scan(&bodyEdited, &editedBody)
	if err != nil && err != sql.ErrNoRows {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if err == nil && bodyEdited != 0 {
		body = editedBody
	}
	return &DigestExport{
		Entry: DigestEntry{
			ID:         entry.ID,
			BoardID:    entry.Board,
			BoardName:  boardName,
			TargetKind: entry.TargetKind,
			TargetID:   entry.TargetID,
			Kind:       entry.Kind,
			Title:      entry.Title,
			Path:       entry.Path,
			Note:       entry.Note,
			CreatedBy:  entry.CreatedBy,
			ThreadID:   threadID,
			PostID:     postID,
			Author:     author,
			BodyEdited: bodyEdited != 0,
		},
		Body: body,
	}, nil
}

func nativeDigestThreadTranscript(db *sql.DB, threadID string) (string, error) {
	rows, err := qQuery(db,
		`SELECT author, body
		   FROM posts
		  WHERE thread=? AND redacted=0
		  ORDER BY created_seq`,
		threadID,
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var author, body string
		if err := rows.Scan(&author, &body); err != nil {
			return "", err
		}
		if b.Len() > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString("From: ")
		b.WriteString(author)
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	return b.String(), rows.Err()
}

func nativeDigestMirrorSystemLogEvents(db *sql.DB, record CommandLogRecord, actor *User, entryID string, export *DigestExport, mirror nativeDigestMirrorSystemBoard, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	if export == nil {
		return nil, nil
	}
	if export.Entry.Kind != mirror.Kind || export.Entry.BoardID == mirror.BoardID {
		return nil, nil
	}
	settings, err := getBoardSettings(db, export.Entry.BoardID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings != nil && settings.MemberReadMode {
		return nil, nil
	}

	threadID := mirror.ThreadID + entryID
	postID := mirror.PostID + entryID
	var exists int
	err = qQueryRow(db, `SELECT 1 FROM threads WHERE id=?`, threadID).Scan(&exists)
	if err == nil {
		return nil, nil
	}
	if err != sql.ErrNoRows {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}

	events := make([]EventAppend, 0, 3)
	err = qQueryRow(db, `SELECT 1 FROM boards WHERE id=?`, mirror.BoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, posErr := nativeBoardCategoryPosition(db, "")
		if posErr != nil {
			return nil, nativeDecisionErr("internal_error", posErr.Error(), true)
		}
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, startIndex),
			Kind:   proto.EvtBoardCreated,
			Scopes: []string{"board:" + mirror.BoardID},
			Payload: &proto.BoardCreatedPayload{
				ID:          mirror.BoardID,
				Name:        mirror.Name,
				Description: mirror.Description,
				Position:    position,
				By:          actor.ID,
				TS:          ts,
			},
			TS: ts,
		})
	} else if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}

	title := strings.TrimSpace(export.Entry.Title)
	if title == "" {
		title = mirror.Default
	}
	body := projections.FormatDigestExportText(export)
	scopes := []string{"board:" + mirror.BoardID}
	threadEventIndex := startIndex + len(events)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, threadEventIndex),
		Kind:   proto.EvtThreadNew,
		Scopes: scopes,
		Payload: &proto.ThreadNewPayload{
			ID:       threadID,
			Board:    mirror.BoardID,
			Author:   actor.Name,
			AuthorID: actor.ID,
			Title:    title,
			TS:       ts,
		},
		TS: ts,
	})
	postEventIndex := startIndex + len(events)
	events = append(events, EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, postEventIndex),
		Kind:   proto.EvtPostAppended,
		Scopes: append(scopes, "thread:"+threadID),
		Payload: &proto.PostAppendedPayload{
			ID:          postID,
			Thread:      threadID,
			Author:      actor.Name,
			AuthorID:    actor.ID,
			Body:        body,
			RawBody:     body,
			ContentType: "markup",
			TS:          ts,
		},
		TS: ts,
	})
	return events, nil
}

func nativeFindUserRef(db *sql.DB, ref string) (*User, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	u := &User{}
	err := qQueryRow(db,
		`SELECT id, name, role, password, created FROM users WHERE id=? OR name=? ORDER BY CASE WHEN id=? THEN 0 ELSE 1 END LIMIT 1`,
		ref, ref, ref,
	).Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func nativeDirectMessageAllowed(db *sql.DB, recipientID, senderID string) (bool, error) {
	settings, err := getDirectMessageSettings(db, recipientID)
	if err != nil {
		return false, err
	}
	policy := ""
	if settings != nil {
		policy = strings.TrimSpace(settings.Policy)
	}
	switch policy {
	case "", "all":
		return true, nil
	case "none":
		return false, nil
	case "friends":
		return nativeRelationshipExists(db, recipientID, senderID, "friend")
	default:
		return true, nil
	}
}

type nativeDirectMessageReadState struct {
	fromUserID       string
	toUserID         string
	readAt           int64
	recipientDeleted bool
}

func nativeDirectMessageReadTarget(db *sql.DB, messageID string) (nativeDirectMessageReadState, bool, error) {
	var state nativeDirectMessageReadState
	var recipientDeleted int
	err := qQueryRow(db,
		`SELECT from_user_id, to_user_id, read_at, recipient_deleted
		   FROM direct_messages
		  WHERE id=?`,
		messageID,
	).Scan(&state.fromUserID, &state.toUserID, &state.readAt, &recipientDeleted)
	if err == sql.ErrNoRows {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	state.recipientDeleted = recipientDeleted != 0
	return state, true, nil
}

type nativeDirectMessageDeleteState struct {
	fromUserID       string
	toUserID         string
	senderDeleted    bool
	recipientDeleted bool
}

func nativeDirectMessageDeleteTarget(db *sql.DB, messageID string) (nativeDirectMessageDeleteState, bool, error) {
	var state nativeDirectMessageDeleteState
	var senderDeleted int
	var recipientDeleted int
	err := qQueryRow(db,
		`SELECT from_user_id, to_user_id, sender_deleted, recipient_deleted
		   FROM direct_messages
		  WHERE id=?`,
		messageID,
	).Scan(&state.fromUserID, &state.toUserID, &senderDeleted, &recipientDeleted)
	if err == sql.ErrNoRows {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	state.senderDeleted = senderDeleted != 0
	state.recipientDeleted = recipientDeleted != 0
	return state, true, nil
}

func nativeResolveMailGroupID(db *sql.DB, ownerID, groupRef, name string, record CommandLogRecord) (string, *proto.ErrorDetail) {
	if groupRef != "" {
		groupID, err := projections.GetMailGroupID(db, ownerID, groupRef)
		if err != nil {
			return "", nativeDecisionErr("internal_error", err.Error(), true)
		}
		if groupID == "" {
			return "", nativeDecisionErr(proto.ErrNotFound, "mail group not found", false)
		}
		return groupID, nil
	}
	groupID, err := nativeMailGroupIDByName(db, ownerID, name)
	if err != nil {
		return "", nativeDecisionErr("internal_error", err.Error(), true)
	}
	if groupID != "" {
		return groupID, nil
	}
	return stableCommandLogDecisionID("mgrp_", record, 0), nil
}

func nativeMailGroupIDByName(db *sql.DB, ownerID, name string) (string, error) {
	var groupID string
	err := qQueryRow(db,
		`SELECT id FROM mail_groups WHERE user_id=? AND name=? LIMIT 1`,
		ownerID, strings.TrimSpace(name),
	).Scan(&groupID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return groupID, err
}

func nativeMailGroupDeletion(db *sql.DB, eventID string) (string, bool, error) {
	var groupID string
	err := qQueryRow(db, `SELECT group_id FROM mail_group_deletions WHERE event_id=?`, eventID).Scan(&groupID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return groupID, true, nil
}

func nativeResolveUniqueMailGroupMembers(db *sql.DB, refs []string, ownerID string) ([]string, *proto.ErrorDetail) {
	out := []string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		target, err := nativeFindUserRef(db, ref)
		if err != nil {
			return nil, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if target == nil {
			return nil, nativeDecisionErr(proto.ErrNotFound, "user not found: "+strings.TrimSpace(ref), false)
		}
		if target.ID == ownerID {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "mail group cannot include yourself", false)
		}
		if !seen[target.ID] {
			seen[target.ID] = true
			out = append(out, target.ID)
		}
	}
	return out, nil
}

func nativeNormalizeMailbox(mailbox string) (string, *proto.ErrorDetail) {
	mailbox = strings.TrimSpace(strings.ToLower(mailbox))
	switch mailbox {
	case "deleted", "delete":
		mailbox = "trash"
	}
	if mailbox == "" {
		return "", nativeDecisionErr(proto.ErrValidationFailed, "mailbox is required", false)
	}
	if len(mailbox) > 64 {
		return "", nativeDecisionErr(proto.ErrValidationFailed, "mailbox is too long", false)
	}
	for _, r := range mailbox {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return "", nativeDecisionErr(proto.ErrValidationFailed, "mailbox may contain only lowercase letters, numbers, hyphens, and underscores", false)
	}
	return mailbox, nil
}

type nativeMailCopyUpdateState struct {
	fromUserID    string
	trashedCopies int
}

func nativeMailCopyUpdateTarget(db *sql.DB, userID, mailID string) (nativeMailCopyUpdateState, bool, error) {
	var state nativeMailCopyUpdateState
	var trashedCopies sql.NullInt64
	err := qQueryRow(db,
		`SELECT m.from_user_id,
		        SUM(CASE WHEN c.mailbox='trash' THEN 1 ELSE 0 END)
		   FROM mail_messages m
		   JOIN mail_copies c ON c.message_id=m.id
		  WHERE c.user_id=? AND c.message_id=?
		  GROUP BY m.from_user_id`,
		userID, mailID,
	).Scan(&state.fromUserID, &trashedCopies)
	if err == sql.ErrNoRows {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	state.trashedCopies = int(trashedCopies.Int64)
	return state, true, nil
}

func nativeMailStoredSize(db *sql.DB, mailID string) (int64, error) {
	var size sql.NullInt64
	err := qQueryRow(db,
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

func nativeExpandMailRecipients(db *sql.DB, actor *User, payload proto.SendMailPayload) ([]string, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrForbidden, "authentication required", false)
	}
	refs := []string{}
	if payload.ToAll {
		if !actor.IsAdmin() {
			return nil, nativeDecisionErr(proto.ErrForbidden, "admin role required for mail-all", false)
		}
		allUserIDs, err := nativeListMailAllRecipients(db, actor.ID)
		if err != nil {
			return nil, nativeDecisionErr("internal_error", err.Error(), true)
		}
		refs = append(refs, allUserIDs...)
	}
	for _, ref := range payload.To {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if groupRef, ok := strings.CutPrefix(ref, "group:"); ok {
			payload.ToGroups = append(payload.ToGroups, strings.TrimSpace(groupRef))
			continue
		}
		refs = append(refs, ref)
	}
	for _, groupRef := range payload.ToGroups {
		groupRef = strings.TrimSpace(groupRef)
		if groupRef == "" {
			continue
		}
		if nativeIsFriendMailGroupRef(groupRef) {
			friendIDs, err := projections.ListFriendUserIDs(db, actor.ID)
			if err != nil {
				return nil, nativeDecisionErr("internal_error", err.Error(), true)
			}
			refs = append(refs, friendIDs...)
			continue
		}
		groupID, err := projections.GetMailGroupID(db, actor.ID, groupRef)
		if err != nil {
			return nil, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if groupID == "" {
			return nil, nativeDecisionErr(proto.ErrNotFound, "mail group not found: "+groupRef, false)
		}
		members, err := projections.ListMailGroupMembers(db, actor.ID, groupID)
		if err != nil {
			return nil, nativeDecisionErr("internal_error", err.Error(), true)
		}
		for _, member := range members {
			refs = append(refs, member.UserID)
		}
	}
	if payload.ToFriends {
		friendIDs, err := projections.ListFriendUserIDs(db, actor.ID)
		if err != nil {
			return nil, nativeDecisionErr("internal_error", err.Error(), true)
		}
		refs = append(refs, friendIDs...)
	}
	return refs, nil
}

func nativeListMailAllRecipients(db *sql.DB, actorID string) ([]string, error) {
	rows, err := qQuery(db, `SELECT id FROM users WHERE id<>? ORDER BY name`, actorID)
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

func nativeIsFriendMailGroupRef(ref string) bool {
	switch strings.ToLower(strings.TrimSpace(ref)) {
	case "friend", "friends", "@friends", "friend-list", "friends-list":
		return true
	default:
		return false
	}
}

func nativeNormalizeMailAttachments(record CommandLogRecord, input []proto.AttachmentPayload) ([]proto.AttachmentPayload, *proto.ErrorDetail) {
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > 8 {
		return nil, nativeDecisionErr(proto.ErrValidationFailed, "mail can have at most 8 attachments", false)
	}
	out := make([]proto.AttachmentPayload, 0, len(input))
	for i, item := range input {
		filename := strings.TrimSpace(item.Filename)
		contentType := strings.TrimSpace(item.ContentType)
		url := strings.TrimSpace(item.URL)
		if filename == "" {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "attachment filename is required", false)
		}
		if len(filename) > 160 {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "attachment filename must be 160 characters or less", false)
		}
		if len(contentType) > 120 {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "attachment content type must be 120 characters or less", false)
		}
		if len(url) > 500 {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "attachment URL must be 500 characters or less", false)
		}
		if item.SizeBytes < 0 {
			return nil, nativeDecisionErr(proto.ErrValidationFailed, "attachment size cannot be negative", false)
		}
		out = append(out, proto.AttachmentPayload{
			ID:          stableCommandLogDecisionID("matt_", record, 100+i),
			Filename:    filename,
			ContentType: contentType,
			SizeBytes:   item.SizeBytes,
			URL:         url,
		})
	}
	return out, nil
}

func nativeMailMessageSize(subject, body string, attachments []proto.AttachmentPayload) int64 {
	size := int64(len(subject) + len(body))
	for _, item := range attachments {
		size += item.SizeBytes
	}
	if size < 0 {
		return 0
	}
	return size
}

func nativeActorHasMailCopy(db *sql.DB, userID, messageID string) (bool, error) {
	var found int
	err := qQueryRow(db, `SELECT 1 FROM mail_copies WHERE user_id=? AND message_id=? LIMIT 1`, userID, messageID).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func nativeMailSender(db *sql.DB, mailID string) (string, bool, error) {
	var senderID string
	err := qQueryRow(db, `SELECT from_user_id FROM mail_messages WHERE id=?`, mailID).Scan(&senderID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return senderID, err == nil, err
}

func nativeActiveMailCopyCounts(db *sql.DB, mailID string) (map[string]int, error) {
	rows, err := qQuery(db, `SELECT user_id, COUNT(*) FROM mail_copies WHERE message_id=? AND mailbox <> 'trash' GROUP BY user_id`, mailID)
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

func nativeMailAccountScopes(db *sql.DB, mailID, actorID string) ([]string, error) {
	rows, err := qQuery(db, `SELECT DISTINCT user_id FROM mail_copies WHERE message_id=?`, mailID)
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

func nativeForwardMailSubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "Fwd: (no subject)"
	}
	if strings.HasPrefix(strings.ToLower(subject), "fwd:") {
		return subject
	}
	return "Fwd: " + subject
}

func nativeFormatForwardMailBody(source *MailItem, note string) string {
	if source == nil {
		return ""
	}
	var b strings.Builder
	if note = strings.TrimSpace(note); note != "" {
		b.WriteString(note)
		b.WriteString("\n\n")
	}
	b.WriteString("----- Forwarded mail -----\n")
	fmt.Fprintf(&b, "From: %s\n", source.FromName)
	if len(source.ToNames) > 0 {
		fmt.Fprintf(&b, "To: %s\n", strings.Join(source.ToNames, ", "))
	}
	fmt.Fprintf(&b, "Subject: %s\n", source.Subject)
	if len(source.Attachments) > 0 {
		names := make([]string, 0, len(source.Attachments))
		for _, attachment := range source.Attachments {
			names = append(names, attachment.Filename)
		}
		fmt.Fprintf(&b, "Attachments: %s\n", strings.Join(names, ", "))
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(source.Body))
	return b.String()
}

func nativeFormatMailBoardBody(source *MailItem, note string) string {
	if source == nil {
		return ""
	}
	var b strings.Builder
	if note = strings.TrimSpace(note); note != "" {
		b.WriteString(note)
		b.WriteString("\n\n")
	}
	b.WriteString("Posted from private mail.\n")
	fmt.Fprintf(&b, "From: %s\n", source.FromName)
	if len(source.ToNames) > 0 {
		fmt.Fprintf(&b, "To: %s\n", strings.Join(source.ToNames, ", "))
	}
	fmt.Fprintf(&b, "Subject: %s\n", source.Subject)
	if len(source.Attachments) > 0 {
		names := make([]string, 0, len(source.Attachments))
		for _, attachment := range source.Attachments {
			names = append(names, attachment.Filename)
		}
		fmt.Fprintf(&b, "Attachments: %s\n", strings.Join(names, ", "))
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(source.Body))
	return b.String()
}

func nativeFormatPostAuthorMailBody(thread *Thread, post *Post, senderName, note string) string {
	if thread == nil || post == nil {
		return strings.TrimSpace(note)
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(note))
	b.WriteString("\n\n---\n")
	b.WriteString("Sent from article reading context.\n")
	fmt.Fprintf(&b, "Board: %s\n", thread.Board)
	fmt.Fprintf(&b, "Thread: %s\n", thread.Title)
	fmt.Fprintf(&b, "Post: #%d (%s)\n", post.CreatedSeq, post.ID)
	fmt.Fprintf(&b, "Article author: %s\n", post.Author)
	fmt.Fprintf(&b, "Mail author: %s\n\n", senderName)
	b.WriteString("Article excerpt:\n")
	b.WriteString(nativeArticleMailExcerpt(post.Body, 1200))
	return strings.TrimSpace(b.String())
}

func nativeArticleMailExcerpt(body string, max int) string {
	body = strings.TrimSpace(body)
	runes := []rune(body)
	if max <= 0 || len(runes) <= max {
		return body
	}
	return strings.TrimSpace(string(runes[:max])) + "..."
}

func nativeDirectConversationID(a, b string) string {
	if b < a {
		return b + ":" + a
	}
	return a + ":" + b
}

func nativeFormatContentFilterReviewBody(title, reviewID string, filter *ContentFilter, boardID, threadID, postID, publicAuthor string) string {
	filterID := ""
	filterScope := ""
	if filter != nil {
		filterID = strings.TrimSpace(filter.ID)
		filterScope = strings.TrimSpace(filter.Scope)
	}
	return fmt.Sprintf("# %s\n\n- Review: %s\n- Status: opened\n- Filter: %s\n- Filter scope: %s\n- Board: %s\n- Thread: %s\n- Post: %s\n- Public author: %s\n\nSensitive filter pattern and article body are kept out of this generated record.\n",
		title,
		reviewID,
		filterID,
		filterScope,
		boardID,
		threadID,
		postID,
		publicAuthor,
	)
}

func nativeValidateStagedPostAttachmentBlob(db *sql.DB, stagedBlobID, attachmentID string, expectedSize int64, contentType string) *proto.ErrorDetail {
	stagedBlobID = strings.TrimSpace(stagedBlobID)
	if stagedBlobID == "" {
		return nil
	}
	var kind string
	var stagedSize int64
	err := qQueryRow(db,
		`SELECT kind, size_bytes FROM attachment_blob_staging WHERE id=?`,
		stagedBlobID,
	).Scan(&kind, &stagedSize)
	if err == sql.ErrNoRows {
		ok, promotedErr := nativePromotedPostAttachmentBlobMatchesDB(db, attachmentID, expectedSize, contentType)
		if promotedErr != nil {
			return nativeDecisionErr("internal_error", promotedErr.Error(), true)
		}
		if ok {
			return nil
		}
		return nativeDecisionErr(proto.ErrBlobStagingRequired, "staged attachment blob is not available yet", true)
	}
	if err != nil {
		return nativeDecisionErr("internal_error", err.Error(), true)
	}
	if kind != projections.StagedBlobPostAttachment {
		return nativeDecisionErr(proto.ErrValidationFailed, "staged attachment blob kind does not match post attachment", false)
	}
	if expectedSize >= 0 && stagedSize != expectedSize {
		return nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("%s: staged size %d does not match command size %d", projections.ErrStagedAttachmentBlobMismatch, stagedSize, expectedSize), false)
	}
	return nil
}

func nativeValidateStagedMailAttachmentBlob(db *sql.DB, stagedBlobID, attachmentID string, expectedSize int64, contentType string) *proto.ErrorDetail {
	stagedBlobID = strings.TrimSpace(stagedBlobID)
	if stagedBlobID == "" {
		return nil
	}
	var kind string
	var stagedSize int64
	err := qQueryRow(db,
		`SELECT kind, size_bytes FROM attachment_blob_staging WHERE id=?`,
		stagedBlobID,
	).Scan(&kind, &stagedSize)
	if err == sql.ErrNoRows {
		ok, promotedErr := nativePromotedMailAttachmentBlobMatchesDB(db, attachmentID, expectedSize, contentType)
		if promotedErr != nil {
			return nativeDecisionErr("internal_error", promotedErr.Error(), true)
		}
		if ok {
			return nil
		}
		return nativeDecisionErr(proto.ErrBlobStagingRequired, "staged mail attachment blob is not available yet", true)
	}
	if err != nil {
		return nativeDecisionErr("internal_error", err.Error(), true)
	}
	if kind != projections.StagedBlobMailAttachment {
		return nativeDecisionErr(proto.ErrValidationFailed, "staged attachment blob kind does not match mail attachment", false)
	}
	if expectedSize >= 0 && stagedSize != expectedSize {
		return nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("%s: staged size %d does not match command size %d", projections.ErrStagedAttachmentBlobMismatch, stagedSize, expectedSize), false)
	}
	return nil
}

func nativePromotedPostAttachmentBlobMatchesDB(db *sql.DB, attachmentID string, expectedSize int64, contentType string) (bool, error) {
	var storedContentType string
	var storedSize int64
	err := qQueryRow(db,
		`SELECT content_type, size_bytes FROM attachment_blobs WHERE attachment_id=?`,
		attachmentID,
	).Scan(&storedContentType, &storedSize)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return nativePromotedAttachmentBlobMetadataMatches(storedContentType, storedSize, expectedSize, contentType), nil
}

func nativePromotedMailAttachmentBlobMatchesDB(db *sql.DB, attachmentID string, expectedSize int64, contentType string) (bool, error) {
	var storedContentType string
	var storedSize int64
	err := qQueryRow(db,
		`SELECT content_type, size_bytes FROM mail_attachment_blobs WHERE attachment_id=?`,
		attachmentID,
	).Scan(&storedContentType, &storedSize)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return nativePromotedAttachmentBlobMetadataMatches(storedContentType, storedSize, expectedSize, contentType), nil
}

func nativePromotedMailAttachmentBlobMatches(tx *sql.Tx, attachmentID string, expectedSize int64, contentType string) (bool, error) {
	var storedContentType string
	var storedSize int64
	err := qQueryRow(tx,
		`SELECT content_type, size_bytes FROM mail_attachment_blobs WHERE attachment_id=?`,
		attachmentID,
	).Scan(&storedContentType, &storedSize)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return nativePromotedAttachmentBlobMetadataMatches(storedContentType, storedSize, expectedSize, contentType), nil
}

func nativePromotedPostAttachmentBlobMatches(tx *sql.Tx, attachmentID string, expectedSize int64, contentType string) (bool, error) {
	var storedContentType string
	var storedSize int64
	err := qQueryRow(tx,
		`SELECT content_type, size_bytes FROM attachment_blobs WHERE attachment_id=?`,
		attachmentID,
	).Scan(&storedContentType, &storedSize)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return nativePromotedAttachmentBlobMetadataMatches(storedContentType, storedSize, expectedSize, contentType), nil
}

func nativePromotedAttachmentBlobMetadataMatches(storedContentType string, storedSize int64, expectedSize int64, contentType string) bool {
	if expectedSize >= 0 && storedSize != expectedSize {
		return false
	}
	contentType = strings.TrimSpace(contentType)
	return contentType == "" || storedContentType == contentType
}

func nativeArticleMailBackEvent(db *sql.DB, record CommandLogRecord, actor *User, authorName, authorID string, thread *Thread, target *Post, replyPostID, replyBody string, ts int64, eventIndex int) (*EventAppend, *proto.ErrorDetail) {
	if actor == nil || thread == nil || target == nil || !target.MailBack || target.Redacted {
		return nil, nil
	}
	authorID = strings.TrimSpace(authorID)
	if authorID == "" || strings.TrimSpace(target.AuthorID) == "" || target.AuthorID == authorID {
		return nil, nil
	}
	recipient, err := getUserByID(db, target.AuthorID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if recipient == nil {
		return nil, nil
	}
	ignored, err := nativeRelationshipExists(db, recipient.ID, actor.ID, "ignore")
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if ignored {
		return nil, nil
	}
	subject := "Article reply: " + thread.Title
	body := nativeFormatArticleMailBackBody(thread, target, replyPostID, actor.Name, replyBody)
	ok, err := nativeMailQuotaAllows(db, recipient.ID, int64(len(subject)+len(body)))
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !ok {
		return nil, nil
	}
	authorName = strings.TrimSpace(authorName)
	if authorName == "" {
		authorName = actor.Name
	}
	mailID := stableCommandLogDecisionID("mail_", record, eventIndex)
	return &EventAppend{
		ID:     stableCommandLogDecisionID("evt_", record, eventIndex),
		Kind:   proto.EvtMailSent,
		Scopes: []string{"account:" + actor.ID, "account:" + recipient.ID},
		Payload: &proto.MailSentPayload{
			ID:         mailID,
			FromUserID: authorID,
			From:       authorName,
			ToUserIDs:  []string{recipient.ID},
			To:         []string{recipient.Name},
			Subject:    subject,
			Body:       body,
			SaveSent:   false,
			TS:         ts,
		},
		TS: ts,
	}, nil
}

func nativePostIdentity(actor *User, settings *BoardSettings, anonymous bool, canModerateBoard bool) (string, string, *proto.ErrorDetail) {
	if actor == nil {
		return "", "", nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	if !anonymous {
		return actor.Name, actor.ID, nil
	}
	if settings == nil || (!settings.AnonymousAllowed && !canModerateBoard) {
		return "", "", nativeDecisionErr(proto.ErrForbidden, "anonymous posting is not enabled for this board", false)
	}
	return "Anonymous", "", nil
}

func nativeRelationshipExists(db *sql.DB, userID, targetUserID, kind string) (bool, error) {
	var found int
	err := qQueryRow(db,
		`SELECT 1 FROM user_relationships WHERE user_id=? AND target_user_id=? AND kind=? LIMIT 1`,
		userID, targetUserID, kind,
	).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func nativeUserRecentlyOnline(db *sql.DB, userID string) (bool, error) {
	var lastSeen int64
	var status string
	err := qQueryRow(db,
		`SELECT last_seen, status
		   FROM user_presence_sessions
		  WHERE user_id=?
		    AND LOWER(status) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')
		  ORDER BY last_seen DESC, updated_at DESC
		  LIMIT 1`,
		userID,
	).Scan(&lastSeen, &status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return lastSeen >= nowMS()-5*60*1000 && nativeVisiblePresenceStatus(status), nil
}

func nativeVisiblePresenceStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "offline", "invisible", "cloak", "cloaked":
		return false
	default:
		return true
	}
}

func nativeMailQuotaAllows(db *sql.DB, userID string, addedBytes int64) (bool, error) {
	if addedBytes <= 0 || strings.TrimSpace(userID) == "" {
		return true, nil
	}
	var used sql.NullInt64
	err := qQueryRow(db,
		`SELECT COALESCE(SUM(LENGTH(m.subject) + LENGTH(m.body) +
		        COALESCE((SELECT SUM(size_bytes) FROM mail_attachments a WHERE a.message_id=m.id), 0)), 0)
		   FROM mail_copies c
		   JOIN mail_messages m ON m.id = c.message_id
		  WHERE c.user_id=? AND c.mailbox <> 'trash'`,
		userID,
	).Scan(&used)
	if err != nil {
		return false, err
	}
	return used.Int64+addedBytes <= projections.DefaultMailQuotaBytes, nil
}

func nativeFormatArticleMailBackBody(thread *Thread, target *Post, replyPostID, authorName, replyBody string) string {
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

func nativePostSignature(db *sql.DB, authorID string, record CommandLogRecord) (string, error) {
	authorID = strings.TrimSpace(authorID)
	if authorID == "" {
		return "", nil
	}
	var randomEnabled, activeCount int
	var selectedSignature, bankSignature, profileSignature string
	err := qQueryRow(db,
		`SELECT
		   COALESCE(s.random_enabled, 0),
		   COALESCE((
		     SELECT COUNT(*)
		       FROM user_signatures us
		      WHERE us.user_id=u.user_id
		        AND us.active=1
		        AND TRIM(COALESCE(us.body,'')) <> ''
		   ), 0),
		   COALESCE((
		     SELECT body
		       FROM user_signatures us
		      WHERE us.user_id=u.user_id
		        AND us.id=COALESCE(s.selected_signature_id, '')
		        AND us.active=1
		      LIMIT 1
		   ), ''),
		   COALESCE((
		     SELECT body
		       FROM user_signatures us
		      WHERE us.user_id=u.user_id
		        AND us.active=1
		        AND TRIM(COALESCE(us.body,'')) <> ''
		      ORDER BY us.position, us.updated_at, us.id
		      LIMIT 1
		   ), ''),
		   COALESCE(p.signature, '')
		  FROM (SELECT ? AS user_id) u
		  LEFT JOIN user_signature_settings s ON s.user_id=u.user_id
		  LEFT JOIN user_profiles p ON p.user_id=u.user_id`,
		authorID,
	).Scan(&randomEnabled, &activeCount, &selectedSignature, &bankSignature, &profileSignature)
	if err != nil {
		return "", err
	}
	if randomEnabled != 0 && activeCount > 0 {
		var signature string
		offset := nativeSignatureOffset(record, activeCount)
		if err := qQueryRow(db,
			`SELECT COALESCE(body,'') FROM user_signatures
			  WHERE user_id=? AND active=1 AND TRIM(COALESCE(body,'')) <> ''
			  ORDER BY position, updated_at, id LIMIT 1 OFFSET ?`,
			authorID, offset,
		).Scan(&signature); err != nil {
			return "", err
		}
		return trimNativeSignature(signature), nil
	}
	if signature := trimNativeSignature(selectedSignature); signature != "" {
		return signature, nil
	}
	if signature := trimNativeSignature(bankSignature); signature != "" {
		return signature, nil
	}
	return trimNativeSignature(profileSignature), nil
}

func nativeSignatureOffset(record CommandLogRecord, count int) int {
	if count <= 0 {
		return 0
	}
	partition := record.Partition.Normalize()
	h := sha256.New()
	_, _ = h.Write([]byte("signature"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(partition.Kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(partition.Key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.FormatInt(record.Offset, 10)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(record.ActorID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(record.CID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(record.Command))
	sum := h.Sum(nil)
	var n uint64
	for _, b := range sum[:8] {
		n = (n << 8) | uint64(b)
	}
	return int(n % uint64(count))
}

func trimNativeSignature(signature string) string {
	signature = strings.TrimSpace(signature)
	if len(signature) > 500 {
		signature = signature[:500]
	}
	return signature
}
