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

func ActiveBoardSanctionError(kind string) *proto.ErrorDetail {
	code := proto.ErrMuted
	if kind == "ban" {
		code = proto.ErrBanned
	}
	return newErrDetail(code, "you are "+kind+"d in this board", false)
}
