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

func RequireSanctionTargetAllowed(actorIsAdmin, targetIsAdmin, targetIsModerator bool) *proto.ErrorDetail {
	if targetIsAdmin {
		return newErrDetail(proto.ErrForbidden, "cannot sanction an admin", false)
	}
	if targetIsModerator && !actorIsAdmin {
		return newErrDetail(proto.ErrForbidden, "only admins can sanction moderators", false)
	}
	return nil
}

func RequireClearSanctionTargetAllowed(actorIsAdmin, targetIsModerator bool) *proto.ErrorDetail {
	if targetIsModerator && !actorIsAdmin {
		return newErrDetail(proto.ErrForbidden, "only admins can clear moderator sanctions", false)
	}
	return nil
}
