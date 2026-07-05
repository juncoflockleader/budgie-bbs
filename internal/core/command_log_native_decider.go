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

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandevents"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandrules"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/core/statsplan"
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

func nativeRequireCommandLogPartition(record CommandLogRecord, expected LogPartition) *proto.ErrorDetail {
	partition := record.Partition.Normalize()
	if partition == expected {
		return nil
	}
	return nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
		partition.Kind, partition.Key, expected.Kind, expected.Key), false)
}

func nativePartitionKeyOrGlobal(key string) string {
	if key != "" {
		return key
	}
	return partitionGlobal
}

func (e *CommandLogNativeDecisionExecutor) loadNativeDecisionActorForPartition(record CommandLogRecord, expected LogPartition) (*User, *proto.ErrorDetail) {
	if errDetail := nativeRequireCommandLogPartition(record, expected); errDetail != nil {
		return nil, errDetail
	}
	return e.loadNativeDecisionActor(record.ActorID)
}

func (e *CommandLogNativeDecisionExecutor) loadNativeDecisionActorForOwnPartition(record CommandLogRecord, kind string) (*User, *proto.ErrorDetail) {
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nil, errDetail
	}
	return actor, nativeRequireCommandLogPartition(record, LogPartition{Kind: kind, Key: actor.ID})
}

func (e *CommandLogNativeDecisionExecutor) loadNativeDecisionAdminForPartition(record CommandLogRecord, expected LogPartition) (*User, *proto.ErrorDetail) {
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, expected)
	if errDetail != nil {
		return nil, errDetail
	}
	if errDetail := commandrules.RequireAdminRole(actor.IsAdmin()); errDetail != nil {
		return nil, errDetail
	}
	return actor, nil
}

func nativeDecodeRequiredCommandPayload[T any](record CommandLogRecord, invalidMessage string) (T, *proto.ErrorDetail) {
	var payload T
	if record.Offset <= 0 {
		return payload, nativeDecisionErr(proto.ErrValidationFailed, "command offset is required", false)
	}
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return payload, nativeDecisionErr(proto.ErrValidationFailed, invalidMessage, false)
	}
	return payload, nil
}

func nativeDecodeNormalizedCommandPayload[T any](record CommandLogRecord, invalidMessage string, normalize func(T) (T, string)) (T, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[T](record, invalidMessage)
	if errDetail != nil {
		return payload, errDetail
	}
	var msg string
	payload, msg = normalize(payload)
	if msg != "" {
		return payload, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	return payload, nil
}

func nativeDecodeCommandPayloadMessage[T any](record CommandLogRecord, invalidMessage string, normalize func(T) (T, string)) (T, string, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[T](record, invalidMessage)
	if errDetail != nil {
		return payload, "", errDetail
	}
	payload, msg := normalize(payload)
	return payload, msg, nil
}

func nativeCommandTimestamp(record CommandLogRecord) int64 {
	if record.EnqueuedAt > 0 {
		return record.EnqueuedAt
	}
	return nowMS()
}

func nativeDecisionAckEvent(id string, event EventAppend) nativeCommandDecision {
	return nativeDecisionAckEvents(id, []EventAppend{event})
}

func nativeDecisionAckEvents(id string, events []EventAppend) nativeCommandDecision {
	return nativeCommandDecision{
		reply:  Reply{Result: &proto.AckResult{ID: id}},
		events: events,
	}
}

func nativeEvent(record CommandLogRecord, ordinal int, kind proto.EventKind, scopes []string, payload any, ts int64) EventAppend {
	return EventAppend{
		ID:      stableCommandLogDecisionID("evt_", record, ordinal),
		Kind:    kind,
		Scopes:  scopes,
		Payload: payload,
		TS:      ts,
	}
}

func (e *CommandLogNativeDecisionExecutor) decideCreateThread(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.CreateThreadPayload](record, "invalid createThread payload", proto.NormalizeCreateThreadPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	pollBlock, cleanBody := extractPoll(payload.Body)
	pollStripped := pollBlock != nil && cleanBody != payload.Body

	if errDetail := nativeRequireCommandLogPartition(record, LogPartition{Kind: partitionBoard, Key: payload.Board}); errDetail != nil {
		return nativeCommandDecision{}, errDetail
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
		if errDetail := commandrules.RequireMinTrustForPoll(e.core.DB, actor, 2, "create thread", userTrustLevel); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
	}
	if kind := decisionCtx.SanctionKind; kind != "" {
		return nativeCommandDecision{}, commandrules.ActiveBoardSanctionError(kind)
	}
	settings := decisionCtx.Settings
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canModerateBoard := decisionCtx.CanModerateBoard
	if errDetail := commandrules.RequireThreadCreationBoardAccessStrict(e.core.DB, actor, payload.Board, settings, canModerateBoard); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	authorName, authorID, errDetail := commandrules.PostIdentity(actor, settings, payload.Anonymous, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	attachments, errDetail := commandrules.NormalizePostAttachments(payload.Attachments, settings.AttachmentsAllowed, canModerateBoard, func(i int) string {
		return stableCommandLogDecisionID("att_", record, 100+i)
	})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	contentFilter, err := projections.MatchContentFilter(e.core.DB, payload.Board, payload.Title+"\n"+payload.Body)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}

	signature, err := nativePostSignature(e.core.DB, authorID, record)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := nativeCommandTimestamp(record)
	threadID := stableCommandLogDecisionID("thr_", record, 0)
	postID := stableCommandLogDecisionID("pst_", record, 1)
	scopes := []string{"board:" + payload.Board}
	threadScopes := []string{"board:" + payload.Board, "thread:" + threadID}
	contentType := proto.NormalizePostContentType(payload.ContentType)
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
		nativeEvent(record, 0, proto.EvtThreadNew, scopes, threadPayload, ts),
		nativeEvent(record, 1, proto.EvtPostAppended, threadScopes, postPayload, ts),
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
	return nativeDecisionAckEvents(threadID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideAppendPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.AppendPostPayload](record, "invalid appendPost payload", proto.NormalizeAppendPostPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
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
		if errDetail := commandrules.RequireMinTrustForPoll(e.core.DB, actor, 2, "reply", userTrustLevel); errDetail != nil {
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
	if errDetail := commandrules.RequireThreadOpenForReply(thread.Locked, canModerateBoard); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.RequireReplyBoardAccessStrict(e.core.DB, actor, thread.Board, settings, canModerateBoard); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	authorName, authorID, errDetail := commandrules.PostIdentity(actor, settings, payload.Anonymous, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	attachments, errDetail := commandrules.NormalizePostAttachments(payload.Attachments, settings.AttachmentsAllowed, canModerateBoard, func(i int) string {
		return stableCommandLogDecisionID("att_", record, 100+i)
	})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.RequireThreadStarterAcceptsReplies(rootReplyGuards.NoReply, canModerateThread); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	effectiveReplyTo := ""
	var quoteSource *Post
	var mailBackTarget *Post
	var parent *Post
	if payload.ReplyTo != "" {
		parent, err = projections.GetPost(e.core.DB, payload.ReplyTo)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	replyPlan, errDetail := commandrules.PlanReplyTarget(payload.ReplyTo, parent, thread.ID, payload.QuotePost, canModerateThread)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	effectiveReplyTo = replyPlan.EffectiveReplyTo
	quoteSource = replyPlan.QuoteSource
	mailBackTarget = replyPlan.MailBackTarget
	if replyPlan.NeedsRootMailBack {
		if rootReplyGuards.MailBack {
			root, err := projections.ThreadRootPost(e.core.DB, thread.ID)
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
		prefix := proto.FormatQuotedReplyPrefix(quoteSource.Author, quoteSource.Body)
		cleanBody = prefix + cleanBody
		rawBody = prefix + payload.Body
		postCommitBody = &notificationBody
	}
	if kind := decisionCtx.SanctionKind; kind != "" {
		return nativeCommandDecision{}, commandrules.ActiveBoardSanctionError(kind)
	}
	contentFilter, err := projections.MatchContentFilter(e.core.DB, thread.Board, rawBody)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}

	signature, err := nativePostSignature(e.core.DB, authorID, record)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := nativeCommandTimestamp(record)
	postID := stableCommandLogDecisionID("pst_", record, 0)
	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	contentType := proto.NormalizePostContentType(payload.ContentType)
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
	event := nativeEvent(record, 0, proto.EvtPostAppended, scopes, postPayload, ts)
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
	return nativeDecisionAckEvents(postID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decidePostBoardMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.PostBoardMailPayload](record, "invalid postBoardMail payload", proto.NormalizePostBoardMailPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	rawBoardID := payload.Board
	threadID := payload.Thread
	expectedKey := rawBoardID
	if expectedKey == "" {
		expectedKey = threadID
	}
	if expectedKey == "" {
		expectedKey = partitionGlobal
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := rawBoardID
	var thread *Thread
	if threadID != "" {
		var err error
		thread, err = projections.GetThread(e.core.DB, threadID)
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
	settings, err := projections.GetBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canModerateBoard, err := projections.ActorCanModerateBoard(e.core.DB, actor, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !settings.MailInAllowed && !canModerateBoard {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, "board mail-in is disabled", false)
	}
	if thread != nil {
		return e.decidePostBoardMailAppend(record, actor, thread, payload.Body, payload.ContentType, payload.Attachments)
	}
	title := payload.Subject
	if title == "" {
		title = "(no subject)"
	}
	threadPayload := proto.CreateThreadPayload{
		Board:       boardID,
		Title:       title,
		Body:        payload.Body,
		ContentType: payload.ContentType,
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
		if errDetail := commandrules.RequireMinTrustForPoll(e.core.DB, actor, 2, "reply", userTrustLevel); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
	}
	settings, err := projections.GetBoardSettings(e.core.DB, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canModerateBoard, err := projections.ActorCanModerateBoard(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	canModerateThread, err := projections.ActorCanModerateBoardThreads(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireThreadOpenForReply(thread.Locked, canModerateBoard); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.RequireReplyBoardAccessStrict(e.core.DB, actor, thread.Board, settings, canModerateBoard); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	authorName, authorID, errDetail := commandrules.PostIdentity(actor, settings, false, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	attachments, errDetail := commandrules.NormalizePostAttachments(attachmentsIn, settings.AttachmentsAllowed, canModerateBoard, func(i int) string {
		return stableCommandLogDecisionID("att_", record, 100+i)
	})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	rootReplyGuards, err := projections.ThreadRootReplyGuardsForThread(e.core.DB, thread.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireThreadStarterAcceptsReplies(rootReplyGuards.NoReply, canModerateThread); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	var mailBackTarget *Post
	if rootReplyGuards.MailBack {
		root, err := projections.ThreadRootPost(e.core.DB, thread.ID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		mailBackTarget = root
	}
	if kind, ok := projections.ActiveSanction(e.core.DB, actor.ID, thread.Board); ok {
		return nativeCommandDecision{}, commandrules.ActiveBoardSanctionError(kind)
	}
	rawBody := body
	contentFilter, err := projections.MatchContentFilter(e.core.DB, thread.Board, rawBody)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	signature, err := nativePostSignature(e.core.DB, authorID, record)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := nativeCommandTimestamp(record)
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
		ContentType: proto.NormalizePostContentType(contentType),
		Attachments: attachments,
		TS:          ts,
	}
	if authorID == "" && actor.ID != "" {
		postPayload.PostCommitActorID = actor.ID
		postPayload.PostCommitActorName = authorName
	}
	event := nativeEvent(record, 0, proto.EvtPostAppended, scopes, postPayload, ts)
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
	return nativeDecisionAckEvents(postID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideRepostPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.RepostPostPayload](record, "invalid repostPost payload", proto.NormalizeRepostPostPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: payload.Board})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	sourcePost, err := projections.GetPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if sourcePost == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "source post not found", false)
	}
	if errDetail := commandrules.RequirePostNotRedacted(sourcePost.Redacted, "cannot repost a redacted post"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	sourceThread, err := projections.GetThread(e.core.DB, sourcePost.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if sourceThread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "source thread not found", false)
	}
	sourceSettings, err := projections.GetBoardSettings(e.core.DB, sourceThread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireMemberBoardReadAccessStrict(e.core.DB, actor, sourceThread.Board, sourceSettings, "source board members only"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	settings, err := projections.GetBoardSettings(e.core.DB, payload.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "destination board not found", false)
	}
	canModerateBoard, err := projections.ActorCanModerateBoard(e.core.DB, actor, payload.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireThreadCreationBoardAccessStrict(e.core.DB, actor, payload.Board, settings, canModerateBoard); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if kind, ok := projections.ActiveSanction(e.core.DB, actor.ID, payload.Board); ok {
		return nativeCommandDecision{}, commandrules.ActiveBoardSanctionError(kind)
	}

	title := payload.Title
	if title == "" {
		title = sourceThread.Title
	}
	body := proto.FormatRepostBody(sourceThread.Board, sourceThread.Title, sourcePost.Author, sourcePost.ID, sourcePost.Body)
	authorName, authorID, errDetail := commandrules.PostIdentity(actor, settings, false, canModerateBoard)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	signature, err := nativePostSignature(e.core.DB, authorID, record)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	contentFilter, err := projections.MatchContentFilter(e.core.DB, payload.Board, title+"\n"+body)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := nativeCommandTimestamp(record)
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
		ContentType:    proto.NormalizePostContentType(sourcePost.ContentType),
		SourcePost:     sourcePost.ID,
		SourceThread:   sourceThread.ID,
		SourceBoard:    sourceThread.Board,
		SourceAuthor:   sourcePost.Author,
		SourceAuthorID: sourcePost.AuthorID,
		SourceTitle:    sourceThread.Title,
		TS:             ts,
	}
	events := []EventAppend{
		nativeEvent(record, 0, proto.EvtThreadNew, scopes, threadPayload, ts),
		nativeEvent(record, 1, proto.EvtPostAppended, threadScopes, postPayload, ts),
	}
	if filterEvents, errDetail := nativeContentFilterReviewEvents(e.core.DB, record, actor, authorName, contentFilter, postID, threadID, payload.Board, !settings.MemberReadMode, ts, len(events)); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	} else {
		events = append(events, filterEvents...)
	}
	if automodEvents, errDetail := nativeAutomodEvents(e.core.DB, record, actor.ID, postID, threadID, payload.Board, title+"\n"+body, ts, len(events)); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	} else {
		events = append(events, automodEvents...)
	}
	return nativeDecisionAckEvents(threadID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideAttachPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.AttachPostPayload](record, "invalid attachPost payload", proto.NormalizeAttachPostPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	contentType := payload.ContentType
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionPost, Key: payload.Post})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := projections.GetPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if errDetail := commandrules.RequirePostNotRedacted(post.Redacted, "cannot attach to a redacted post"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := projections.GetThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	settings, err := projections.GetBoardSettings(e.core.DB, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	if errDetail := commandrules.RequirePostAttachmentsAllowed(settings.AttachmentsAllowed, false); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	isAuthor := commandrules.ActorAuthoredBy(actor, post.AuthorID, post.Author)
	withinWindow := commandrules.WithinAuthorEditWindow(ts, post.CreatedAt, nativeAuthorEditWindow.Milliseconds())
	if errDetail := commandrules.RequirePostAuthorEditWindow(false, isAuthor, withinWindow); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	count, err := projections.PostAttachmentCount(e.core.DB, post.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequirePostAttachmentCapacity(count); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}

	attachmentID := payload.ID
	if attachmentID == "" {
		attachmentID = stableCommandLogDecisionID("att_", record, 0)
	}
	stagedBlobID := payload.StagedBlobID
	if errDetail := nativeValidateStagedPostAttachmentBlob(e.core.DB, stagedBlobID, attachmentID, payload.SizeBytes, contentType); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	event := nativeEvent(record, 0, proto.EvtPostAttachmentAdded, []string{"board:" + thread.Board, "thread:" + thread.ID}, &proto.PostAttachmentAddedPayload{
		ID:           attachmentID,
		Post:         post.ID,
		Thread:       thread.ID,
		Filename:     payload.Filename,
		ContentType:  contentType,
		SizeBytes:    payload.SizeBytes,
		AuthorID:     actor.ID,
		StagedBlobID: stagedBlobID,
		TS:           ts,
	}, ts)
	return nativeDecisionAckEvent(attachmentID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideEditPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.EditPostPayload](record, "invalid editPost payload", proto.NormalizeEditPostPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if pollBlock, _ := extractPoll(payload.Body); pollBlock != nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "editing posts with poll markup is not supported", false)
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionPost, Key: payload.Post})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if _, found, err := projections.PollIDForPost(e.core.DB, payload.Post); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	} else if found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "editing posts that contain a poll is not supported", false)
	}
	post, err := projections.GetPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if errDetail := commandrules.RequirePostNotRedacted(post.Redacted, "cannot edit a redacted post"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := projections.GetThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	ts := nativeCommandTimestamp(record)
	isAuthor := commandrules.ActorAuthoredBy(actor, post.AuthorID, post.Author)
	withinWindow := commandrules.WithinAuthorEditWindow(ts, post.CreatedAt, nativeAuthorEditWindow.Milliseconds())
	if errDetail := commandrules.RequirePostAuthorEditWindow(actor.IsMod(), isAuthor, withinWindow); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}

	event := nativeEvent(record, 0, proto.EvtPostEdited, []string{"thread:" + post.Thread, "board:" + thread.Board}, &proto.PostEditedPayload{
		ID:      post.ID,
		Thread:  post.Thread,
		NewBody: payload.Body,
		Version: post.Version + 1,
		TS:      ts,
	}, ts)
	return nativeDecisionAckEvent(post.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetPostFlag(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.SetPostFlagPayload](record, "invalid setPostFlag payload", proto.NormalizeSetPostFlagPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionPost, Key: payload.Post})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := projections.GetPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if errDetail := commandrules.RequirePostNotRedacted(post.Redacted, "cannot flag a redacted post"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := projections.GetThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}

	flagPlan := commandrules.PlanPostFlagUpdate(post, payload)
	canModerateThread := false
	if flagPlan.ThreadModerationChange || flagPlan.AuthorMetadataChange {
		canModerateThread, err = projections.ActorCanModerateBoardThreads(e.core.DB, actor, thread.Board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	canCurate := false
	if flagPlan.CuratorChange {
		canCurate, err = projections.ActorCanCurateBoard(e.core.DB, actor, thread.Board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	if errDetail := commandrules.RequirePostFlagPermissions(flagPlan, actor, post, canCurate, canModerateThread); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !flagPlan.HasChanges() {
		return nativeDecisionAckEvents(post.ID, nil), nil
	}

	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtPostFlagsSet, []string{"thread:" + post.Thread, "board:" + thread.Board}, &proto.PostFlagsSetPayload{
		ID:          post.ID,
		Thread:      post.Thread,
		Marked:      flagPlan.Marked,
		Recommended: flagPlan.Recommended,
		NoReply:     flagPlan.NoReply,
		TeX:         flagPlan.TeX,
		MailBack:    flagPlan.MailBack,
		By:          actor.ID,
		TS:          ts,
	}, ts)
	return nativeDecisionAckEvent(post.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideRedactPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.RedactPostPayload](record, "invalid redactPost payload", proto.NormalizeRedactPostPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionPost, Key: payload.Post})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := projections.GetPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if errDetail := commandrules.RequirePostNotRedacted(post.Redacted, "post is already redacted"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := projections.GetThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	canModeratePosts, err := projections.ActorCanModerateBoardPosts(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := nativeCommandTimestamp(record)
	isAuthor := commandrules.ActorAuthoredByID(actor, post.AuthorID)
	withinWindow := commandrules.WithinAuthorEditWindow(ts, post.CreatedAt, nativeAuthorEditWindow.Milliseconds())
	if _, errDetail := commandrules.PlanPostRedaction(canModeratePosts, isAuthor, withinWindow); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	event := nativeEvent(record, 0, proto.EvtPostRedacted, []string{"thread:" + post.Thread, "board:" + thread.Board}, &proto.PostRedactedPayload{
		ID:     post.ID,
		Thread: post.Thread,
		By:     actor.ID,
		Reason: payload.Reason,
		TS:     ts,
	}, ts)
	return nativeDecisionAckEvent(post.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideRestorePost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.RestorePostPayload](record, "invalid restorePost payload", proto.NormalizeRestorePostPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionPost, Key: payload.Post})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := projections.GetPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if errDetail := commandrules.RequirePostRedacted(post.Redacted, "post is not redacted"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := projections.GetThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	canModeratePosts, err := projections.ActorCanModerateBoardPosts(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireBoardPostModeration(canModeratePosts); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtPostRestored, []string{"thread:" + post.Thread, "board:" + thread.Board}, &proto.PostRestoredPayload{
		ID:     post.ID,
		Thread: post.Thread,
		By:     actor.ID,
		TS:     ts,
	}, ts)
	return nativeDecisionAckEvent(post.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideRedactPostRange(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.RedactPostRangePayload](record, "invalid redactPostRange payload", proto.NormalizeRedactPostRangePayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	postIDs := payload.Posts
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: payload.Board})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.EnsureRangeBoardAccess(e.core.DB, actor, payload.Board); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	events := make([]EventAppend, 0, len(postIDs))
	for i, postID := range postIDs {
		post, thread, errDetail := commandrules.LoadRangePostFromDB(e.core.DB, postID, payload.Board)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		if errDetail := commandrules.RequirePostNotRedacted(post.Redacted, "post is already redacted: "+postID); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, nativeEvent(record, i, proto.EvtPostRedacted, []string{"thread:" + post.Thread, "board:" + thread.Board}, &proto.PostRedactedPayload{
			ID:           post.ID,
			Thread:       post.Thread,
			By:           actor.ID,
			Reason:       payload.Reason,
			DeletionKind: "recycle",
			TS:           ts,
		}, ts))
	}
	return nativeDecisionAckEvents(strconv.Itoa(len(postIDs)), events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideRestorePostRange(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.RestorePostRangePayload](record, "invalid restorePostRange payload", proto.NormalizeRestorePostRangePayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	postIDs := payload.Posts
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: payload.Board})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.EnsureRangeBoardAccess(e.core.DB, actor, payload.Board); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	events := make([]EventAppend, 0, len(postIDs))
	for i, postID := range postIDs {
		post, thread, errDetail := commandrules.LoadRangePostFromDB(e.core.DB, postID, payload.Board)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		if errDetail := commandrules.RequirePostRedacted(post.Redacted, "post is not redacted: "+postID); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, nativeEvent(record, i, proto.EvtPostRestored, []string{"thread:" + post.Thread, "board:" + thread.Board}, &proto.PostRestoredPayload{
			ID:     post.ID,
			Thread: post.Thread,
			By:     actor.ID,
			TS:     ts,
		}, ts))
	}
	return nativeDecisionAckEvents(strconv.Itoa(len(postIDs)), events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideClearBoardJunk(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.ClearBoardJunkPayload](record, "invalid clearBoardJunk payload", proto.NormalizeClearBoardJunkPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: payload.Board})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.EnsureRangeBoardAccess(e.core.DB, actor, payload.Board); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	postIDs, errDetail := commandrules.BoardJunkPostIDs(e.core.DB, payload.Board, payload.Posts)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	events := make([]EventAppend, 0, len(postIDs))
	for i, postID := range postIDs {
		threadID, errDetail := commandrules.JunkPostThreadID(e.core.DB, postID, payload.Board)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, nativeEvent(record, i, proto.EvtPostDeletionCleared, []string{"thread:" + threadID, "board:" + payload.Board}, &proto.PostDeletionClearedPayload{
			ID:     postID,
			Thread: threadID,
			Board:  payload.Board,
			Kind:   "junk",
			By:     actor.ID,
			TS:     ts,
		}, ts))
	}
	return nativeDecisionAckEvents(strconv.Itoa(len(postIDs)), events), nil
}

func (e *CommandLogNativeDecisionExecutor) decidePurgePost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.PurgePostPayload](record, "invalid purgePost payload", proto.NormalizePurgePostPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionAdminForPartition(record, LogPartition{Kind: partitionPost, Key: payload.Post})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := projections.GetPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	thread, err := projections.GetThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", "thread not found", true)
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtPostPurged, []string{"thread:" + post.Thread, "board:" + thread.Board}, &proto.PostPurgedPayload{
		ID:     post.ID,
		Thread: post.Thread,
		By:     actor.ID,
		Reason: payload.Reason,
		TS:     ts,
	}, ts)
	return nativeDecisionAckEvent(post.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetThreadTitle(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.SetThreadTitlePayload](record, "invalid setThreadTitle payload", proto.NormalizeSetThreadTitlePayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionThread, Key: payload.Thread})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := projections.GetThread(e.core.DB, payload.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	if thread.Title == payload.Title {
		return nativeDecisionAckEvents(thread.ID, nil), nil
	}
	canModerateThread, err := projections.ActorCanModerateBoardThreads(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	isAuthor := commandrules.ActorAuthoredBy(actor, thread.AuthorID, thread.Author)
	ts := nativeCommandTimestamp(record)
	withinWindow := commandrules.WithinAuthorEditWindow(ts, thread.CreatedAt, nativeAuthorEditWindow.Milliseconds())
	if errDetail := commandrules.RequireThreadTitlePermission(canModerateThread, isAuthor, withinWindow); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	event := nativeEvent(record, 0, proto.EvtThreadTitleSet, []string{"board:" + thread.Board, "thread:" + thread.ID}, &proto.ThreadTitleSetPayload{
		Thread: thread.ID,
		Title:  payload.Title,
		By:     actor.ID,
		TS:     ts,
	}, ts)
	return nativeDecisionAckEvent(thread.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideLockThread(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.LockThreadPayload](record, "invalid lockThread payload", proto.NormalizeLockThreadPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionThread, Key: payload.Thread})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := projections.GetThread(e.core.DB, payload.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	canModerateThread, err := projections.ActorCanModerateBoardThreads(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireThreadModeration(canModerateThread); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtThreadLocked, []string{"board:" + thread.Board, "thread:" + thread.ID}, &proto.ThreadLockedPayload{
		Thread: thread.ID,
		Locked: payload.Locked,
		By:     actor.ID,
		TS:     ts,
	}, ts)
	return nativeDecisionAckEvent(thread.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideMoveThread(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.MoveThreadPayload](record, "invalid moveThread payload", proto.NormalizeMoveThreadPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionThread, Key: payload.Thread})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := projections.GetThread(e.core.DB, payload.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	canModerateThread, err := projections.ActorCanModerateBoardThreads(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireThreadModeration(canModerateThread); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if _, found, err := projections.BoardName(e.core.DB, payload.ToBoard); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	} else if !found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "destination board not found", false)
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtThreadMoved, []string{"board:" + thread.Board, "board:" + payload.ToBoard}, &proto.ThreadMovedPayload{
		Thread:    thread.ID,
		FromBoard: thread.Board,
		ToBoard:   payload.ToBoard,
		By:        actor.ID,
		TS:        ts,
	}, ts)
	return nativeDecisionAckEvent(thread.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideFlagPost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.FlagPostPayload](record, "invalid flagPost payload", proto.NormalizeFlagPostPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionPost, Key: payload.Post})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	post, err := projections.GetPost(e.core.DB, payload.Post)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	thread, err := projections.GetThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", "thread not found", true)
	}
	publicBoard, err := projections.BoardAllowsPublicSystemPost(e.core.DB, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := nativeCommandTimestamp(record)
	reviewID := stableCommandLogDecisionID("rev_", record, 0)
	// Moderation-only: reporter and reason are not broadcast to the board (M8).
	events := []EventAppend{
		nativeEvent(record, 0, proto.EvtPostFlagged, []string{"moderation:global"}, &proto.PostFlaggedPayload{
			ReviewID: reviewID,
			Kind:     "post_flag",
			PostID:   post.ID,
			Thread:   post.Thread,
			Reporter: actor.ID,
			Reason:   payload.Reason,
			TS:       ts,
		}, ts),
	}
	logEvents, errDetail := nativeModerationSystemLogEvents(e.core.DB, record, actor, proto.ModerationLogFlag, reviewID, post.ID, post.Thread, thread.Board, publicBoard, ts, len(events))
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events = append(events, logEvents...)
	return nativeDecisionAckEvents(reviewID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideResolveReview(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.ResolveReviewPayload](record, "invalid resolveReview payload", proto.NormalizeResolveReviewPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionReview, Key: payload.Review})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.RequireModeratorRole(actor.IsMod()); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	target, found, err := projections.GetModerationReviewLogTarget(e.core.DB, payload.Review)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found || target.Status != "open" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "review not found", false)
	}
	ts := nativeCommandTimestamp(record)
	events := []EventAppend{
		nativeEvent(record, 0, proto.EvtReviewResolved, []string{"moderation:global"}, &proto.ReviewResolvedPayload{
			ReviewID:   payload.Review,
			Resolution: payload.Resolution,
			By:         actor.ID,
			TS:         ts,
		}, ts),
	}
	logEvents, errDetail := nativeModerationSystemLogEvents(e.core.DB, record, actor, proto.ModerationLogResolve, payload.Review, target.PostID, target.ThreadID, target.BoardID, target.Public, ts, len(events))
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events = append(events, logEvents...)
	return nativeDecisionAckEvents(payload.Review, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decidePublishPollResult(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.PublishPollResultPayload](record, "invalid publishPollResult payload", proto.NormalizePublishPollResultPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionPoll, Key: payload.Poll})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	poll, err := projections.GetPollWithVotes(e.core.DB, payload.Poll, actor.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if poll == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "poll not found", false)
	}
	post, err := projections.GetPost(e.core.DB, poll.PostID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", "poll post not found", true)
	}
	thread, err := projections.GetThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", "poll thread not found", true)
	}
	canManagePolls, err := projections.ActorCanManageBoardPolls(e.core.DB, actor, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequirePollResultPublisher(canManagePolls, post.AuthorID == actor.ID, thread.AuthorID == actor.ID); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	emit, err := projections.BoardAllowsPublicSystemPost(e.core.DB, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequirePollResultPublicBoard(emit); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}

	threadID, postID := proto.PollResultPostIDs(poll.ID)
	if existingSeq, found, err := projections.ThreadLastSeq(e.core.DB, threadID); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	} else if found {
		return nativeCommandDecision{reply: Reply{Result: &proto.AckResult{ID: threadID, Seq: existingSeq}}}, nil
	}

	ts := nativeCommandTimestamp(record)
	title := proto.PollResultTitle(poll.Question)
	body := proto.FormatPollResultBody(thread.Title, thread.Board, poll.Question, projections.PollResultOptions(poll))
	events, errDetail := nativeGeneratedSystemPostEvents(e.core.DB, record, actor, nativeGeneratedSystemPostSpec{
		BoardID:     proto.VoteSystemBoardID,
		BoardName:   proto.VoteSystemBoardName,
		Description: proto.VoteSystemBoardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
		BoardMode:   nativeGeneratedBoardIfMissingCompact,
	}, ts, 0)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return nativeDecisionAckEvents(threadID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetContentFilter(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.SetContentFilterPayload](record, "invalid setContentFilter payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	payload = proto.NormalizeContentFilterPayload(payload)
	filterID := payload.ID
	if filterID == "" {
		filterID = stableCommandLogDecisionID("filter_", record, 0)
	} else if msg := proto.ValidateContentFilterID(filterID); msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	pattern := payload.Pattern
	if msg := proto.ValidateContentFilterPattern(pattern); msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	scope := payload.Scope
	expectedPartition := LogPartition{Kind: partitionBoard, Key: scope}
	if expectedPartition.Key == "" {
		expectedPartition.Key = partitionGlobal
	}
	actor, errDetail := e.loadNativeDecisionAdminForPartition(record, expectedPartition)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if scope != proto.DefaultContentFilterScope {
		if _, found, err := projections.BoardName(e.core.DB, scope); err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		} else if !found {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found for scope", false)
		}
	}
	active := true
	if payload.Active != nil {
		active = *payload.Active
	}
	ts := nativeCommandTimestamp(record)
	scopes := []string{"moderation:global"}
	if scope != proto.DefaultContentFilterScope {
		scopes = append(scopes, "board:"+scope)
	}
	event := nativeEvent(record, 0, proto.EvtContentFilterSet, scopes, &proto.ContentFilterSetPayload{
		ID:      filterID,
		Pattern: pattern,
		Scope:   scope,
		Active:  active,
		By:      actor.ID,
		TS:      ts,
	}, ts)
	return nativeDecisionAckEvent(filterID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardAutomodRule(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.SetBoardAutomodRulePayload](record, "invalid setBoardAutomodRule payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	payload, actions, msg := proto.NormalizeSetBoardAutomodRulePayload(payload)
	board := payload.Board
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	if errDetail := nativeRequireCommandLogPartition(record, LogPartition{Kind: partitionBoard, Key: board}); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if msg := proto.ValidateAutomodRule(payload); msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	action := payload.Action
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	exists, err := projections.BoardExists(e.core.DB, board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	req := proto.AutomodActionPermissionRequirements(actions)
	canModerateThreads := false
	if req.ThreadModeration {
		canModerateThreads, err = projections.ActorCanModerateBoardThreads(e.core.DB, actor, board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	canModeratePosts := false
	if req.PostModeration {
		canModeratePosts, err = projections.ActorCanModerateBoardPosts(e.core.DB, actor, board)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	if failure := proto.CheckAutomodActionPermissions(req, actor.IsAdmin(), canModerateThreads, canModeratePosts); failure != nil {
		return nativeCommandDecision{}, nativeDecisionErr(failure.Code, failure.Message, false)
	}
	ruleID := payload.ID
	if ruleID == "" {
		ruleID = stableCommandLogDecisionID("rule_", record, 0)
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtBoardAutomodRuleSet, []string{"board:" + board}, &proto.BoardAutomodRuleSetPayload{
		ID: ruleID, Board: board, Enabled: enabled, Priority: payload.Priority,
		MatchType: payload.MatchType, Pattern: payload.Pattern,
		Threshold: payload.Threshold, WindowSec: payload.WindowSec, Action: action,
		DurationSec: payload.DurationSec, Reason: payload.Reason,
		Note: payload.Note, By: actor.ID, TS: ts,
	}, ts)
	return nativeDecisionAckEvent(ruleID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteBoardAutomodRule(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.DeleteBoardAutomodRulePayload](record, "invalid deleteBoardAutomodRule payload", proto.NormalizeDeleteBoardAutomodRulePayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	board := payload.Board
	id := payload.ID
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: board})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	canModerateBoard, err := projections.ActorCanModerateBoardContent(e.core.DB, actor, board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireBoardModerationPermission(canModerateBoard); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtBoardAutomodRuleDeleted, []string{"board:" + board}, &proto.BoardAutomodRuleDeletedPayload{ID: id, Board: board, By: actor.ID, TS: ts}, ts)
	return nativeDecisionAckEvent(id, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSanctionUser(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, msg, errDetail := nativeDecodeCommandPayloadMessage[proto.SanctionUserPayload](record, "invalid sanctionUser payload", proto.NormalizeSanctionUserPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	targetID := payload.User
	expectedKey := nativePartitionKeyOrGlobal(targetID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionUser, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.RequireModeratorRole(actor.IsMod()); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	scope := payload.Scope
	target, err := projections.GetUserByID(e.core.DB, targetID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if target == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	if errDetail := commandrules.RequireSanctionTargetAllowed(actor.IsAdmin(), target.IsAdmin(), target.IsMod()); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	sourceBoardName := ""
	if scope != "global" {
		var found bool
		sourceBoardName, found, err = projections.BoardName(e.core.DB, scope)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !found {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found for scope", false)
		}
	}
	ts := nativeCommandTimestamp(record)
	events := []EventAppend{
		nativeEvent(record, 0, proto.EvtUserSanctioned, []string{"account:" + target.ID}, &proto.UserSanctionedPayload{
			User:        target.ID,
			Kind:        payload.Kind,
			Scope:       scope,
			DurationSec: payload.DurationSec,
			By:          actor.ID,
			Reason:      payload.Reason,
			TS:          ts,
		}, ts),
	}
	if scope != "global" {
		auditEvents, errDetail := nativeDenyPostSystemLogEvents(e.core.DB, record, actor, target, scope, sourceBoardName, payload.Kind, payload.Reason, ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, auditEvents...)
	}
	return nativeDecisionAckEvents(stableCommandLogDecisionID("san_", record, 0), events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideClearUserSanction(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, msg, errDetail := nativeDecodeCommandPayloadMessage[proto.ClearUserSanctionPayload](record, "invalid clearUserSanction payload", proto.NormalizeClearUserSanctionPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	targetRef := payload.User
	expectedKey := nativePartitionKeyOrGlobal(targetRef)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionUser, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.RequireModeratorRole(actor.IsMod()); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	kind := payload.Kind
	scope := payload.Scope
	reason := payload.Reason
	target, errDetail := commandrules.ResolveUserRef(e.core.DB, targetRef)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.RequireClearSanctionTargetAllowed(actor.IsAdmin(), target.IsMod()); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	var err error
	sourceBoardName := ""
	if scope != "global" {
		var found bool
		sourceBoardName, found, err = projections.BoardName(e.core.DB, scope)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !found {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found for scope", false)
		}
	}
	ts := nativeCommandTimestamp(record)
	events := []EventAppend{
		nativeEvent(record, 0, proto.EvtUserSanctionCleared, []string{"account:" + target.ID}, &proto.UserSanctionClearedPayload{
			User:   target.ID,
			Kind:   kind,
			Scope:  scope,
			By:     actor.ID,
			Reason: reason,
			TS:     ts,
		}, ts),
	}
	if scope != "global" {
		auditEvents, errDetail := nativeUndenyPostSystemLogEvents(e.core.DB, record, actor, target, scope, sourceBoardName, kind, reason, ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, auditEvents...)
	}
	return nativeDecisionAckEvents(target.ID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideCreateBoardCommand(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, msg, errDetail := nativeDecodeCommandPayloadMessage[proto.CreateBoardPayload](record, "invalid createBoard payload", proto.NormalizeCreateBoardPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	expectedKey := nativePartitionKeyOrGlobal(payload.ID)
	actor, errDetail := e.loadNativeDecisionAdminForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	if payload.ParentID != "" {
		found, err := projections.CategoryExists(e.core.DB, payload.ParentID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !found {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "parent category not found", false)
		}
	}
	position, err := projections.CategoryPositionForCreate(e.core.DB, payload.ID, payload.ParentID, payload.Position)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	matches, err := projections.CreatedBoardMatches(e.core.DB, payload.ID, payload.Name, payload.Description, payload.ParentID, position)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !matches {
		exists, err := projections.BoardExists(e.core.DB, payload.ID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if exists {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "board already exists", false)
		}
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtBoardCreated, []string{"board:" + payload.ID}, &proto.BoardCreatedPayload{
		ID:          payload.ID,
		Name:        payload.Name,
		Description: payload.Description,
		ParentID:    payload.ParentID,
		Position:    position,
		By:          actor.ID,
		TS:          ts,
	}, ts)
	return nativeDecisionAckEvent(payload.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardSettings(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, payloadMsg, errDetail := nativeDecodeCommandPayloadMessage[proto.SetBoardSettingsPayload](record, "invalid setBoardSettings payload", proto.NormalizeSetBoardSettingsPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := payload.Board
	expectedKey := nativePartitionKeyOrGlobal(boardID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	settings, err := projections.GetBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canSet, err := projections.ActorCanSetBoardSettings(e.core.DB, actor, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireBoardSettingsPermission(canSet); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	projections.ApplyBoardSettingsPatch(settings, projections.BoardSettingsPatchFromPayload(payload))
	ts := nativeCommandTimestamp(record)
	events := []EventAppend{nativeEvent(record, 0, proto.EvtBoardSettingsSet, []string{"board:" + boardID}, &proto.BoardSettingsSetPayload{
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
		GuestAccess:        settings.GuestAccess,
		By:                 actor.ID,
		TS:                 ts,
	}, ts)}
	if settingLines := proto.BoardSettingsAuditLines(payload); len(settingLines) > 0 && !settings.MemberReadMode {
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
	return nativeDecisionAckEvents(boardID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardMemberRequirements(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, payloadMsg, errDetail := nativeDecodeCommandPayloadMessage[proto.SetBoardMemberRequirementsPayload](record, "invalid setBoardMemberRequirements payload", proto.NormalizeSetBoardMemberRequirementsPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := payload.Board
	expectedKey := nativePartitionKeyOrGlobal(boardID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	req, err := projections.GetBoardMemberRequirements(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if req == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canSet, err := projections.ActorCanSetBoardSettings(e.core.DB, actor, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireBoardSettingsPermission(canSet); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if payloadMsg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	patch := projections.BoardMemberRequirementsPatchFromPayload(payload)
	projections.ApplyBoardMemberRequirementsPatch(req, patch)
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtBoardMemberRequirementsSet, []string{"board:" + boardID}, &proto.BoardMemberRequirementsSetPayload{
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
	}, ts)
	return nativeDecisionAckEvent(boardID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardModerator(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, payloadMsg, errDetail := nativeDecodeCommandPayloadMessage[proto.SetBoardModeratorPayload](record, "invalid setBoardModerator payload", proto.NormalizeSetBoardModeratorPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := payload.Board
	expectedKey := nativePartitionKeyOrGlobal(boardID)
	actor, errDetail := e.loadNativeDecisionAdminForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	userRef := payload.User
	if payloadMsg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	exists, err := projections.BoardExists(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	target, errDetail := commandrules.ResolveUserRef(e.core.DB, userRef)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	position, err := projections.BoardModeratorEventPosition(e.core.DB, boardID, target.ID, actor.ID, payload.Moderator, payload.Position, ts)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	events := []EventAppend{nativeEvent(record, 0, proto.EvtBoardModeratorSet, []string{"board:" + boardID, "user:" + target.ID}, &proto.BoardModeratorSetPayload{
		Board:     boardID,
		User:      target.ID,
		Moderator: payload.Moderator,
		Position:  position,
		By:        actor.ID,
		TS:        ts,
	}, ts)}
	emitAudit, err := projections.BoardAllowsPublicSystemPost(e.core.DB, boardID)
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
	return nativeDecisionAckEvents(boardID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardMember(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, payloadMsg, errDetail := nativeDecodeCommandPayloadMessage[proto.SetBoardMemberPayload](record, "invalid setBoardMember payload", proto.NormalizeSetBoardMemberPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := payload.Board
	expectedKey := nativePartitionKeyOrGlobal(boardID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	userRef := payload.User
	if payloadMsg != "" && (boardID == "" || userRef == "") {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	exists, err := projections.BoardExists(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canModerateBoard, err := projections.ActorCanModerateBoard(e.core.DB, actor, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	canManageMembers, err := projections.ActorCanManageBoardMembers(e.core.DB, actor, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if failure := proto.CheckBoardMemberManagerPermission(canModerateBoard, canManageMembers); failure != nil {
		return nativeCommandDecision{}, nativeDecisionErr(failure.Code, failure.Message, false)
	}
	target, errDetail := commandrules.ResolveUserRef(e.core.DB, userRef)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !canModerateBoard {
		if failure := proto.CheckSetBoardMemberPermissionChange(payload, canModerateBoard); failure != nil {
			return nativeCommandDecision{}, nativeDecisionErr(failure.Code, failure.Message, false)
		}
		isModerator, err := projections.BoardModeratorExists(e.core.DB, boardID, target.ID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if failure := proto.CheckSetBoardMemberTargetPermission(canModerateBoard, isModerator, false); failure != nil {
			return nativeCommandDecision{}, nativeDecisionErr(failure.Code, failure.Message, false)
		}
		privilegedMember, err := projections.BoardMemberHasDelegatedPermissions(e.core.DB, boardID, target.ID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if failure := proto.CheckSetBoardMemberTargetPermission(canModerateBoard, false, privilegedMember); failure != nil {
			return nativeCommandDecision{}, nativeDecisionErr(failure.Code, failure.Message, false)
		}
	}
	if payloadMsg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	member, err := projections.BoardMemberFinalState(e.core.DB, boardID, target.ID, payload.Member, projections.BoardMemberPatchFromPayload(payload))
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtBoardMemberSet, []string{"board:" + boardID, "user:" + target.ID}, &proto.BoardMemberSetPayload{
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
	}, ts)
	return nativeDecisionAckEvent(boardID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideLeaveBoardMembership(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, payloadMsg, errDetail := nativeDecodeCommandPayloadMessage[proto.LeaveBoardMembershipPayload](record, "invalid leaveBoardMembership payload", proto.NormalizeLeaveBoardMembershipPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := payload.Board
	expectedKey := nativePartitionKeyOrGlobal(boardID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	exists, err := projections.BoardExists(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtBoardMemberSet, []string{"board:" + boardID, "user:" + actor.ID}, &proto.BoardMemberSetPayload{
		Board:  boardID,
		User:   actor.ID,
		Member: false,
		By:     actor.ID,
		TS:     ts,
	}, ts)
	return nativeDecisionAckEvent(boardID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideApplyBoardMembership(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, payloadMsg, errDetail := nativeDecodeCommandPayloadMessage[proto.ApplyBoardMembershipPayload](record, "invalid applyBoardMembership payload", proto.NormalizeApplyBoardMembershipPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := payload.Board
	expectedKey := nativePartitionKeyOrGlobal(boardID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	exists, err := projections.BoardExists(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	if payloadMsg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	requirements, err := projections.GetBoardMemberRequirements(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	autoApprove := requirements != nil && requirements.ApprovalMode == "auto"
	applicationID := stableCommandLogDecisionID("bmap_", record, 0)
	ts := nativeCommandTimestamp(record)
	existingApplication, err := projections.GetBoardMemberApplication(e.core.DB, applicationID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if existingApplication != nil {
		if existingApplication.BoardID != boardID || existingApplication.UserID != actor.ID || existingApplication.Note != payload.Note {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "membership application id conflict", false)
		}
		switch existingApplication.Status {
		case "pending":
		case "approved":
			autoApprove = autoApprove ||
				(existingApplication.ReviewerID == actor.ID &&
					existingApplication.Title == "" &&
					existingApplication.ReviewNote == proto.BoardMembershipAutoApprovalNote)
		default:
			if errDetail := commandrules.RequireBoardMembershipApplicationPending(existingApplication.Status); errDetail != nil {
				return nativeCommandDecision{}, errDetail
			}
		}
		events, errDetail := nativeBoardMembershipApplicationEvents(e.core.DB, record, actor, applicationID, boardID, actor.ID, payload.Note, autoApprove, ts)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		return nativeDecisionAckEvents(applicationID, events), nil
	}
	isMember, err := projections.BoardMemberExists(e.core.DB, boardID, actor.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireBoardMembershipApplicantNotMember(isMember); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	status, err := projections.LatestBoardMemberApplicationStatus(e.core.DB, boardID, actor.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireBoardMembershipApplicationCanStart(status); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := nativeRequireBoardMembershipAdmission(e.core, boardID, actor.ID, requirements); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events, errDetail := nativeBoardMembershipApplicationEvents(e.core.DB, record, actor, applicationID, boardID, actor.ID, payload.Note, autoApprove, ts)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return nativeDecisionAckEvents(applicationID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideReviewBoardMembership(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, targetMsg, errDetail := nativeDecodeCommandPayloadMessage[proto.ReviewBoardMembershipPayload](record, "invalid reviewBoardMembership payload", proto.NormalizeReviewBoardMembershipTargetPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	applicationID := payload.Application
	if targetMsg != "" && (payload.Application == "" || payload.Status == "") {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, targetMsg, false)
	}
	if errDetail := nativeRequireCommandLogPartition(record, LogPartition{Kind: partitionReview, Key: applicationID}); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if targetMsg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, targetMsg, false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	app, err := projections.GetBoardMemberApplication(e.core.DB, applicationID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if app == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "membership application not found", false)
	}
	payload, msg := proto.NormalizeReviewBoardMembershipContent(payload)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	ts := nativeCommandTimestamp(record)
	if app.Status != "pending" {
		if app.Status == payload.Status &&
			app.ReviewerID == actor.ID &&
			app.Title == payload.Title &&
			app.ReviewNote == payload.Note &&
			app.ReviewedAt == ts {
			events, errDetail := nativeBoardMembershipReviewEvents(e.core.DB, record, actor, applicationID, app.BoardID, app.UserID, payload.Status, payload.Title, payload.Note, ts)
			if errDetail != nil {
				return nativeCommandDecision{}, errDetail
			}
			return nativeDecisionAckEvents(applicationID, events), nil
		}
		if errDetail := commandrules.RequireBoardMembershipApplicationPending(app.Status); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
	}
	canModerateBoard, err := projections.ActorCanModerateBoard(e.core.DB, actor, app.BoardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	canManageMembers, err := projections.ActorCanManageBoardMembers(e.core.DB, actor, app.BoardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if failure := proto.CheckReviewBoardMembershipPermission(canModerateBoard, canManageMembers, actor.ID, app.UserID, payload.Status); failure != nil {
		return nativeCommandDecision{}, nativeDecisionErr(failure.Code, failure.Message, false)
	}
	if payload.Status == "approved" {
		requirements, err := projections.GetBoardMemberRequirements(e.core.DB, app.BoardID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if errDetail := nativeRequireBoardMembershipAdmission(e.core, app.BoardID, app.UserID, requirements); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
	}
	events, errDetail := nativeBoardMembershipReviewEvents(e.core.DB, record, actor, applicationID, app.BoardID, app.UserID, payload.Status, payload.Title, payload.Note, ts)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return nativeDecisionAckEvents(applicationID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetRecommendedBoard(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, payloadMsg, errDetail := nativeDecodeCommandPayloadMessage[proto.SetRecommendedBoardPayload](record, "invalid setRecommendedBoard payload", proto.NormalizeSetRecommendedBoardPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := payload.Board
	expectedKey := nativePartitionKeyOrGlobal(boardID)
	actor, errDetail := e.loadNativeDecisionAdminForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if boardID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	exists, err := projections.BoardExists(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	if payloadMsg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	if payload.Recommended {
		ok, reason, err := projections.BoardCanBePubliclyRecommended(e.core.DB, boardID)
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
		position, err = projections.RecommendedBoardTargetPosition(e.core.DB, boardID, payload.Position)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtBoardRecommendedSet, []string{"board:" + boardID}, &proto.BoardRecommendedSetPayload{
		Board:       boardID,
		Recommended: payload.Recommended,
		Note:        payload.Note,
		Position:    position,
		CuratedBy:   actor.ID,
		TS:          ts,
	}, ts)
	return nativeDecisionAckEvent(boardID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideGrantRole(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.GrantRolePayload](record, "invalid grantRole payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return e.decideRoleChange(record, payload.User, payload.Role, proto.EvtRoleGranted, "granted")
}

func (e *CommandLogNativeDecisionExecutor) decideRevokeRole(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.RevokeRolePayload](record, "invalid revokeRole payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return e.decideRoleChange(record, payload.User, payload.Role, proto.EvtRoleRevoked, "revoked")
}

func (e *CommandLogNativeDecisionExecutor) decideRoleChange(record CommandLogRecord, userID, role string, kind proto.EventKind, action string) (nativeCommandDecision, *proto.ErrorDetail) {
	targetID := strings.TrimSpace(userID)
	expectedKey := nativePartitionKeyOrGlobal(targetID)
	actor, errDetail := e.loadNativeDecisionAdminForPartition(record, LogPartition{Kind: partitionUser, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	target, err := projections.GetUserByID(e.core.DB, targetID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if target == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	ts := nativeCommandTimestamp(record)
	events := []EventAppend{
		nativeEvent(record, 0, kind, []string{"account:" + target.ID}, proto.RoleChangePayload(kind, target.ID, role, actor.ID, ts), ts),
	}
	auditEvents, errDetail := nativeSyssecuritySystemLogEvents(e.core.DB, record, actor, "Role "+action+": "+target.Name, []string{
		"Action: role " + action,
		"User: " + target.Name,
		"Role: " + role,
		"Actor: " + actor.Name,
	}, ts, len(events))
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events = append(events, auditEvents...)
	return nativeDecisionAckEvents(target.ID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decidePublishStatsSnapshot(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.PublishStatsSnapshotPayload](record, "invalid publishStatsSnapshot payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionAdminForPartition(record, LogPartition{Kind: partitionGlobal, Key: partitionGlobal})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	dateLabel, _, msg := proto.NormalizeStatsSnapshotDate(payload.Date, ts)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	plan, err := statsplan.PlanStatsSnapshotSystemPosts(e.core.DB, dateLabel, ts)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	events := []EventAppend{nativeEvent(record, 0, proto.EvtCommunityStatsSnapshotRecorded, nil, &proto.CommunityStatsSnapshotRecordedPayload{
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
	}, ts)}
	if len(plan.Posts) > 0 {
		exists, err := projections.BoardExists(e.core.DB, statsplan.SystemBoardID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			position, err := projections.NextCategoryPosition(e.core.DB, "")
			if err != nil {
				return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
			}
			events = append(events, nativeGeneratedBoardCreatedEvent(record, actor, nativeGeneratedSystemPostSpec{
				BoardID:     statsplan.SystemBoardID,
				BoardName:   statsplan.SystemBoardName,
				Description: statsplan.SystemBoardDescription,
			}, position, ts, len(events)))
		}
	}
	for _, post := range plan.Posts {
		generatedEvents, errDetail := nativeGeneratedSystemPostEvents(e.core.DB, record, actor, nativeGeneratedSystemPostSpec{
			BoardID:   statsplan.SystemBoardID,
			ThreadID:  post.ThreadID,
			PostID:    post.PostID,
			Title:     post.Title,
			Body:      post.Body,
			BoardMode: nativeGeneratedBoardNever,
		}, ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, generatedEvents...)
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
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.PublishSystemNoticePayload](record, "invalid publishSystemNotice payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	rawBoard := strings.TrimSpace(payload.Board)
	expectedKey := nativePartitionKeyOrGlobal(rawBoard)
	actor, errDetail := e.loadNativeDecisionAdminForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	payload, board, msg := proto.NormalizePublishSystemNoticePayload(payload)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}

	threadID := stableCommandLogDecisionID("notice_thr_", record, 0)
	postID := stableCommandLogDecisionID("notice_pst_", record, 0)
	if existingSeq, found, err := projections.ThreadLastSeq(e.core.DB, threadID); err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	} else if found {
		return nativeCommandDecision{reply: Reply{Result: &proto.AckResult{ID: threadID, Seq: existingSeq}}}, nil
	}

	ts := nativeCommandTimestamp(record)
	noticeBody := proto.FormatSystemNoticeBody(board, payload.Title, payload.Body, payload.Source, actor.Name)
	events, errDetail := nativeGeneratedSystemPostEvents(e.core.DB, record, actor, nativeGeneratedSystemPostSpec{
		BoardID:     board.ID,
		BoardName:   board.Name,
		Description: board.Description,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       payload.Title,
		Body:        noticeBody,
		BoardMode:   nativeGeneratedBoardIfMissingReserve,
	}, ts, 0)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return nativeDecisionAckEvents(threadID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideBlessUser(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, msg, errDetail := nativeDecodeCommandPayloadMessage[proto.BlessUserPayload](record, "invalid blessUser payload", proto.NormalizeBlessUserPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	targetRef := payload.User
	expectedKey := nativePartitionKeyOrGlobal(targetRef)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionUser, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	target, errDetail := commandrules.ValidateBlessUserMutation(e.core.DB, actor, targetRef)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}

	ts := nativeCommandTimestamp(record)
	blessingID := stableCommandLogDecisionID("bless_", record, 0)
	scopes, eventPayload := commandevents.UserBlessed(actor.ID, actor.Name, target.ID, target.Name, blessingID, payload.Message, ts)
	events := []EventAppend{
		nativeEvent(record, 0, proto.EvtUserBlessed, scopes, eventPayload, ts),
	}
	auditEvents, errDetail := nativeBlessingSystemLogEvents(e.core.DB, record, actor, target, blessingID, payload.Message, ts, len(events))
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	events = append(events, auditEvents...)
	return nativeDecisionAckEvents(blessingID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSendMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.SendMailPayload](record, "invalid sendMail payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForOwnPartition(record, partitionMail)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return e.decideSendMailPayload(record, actor, payload)
}

func (e *CommandLogNativeDecisionExecutor) decideForwardMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, msg, errDetail := nativeDecodeCommandPayloadMessage[proto.ForwardMailPayload](record, "invalid forwardMail payload", proto.NormalizeForwardMailPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	mailID := payload.Mail
	expectedKey := nativePartitionKeyOrGlobal(mailID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionMail, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	source, err := projections.GetMail(e.core.DB, actor.ID, mailID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if source == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found", false)
	}
	return e.decideSendMailPayload(record, actor, commandrules.ForwardMailSendPayload(payload, source))
}

func (e *CommandLogNativeDecisionExecutor) decidePostMailToBoard(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.PostMailToBoardPayload](record, "invalid postMailToBoard payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	payload, mailMsg, targetMsg := proto.NormalizePostMailToBoardPayload(payload)
	mailID := payload.Mail
	boardID := payload.Board
	threadID := payload.Thread
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
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if mailMsg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, mailMsg, false)
	}
	source, err := projections.GetMail(e.core.DB, actor.ID, mailID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if source == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found", false)
	}
	title, body := commandrules.PostMailToBoardContent(payload, source)
	if threadID != "" {
		thread, err := projections.GetThread(e.core.DB, threadID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if thread == nil {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
		}
		return e.decidePostBoardMailAppend(record, actor, thread, body, "markup", nil)
	}
	if targetMsg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, targetMsg, false)
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
	payload, msg, errDetail := nativeDecodeCommandPayloadMessage[proto.MailPostAuthorPayload](record, "invalid mailPostAuthor payload", proto.NormalizeMailPostAuthorPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	postID := payload.Post
	expectedKey := nativePartitionKeyOrGlobal(postID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionPost, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	post, err := projections.GetPost(e.core.DB, postID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if errDetail := commandrules.RequirePostNotRedacted(post.Redacted, "cannot mail author from a redacted post"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := projections.GetThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	settings, err := projections.GetBoardSettings(e.core.DB, thread.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireMemberBoardReadAccessStrict(e.core.DB, actor, thread.Board, settings, "board members only"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	sendPayload, errDetail := commandrules.MailPostAuthorSendPayload(actor, payload, thread, post)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return e.decideSendMailPayload(record, actor, sendPayload)
}

func (e *CommandLogNativeDecisionExecutor) decideSendDigestEntryMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, msg, errDetail := nativeDecodeCommandPayloadMessage[proto.SendDigestEntryMailPayload](record, "invalid sendDigestEntryMail payload", proto.NormalizeSendDigestEntryMailPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	entryID := payload.Entry
	expectedKey := nativePartitionKeyOrGlobal(entryID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionMail, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	export, err := projections.GetDigestExport(e.core.DB, entryID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if export == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "digest entry not found", false)
	}
	settings, err := projections.GetBoardSettings(e.core.DB, export.Entry.BoardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if errDetail := commandrules.RequireMemberBoardReadAccessStrict(e.core.DB, actor, export.Entry.BoardID, settings, "board members only"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	return e.decideSendMailPayload(record, actor, commandrules.DigestEntryMailSendPayload(payload, export))
}

func (e *CommandLogNativeDecisionExecutor) decideCuratePost(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, payloadMsg, errDetail := nativeDecodeCommandPayloadMessage[proto.CuratePostPayload](record, "invalid curatePost payload", proto.NormalizeCuratePostTargetPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	postID := payload.Post
	expectedKey := nativePartitionKeyOrGlobal(postID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionPost, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if postID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	post, err := projections.GetPost(e.core.DB, postID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if post == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "post not found", false)
	}
	if errDetail := commandrules.RequirePostNotRedacted(post.Redacted, "cannot curate a redacted post"); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	thread, err := projections.GetThread(e.core.DB, post.Thread)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if thread == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "thread not found", false)
	}
	return e.decideDigestCuration(record, actor, thread, post, "post", post.ID, payload.Kind, payload.Title, payload.Path, payload.Note)
}

func (e *CommandLogNativeDecisionExecutor) decideCurateThread(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, payloadMsg, errDetail := nativeDecodeCommandPayloadMessage[proto.CurateThreadPayload](record, "invalid curateThread payload", proto.NormalizeCurateThreadTargetPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	threadID := payload.Thread
	expectedKey := nativePartitionKeyOrGlobal(threadID)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionThread, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if threadID == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, payloadMsg, false)
	}
	thread, err := projections.GetThread(e.core.DB, threadID)
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
	kind, title, path, note, msg := proto.NormalizeDigestCurationFields(rawKind, rawTitle, rawPath, rawNote)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	canCurate, err := projections.ActorCanCurateBoardKind(e.core.DB, actor, thread.Board, kind)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canCurate {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrForbidden, proto.DigestCurationPermissionMessage(kind), false)
	}
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
	entryID, err := projections.DigestEntryID(e.core.DB, thread.Board, targetKind, targetID, kind, path)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if entryID == "" {
		entryID = stableCommandLogDecisionID("dig_", record, 0)
	}
	ts := nativeCommandTimestamp(record)
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
	events := []EventAppend{nativeEvent(record, 0, proto.EvtDigestEntryUpserted, proto.DigestEventScopes(thread.Board), eventPayload, ts)}
	if mirror, ok := projections.DigestMirrorSystemBoardForKind(kind); ok {
		export, err := projections.DigestExportForUpsertedEntry(e.core.DB, eventPayload, thread, post)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if export == nil {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "digest export target not found", false)
		}
		mirrorEvents, errDetail := nativeDigestMirrorSystemLogEvents(e.core.DB, record, actor, entryID, export, mirror, ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, mirrorEvents...)
	}
	return nativeDecisionAckEvents(entryID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideRemoveDigestEntry(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.RemoveDigestEntryPayload](record, "invalid removeDigestEntry payload", proto.NormalizeRemoveDigestEntryPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, entry, errDetail := e.loadNativeDigestEntryMutationActor(record, payload.Entry, true)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtDigestEntryRemoved, proto.DigestEventScopes(entry.BoardID), &proto.DigestEntryRemovedPayload{
		ID:    entry.ID,
		Board: entry.BoardID,
		Kind:  entry.Kind,
		By:    actor.ID,
		TS:    ts,
	}, ts)
	return nativeDecisionAckEvent(entry.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideUpdateDigestEntry(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.UpdateDigestEntryPayload](record, "invalid updateDigestEntry payload", proto.NormalizeUpdateDigestEntryTargetPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, entry, errDetail := e.loadNativeDigestEntryMutationActor(record, payload.Entry, false)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	payload, msg := proto.NormalizeUpdateDigestEntryPayload(payload)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	title := entry.Title
	if payload.Title != nil {
		title = *payload.Title
	}
	path := entry.Path
	if payload.Path != nil {
		path = *payload.Path
	}
	note := entry.Note
	if payload.Note != nil {
		note = *payload.Note
	}
	if path != entry.Path {
		conflictID, found, err := projections.DigestPathEntryConflictID(e.core.DB, entry.BoardID, entry.TargetKind, entry.TargetID, entry.Kind, path)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if found && conflictID != entry.ID {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "digest entry already exists at that path", false)
		}
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtDigestEntryUpdated, proto.DigestEventScopes(entry.BoardID), &proto.DigestEntryUpdatedPayload{
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
	}, ts)
	return nativeDecisionAckEvent(entry.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetDigestEntryBody(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.SetDigestEntryBodyPayload](record, "invalid setDigestEntryBody payload", proto.NormalizeSetDigestEntryBodyPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, entry, errDetail := e.loadNativeDigestEntryMutationActor(record, payload.Entry, false)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	body := payload.Body
	edited := !payload.Reset
	if payload.Reset {
		body = ""
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtDigestEntryBodySet, proto.DigestEventScopes(entry.BoardID), &proto.DigestEntryBodySetPayload{
		ID:     entry.ID,
		Board:  entry.BoardID,
		Kind:   entry.Kind,
		Body:   body,
		Edited: edited,
		By:     actor.ID,
		TS:     ts,
	}, ts)
	return nativeDecisionAckEvent(entry.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideCreateDigestDirectory(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.CreateDigestDirectoryPayload](record, "invalid createDigestDirectory payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, boardID, kind, errDetail := e.loadNativeDigestPathMutationActor(record, payload.Board, payload.Kind)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	path, msg := proto.NormalizeDigestPathMutationSourcePath(payload.Path)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	directoryID, err := projections.DigestDirectoryID(e.core.DB, boardID, kind, path)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if directoryID == "" {
		directoryID = stableCommandLogDecisionID("dir_", record, 0)
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtDigestDirectorySet, proto.DigestEventScopes(boardID), &proto.DigestDirectorySetPayload{
		ID:        directoryID,
		Board:     boardID,
		Kind:      kind,
		Path:      path,
		CreatedBy: actor.ID,
		TS:        ts,
	}, ts)
	return nativeDecisionAckEvent(directoryID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideMoveDigestPath(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.MoveDigestPathPayload](record, "invalid moveDigestPath payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, boardID, kind, errDetail := e.loadNativeDigestPathMutationActor(record, payload.Board, payload.Kind)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	fromPath, toPath, msg := proto.NormalizeDigestPathMutationPaths(payload.FromPath, payload.ToPath)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	if fromPath == toPath {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "destination path must differ from source path", false)
	}
	if toPath != "" && (toPath == fromPath || strings.HasPrefix(toPath, fromPath+"/")) {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "cannot move an archive path into itself", false)
	}
	eventID := stableCommandLogDecisionID("evt_", record, 0)
	count, found, err := projections.DigestPathMutationCount(e.core.DB, eventID, "move")
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		entries, dirs, errDetail := nativeDigestPathChildrenForCopy(e.core.DB, record, boardID, kind, fromPath)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
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
			conflict, err := projections.DigestPathEntryConflictExists(e.core.DB, entry, projections.RemapDigestPath(entry.Path, fromPath, toPath), movingEntryIDs)
			if err != nil {
				return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
			}
			if conflict {
				return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "digest path move would overwrite an existing entry", false)
			}
		}
		for _, dir := range dirs {
			conflict, err := projections.DigestPathDirectoryConflictExists(e.core.DB, dir, projections.RemapDigestPath(dir.Path, fromPath, toPath), movingDirectoryIDs)
			if err != nil {
				return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
			}
			if conflict {
				return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "digest path move would overwrite an existing entry", false)
			}
		}
		count = len(entries) + len(dirs)
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(
		record,
		0,
		proto.EvtDigestPathMoved,
		proto.DigestEventScopes(boardID),
		&proto.DigestPathMovedPayload{
			Board:    boardID,
			Kind:     kind,
			FromPath: fromPath,
			ToPath:   toPath,
			Count:    count,
			By:       actor.ID,
			TS:       ts,
		},
		ts,
	)
	return nativeDecisionAckEvent(fmt.Sprintf("%s:%s:%d", boardID, kind, count), event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideCopyDigestPath(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.CopyDigestPathPayload](record, "invalid copyDigestPath payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, boardID, kind, errDetail := e.loadNativeDigestPathMutationActor(record, payload.Board, payload.Kind)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	fromPath, toPath, msg := proto.NormalizeDigestPathMutationPaths(payload.FromPath, payload.ToPath)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	if fromPath == toPath {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, "destination path must differ from source path", false)
	}

	entries, dirs, errDetail := nativeDigestPathChildrenForCopy(e.core.DB, record, boardID, kind, fromPath)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	entryIDs := make([]string, len(entries))
	for i, entry := range entries {
		entryIDs[i] = stableCommandLogDecisionID("dig_", record, i)
		conflict, err := projections.DigestPathEntryConflictExists(e.core.DB, entry, projections.RemapDigestPath(entry.Path, fromPath, toPath), map[string]struct{}{entryIDs[i]: {}})
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
		conflict, err := projections.DigestPathDirectoryConflictExists(e.core.DB, dir, projections.RemapDigestPath(dir.Path, fromPath, toPath), map[string]struct{}{directoryIDs[i]: {}})
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if conflict {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrConflict, "digest path copy would overwrite an existing entry", false)
		}
	}
	ts := nativeCommandTimestamp(record)
	count := len(entries) + len(dirs)
	event := nativeEvent(record, 0, proto.EvtDigestPathCopied, proto.DigestEventScopes(boardID), &proto.DigestPathCopiedPayload{
		Board:        boardID,
		Kind:         kind,
		FromPath:     fromPath,
		ToPath:       toPath,
		EntryIDs:     entryIDs,
		DirectoryIDs: directoryIDs,
		Count:        count,
		CreatedBy:    actor.ID,
		TS:           ts,
	}, ts)
	return nativeDecisionAckEvent(fmt.Sprintf("%s:%s:%d", boardID, kind, count), event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteDigestPath(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.DeleteDigestPathPayload](record, "invalid deleteDigestPath payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, boardID, kind, errDetail := e.loadNativeDigestPathMutationActor(record, payload.Board, payload.Kind)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	path, msg := proto.NormalizeDigestPathMutationSourcePath(payload.Path)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	eventID := stableCommandLogDecisionID("evt_", record, 0)
	count, found, err := projections.DigestPathMutationCount(e.core.DB, eventID, "delete")
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		entries, dirs, errDetail := nativeDigestPathChildrenForCopy(e.core.DB, record, boardID, kind, path)
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		count = len(entries) + len(dirs)
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtDigestPathDeleted, proto.DigestEventScopes(boardID), &proto.DigestPathDeletedPayload{
		Board: boardID,
		Kind:  kind,
		Path:  path,
		Count: count,
		By:    actor.ID,
		TS:    ts,
	}, ts)
	return nativeDecisionAckEvent(fmt.Sprintf("%s:%s:%d", boardID, kind, count), event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetMailGroup(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.SetMailGroupPayload](record, "invalid setMailGroup payload", proto.NormalizeMailGroupPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	groupRef := payload.Group
	name := payload.Name
	expectedKey := groupRef
	if expectedKey == "" {
		expectedKey = strings.TrimSpace(record.ActorID)
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionMail, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	groupID, memberIDs, errDetail := commandrules.ResolveMailGroupMutation(e.core.DB, actor.ID, payload, func() string {
		return stableCommandLogDecisionID("mgrp_", record, 0)
	})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(
		record,
		0,
		proto.EvtMailGroupSet,
		[]string{"account:" + actor.ID, "mail:" + groupID},
		&proto.MailGroupSetPayload{
			ID:        groupID,
			OwnerID:   actor.ID,
			Name:      name,
			MemberIDs: memberIDs,
			TS:        ts,
		},
		ts,
	)
	return nativeDecisionAckEvent(groupID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteMailGroup(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.DeleteMailGroupPayload](record, "invalid deleteMailGroup payload", proto.NormalizeDeleteMailGroupPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	groupRef := payload.Group
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionMail, Key: groupRef})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	eventID := stableCommandLogDecisionID("evt_", record, 0)
	groupID, found, err := projections.MailGroupDeletion(e.core.DB, eventID)
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
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(
		record,
		0,
		proto.EvtMailGroupDeleted,
		[]string{"account:" + actor.ID, "mail:" + groupID},
		&proto.MailGroupDeletedPayload{
			ID:      groupID,
			OwnerID: actor.ID,
			TS:      ts,
		},
		ts,
	)
	return nativeDecisionAckEvent(groupID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideAttachMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.AttachMailPayload](record, "invalid attachMail payload", proto.NormalizeAttachMailPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	mailID := payload.Mail
	filename := payload.Filename
	contentType := payload.ContentType
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionMail, Key: mailID})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	copyCounts, errDetail := commandrules.ValidateMailAttachmentMutation(e.core.DB, actor, mailID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if errDetail := commandrules.EnsureMailQuota(e.core.DB, copyCounts, payload.SizeBytes); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	attachmentID := payload.ID
	if attachmentID == "" {
		attachmentID = stableCommandLogDecisionID("matt_", record, 0)
	}
	stagedBlobID := payload.StagedBlobID
	if errDetail := nativeValidateStagedMailAttachmentBlob(e.core.DB, stagedBlobID, attachmentID, payload.SizeBytes, contentType); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	scopes, err := projections.MailAccountScopes(e.core.DB, mailID, actor.ID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(
		record,
		0,
		proto.EvtMailAttachmentAdded,
		scopes,
		&proto.MailAttachmentAddedPayload{
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
		ts,
	)
	return nativeDecisionAckEvent(attachmentID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideUpdateMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.UpdateMailPayload](record, "invalid updateMail payload", proto.NormalizeUpdateMailPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	mailID := payload.Mail
	mailbox := payload.Mailbox
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionMail, Key: mailID})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	target, found, err := projections.GetMailCopyUpdateTarget(e.core.DB, actor.ID, mailID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found", false)
	}
	if mailbox != nil && *mailbox != "trash" && target.TrashedCopies > 0 {
		size, err := projections.MailStoredSize(e.core.DB, mailID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if errDetail := commandrules.EnsureMailQuota(e.core.DB, map[string]int{actor.ID: target.TrashedCopies}, size); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
	}
	ts := nativeCommandTimestamp(record)
	event := nativeMailCopyUpdatedEvent(record, 0, target, actor.ID, mailID, mailbox, payload.Read, payload.Kept, ts)
	return nativeDecisionAckEvent(mailID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteMail(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.DeleteMailPayload](record, "invalid deleteMail payload", proto.NormalizeDeleteMailPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	mailID := payload.Mail
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionMail, Key: mailID})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	target, found, err := projections.GetMailCopyUpdateTarget(e.core.DB, actor.ID, mailID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found", false)
	}
	ts := nativeCommandTimestamp(record)
	mailbox := "trash"
	event := nativeMailCopyUpdatedEvent(record, 0, target, actor.ID, mailID, &mailbox, nil, nil, ts)
	return nativeDecisionAckEvent(mailID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteMailRange(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.DeleteMailRangePayload](record, "invalid deleteMailRange payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	mailIDs, msg := proto.NormalizeMailRangeIDs(payload.Mail)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionMail, Key: mailIDs[0]})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	targets := make([]projections.MailCopyUpdateTarget, 0, len(mailIDs))
	for _, mailID := range mailIDs {
		target, found, err := projections.GetMailCopyUpdateTarget(e.core.DB, actor.ID, mailID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !found {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "mail not found: "+mailID, false)
		}
		targets = append(targets, target)
	}
	ts := nativeCommandTimestamp(record)
	events := make([]EventAppend, 0, len(mailIDs))
	for i, mailID := range mailIDs {
		target := targets[i]
		mailbox := "trash"
		events = append(events, nativeMailCopyUpdatedEvent(record, i, target, actor.ID, mailID, &mailbox, nil, nil, ts))
	}
	return nativeDecisionAckEvents(fmt.Sprintf("%d", len(mailIDs)), events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSendMailPayload(record CommandLogRecord, actor *User, payload proto.SendMailPayload) (nativeCommandDecision, *proto.ErrorDetail) {
	recipientRefs, errDetail := commandrules.ExpandMailRecipients(e.core.DB, actor, payload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	var msg string
	payload, msg = proto.NormalizeSendMailContentPayload(payload)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	body := payload.Body
	subject := payload.Subject
	attachments, errDetail := commandrules.NormalizeMailAttachments(payload.Attachments, func(i int) string {
		return stableCommandLogDecisionID("matt_", record, 100+i)
	})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	saveSent := true
	if payload.SaveSent != nil {
		saveSent = *payload.SaveSent
	}
	recipients, errDetail := commandrules.ResolveMailRecipients(e.core.DB, actor, recipientRefs, payload.ToAll)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	addedBytes := proto.MailMessageSize(subject, body, attachments)
	copyCounts := commandrules.MailCopyCounts(recipients, actor.ID, saveSent)
	if errDetail := commandrules.EnsureMailQuota(e.core.DB, copyCounts, addedBytes); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	parentID := payload.ReplyTo
	if parentID != "" {
		ok, err := projections.UserHasMailCopy(e.core.DB, actor.ID, parentID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !ok {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "reply target not found", false)
		}
	}

	ts := nativeCommandTimestamp(record)
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
		nativeEvent(record, 0, proto.EvtMailSent, scopes, proto.NewMailSentPayload(mailID, actor.ID, actor.Name, toIDs, toNames, subject, body, parentID, saveSent, attachments, ts), ts),
	}
	if payload.ToAll {
		sysmailEvents, errDetail := nativeSysmailSystemLogEvents(e.core.DB, record, actor, mailID, subject, body, len(toNames), ts, len(events))
		if errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		events = append(events, sysmailEvents...)
	}
	return nativeDecisionAckEvents(mailID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSendDirectMessage(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, msg, errDetail := nativeDecodeCommandPayloadMessage[proto.SendDirectMessagePayload](record, "invalid sendDirectMessage payload", proto.NormalizeSendDirectMessagePayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	targetRef := payload.To
	expectedKey := nativePartitionKeyOrGlobal(targetRef)
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionUser, Key: expectedKey})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	target, errDetail := commandrules.ResolveDirectMessageRecipient(e.core.DB, actor, targetRef)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	messageID := stableCommandLogDecisionID("dm_", record, 0)
	scopes, eventPayload := commandevents.DirectMessageSent(messageID, actor.ID, actor.Name, target.ID, target.Name, payload.Body, ts)
	event := nativeEvent(record, 0, proto.EvtDirectMessageSent, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(messageID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideMarkDirectMessageRead(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.MarkDirectMessageReadPayload](record, "invalid markDirectMessageRead payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	messageID, msg := proto.NormalizeDirectMessageTarget(payload.Message)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionUser, Key: messageID})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	message, found, err := projections.DirectMessageTarget(e.core.DB, messageID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found || message.ToUserID != actor.ID || message.RecipientDeleted {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "message not found", false)
	}
	if message.ReadAt > 0 {
		return nativeDecisionAckEvents(messageID, nil), nil
	}
	ts := nativeCommandTimestamp(record)
	scopes, eventPayload := commandevents.DirectMessageRead(messageID, actor.ID, message.FromUserID, message.ToUserID, ts)
	event := nativeEvent(record, 0, proto.EvtDirectMessageRead, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(messageID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteDirectMessage(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.DeleteDirectMessagePayload](record, "invalid deleteDirectMessage payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	messageID, msg := proto.NormalizeDirectMessageTarget(payload.Message)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionUser, Key: messageID})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	message, found, err := projections.DirectMessageTarget(e.core.DB, messageID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	deleteSenderCopy := found && message.FromUserID == actor.ID
	deleteRecipientCopy := found && message.ToUserID == actor.ID
	if !deleteSenderCopy && !deleteRecipientCopy {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "message not found", false)
	}
	if (!deleteSenderCopy || message.SenderDeleted) && (!deleteRecipientCopy || message.RecipientDeleted) {
		return nativeDecisionAckEvents(messageID, nil), nil
	}
	ts := nativeCommandTimestamp(record)
	scopes, eventPayload := commandevents.DirectMessageDeleted(messageID, actor.ID, message.FromUserID, message.ToUserID, deleteSenderCopy, deleteRecipientCopy, ts)
	event := nativeEvent(record, 0, proto.EvtDirectMessageDeleted, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(messageID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetDirectMessageSettings(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeRequiredCommandPayload[proto.SetDirectMessageSettingsPayload](record, "invalid setDirectMessageSettings payload")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForOwnPartition(record, partitionUser)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	policy, msg := proto.NormalizeDirectMessagePolicy(payload.Policy)
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	ts := nativeCommandTimestamp(record)
	scopes, eventPayload := commandevents.DirectMessageSettingsSet(actor.ID, policy, ts)
	event := nativeEvent(record, 0, proto.EvtDirectMessageSettingsSet, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(actor.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetUserRelationship(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, msg, errDetail := nativeDecodeCommandPayloadMessage[proto.SetUserRelationshipPayload](record, "invalid setUserRelationship payload", proto.NormalizeSetUserRelationshipPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	targetRef := payload.User
	if targetRef == "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	if errDetail := nativeRequireCommandLogPartition(record, LogPartition{Kind: partitionUser, Key: targetRef}); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if msg != "" {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	target, errDetail := commandrules.ResolveOtherUser(e.core.DB, actor, targetRef, "user not found", "cannot create a relationship with yourself")
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	if !payload.Active {
		exists, err := projections.UserRelationshipExists(e.core.DB, actor.ID, target.ID, payload.Kind)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			return nativeDecisionAckEvents(target.ID, nil), nil
		}
	}
	ts := nativeCommandTimestamp(record)
	scopes, eventPayload := commandevents.UserRelationshipSet(actor.ID, target.ID, payload.Kind, payload.Note, payload.Active, ts)
	event := nativeEvent(record, 0, proto.EvtUserRelationshipSet, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(target.ID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetLoginWatch(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.SetLoginWatchPayload](record, "invalid setLoginWatch payload", proto.NormalizeSetLoginWatchPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionUser, Key: payload.User})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	target, online, errDetail := commandrules.ValidateLoginWatchMutation(e.core.DB, actor, payload.User, payload.Active)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	relationshipActive := payload.Active && !online
	if !payload.Active {
		exists, err := projections.UserRelationshipExists(e.core.DB, actor.ID, target.ID, commandevents.LoginWatchRelationshipKind)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			return nativeDecisionAckEvents(target.ID, nil), nil
		}
	}
	ts := nativeCommandTimestamp(record)
	events := make([]EventAppend, 0, 2)
	if online {
		events = append(events, nativeEvent(record, 0, proto.EvtNotificationCreated, []string{"user:" + actor.ID}, &proto.NotificationCreatedPayload{
			ID:     stableCommandLogDecisionID("notif_", record, 0),
			UserID: actor.ID,
			Kind:   "login",
			Actor:  target.Name,
			TS:     ts,
		}, ts))
	}
	scopes, eventPayload := commandevents.UserRelationshipSet(actor.ID, target.ID, commandevents.LoginWatchRelationshipKind, "", relationshipActive, ts)
	events = append(events, nativeEvent(record, len(events), proto.EvtUserRelationshipSet, scopes, eventPayload, ts))
	return nativeDecisionAckEvents(target.ID, events), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardFavorite(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.SetBoardFavoritePayload](record, "invalid setBoardFavorite payload", proto.NormalizeSetBoardFavoritePayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := payload.Board
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: boardID})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	settings, err := projections.GetBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	if payload.Favorite {
		exists, err := projections.FavoriteFolderExists(e.core.DB, actor.ID, payload.FolderID)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
		}
	}
	ts := nativeCommandTimestamp(record)
	scopes, eventPayload := commandevents.BoardFavoriteSet(actor.ID, boardID, payload.FolderID, payload.Favorite, payload.Position, ts)
	event := nativeEvent(record, 0, proto.EvtBoardFavoriteSet, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(boardID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideCreateFavoriteFolder(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.CreateFavoriteFolderPayload](record, "invalid createFavoriteFolder payload", proto.NormalizeCreateFavoriteFolderPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForOwnPartition(record, partitionUser)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	exists, err := projections.FavoriteFolderExists(e.core.DB, actor.ID, payload.ParentID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
	}
	position, err := projections.FavoriteFolderTargetPosition(e.core.DB, actor.ID, payload.ParentID, payload.Position)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	ts := nativeCommandTimestamp(record)
	folderID := stableCommandLogDecisionID("favfld_", record, 0)
	scopes, eventPayload := commandevents.FavoriteFolderCreated(actor.ID, folderID, payload.ParentID, payload.Name, position, ts)
	event := nativeEvent(record, 0, proto.EvtFavoriteFolderCreated, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(folderID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideUpdateFavoriteFolder(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.UpdateFavoriteFolderPayload](record, "invalid updateFavoriteFolder payload", proto.NormalizeUpdateFavoriteFolderPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionUser, Key: payload.Folder})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	current, found, err := projections.FavoriteFolderStateForUser(e.core.DB, actor.ID, payload.Folder)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
	}
	name := payload.Name
	if name == "" {
		name = current.Name
	}
	nextParent := current.ParentID
	if payload.ParentID != nil {
		nextParent = *payload.ParentID
		if errDetail := commandrules.RequireFavoriteFolderNotSelfParent(payload.Folder, nextParent); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
		exists, err := projections.FavoriteFolderExists(e.core.DB, actor.ID, nextParent)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !exists {
			return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
		}
		contains, err := projections.FavoriteFolderContains(e.core.DB, actor.ID, payload.Folder, nextParent)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if errDetail := commandrules.RequireFavoriteFolderNotDescendantParent(contains); errDetail != nil {
			return nativeCommandDecision{}, errDetail
		}
	}
	targetPosition := current.Position
	if payload.Position != nil {
		if *payload.Position < 0 {
			targetPosition = 0
		} else {
			targetPosition = *payload.Position
		}
	} else if nextParent != current.ParentID {
		targetPosition, err = projections.FavoriteFolderTargetPosition(e.core.DB, actor.ID, nextParent, nil)
		if err != nil {
			return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
	}
	ts := nativeCommandTimestamp(record)
	scopes, eventPayload := commandevents.FavoriteFolderUpdated(actor.ID, payload.Folder, nextParent, name, targetPosition, ts)
	event := nativeEvent(record, 0, proto.EvtFavoriteFolderUpdated, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(payload.Folder, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideDeleteFavoriteFolder(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.DeleteFavoriteFolderPayload](record, "invalid deleteFavoriteFolder payload", proto.NormalizeDeleteFavoriteFolderPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionUser, Key: payload.Folder})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	current, found, err := projections.FavoriteFolderStateForUser(e.core.DB, actor.ID, payload.Folder)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
	}
	ts := nativeCommandTimestamp(record)
	scopes, eventPayload := commandevents.FavoriteFolderDeleted(actor.ID, payload.Folder, current.ParentID, ts)
	event := nativeEvent(record, 0, proto.EvtFavoriteFolderDeleted, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(payload.Folder, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideMoveBoardFavorite(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.MoveBoardFavoritePayload](record, "invalid moveBoardFavorite payload", proto.NormalizeMoveBoardFavoritePayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: payload.Board})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	settings, err := projections.GetBoardSettings(e.core.DB, payload.Board)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	exists, err := projections.FavoriteFolderExists(e.core.DB, actor.ID, payload.FolderID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !exists {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "favorite folder not found", false)
	}
	ts := nativeCommandTimestamp(record)
	scopes, eventPayload := commandevents.BoardFavoriteSet(actor.ID, payload.Board, payload.FolderID, true, payload.Position, ts)
	event := nativeEvent(record, 0, proto.EvtBoardFavoriteSet, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(payload.Board, event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideImportFavoriteTree(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.ImportFavoriteTreePayload](record, "invalid importFavoriteTree payload", proto.NormalizeImportFavoriteTreePayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	actor, errDetail := e.loadNativeDecisionActorForOwnPartition(record, partitionUser)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}

	type importFolder struct {
		finalID  string
		parentID string
		name     string
		position int
	}
	folderIDMap := map[string]string{}
	folders := make([]importFolder, 0, len(payload.Folders))
	for i, folder := range payload.Folders {
		finalID := stableCommandLogDecisionID("favfld_", record, i)
		folderIDMap[folder.ID] = finalID
		folders = append(folders, importFolder{
			finalID:  finalID,
			parentID: folder.ParentID,
			name:     folder.Name,
			position: folder.Position,
		})
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
		boardID := board.ID
		sourceFolderID := board.FolderID
		exists, err := projections.BoardExists(e.core.DB, boardID)
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
	ts := nativeCommandTimestamp(record)
	event := nativeEvent(record, 0, proto.EvtFavoriteTreeImported, []string{"user:" + actor.ID}, &proto.FavoriteTreeImportedPayload{
		UserID:  actor.ID,
		Folders: eventFolders,
		Boards:  eventBoards,
		Replace: replace,
		TS:      ts,
	}, ts)
	return nativeDecisionAckEvent("", event), nil
}

func (e *CommandLogNativeDecisionExecutor) decideSetBoardZap(ctx context.Context, record CommandLogRecord) (nativeCommandDecision, *proto.ErrorDetail) {
	payload, errDetail := nativeDecodeNormalizedCommandPayload[proto.SetBoardZapPayload](record, "invalid setBoardZap payload", proto.NormalizeSetBoardZapPayload)
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	boardID := payload.Board
	actor, errDetail := e.loadNativeDecisionActorForPartition(record, LogPartition{Kind: partitionBoard, Key: boardID})
	if errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	settings, err := projections.GetBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeCommandDecision{}, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeCommandDecision{}, nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	if errDetail := commandrules.RequireBoardZapAllowed(payload.Zapped, settings); errDetail != nil {
		return nativeCommandDecision{}, errDetail
	}
	ts := nativeCommandTimestamp(record)
	scopes, eventPayload := commandevents.BoardZapSet(actor.ID, boardID, payload.Zapped, ts)
	event := nativeEvent(record, 0, proto.EvtBoardZapSet, scopes, eventPayload, ts)
	return nativeDecisionAckEvent(boardID, event), nil
}

func (e *CommandLogNativeDecisionExecutor) loadNativeDecisionActor(actorID string) (*User, *proto.ErrorDetail) {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "command actor is required", false)
	}
	actor, err := projections.GetUserByID(e.core.DB, actorID)
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

	actor, err := projections.GetUserByID(db, actorID)
	if err != nil {
		return out, err
	}
	if actor == nil {
		return out, nil
	}
	out.Actor = actor
	out.Settings, err = projections.GetBoardSettings(db, boardID)
	if err != nil {
		return out, err
	}
	out.CanModerateBoard, err = projections.ActorCanModerateBoard(db, actor, boardID)
	if err != nil {
		return out, err
	}
	if sanctionKind, found, err := projections.ActiveSanctionKind(db, actor.ID, boardID, nowMS()); err != nil {
		return out, err
	} else if found {
		out.SanctionKind = strings.TrimSpace(sanctionKind)
	}
	return out, nil
}

type nativeAppendPostContext struct {
	Actor             *User
	Thread            *Thread
	RootReplyGuards   projections.ThreadRootReplyGuards
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

	actor, err := projections.GetUserByID(db, actorID)
	if err != nil {
		return out, err
	}
	if actor == nil {
		return out, nil
	}
	out.Actor = actor
	thread, err := projections.GetThread(db, threadID)
	if err != nil {
		return out, err
	}
	if thread == nil {
		return out, nil
	}
	out.Thread = thread
	out.RootReplyGuards, err = projections.ThreadRootReplyGuardsForThread(db, thread.ID)
	if err != nil {
		return out, err
	}
	out.Settings, err = projections.GetBoardSettings(db, thread.Board)
	if err != nil {
		return out, err
	}
	if actor.IsMod() {
		out.CanModerateBoard = true
		out.CanModerateThread = true
	} else {
		out.CanModerateBoard, out.CanModerateThread, err = projections.BoardThreadModerationPermissions(db, thread.Board, actor.ID)
		if err != nil {
			return out, err
		}
	}
	if sanctionKind, found, err := projections.ActiveSanctionKind(db, actor.ID, thread.Board, nowMS()); err != nil {
		return out, err
	} else if found {
		out.SanctionKind = strings.TrimSpace(sanctionKind)
	}
	return out, nil
}

func nativeDecisionErr(code, message string, retryable bool) *proto.ErrorDetail {
	return &proto.ErrorDetail{Code: code, Message: message, Retryable: retryable}
}

const nativeAuthorEditWindow = 24 * time.Hour

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

type nativeGeneratedBoardEventMode int

const (
	nativeGeneratedBoardNever nativeGeneratedBoardEventMode = iota
	nativeGeneratedBoardIfMissingCompact
	nativeGeneratedBoardIfMissingReserve
	nativeGeneratedBoardAlwaysNextPosition
	nativeGeneratedBoardAlwaysUpsert
)

type nativeGeneratedSystemPostSpec struct {
	BoardID     string
	BoardName   string
	Description string
	ThreadID    string
	PostID      string
	Title       string
	Body        string
	BoardMode   nativeGeneratedBoardEventMode
}

func nativeGeneratedSystemPostEvents(db *sql.DB, record CommandLogRecord, actor *User, spec nativeGeneratedSystemPostSpec, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	events := make([]EventAppend, 0, 3)
	reserveBoardOrdinal := false
	switch spec.BoardMode {
	case nativeGeneratedBoardNever:
	case nativeGeneratedBoardIfMissingCompact, nativeGeneratedBoardIfMissingReserve:
		exists, err := projections.BoardExists(db, spec.BoardID)
		if err != nil {
			return nil, nativeDecisionErr("internal_error", err.Error(), true)
		}
		reserveBoardOrdinal = spec.BoardMode == nativeGeneratedBoardIfMissingReserve
		if !exists {
			position, errDetail := nativeGeneratedSystemPostPosition(db, spec.BoardID, spec.BoardMode)
			if errDetail != nil {
				return nil, errDetail
			}
			events = append(events, nativeGeneratedBoardCreatedEvent(record, actor, spec, position, ts, startIndex+len(events)))
		}
	case nativeGeneratedBoardAlwaysNextPosition, nativeGeneratedBoardAlwaysUpsert:
		position, errDetail := nativeGeneratedSystemPostPosition(db, spec.BoardID, spec.BoardMode)
		if errDetail != nil {
			return nil, errDetail
		}
		events = append(events, nativeGeneratedBoardCreatedEvent(record, actor, spec, position, ts, startIndex+len(events)))
	default:
		return nil, nativeDecisionErr("internal_error", "unknown native generated board event mode", true)
	}

	threadEventIndex := startIndex + len(events)
	if reserveBoardOrdinal && len(events) == 0 {
		threadEventIndex = startIndex + 1
	}
	scopes := []string{"board:" + spec.BoardID}
	events = append(events, nativeEvent(record, threadEventIndex, proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID:       spec.ThreadID,
		Board:    spec.BoardID,
		Author:   actor.Name,
		AuthorID: actor.ID,
		Title:    spec.Title,
		TS:       ts,
	}, ts))
	events = append(events, nativeEvent(record, threadEventIndex+1, proto.EvtPostAppended, append(scopes, "thread:"+spec.ThreadID), &proto.PostAppendedPayload{
		ID:          spec.PostID,
		Thread:      spec.ThreadID,
		Author:      actor.Name,
		AuthorID:    actor.ID,
		Body:        spec.Body,
		RawBody:     spec.Body,
		ContentType: "markup",
		TS:          ts,
	}, ts))
	return events, nil
}

func nativeGeneratedSystemPostPosition(db *sql.DB, boardID string, mode nativeGeneratedBoardEventMode) (int, *proto.ErrorDetail) {
	if mode == nativeGeneratedBoardAlwaysUpsert {
		position, err := projections.CategoryUpsertPosition(db, boardID, "")
		if err != nil {
			return 0, nativeDecisionErr("internal_error", err.Error(), true)
		}
		return position, nil
	}
	position, err := projections.NextCategoryPosition(db, "")
	if err != nil {
		return 0, nativeDecisionErr("internal_error", err.Error(), true)
	}
	return position, nil
}

func nativeGeneratedBoardCreatedEvent(record CommandLogRecord, actor *User, spec nativeGeneratedSystemPostSpec, position int, ts int64, eventIndex int) EventAppend {
	return nativeEvent(record, eventIndex, proto.EvtBoardCreated, []string{"board:" + spec.BoardID}, &proto.BoardCreatedPayload{
		ID:          spec.BoardID,
		Name:        spec.BoardName,
		Description: spec.Description,
		Position:    position,
		By:          actor.ID,
		TS:          ts,
	}, ts)
}

func (e *CommandLogNativeDecisionExecutor) loadNativeDigestEntryMutationActor(record CommandLogRecord, entryID string, allowRemoved bool) (*User, projections.DigestPathEntryRow, *proto.ErrorDetail) {
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nil, projections.DigestPathEntryRow{}, errDetail
	}
	entry, errDetail := nativeDigestEntryForCuration(e.core.DB, actor, entryID)
	if errDetail != nil {
		if !allowRemoved || errDetail.Code != proto.ErrNotFound {
			return nil, projections.DigestPathEntryRow{}, errDetail
		}
		removed, found, err := projections.DigestEntryRemovalByID(e.core.DB, entryID)
		if err != nil {
			return nil, projections.DigestPathEntryRow{}, nativeDecisionErr("internal_error", err.Error(), true)
		}
		if !found || (removed.RemovedBy != "" && removed.RemovedBy != actor.ID) {
			return nil, projections.DigestPathEntryRow{}, errDetail
		}
		entry = projections.DigestPathEntryRow{
			ID:      removed.ID,
			BoardID: removed.BoardID,
			Kind:    removed.Kind,
		}
	}
	if errDetail := nativeValidateDigestEntryCommandPartition(record, entry); errDetail != nil {
		return nil, projections.DigestPathEntryRow{}, errDetail
	}
	return actor, entry, nil
}

func (e *CommandLogNativeDecisionExecutor) nativeDigestPathMutationBoardKind(record CommandLogRecord, rawBoard, rawKind string) (string, string, *proto.ErrorDetail) {
	boardID, msg := proto.NormalizeDigestPathMutationBoard(rawBoard)
	expectedKey := nativePartitionKeyOrGlobal(boardID)
	if errDetail := nativeRequireCommandLogPartition(record, LogPartition{Kind: partitionBoard, Key: expectedKey}); errDetail != nil {
		return "", "", errDetail
	}
	if msg != "" {
		return "", "", nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	normalizedKind, msg := proto.NormalizeDigestPathMutationKind(rawKind)
	if msg != "" {
		return "", "", nativeDecisionErr(proto.ErrValidationFailed, msg, false)
	}
	return boardID, normalizedKind, nil
}

func (e *CommandLogNativeDecisionExecutor) loadNativeDigestPathMutationActor(record CommandLogRecord, rawBoard, rawKind string) (*User, string, string, *proto.ErrorDetail) {
	boardID, kind, errDetail := e.nativeDigestPathMutationBoardKind(record, rawBoard, rawKind)
	if errDetail != nil {
		return nil, "", "", errDetail
	}
	actor, errDetail := e.loadNativeDecisionActor(record.ActorID)
	if errDetail != nil {
		return nil, "", "", errDetail
	}
	if errDetail := e.nativeRequireDigestPathMutation(actor, boardID, kind); errDetail != nil {
		return nil, "", "", errDetail
	}
	return actor, boardID, kind, nil
}

func (e *CommandLogNativeDecisionExecutor) nativeRequireDigestPathMutation(actor *User, boardID, kind string) *proto.ErrorDetail {
	settings, err := projections.GetBoardSettings(e.core.DB, boardID)
	if err != nil {
		return nativeDecisionErr("internal_error", err.Error(), true)
	}
	if settings == nil {
		return nativeDecisionErr(proto.ErrNotFound, "board not found", false)
	}
	canCurate, err := projections.ActorCanCurateBoardKind(e.core.DB, actor, boardID, kind)
	if err != nil {
		return nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canCurate {
		return nativeDecisionErr(proto.ErrForbidden, proto.DigestCurationPermissionMessage(kind), false)
	}
	return nil
}

func nativeRequireBoardMembershipAdmission(c *Core, boardID, userID string, requirements *BoardMemberRequirements) *proto.ErrorDetail {
	if requirements == nil {
		return nil
	}
	if c == nil || c.DB == nil {
		return nativeDecisionErr("internal_error", "core is not initialized", true)
	}
	db := c.DB
	counterStore := c.counterStore
	if counterStore == nil {
		counterStore = sqlCounterStore{db: db}
	}
	return commandrules.RequireBoardMembershipAdmission(db, counterStore, boardID, userID, requirements)
}

func nativeBoardMembershipApplicationEvents(db *sql.DB, record CommandLogRecord, actor *User, applicationID, boardID, userID, note string, autoApprove bool, ts int64) ([]EventAppend, *proto.ErrorDetail) {
	events := []EventAppend{nativeEvent(record, 0, proto.EvtBoardMemberApplicationSubmitted, []string{"board:" + boardID, "user:" + userID}, &proto.BoardMemberApplicationSubmittedPayload{
		ID:    applicationID,
		Board: boardID,
		User:  userID,
		Note:  note,
		TS:    ts,
	}, ts)}
	if !autoApprove {
		return events, nil
	}
	events = append(events, nativeEvent(record, 1, proto.EvtBoardMemberApplicationReviewed, []string{"board:" + boardID, "user:" + userID}, &proto.BoardMemberApplicationReviewedPayload{
		Application: applicationID,
		Board:       boardID,
		User:        userID,
		Status:      "approved",
		Reviewer:    actor.ID,
		ReviewNote:  proto.BoardMembershipAutoApprovalNote,
		TS:          ts,
	}, ts))
	registryEvents, errDetail := nativeBoardRegistrationSystemLogEvents(db, record, actor, applicationID, "approved", boardID, userID, ts, 2)
	if errDetail != nil {
		return nil, errDetail
	}
	events = append(events, registryEvents...)
	return events, nil
}

func nativeBoardMembershipReviewEvents(db *sql.DB, record CommandLogRecord, actor *User, applicationID, boardID, userID, status, title, note string, ts int64) ([]EventAppend, *proto.ErrorDetail) {
	events := []EventAppend{nativeEvent(record, 0, proto.EvtBoardMemberApplicationReviewed, []string{"board:" + boardID, "user:" + userID}, &proto.BoardMemberApplicationReviewedPayload{
		Application: applicationID,
		Board:       boardID,
		User:        userID,
		Status:      status,
		Title:       title,
		Reviewer:    actor.ID,
		ReviewNote:  note,
		TS:          ts,
	}, ts)}
	registryEvents, errDetail := nativeBoardRegistrationSystemLogEvents(db, record, actor, applicationID, status, boardID, userID, ts, 1)
	if errDetail != nil {
		return nil, errDetail
	}
	events = append(events, registryEvents...)
	return events, nil
}

func nativeBoardRegistrationSystemLogEvents(db *sql.DB, record CommandLogRecord, actor *User, applicationID, status, boardID, userID string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	boardIDOut, boardDescription, threadID, postID, ok := proto.BoardRegistrationSystemPlan(status, applicationID)
	if !ok {
		return nil, nil
	}
	emit, err := projections.BoardAllowsPublicSystemPost(db, boardID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !emit {
		return nil, nil
	}
	exists, err := projections.ThreadExists(db, threadID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if exists {
		return nil, nil
	}
	sourceBoardName, found, err := projections.BoardName(db, boardID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return nil, nativeDecisionErr("internal_error", sql.ErrNoRows.Error(), true)
	}
	applicantName, err := projections.UserName(db, userID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	title, body := proto.BoardRegistrationSystemContent(status, applicationID, sourceBoardName, boardID, applicantName, actor.Name)

	return nativeGeneratedSystemPostEvents(db, record, actor, nativeGeneratedSystemPostSpec{
		BoardID:     boardIDOut,
		BoardName:   boardIDOut,
		Description: boardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
		BoardMode:   nativeGeneratedBoardIfMissingReserve,
	}, ts, startIndex)
}

func nativeSyssecuritySystemLogEvents(db *sql.DB, record CommandLogRecord, actor *User, title string, lines []string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	title = proto.NormalizeSyssecuritySystemTitle(title)
	body := proto.FormatSyssecuritySystemBody(title, lines)
	threadID := stableCommandLogDecisionID("syssecurity_thr_", record, startIndex)
	postID := stableCommandLogDecisionID("syssecurity_pst_", record, startIndex)
	return nativeGeneratedSystemPostEvents(db, record, actor, nativeGeneratedSystemPostSpec{
		BoardID:     proto.SyssecuritySystemBoardID,
		BoardName:   proto.SyssecuritySystemBoardID,
		Description: proto.SyssecuritySystemBoardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
		BoardMode:   nativeGeneratedBoardIfMissingReserve,
	}, ts, startIndex)
}

func nativeSysmailSystemLogEvents(db *sql.DB, record CommandLogRecord, actor *User, mailID, subject, mailBody string, recipientCount int, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	threadID := "sysmail_thr_" + mailID
	postID := "sysmail_pst_" + mailID
	exists, err := projections.ThreadExists(db, threadID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if exists {
		return nil, nil
	}

	title := "Sysop mail: " + subject
	body := proto.FormatSysmailSystemBody(mailID, subject, mailBody, actor.Name, recipientCount)
	return nativeGeneratedSystemPostEvents(db, record, actor, nativeGeneratedSystemPostSpec{
		BoardID:     proto.SysmailSystemBoardID,
		BoardName:   proto.SysmailSystemBoardID,
		Description: proto.SysmailSystemBoardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
		BoardMode:   nativeGeneratedBoardIfMissingCompact,
	}, ts, startIndex)
}

func nativeBlessingSystemLogEvents(db *sql.DB, record CommandLogRecord, actor, target *User, blessingID, message string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	if target == nil {
		return nil, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	threadID, postID := proto.BlessingSystemPostIDs(blessingID)
	exists, err := projections.ThreadExists(db, threadID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if exists {
		return nil, nil
	}

	title := proto.BlessingSystemTitle(actor.Name, target.Name)
	body := proto.FormatBlessingSystemBody(actor.Name, target.Name, message)
	return nativeGeneratedSystemPostEvents(db, record, actor, nativeGeneratedSystemPostSpec{
		BoardID:     proto.BlessingSystemBoardID,
		BoardName:   proto.BlessingSystemBoardName,
		Description: proto.BlessingSystemBoardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
		BoardMode:   nativeGeneratedBoardIfMissingReserve,
	}, ts, startIndex)
}

func nativeDenyPostSystemLogEvents(db *sql.DB, record CommandLogRecord, actor, target *User, sourceBoardID, sourceBoardName, kind, reason string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	return nativeSanctionSystemLogEvents(db, record, proto.DenyPostSystemBoardID, proto.DenyPostSystemBoardID, proto.DenyPostSystemBoardDescription, actor, target, sourceBoardID, sourceBoardName, kind, reason, "Board posting denied", ts, startIndex)
}

func nativeUndenyPostSystemLogEvents(db *sql.DB, record CommandLogRecord, actor, target *User, sourceBoardID, sourceBoardName, kind, reason string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	return nativeSanctionSystemLogEvents(db, record, proto.UndenyPostSystemBoardID, proto.UndenyPostSystemBoardID, proto.UndenyPostSystemBoardDescription, actor, target, sourceBoardID, sourceBoardName, kind, reason, "Board posting restored", ts, startIndex)
}

func nativeSanctionSystemLogEvents(db *sql.DB, record CommandLogRecord, systemBoardID, systemBoardName, systemBoardDescription string, actor, target *User, sourceBoardID, sourceBoardName, kind, reason, action string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	if target == nil {
		return nil, nativeDecisionErr(proto.ErrNotFound, "user not found", false)
	}
	emit, err := projections.BoardAllowsPublicSystemPost(db, sourceBoardID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !emit {
		return nil, nil
	}

	threadID := stableCommandLogDecisionID(systemBoardID+"_thr_", record, startIndex)
	postID := stableCommandLogDecisionID(systemBoardID+"_pst_", record, startIndex)
	title := fmt.Sprintf("%s: %s on %s", action, target.Name, sourceBoardID)
	body := proto.FormatSanctionSystemBody(action, target.Name, sourceBoardName, sourceBoardID, kind, actor.Name, reason)
	return nativeGeneratedSystemPostEvents(db, record, actor, nativeGeneratedSystemPostSpec{
		BoardID:     systemBoardID,
		BoardName:   systemBoardName,
		Description: systemBoardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
		BoardMode:   nativeGeneratedBoardAlwaysUpsert,
	}, ts, startIndex)
}

func nativeModerationSystemLogEvents(db *sql.DB, record CommandLogRecord, actor *User, action, reviewID, postID, threadID, boardID string, publicBoard bool, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if !publicBoard {
		return nil, nil
	}
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	title := proto.ModerationSystemTitle(action, reviewID)
	body := proto.FormatModerationSystemBody(action, reviewID, boardID, threadID, postID, actor.Name)
	threadIDOut, postIDOut := proto.ModerationSystemPostIDs(action, reviewID)
	return nativeGeneratedSystemPostEvents(db, record, actor, nativeGeneratedSystemPostSpec{
		BoardID:     proto.ModerationSystemBoardID,
		BoardName:   proto.ModerationSystemBoardName,
		Description: proto.ModerationSystemBoardDescription,
		ThreadID:    threadIDOut,
		PostID:      postIDOut,
		Title:       title,
		Body:        body,
		BoardMode:   nativeGeneratedBoardAlwaysNextPosition,
	}, ts, startIndex)
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
		nativeEvent(
			record,
			startIndex,
			proto.EvtPostFlagged,
			[]string{"moderation:global"}, // moderation-only: reporter/reason not broadcast to board (M8)
			&proto.PostFlaggedPayload{
				ReviewID: reviewID,
				Kind:     "content_filter",
				PostID:   postID,
				Thread:   threadID,
				Reporter: actor.ID,
				Reason:   reason,
				TS:       ts,
			},
			ts,
		),
	}
	if !publicBoard {
		return events, nil
	}

	threadIDOut, postIDOut := proto.ContentFilterReviewPostIDs(reviewID)
	title := proto.ContentFilterReviewTitle(reviewID)
	filterID := ""
	filterScope := ""
	if filter != nil {
		filterID = strings.TrimSpace(filter.ID)
		filterScope = strings.TrimSpace(filter.Scope)
	}
	body := proto.FormatContentFilterReviewBody(title, reviewID, filterID, filterScope, boardID, threadID, postID, publicAuthor)
	generatedEvents, errDetail := nativeGeneratedSystemPostEvents(db, record, actor, nativeGeneratedSystemPostSpec{
		BoardID:     proto.ContentFilterSystemBoardID,
		BoardName:   proto.ContentFilterSystemBoardName,
		Description: proto.ContentFilterSystemBoardDescription,
		ThreadID:    threadIDOut,
		PostID:      postIDOut,
		Title:       title,
		Body:        body,
		BoardMode:   nativeGeneratedBoardAlwaysNextPosition,
	}, ts, startIndex+len(events))
	if errDetail != nil {
		return nil, errDetail
	}
	events = append(events, generatedEvents...)
	return events, nil
}

func nativeDigestEntryForCuration(db *sql.DB, actor *User, entryID string) (projections.DigestPathEntryRow, *proto.ErrorDetail) {
	entry, found, err := projections.DigestPathEntryByID(db, entryID)
	if err != nil {
		return entry, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		return entry, nativeDecisionErr(proto.ErrNotFound, "digest entry not found", false)
	}
	canCurate, err := projections.ActorCanCurateBoardKind(db, actor, entry.BoardID, entry.Kind)
	if err != nil {
		return entry, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !canCurate {
		return entry, nativeDecisionErr(proto.ErrForbidden, proto.DigestCurationPermissionMessage(entry.Kind), false)
	}
	return entry, nil
}

func nativeValidateDigestEntryCommandPartition(record CommandLogRecord, entry projections.DigestPathEntryRow) *proto.ErrorDetail {
	partition := record.Partition.Normalize()
	if partition.Kind == partitionBoard && (partition.Key == entry.ID || partition.Key == entry.BoardID) {
		return nil
	}
	return nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
		partition.Kind, partition.Key, partitionBoard, entry.ID), false)
}

func nativeDigestPathChildrenForCopy(db *sql.DB, record CommandLogRecord, boardID, kind, path string) ([]projections.DigestPathEntryRow, []projections.DigestPathDirectoryRow, *proto.ErrorDetail) {
	entries, err := projections.DigestPathEntries(db, boardID, kind, path)
	if err != nil {
		return nil, nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	dirs, err := projections.DigestPathDirectories(db, boardID, kind, path)
	if err != nil {
		return nil, nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	entryIDs := nativeStableDecisionIDSet("dig_", record, len(entries))
	filteredEntries := entries[:0]
	for _, entry := range entries {
		if _, copiedByThisCommand := entryIDs[entry.ID]; !copiedByThisCommand {
			filteredEntries = append(filteredEntries, entry)
		}
	}
	dirIDs := nativeStableDecisionIDSet("dir_", record, len(dirs))
	filteredDirs := dirs[:0]
	for _, dir := range dirs {
		if _, copiedByThisCommand := dirIDs[dir.ID]; !copiedByThisCommand {
			filteredDirs = append(filteredDirs, dir)
		}
	}
	return filteredEntries, filteredDirs, nil
}

func nativeStableDecisionIDSet(prefix string, record CommandLogRecord, count int) map[string]struct{} {
	ids := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		ids[stableCommandLogDecisionID(prefix, record, i)] = struct{}{}
	}
	return ids
}

func nativeDigestMirrorSystemLogEvents(db *sql.DB, record CommandLogRecord, actor *User, entryID string, export *DigestExport, mirror projections.DigestMirrorSystemBoard, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	if actor == nil {
		return nil, nativeDecisionErr(proto.ErrUnauthenticated, "login required", false)
	}
	if export == nil {
		return nil, nil
	}
	if export.Entry.Kind != mirror.Kind || export.Entry.BoardID == mirror.BoardID {
		return nil, nil
	}
	emit, err := projections.BoardAllowsPublicSystemPost(db, export.Entry.BoardID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !emit {
		return nil, nil
	}

	threadID := mirror.ThreadID + entryID
	postID := mirror.PostID + entryID
	exists, err := projections.ThreadExists(db, threadID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if exists {
		return nil, nil
	}

	title := strings.TrimSpace(export.Entry.Title)
	if title == "" {
		title = mirror.Default
	}
	body := projections.FormatDigestExportText(export)
	return nativeGeneratedSystemPostEvents(db, record, actor, nativeGeneratedSystemPostSpec{
		BoardID:     mirror.BoardID,
		BoardName:   mirror.Name,
		Description: mirror.Description,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
		BoardMode:   nativeGeneratedBoardIfMissingCompact,
	}, ts, startIndex)
}

func nativeMailCopyUpdateScopes(fromUserID, actorID, mailID string) []string {
	scopes := []string{"account:" + fromUserID}
	if actorID != fromUserID {
		scopes = append(scopes, "account:"+actorID)
	}
	return append(scopes, "mail:"+mailID)
}

func nativeMailCopyUpdatedEvent(record CommandLogRecord, index int, target projections.MailCopyUpdateTarget, actorID, mailID string, mailbox *string, read, kept *bool, ts int64) EventAppend {
	return nativeEvent(record, index, proto.EvtMailCopyUpdated, nativeMailCopyUpdateScopes(target.FromUserID, actorID, mailID), &proto.MailCopyUpdatedPayload{
		Mail:    mailID,
		UserID:  actorID,
		Mailbox: mailbox,
		Read:    read,
		Kept:    kept,
		TS:      ts,
	}, ts)
}

func nativeValidateStagedPostAttachmentBlob(db *sql.DB, stagedBlobID, attachmentID string, expectedSize int64, contentType string) *proto.ErrorDetail {
	return nativeValidateStagedAttachmentBlob(db, projections.StagedBlobPostAttachment, stagedBlobID, attachmentID, expectedSize, contentType,
		"staged attachment blob is not available yet", "staged attachment blob kind does not match post attachment")
}

func nativeValidateStagedMailAttachmentBlob(db *sql.DB, stagedBlobID, attachmentID string, expectedSize int64, contentType string) *proto.ErrorDetail {
	return nativeValidateStagedAttachmentBlob(db, projections.StagedBlobMailAttachment, stagedBlobID, attachmentID, expectedSize, contentType,
		"staged mail attachment blob is not available yet", "staged attachment blob kind does not match mail attachment")
}

func nativeValidateStagedAttachmentBlob(db *sql.DB, expectedKind, stagedBlobID, attachmentID string, expectedSize int64, contentType, missingMessage, kindMismatchMessage string) *proto.ErrorDetail {
	stagedBlobID = strings.TrimSpace(stagedBlobID)
	if stagedBlobID == "" {
		return nil
	}
	info, found, err := projections.GetStagedAttachmentBlobInfo(db, stagedBlobID)
	if err != nil {
		return nativeDecisionErr("internal_error", err.Error(), true)
	}
	if !found {
		ok, promotedErr := projections.PromotedAttachmentBlobMatches(db, expectedKind, attachmentID, expectedSize, contentType)
		if promotedErr != nil {
			return nativeDecisionErr("internal_error", promotedErr.Error(), true)
		}
		if ok {
			return nil
		}
		return nativeDecisionErr(proto.ErrBlobStagingRequired, missingMessage, true)
	}
	if info.Kind != expectedKind {
		return nativeDecisionErr(proto.ErrValidationFailed, kindMismatchMessage, false)
	}
	if expectedSize >= 0 && info.SizeBytes != expectedSize {
		return nativeDecisionErr(proto.ErrValidationFailed, fmt.Sprintf("%s: staged size %d does not match command size %d", projections.ErrStagedAttachmentBlobMismatch, info.SizeBytes, expectedSize), false)
	}
	return nil
}

func nativeArticleMailBackEvent(db *sql.DB, record CommandLogRecord, actor *User, authorName, authorID string, thread *Thread, target *Post, replyPostID, replyBody string, ts int64, eventIndex int) (*EventAppend, *proto.ErrorDetail) {
	if actor == nil || thread == nil || target == nil || !target.MailBack || target.Redacted {
		return nil, nil
	}
	authorID = strings.TrimSpace(authorID)
	if authorID == "" || strings.TrimSpace(target.AuthorID) == "" || target.AuthorID == authorID {
		return nil, nil
	}
	recipient, err := projections.GetUserByID(db, target.AuthorID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if recipient == nil {
		return nil, nil
	}
	ignored, err := projections.UserRelationshipExists(db, recipient.ID, actor.ID, "ignore")
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if ignored {
		return nil, nil
	}
	subject := "Article reply: " + thread.Title
	body := proto.FormatArticleMailBackBody(thread.Board, thread.Title, target.ID, replyPostID, actor.Name, replyBody)
	if errDetail := commandrules.EnsureMailQuota(db, map[string]int{recipient.ID: 1}, proto.MailMessageSize(subject, body, nil)); errDetail != nil {
		if errDetail.Code != proto.ErrValidationFailed {
			return nil, errDetail
		}
		return nil, nil
	}
	authorName = strings.TrimSpace(authorName)
	if authorName == "" {
		authorName = actor.Name
	}
	mailID := stableCommandLogDecisionID("mail_", record, eventIndex)
	event := nativeEvent(record, eventIndex, proto.EvtMailSent, []string{"account:" + actor.ID, "account:" + recipient.ID}, proto.NewMailSentPayload(mailID, authorID, authorName, []string{recipient.ID}, []string{recipient.Name}, subject, body, "", false, nil, ts), ts)
	return &event, nil
}

func nativePostSignature(db *sql.DB, authorID string, record CommandLogRecord) (string, error) {
	return projections.CurrentPostSignature(db, authorID, func(count int) int {
		return nativeSignatureOffset(record, count)
	})
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
