package commandrules

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type DigestPathMutation struct {
	BoardID  string
	Kind     string
	FromPath string
	ToPath   string
}

func PrepareDigestPathMutation(queryable Queryable, actor *projections.User, boardID, kind, fromPath, toPath string) (DigestPathMutation, *proto.ErrorDetail) {
	boardID, msg := proto.NormalizeDigestPathMutationBoard(boardID)
	if msg != "" {
		return DigestPathMutation{}, newErrDetail(proto.ErrValidationFailed, msg, false)
	}
	if errDetail := RequireBoard(queryable, boardID); errDetail != nil {
		return DigestPathMutation{}, errDetail
	}
	normalizedKind, msg := proto.NormalizeDigestPathMutationKind(kind)
	if msg != "" {
		return DigestPathMutation{}, newErrDetail(proto.ErrValidationFailed, msg, false)
	}
	if !ActorCanCurateBoardKind(queryable, actor, boardID, normalizedKind) {
		return DigestPathMutation{}, newErrDetail(proto.ErrForbidden, proto.DigestCurationPermissionMessage(normalizedKind), false)
	}
	normalizedFrom, normalizedTo, msg := proto.NormalizeDigestPathMutationPaths(fromPath, toPath)
	if msg != "" {
		return DigestPathMutation{}, newErrDetail(proto.ErrValidationFailed, msg, false)
	}
	return DigestPathMutation{BoardID: boardID, Kind: normalizedKind, FromPath: normalizedFrom, ToPath: normalizedTo}, nil
}
