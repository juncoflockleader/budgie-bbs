package accountmodel

type SanctionTargetFailure string

const (
	SanctionTargetOK             SanctionTargetFailure = ""
	SanctionTargetAdmin          SanctionTargetFailure = "admin"
	SanctionTargetModerator      SanctionTargetFailure = "moderator"
	SanctionTargetClearModerator SanctionTargetFailure = "clear_moderator"
)

func AdminRoleAllowed(isAdmin bool) bool {
	return isAdmin
}

func ModeratorRoleAllowed(isModerator bool) bool {
	return isModerator
}

func SanctionTargetFailureFor(actorIsAdmin, targetIsAdmin, targetIsModerator bool) SanctionTargetFailure {
	if targetIsAdmin {
		return SanctionTargetAdmin
	}
	if targetIsModerator && !actorIsAdmin {
		return SanctionTargetModerator
	}
	return SanctionTargetOK
}

func ClearSanctionTargetFailureFor(actorIsAdmin, targetIsModerator bool) SanctionTargetFailure {
	if targetIsModerator && !actorIsAdmin {
		return SanctionTargetClearModerator
	}
	return SanctionTargetOK
}
