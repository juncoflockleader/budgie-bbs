package core

// Board read-access control, shared by every transport (HTTP, NNTP, SSH/TUI).
//
// A board in MemberReadMode (private / members-only) is readable only by site
// moderators/admins, the board's own moderators, and its members. Boards not in
// MemberReadMode are world-readable. This logic previously lived only in the
// HTTP layer, which let the NNTP and SSH transports read private boards; it is
// promoted here so all transports enforce the same rule.

// ActorCanReadBoard reports whether actor may read the board described by info.
func ActorCanReadBoard(actor *User, info *BoardInfo) bool {
	if info == nil {
		return false
	}
	if !info.Settings.MemberReadMode {
		return true
	}
	return actorModeratesBoard(actor, info) || actorIsBoardMember(actor, info)
}

// ActorCanReadBoardID loads the board and applies ActorCanReadBoard. A missing
// board (or load error) is treated as not-readable.
func (c *Core) ActorCanReadBoardID(actor *User, boardID string) (bool, error) {
	info, err := c.GetBoardInfo(boardID)
	if err != nil || info == nil {
		return false, err
	}
	return ActorCanReadBoard(actor, info), nil
}

func actorModeratesBoard(actor *User, info *BoardInfo) bool {
	if actor == nil {
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

func actorIsBoardMember(actor *User, info *BoardInfo) bool {
	if actor == nil {
		return false
	}
	for _, member := range info.Members {
		if member.UserID == actor.ID {
			return true
		}
	}
	return false
}
