package core

import (
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

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
	return actorModeratesBoard(actor, info) || actorIsBoardMember(actor, info)
}

// ActorCanSetBoardSettings reports whether actor may manage a board's settings:
// a site moderator/admin, a board moderator, or a member granted the
// can_set_board_settings permission. Mirrors the handler-side check for use by
// direct-DB endpoints (e.g. the AI config, which is not command-routed).
func (c *Core) ActorCanSetBoardSettings(actor *User, boardID string) bool {
	ok, err := projections.ActorCanSetBoardSettings(c.DB, actor, boardID)
	return err == nil && ok
}

// actorIsGuest reports whether actor is the unauthenticated web guest principal.
// A nil actor (internal/system reads such as NNTP or relay) is deliberately not
// a guest, so those paths keep their existing world-readable behavior.
func actorIsGuest(actor *User) bool {
	return actor != nil && actor.Role == "guest"
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

// AuthorizedScopes filters a client-requested set of live-event subscription
// scopes down to those the actor may receive. Event payloads carry real content
// (post bodies, sanctions, notifications), so without this an authenticated user
// could subscribe to (or replay) another board's, user's, or moderation scope
// and read data they cannot otherwise see. Returns a non-nil slice (possibly
// empty — meaning "match nothing", never "match everything").
func (c *Core) AuthorizedScopes(actor *User, requested []string) []string {
	allowed := make([]string, 0, len(requested))
	for _, sc := range requested {
		if c.scopeVisibleTo(actor, sc) {
			allowed = append(allowed, sc)
		}
	}
	return allowed
}

func (c *Core) scopeVisibleTo(actor *User, scope string) bool {
	switch {
	case strings.HasPrefix(scope, "board:"):
		ok, _ := c.ActorCanReadBoardID(actor, strings.TrimPrefix(scope, "board:"))
		return ok
	case strings.HasPrefix(scope, "thread:"):
		t, err := c.GetThread(strings.TrimPrefix(scope, "thread:"))
		if err != nil || t == nil {
			return false
		}
		ok, _ := c.ActorCanReadBoardID(actor, t.Board)
		return ok
	case strings.HasPrefix(scope, "account:"):
		// Only the account owner may subscribe to their own account scope
		// (notifications, sanctions targeting them, etc.).
		return actor != nil && strings.TrimPrefix(scope, "account:") == actor.ID
	case strings.HasPrefix(scope, "moderation:"):
		return actor != nil && actor.IsMod()
	default:
		// chat:, presence:, and other non-sensitive broadcast scopes.
		return true
	}
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
