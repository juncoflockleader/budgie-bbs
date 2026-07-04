package commandrules

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type Queryable interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func newErrDetail(code, message string, retryable bool) *proto.ErrorDetail {
	return &proto.ErrorDetail{Code: code, Message: message, Retryable: retryable}
}

func internalErr(err error) *proto.ErrorDetail {
	return newErrDetail("internal_error", err.Error(), true)
}

func ResolveOtherUser(queryable Queryable, actor *projections.User, ref, missingMessage, selfMessage string) (*projections.User, *proto.ErrorDetail) {
	if actor == nil {
		return nil, newErrDetail(proto.ErrForbidden, "authentication required", false)
	}
	target, err := projections.FindUserRef(queryable, ref)
	if err != nil {
		return nil, internalErr(err)
	}
	if target == nil {
		return nil, newErrDetail(proto.ErrNotFound, missingMessage, false)
	}
	if target.ID == actor.ID {
		return nil, newErrDetail(proto.ErrValidationFailed, selfMessage, false)
	}
	return target, nil
}

func ResolveUserRef(queryable Queryable, ref string) (*projections.User, *proto.ErrorDetail) {
	user, err := projections.FindUserRef(queryable, ref)
	if err != nil {
		return nil, internalErr(err)
	}
	if user == nil {
		return nil, newErrDetail(proto.ErrNotFound, "user not found", false)
	}
	return user, nil
}

func ValidateLoginWatchMutation(queryable Queryable, actor *projections.User, targetRef string, active bool) (*projections.User, bool, *proto.ErrorDetail) {
	target, errDetail := ResolveOtherUser(queryable, actor, targetRef, "user not found", "cannot wait for yourself")
	if errDetail != nil {
		return nil, false, errDetail
	}
	if !active {
		return target, false, nil
	}

	friend, err := projections.UserRelationshipExists(queryable, actor.ID, target.ID, "friend")
	if err != nil {
		return nil, false, internalErr(err)
	}
	if !friend {
		return nil, false, newErrDetail(proto.ErrForbidden, "friend relationship required", false)
	}
	online, err := projections.UserRecentlyOnline(queryable, target.ID)
	if err != nil {
		return nil, false, internalErr(err)
	}
	return target, online, nil
}

func ValidateBlessUserMutation(queryable Queryable, actor *projections.User, targetRef string) (*projections.User, *proto.ErrorDetail) {
	target, errDetail := ResolveOtherUser(queryable, actor, targetRef, "user not found", "cannot bless yourself")
	if errDetail != nil {
		return nil, errDetail
	}
	ignored, err := projections.UserRelationshipExists(queryable, target.ID, actor.ID, "ignore")
	if err != nil {
		return nil, internalErr(err)
	}
	if ignored {
		return nil, newErrDetail(proto.ErrForbidden, "target user ignores you", false)
	}
	if already, err := projections.BlessingExists(queryable, actor.ID, target.ID); err != nil {
		return nil, internalErr(err)
	} else if already {
		return nil, newErrDetail(proto.ErrConflict, "you have already blessed this user", false)
	}
	return target, nil
}

func ResolveDirectMessageRecipient(queryable Queryable, actor *projections.User, ref string) (*projections.User, *proto.ErrorDetail) {
	if actor == nil {
		return nil, newErrDetail(proto.ErrForbidden, "authentication required", false)
	}
	target, err := projections.FindUserRef(queryable, ref)
	if err != nil {
		return nil, internalErr(err)
	}
	if target == nil {
		return nil, newErrDetail(proto.ErrNotFound, "recipient not found", false)
	}
	if target.ID == actor.ID {
		return target, nil
	}
	ignored, err := projections.UserRelationshipExists(queryable, target.ID, actor.ID, "ignore")
	if err != nil {
		return nil, internalErr(err)
	}
	if ignored {
		return nil, newErrDetail(proto.ErrForbidden, "recipient does not accept messages from this user", false)
	}
	allowed, err := projections.DirectMessageAllowed(queryable, target.ID, actor.ID)
	if err != nil {
		return nil, internalErr(err)
	}
	if !allowed {
		return nil, newErrDetail(proto.ErrForbidden, "recipient only accepts messages from friends", false)
	}
	return target, nil
}
