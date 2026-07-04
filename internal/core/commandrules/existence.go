package commandrules

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func RequireFavoriteFolder(queryable Queryable, userID, folderID string) *proto.ErrorDetail {
	exists, err := projections.FavoriteFolderExists(queryable, userID, folderID)
	if err != nil {
		return internalErr(err)
	}
	if !exists {
		return newErrDetail(proto.ErrNotFound, "favorite folder not found", false)
	}
	return nil
}

func RequireBoard(queryable Queryable, boardID string) *proto.ErrorDetail {
	exists, err := projections.BoardExists(queryable, boardID)
	if err != nil {
		return internalErr(err)
	}
	if !exists {
		return newErrDetail(proto.ErrNotFound, "board not found", false)
	}
	return nil
}

func RequirePost(queryable Queryable, postID string) *proto.ErrorDetail {
	exists, err := projections.PostExists(queryable, postID)
	if err != nil {
		return internalErr(err)
	}
	if !exists {
		return newErrDetail(proto.ErrNotFound, "post not found", false)
	}
	return nil
}

func RequireThread(queryable Queryable, threadID string) *proto.ErrorDetail {
	exists, err := projections.ThreadExists(queryable, threadID)
	if err != nil {
		return internalErr(err)
	}
	if !exists {
		return newErrDetail(proto.ErrNotFound, "thread not found", false)
	}
	return nil
}
