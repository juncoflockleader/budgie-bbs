package commandrules

import "github.com/juncoflockleader/budgie-bbs/internal/proto"

func RequireAdminRole(isAdmin bool) *proto.ErrorDetail {
	if !isAdmin {
		return newErrDetail(proto.ErrForbidden, "admin role required", false)
	}
	return nil
}

func RequireModeratorRole(isModerator bool) *proto.ErrorDetail {
	if !isModerator {
		return newErrDetail(proto.ErrForbidden, "moderator role required", false)
	}
	return nil
}
