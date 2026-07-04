package commandrules

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func EnsureRangeBoardAccess(queryable Queryable, actor *projections.User, boardID string) *proto.ErrorDetail {
	if exists, err := projections.BoardExists(queryable, boardID); err != nil {
		return internalErr(err)
	} else if !exists {
		return newErrDetail(proto.ErrNotFound, "board not found", false)
	}
	canModeratePosts, err := projections.ActorCanModerateBoardPosts(queryable, actor, boardID)
	if err != nil {
		return internalErr(err)
	}
	if !canModeratePosts {
		return newErrDetail(proto.ErrForbidden, "board post moderation permission required", false)
	}
	return nil
}

func LoadRangePost(postID, boardID string, getPost func(string) (*projections.Post, error), getThread func(string) (*projections.Thread, error)) (*projections.Post, *projections.Thread, *proto.ErrorDetail) {
	post, err := getPost(postID)
	if err != nil {
		return nil, nil, internalErr(err)
	}
	if post == nil {
		return nil, nil, newErrDetail(proto.ErrNotFound, "post not found: "+postID, false)
	}
	thread, err := getThread(post.Thread)
	if err != nil {
		return nil, nil, internalErr(err)
	}
	if thread == nil || thread.Board != boardID {
		return nil, nil, newErrDetail(proto.ErrNotFound, "post not found in board: "+postID, false)
	}
	return post, thread, nil
}

func LoadRangePostFromDB(db *sql.DB, postID, boardID string) (*projections.Post, *projections.Thread, *proto.ErrorDetail) {
	return LoadRangePost(postID, boardID, func(id string) (*projections.Post, error) {
		return projections.GetPost(db, id)
	}, func(id string) (*projections.Thread, error) {
		return projections.GetThread(db, id)
	})
}

func BoardJunkPostIDs(queryable Queryable, boardID string, requested []string) ([]string, *proto.ErrorDetail) {
	ids, msg, err := projections.BoardJunkPostIDs(queryable, boardID, requested)
	if msg != "" {
		return nil, newErrDetail(proto.ErrValidationFailed, msg, false)
	}
	if err != nil {
		return nil, internalErr(err)
	}
	return ids, nil
}

func JunkPostThreadID(queryable Queryable, postID, boardID string) (string, *proto.ErrorDetail) {
	threadID, ok, err := projections.BoardJunkPostThreadID(queryable, postID, boardID)
	if err != nil {
		return "", internalErr(err)
	}
	if !ok {
		return "", newErrDetail(proto.ErrNotFound, "junk post not found: "+postID, false)
	}
	return threadID, nil
}
