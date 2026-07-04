package commandrules

import "github.com/juncoflockleader/budgie-bbs/internal/core/projections"

func BoardPermissionAllowed(ok bool, err error) bool {
	return err == nil && ok
}

func ActorCanModerateBoard(queryable Queryable, actor *projections.User, boardID string) bool {
	return BoardPermissionAllowed(projections.ActorCanModerateBoard(queryable, actor, boardID))
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
