package commandrules

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func BoardPermissionAllowed(ok bool, err error) bool {
	return err == nil && ok
}

func ActorCanModerateBoard(queryable Queryable, actor *projections.User, boardID string) bool {
	return BoardPermissionAllowed(projections.ActorCanModerateBoard(queryable, actor, boardID))
}

func RequireBoardModerationPermission(canModerateBoard bool) *proto.ErrorDetail {
	if !canModerateBoard {
		return newErrDetail(proto.ErrForbidden, "board moderation permission required", false)
	}
	return nil
}

func ActorCanUseMemberBoard(queryable Queryable, actor *projections.User, boardID string) bool {
	return BoardPermissionAllowed(projections.ActorCanUseMemberBoard(queryable, actor, boardID))
}

func ActorCanManageBoardMembers(queryable Queryable, actor *projections.User, boardID string) bool {
	return BoardPermissionAllowed(projections.ActorCanManageBoardMembers(queryable, actor, boardID))
}

func ActorCanSetBoardSettings(queryable Queryable, actor *projections.User, boardID string) bool {
	return BoardPermissionAllowed(projections.ActorCanSetBoardSettings(queryable, actor, boardID))
}

func RequireBoardSettingsPermission(canSetBoardSettings bool) *proto.ErrorDetail {
	if !canSetBoardSettings {
		return newErrDetail(proto.ErrForbidden, "board settings permission required", false)
	}
	return nil
}

func ActorCanCurateBoard(queryable Queryable, actor *projections.User, boardID string) bool {
	return BoardPermissionAllowed(projections.ActorCanCurateBoard(queryable, actor, boardID))
}

func ActorCanModerateBoardThreads(queryable Queryable, actor *projections.User, boardID string) bool {
	return BoardPermissionAllowed(projections.ActorCanModerateBoardThreads(queryable, actor, boardID))
}

func ActorCanManageBoardPolls(queryable Queryable, actor *projections.User, boardID string) bool {
	return BoardPermissionAllowed(projections.ActorCanManageBoardPolls(queryable, actor, boardID))
}

func ActorCanCurateBoardKind(queryable Queryable, actor *projections.User, boardID, kind string) bool {
	return BoardPermissionAllowed(projections.ActorCanCurateBoardKind(queryable, actor, boardID, kind))
}

func ActorCanModerateBoardPosts(queryable Queryable, actor *projections.User, boardID string) bool {
	return BoardPermissionAllowed(projections.ActorCanModerateBoardPosts(queryable, actor, boardID))
}

func RequireBoardSanctionScopePermission(queryable Queryable, actor *projections.User, boardID string) *proto.ErrorDetail {
	if _, found, err := projections.BoardName(queryable, boardID); err != nil {
		return internalErr(err)
	} else if !found {
		return newErrDetail(proto.ErrNotFound, "board not found for scope", false)
	}
	if !ActorCanModerateBoardPosts(queryable, actor, boardID) {
		return newErrDetail(proto.ErrForbidden, "you do not moderate this board", false)
	}
	return nil
}
