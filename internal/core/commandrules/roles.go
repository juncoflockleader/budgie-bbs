package commandrules

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/accountmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func RequireAdminRole(isAdmin bool) *proto.ErrorDetail {
	if !accountmodel.AdminRoleAllowed(isAdmin) {
		return newErrDetail(proto.ErrForbidden, "admin role required", false)
	}
	return nil
}

func RequireModeratorRole(isModerator bool) *proto.ErrorDetail {
	if !accountmodel.ModeratorRoleAllowed(isModerator) {
		return newErrDetail(proto.ErrForbidden, "moderator role required", false)
	}
	return nil
}

func RequireSanctionTargetAllowed(actorIsAdmin, targetIsAdmin, targetIsModerator bool) *proto.ErrorDetail {
	switch accountmodel.SanctionTargetFailureFor(actorIsAdmin, targetIsAdmin, targetIsModerator) {
	case accountmodel.SanctionTargetAdmin:
		return newErrDetail(proto.ErrForbidden, "cannot sanction an admin", false)
	case accountmodel.SanctionTargetModerator:
		return newErrDetail(proto.ErrForbidden, "only admins can sanction moderators", false)
	}
	return nil
}

func RequireClearSanctionTargetAllowed(actorIsAdmin, targetIsModerator bool) *proto.ErrorDetail {
	if accountmodel.ClearSanctionTargetFailureFor(actorIsAdmin, targetIsModerator) == accountmodel.SanctionTargetClearModerator {
		return newErrDetail(proto.ErrForbidden, "only admins can clear moderator sanctions", false)
	}
	return nil
}
