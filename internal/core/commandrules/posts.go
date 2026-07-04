package commandrules

import (
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
	if !allowed && !canModerateBoard {
		return nil, newErrDetail(proto.ErrForbidden, "attachments are not enabled for this board", false)
	}
	attachments, msg := proto.NormalizePostAttachments(input)
	if msg != "" {
		return nil, newErrDetail(proto.ErrValidationFailed, msg, false)
	}
	return proto.WithAttachmentIDs(attachments, idFor), nil
}

func boardRequiresPostingMembership(settings *projections.BoardSettings) bool {
	return settings != nil && (settings.MemberReadMode || settings.MemberPostMode)
}

func boardRequiresReadMembership(settings *projections.BoardSettings) bool {
	return settings != nil && settings.MemberReadMode
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
	if settings == nil {
		return nil
	}
	if settings.ReadOnly && !canModerateBoard {
		return newErrDetail(proto.ErrForbidden, "board is read-only", false)
	}
	return requirePostingMemberAccess(settings, canUseMemberBoard, "board members only")
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
	if settings == nil {
		return nil
	}
	if (settings.ReadOnly || settings.NoReply) && !canModerateBoard {
		return newErrDetail(proto.ErrForbidden, "board is not accepting replies", false)
	}
	return requirePostingMemberAccess(settings, canUseMemberBoard, "board members only")
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

func requireMemberBoardReadAccess(settings *projections.BoardSettings, message string, canUseMemberBoard func() (bool, *proto.ErrorDetail)) *proto.ErrorDetail {
	if !boardRequiresReadMembership(settings) {
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

func requirePostingMemberAccess(settings *projections.BoardSettings, canUseMemberBoard func() (bool, *proto.ErrorDetail), message string) *proto.ErrorDetail {
	if !boardRequiresPostingMembership(settings) {
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
