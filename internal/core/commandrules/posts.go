package commandrules

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/boardmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func PostIdentity(actor *projections.User, settings *projections.BoardSettings, anonymous bool, canModerateBoard bool) (string, string, *proto.ErrorDetail) {
	actorName, actorID := "", ""
	if actor != nil {
		actorName, actorID = actor.Name, actor.ID
	}
	anonymousAllowed := settings != nil && settings.AnonymousAllowed
	author, authorID, msg := proto.ResolvePostAuthorIdentity(actorName, actorID, anonymous, anonymousAllowed, canModerateBoard)
	if msg != "" {
		return "", "", newErrDetail(proto.ErrForbidden, msg, false)
	}
	return author, authorID, nil
}

func NormalizePostAttachments(input []proto.AttachmentPayload, allowed bool, canModerateBoard bool, idFor func(int) string) ([]proto.AttachmentPayload, *proto.ErrorDetail) {
	if len(input) == 0 {
		return nil, nil
	}
	if errDetail := RequirePostAttachmentsAllowed(allowed, canModerateBoard); errDetail != nil {
		return nil, errDetail
	}
	attachments, msg := proto.NormalizePostAttachments(input)
	if msg != "" {
		return nil, newErrDetail(proto.ErrValidationFailed, msg, false)
	}
	return proto.WithAttachmentIDs(attachments, idFor), nil
}

func RequirePostAttachmentsAllowed(allowed, canModerateBoard bool) *proto.ErrorDetail {
	if !allowed && !canModerateBoard {
		return newErrDetail(proto.ErrForbidden, "attachments are not enabled for this board", false)
	}
	return nil
}

func RequirePostAttachmentCapacity(existingCount int) *proto.ErrorDetail {
	if msg := proto.ValidatePostAttachmentCount(existingCount + 1); msg != "" {
		return newErrDetail(proto.ErrValidationFailed, msg, false)
	}
	return nil
}

func RequireBoardMailInAllowed(settings *projections.BoardSettings, canModerateBoard bool) *proto.ErrorDetail {
	if !boardPostPolicySettings(settings).AllowsMailIn(canModerateBoard) {
		return newErrDetail(proto.ErrForbidden, "board mail-in is disabled", false)
	}
	return nil
}

func ResolveBoardMailTargetBoard(boardID, threadBoardID string, hasThread bool) (string, *proto.ErrorDetail) {
	if hasThread {
		if boardID == "" {
			return threadBoardID, nil
		}
		if boardID != threadBoardID {
			return "", newErrDetail(proto.ErrValidationFailed, "thread does not belong to board", false)
		}
	}
	if boardID == "" {
		return "", newErrDetail(proto.ErrValidationFailed, "board is required", false)
	}
	return boardID, nil
}

func ActorAuthoredBy(actor *projections.User, authorID, authorName string) bool {
	if actor == nil {
		return false
	}
	if authorID != "" {
		return authorID == actor.ID
	}
	return authorName == actor.Name
}

func ActorAuthoredByID(actor *projections.User, authorID string) bool {
	return actor != nil && authorID != "" && authorID == actor.ID
}

func WithinAuthorEditWindow(nowMS, createdAt, windowMS int64) bool {
	return nowMS-createdAt < windowMS
}

func AuthorEditWindowExpiredError() *proto.ErrorDetail {
	return newErrDetail(proto.ErrEditWindowExpired, "edit window has expired", false)
}

func RequirePostAuthorEditWindow(canBypassAuthorWindow, isAuthor, withinWindow bool) *proto.ErrorDetail {
	if canBypassAuthorWindow || (isAuthor && withinWindow) {
		return nil
	}
	return AuthorEditWindowExpiredError()
}

func RequireThreadCreationBoardAccess(queryable Queryable, actor *projections.User, boardID string, settings *projections.BoardSettings, canModerateBoard bool) *proto.ErrorDetail {
	return requireThreadCreationBoardAccess(settings, canModerateBoard, func() (bool, *proto.ErrorDetail) {
		return ActorCanUseMemberBoard(queryable, actor, boardID), nil
	})
}

func RequireThreadCreationBoardAccessStrict(queryable Queryable, actor *projections.User, boardID string, settings *projections.BoardSettings, canModerateBoard bool) *proto.ErrorDetail {
	return requireThreadCreationBoardAccess(settings, canModerateBoard, func() (bool, *proto.ErrorDetail) {
		return actorCanUseMemberBoardStrict(queryable, actor, boardID)
	})
}

func requireThreadCreationBoardAccess(settings *projections.BoardSettings, canModerateBoard bool, canUseMemberBoard func() (bool, *proto.ErrorDetail)) *proto.ErrorDetail {
	policy := boardPostPolicySettings(settings)
	if policy.BlocksThreadCreation(canModerateBoard) {
		return newErrDetail(proto.ErrForbidden, "board is read-only", false)
	}
	return requirePostingMemberAccess(policy, canUseMemberBoard, "board members only")
}

func RequireReplyBoardAccess(queryable Queryable, actor *projections.User, boardID string, settings *projections.BoardSettings, canModerateBoard bool) *proto.ErrorDetail {
	return requireReplyBoardAccess(settings, canModerateBoard, func() (bool, *proto.ErrorDetail) {
		return ActorCanUseMemberBoard(queryable, actor, boardID), nil
	})
}

func RequireReplyBoardAccessStrict(queryable Queryable, actor *projections.User, boardID string, settings *projections.BoardSettings, canModerateBoard bool) *proto.ErrorDetail {
	return requireReplyBoardAccess(settings, canModerateBoard, func() (bool, *proto.ErrorDetail) {
		return actorCanUseMemberBoardStrict(queryable, actor, boardID)
	})
}

func requireReplyBoardAccess(settings *projections.BoardSettings, canModerateBoard bool, canUseMemberBoard func() (bool, *proto.ErrorDetail)) *proto.ErrorDetail {
	policy := boardPostPolicySettings(settings)
	if policy.BlocksReply(canModerateBoard) {
		return newErrDetail(proto.ErrForbidden, "board is not accepting replies", false)
	}
	return requirePostingMemberAccess(policy, canUseMemberBoard, "board members only")
}

func RequireMemberBoardReadAccess(queryable Queryable, actor *projections.User, boardID string, settings *projections.BoardSettings, message string) *proto.ErrorDetail {
	return requireMemberBoardReadAccess(settings, message, func() (bool, *proto.ErrorDetail) {
		return ActorCanUseMemberBoard(queryable, actor, boardID), nil
	})
}

func RequireMemberBoardReadAccessStrict(queryable Queryable, actor *projections.User, boardID string, settings *projections.BoardSettings, message string) *proto.ErrorDetail {
	return requireMemberBoardReadAccess(settings, message, func() (bool, *proto.ErrorDetail) {
		return actorCanUseMemberBoardStrict(queryable, actor, boardID)
	})
}

type ReplyTargetPlan struct {
	EffectiveReplyTo  string
	QuoteSource       *projections.Post
	MailBackTarget    *projections.Post
	ReplyNotifyTarget *projections.Post
	NeedsRootMailBack bool
}

func PlanReplyTarget(replyTo string, parent *projections.Post, threadID string, quotePost bool, canModerateThread bool) (ReplyTargetPlan, *proto.ErrorDetail) {
	if replyTo == "" {
		if quotePost {
			return ReplyTargetPlan{}, newErrDetail(proto.ErrValidationFailed, "replyTo is required for quoted replies", false)
		}
		return ReplyTargetPlan{NeedsRootMailBack: true}, nil
	}
	if parent == nil {
		return ReplyTargetPlan{}, newErrDetail(proto.ErrNotFound, "replyTo post not found", false)
	}
	if parent.Thread != threadID {
		return ReplyTargetPlan{}, newErrDetail(proto.ErrValidationFailed, "replyTo post belongs to another thread", false)
	}
	plan := ReplyTargetPlan{
		EffectiveReplyTo:  parent.ID,
		ReplyNotifyTarget: parent,
	}
	if quotePost {
		if parent.Redacted {
			return ReplyTargetPlan{}, newErrDetail(proto.ErrConflict, "cannot quote a redacted post", false)
		}
		plan.QuoteSource = parent
	}
	if parent.NoReply && !canModerateThread {
		return ReplyTargetPlan{}, newErrDetail(proto.ErrForbidden, "article is not accepting replies", false)
	}
	if parent.MailBack {
		plan.MailBackTarget = parent
	}
	if parent.ReplyTo != "" {
		plan.EffectiveReplyTo = parent.ReplyTo
	}
	return plan, nil
}

type PostFlagPlan struct {
	Marked                 bool
	Recommended            bool
	NoReply                bool
	TeX                    bool
	MailBack               bool
	CuratorChange          bool
	ThreadModerationChange bool
	AuthorMetadataChange   bool
}

func PlanPostFlagUpdate(post *projections.Post, payload proto.SetPostFlagPayload) PostFlagPlan {
	plan := PostFlagPlan{
		Marked:      post.Marked,
		Recommended: post.Recommended,
		NoReply:     post.NoReply,
		TeX:         post.TeX,
		MailBack:    post.MailBack,
	}
	if payload.Marked != nil {
		plan.CuratorChange = plan.CuratorChange || *payload.Marked != post.Marked
		plan.Marked = *payload.Marked
	}
	if payload.Recommended != nil {
		plan.CuratorChange = plan.CuratorChange || *payload.Recommended != post.Recommended
		plan.Recommended = *payload.Recommended
	}
	if payload.NoReply != nil {
		plan.ThreadModerationChange = *payload.NoReply != post.NoReply
		plan.NoReply = *payload.NoReply
	}
	if payload.TeX != nil {
		plan.AuthorMetadataChange = plan.AuthorMetadataChange || *payload.TeX != post.TeX
		plan.TeX = *payload.TeX
	}
	if payload.MailBack != nil {
		plan.AuthorMetadataChange = plan.AuthorMetadataChange || *payload.MailBack != post.MailBack
		plan.MailBack = *payload.MailBack
	}
	return plan
}

func (plan PostFlagPlan) HasChanges() bool {
	return plan.CuratorChange || plan.ThreadModerationChange || plan.AuthorMetadataChange
}

func RequirePostFlagPermissions(plan PostFlagPlan, actor *projections.User, post *projections.Post, canCurate, canModerateThread bool) *proto.ErrorDetail {
	if plan.CuratorChange && !canCurate {
		return newErrDetail(proto.ErrForbidden, "board curator permission required", false)
	}
	if plan.ThreadModerationChange && !canModerateThread {
		return newErrDetail(proto.ErrForbidden, "board thread moderation permission required", false)
	}
	if plan.AuthorMetadataChange && (actor == nil || (actor.ID != post.AuthorID && !canModerateThread)) {
		return newErrDetail(proto.ErrForbidden, "post author or board thread moderation permission required", false)
	}
	return nil
}

func PlanPostRedaction(canModeratePosts, isAuthor, withinWindow bool) (string, *proto.ErrorDetail) {
	if !canModeratePosts && !(isAuthor && withinWindow) {
		return "", newErrDetail(proto.ErrForbidden, "insufficient permissions to redact this post", false)
	}
	if !canModeratePosts {
		return "junk", nil
	}
	return "recycle", nil
}

func RequirePostNotRedacted(redacted bool, message string) *proto.ErrorDetail {
	if redacted {
		return newErrDetail(proto.ErrConflict, message, false)
	}
	return nil
}

func RequirePostRedacted(redacted bool, message string) *proto.ErrorDetail {
	if !redacted {
		return newErrDetail(proto.ErrConflict, message, false)
	}
	return nil
}

func RequireThreadOpenForReply(locked, canModerateBoard bool) *proto.ErrorDetail {
	if locked && !canModerateBoard {
		return newErrDetail(proto.ErrThreadLocked, "thread is locked", false)
	}
	return nil
}

func RequireThreadStarterAcceptsReplies(noReply, canModerateThread bool) *proto.ErrorDetail {
	if noReply && !canModerateThread {
		return newErrDetail(proto.ErrForbidden, "thread starter is not accepting replies", false)
	}
	return nil
}

func RequireThreadTitlePermission(canModerateThread, isAuthor, withinWindow bool) *proto.ErrorDetail {
	if canModerateThread {
		return nil
	}
	if !isAuthor {
		return newErrDetail(proto.ErrForbidden, "thread author or board thread moderation permission required", false)
	}
	if !withinWindow {
		return AuthorEditWindowExpiredError()
	}
	return nil
}

func RequireThreadModeration(canModerateThread bool) *proto.ErrorDetail {
	if !canModerateThread {
		return newErrDetail(proto.ErrForbidden, "board thread moderation permission required", false)
	}
	return nil
}

func RequireBoardPostModeration(canModeratePosts bool) *proto.ErrorDetail {
	if !canModeratePosts {
		return newErrDetail(proto.ErrForbidden, "board post moderation permission required", false)
	}
	return nil
}

func requireMemberBoardReadAccess(settings *projections.BoardSettings, message string, canUseMemberBoard func() (bool, *proto.ErrorDetail)) *proto.ErrorDetail {
	policy := boardPostPolicySettings(settings)
	if !policy.RequiresReadMembership() {
		return nil
	}
	canUse, errDetail := canUseMemberBoard()
	if errDetail != nil {
		return errDetail
	}
	if !canUse {
		return newErrDetail(proto.ErrForbidden, message, false)
	}
	return nil
}

func requirePostingMemberAccess(settings *boardmodel.PostPolicySettings, canUseMemberBoard func() (bool, *proto.ErrorDetail), message string) *proto.ErrorDetail {
	if !settings.RequiresPostingMembership() {
		return nil
	}
	canUse, errDetail := canUseMemberBoard()
	if errDetail != nil {
		return errDetail
	}
	if !canUse {
		return newErrDetail(proto.ErrForbidden, message, false)
	}
	return nil
}

func boardPostPolicySettings(settings *projections.BoardSettings) *boardmodel.PostPolicySettings {
	if settings == nil {
		return nil
	}
	return &boardmodel.PostPolicySettings{
		ReadOnly:       settings.ReadOnly,
		NoReply:        settings.NoReply,
		MailInAllowed:  settings.MailInAllowed,
		MemberReadMode: settings.MemberReadMode,
		MemberPostMode: settings.MemberPostMode,
	}
}

func actorCanUseMemberBoardStrict(queryable Queryable, actor *projections.User, boardID string) (bool, *proto.ErrorDetail) {
	canUse, err := projections.ActorCanUseMemberBoard(queryable, actor, boardID)
	if err != nil {
		return false, internalErr(err)
	}
	return canUse, nil
}

func ActiveBoardSanctionError(kind string) *proto.ErrorDetail {
	code := proto.ErrMuted
	if kind == "ban" {
		code = proto.ErrBanned
	}
	return newErrDetail(code, "you are "+kind+"d in this board", false)
}
