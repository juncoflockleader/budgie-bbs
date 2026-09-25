package boardmodel

type AccessActor struct {
	ID   string
	Role string
}

func (a AccessActor) IsMod() bool {
	return a.Role == "moderator" || a.Role == "admin"
}

type AccessSettings struct {
	MemberReadMode bool
	GuestAccess    string
}

type AccessModerator struct {
	UserID string
}

type AccessMember struct {
	UserID           string
	CanManageMembers bool
	CanModeratePosts bool
}

type AccessInfo struct {
	Settings   AccessSettings
	Moderators []AccessModerator
	Members    []AccessMember
}

// ActorCanReadBoard reports whether actor may read the board described by info.
func ActorCanReadBoard(actor *AccessActor, info *AccessInfo) bool {
	if info == nil {
		return false
	}
	if actorIsGuest(actor) {
		// Unauthenticated web guests are governed by the board's GuestAccess
		// override: "public" always grants, "hidden" always denies, and the
		// default follows the world-readable rule (non-member boards readable).
		switch info.Settings.GuestAccess {
		case "public":
			return true
		case "hidden":
			return false
		default:
			return !info.Settings.MemberReadMode
		}
	}
	if !info.Settings.MemberReadMode {
		return true
	}
	return ActorModeratesBoard(actor, info) || actorIsBoardMember(actor, info)
}

// actorIsGuest reports whether actor is the unauthenticated web guest principal.
// A nil actor (internal/system reads such as NNTP or relay) is deliberately not
// a guest, so those paths keep their world-readable behavior.
func actorIsGuest(actor *AccessActor) bool {
	return actor != nil && actor.Role == "guest"
}

// ActorModeratesBoard reports whether actor has site or board moderation scope.
func ActorModeratesBoard(actor *AccessActor, info *AccessInfo) bool {
	if actor == nil || info == nil {
		return false
	}
	if actor.IsMod() {
		return true
	}
	for _, mod := range info.Moderators {
		if mod.UserID == actor.ID {
			return true
		}
	}
	return false
}

func actorIsBoardMember(actor *AccessActor, info *AccessInfo) bool {
	if actor == nil || info == nil {
		return false
	}
	for _, member := range info.Members {
		if member.UserID == actor.ID {
			return true
		}
	}
	return false
}

// ActorCanManageBoardMembers reports whether actor may review/manage board
// membership using a loaded BoardInfo snapshot.
func ActorCanManageBoardMembers(actor *AccessActor, info *AccessInfo) bool {
	if ActorModeratesBoard(actor, info) {
		return true
	}
	if actor == nil || info == nil {
		return false
	}
	for _, member := range info.Members {
		if member.UserID == actor.ID && member.CanManageMembers {
			return true
		}
	}
	return false
}

// ActorCanModerateBoardPosts reports whether actor may moderate posts using a
// loaded BoardInfo snapshot.
func ActorCanModerateBoardPosts(actor *AccessActor, info *AccessInfo) bool {
	if ActorModeratesBoard(actor, info) {
		return true
	}
	if actor == nil || info == nil {
		return false
	}
	for _, member := range info.Members {
		if member.UserID == actor.ID && member.CanModeratePosts {
			return true
		}
	}
	return false
}
