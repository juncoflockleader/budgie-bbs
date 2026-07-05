package commandrules

import (
	"database/sql"
	"strconv"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// RequireMinTrustForPoll blocks poll creation for actors below the requested
// trust level. Mod/admin actors bypass this gate.
func RequireMinTrustForPoll(db *sql.DB, actor *projections.User, minLevel int, action string, userTrustLevel func(*sql.DB, string) (int, error)) *proto.ErrorDetail {
	if actor.IsMod() {
		return nil
	}
	trustLevel, err := userTrustLevel(db, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if trustLevel < minLevel {
		return newErrDetail(proto.ErrForbidden, action+" with poll requires trust level "+strconv.Itoa(minLevel), false)
	}
	return nil
}

func RequirePollResultPublisher(canManagePolls, isPostAuthor, isThreadAuthor bool) *proto.ErrorDetail {
	if canManagePolls || isPostAuthor || isThreadAuthor {
		return nil
	}
	return newErrDetail(proto.ErrForbidden, "poll author or board poll manager required", false)
}

func RequirePollResultPublicBoard(emitPublicSystemPost bool) *proto.ErrorDetail {
	if emitPublicSystemPost {
		return nil
	}
	return newErrDetail(proto.ErrForbidden, "member-read poll results stay on the source board", false)
}
