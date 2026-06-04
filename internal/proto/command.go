package proto

// CommandName identifies which command a client is sending.
type CommandName string

const (
	CmdCreateThread CommandName = "createThread"
	CmdAppendPost   CommandName = "appendPost"
	CmdEditPost     CommandName = "editPost"
	CmdRedactPost   CommandName = "redactPost"
	CmdRestorePost  CommandName = "restorePost"
	CmdLockThread   CommandName = "lockThread"
	CmdMoveThread   CommandName = "moveThread"
	CmdSanctionUser CommandName = "sanctionUser"
	CmdGrantRole    CommandName = "grantRole"
	CmdRevokeRole   CommandName = "revokeRole"
	CmdSendChatLine CommandName = "sendChatLine"
	CmdSetPresence  CommandName = "setPresence"
	CmdCreateBoard  CommandName = "createBoard"
	CmdSubscribe    CommandName = "subscribe"
	CmdUnsubscribe  CommandName = "unsubscribe"
	CmdPurgePost    CommandName = "purgePost" // admin-only physical delete of post body (GDPR)

	// M10 — Reactions
	CmdReactPost   CommandName = "reactPost"
	CmdUnreactPost CommandName = "unreactPost"

	// M11 — Polls
	CmdVotePoll CommandName = "votePoll"

	// M8 — Notifications
	CmdSetThreadPref CommandName = "setThreadPref"

	// Modern forum moderation
	CmdFlagPost      CommandName = "flagPost"
	CmdResolveReview CommandName = "resolveReview"
)

type CreateBoardPayload struct {
	ID          string `json:"id"` // URL-safe slug
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type CreateThreadPayload struct {
	Board       string `json:"board"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	ContentType string `json:"contentType,omitempty"`
}

type AppendPostPayload struct {
	Thread      string `json:"thread"`
	Body        string `json:"body"`
	ReplyTo     string `json:"replyTo,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

type EditPostPayload struct {
	Post string `json:"post"`
	Body string `json:"body"`
}

type RedactPostPayload struct {
	Post   string `json:"post"`
	Reason string `json:"reason,omitempty"`
}

type RestorePostPayload struct {
	Post string `json:"post"`
}

type LockThreadPayload struct {
	Thread string `json:"thread"`
	Locked bool   `json:"locked"`
}

type MoveThreadPayload struct {
	Thread  string `json:"thread"`
	ToBoard string `json:"toBoard"`
}

type SanctionUserPayload struct {
	User        string `json:"user"`
	Kind        string `json:"kind"`            // "mute" | "ban"
	Scope       string `json:"scope,omitempty"` // board id or "global"
	DurationSec int64  `json:"durationSec,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type GrantRolePayload struct {
	User string `json:"user"`
	Role string `json:"role"`
}

type RevokeRolePayload struct {
	User string `json:"user"`
	Role string `json:"role"`
}

type SendChatLinePayload struct {
	Room string `json:"room"`
	Text string `json:"text"`
}

type SetPresencePayload struct {
	Status string `json:"status"`
}

type PurgePostPayload struct {
	Post   string `json:"post"`
	Reason string `json:"reason,omitempty"`
}

// M10 — Reactions
type ReactPostPayload struct {
	Post  string `json:"post"`
	Emoji string `json:"emoji,omitempty"` // defaults to "heart"
}

// M11 — Polls
type VotePollPayload struct {
	Poll   string `json:"poll"`
	Option string `json:"option"`
}

// M8 — Thread watch preference
type SetThreadPrefPayload struct {
	Thread string `json:"thread"`
	Level  string `json:"level"` // "watch" | "normal" | "mute"
}

type FlagPostPayload struct {
	Post   string `json:"post"`
	Reason string `json:"reason,omitempty"`
}

type ResolveReviewPayload struct {
	Review     string `json:"review"`
	Resolution string `json:"resolution"`
}

type SubscribePayload struct {
	Scopes []string `json:"scopes"`
}

type UnsubscribePayload struct {
	Scopes []string `json:"scopes"`
}
