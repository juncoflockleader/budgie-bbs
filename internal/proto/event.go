package proto

// EventKind identifies which event type the server emitted.
type EventKind string

const (
	// Durable events — carry seq, persist permanently, replayable.
	EvtThreadNew      EventKind = "thread.new"
	EvtPostAppended   EventKind = "post.appended"
	EvtPostEdited     EventKind = "post.edited"
	EvtPostRedacted   EventKind = "post.redacted"
	EvtPostRestored   EventKind = "post.restored"
	EvtThreadLocked   EventKind = "thread.locked"
	EvtThreadMoved    EventKind = "thread.moved"
	EvtUserSanctioned EventKind = "user.sanctioned"
	EvtRoleGranted    EventKind = "role.granted"
	EvtRoleRevoked    EventKind = "role.revoked"
	EvtBoardCreated   EventKind = "board.created"
	EvtPostPurged     EventKind = "post.purged" // GDPR hard-delete; body removed from projection

	// M10 — Reactions
	EvtPostReacted   EventKind = "post.reacted"
	EvtPostUnreacted EventKind = "post.unreacted"

	// M11 — Polls
	EvtPollVoted EventKind = "poll.voted"

	// M8 — Notifications
	EvtMentioned EventKind = "user.mentioned" // durable: @username in post body

	// M9 — Trust levels
	EvtTrustLevelChanged EventKind = "user.trust_level_changed"

	// Modern forum moderation review queue
	EvtPostFlagged    EventKind = "post.flagged"
	EvtReviewResolved EventKind = "review.resolved"

	// Ephemeral events — carry eseq, best-effort, prunable.
	EvtChatLine       EventKind = "chat.line"
	EvtPresenceUpdate EventKind = "presence.update"
	EvtUserJoined     EventKind = "user.joined"
	EvtUserLeft       EventKind = "user.left"
)

// Event is a fact emitted by the server.
type Event struct {
	Kind    EventKind `json:"event"`
	Seq     int64     `json:"seq,omitempty"`  // durable events
	ESeq    int64     `json:"eseq,omitempty"` // ephemeral events
	Payload any       `json:"payload"`
	TS      int64     `json:"ts"`

	// Scopes tags this event for pub/sub routing; not sent over the wire.
	Scopes []string `json:"-"`
}

// IsDurable reports whether this is a permanent log event.
func (e *Event) IsDurable() bool {
	switch e.Kind {
	case EvtThreadNew, EvtPostAppended, EvtPostEdited, EvtPostRedacted,
		EvtPostRestored, EvtPostPurged, EvtThreadLocked, EvtThreadMoved,
		EvtUserSanctioned, EvtRoleGranted, EvtRoleRevoked, EvtBoardCreated,
		EvtPostReacted, EvtPostUnreacted, EvtPollVoted,
		EvtMentioned, EvtTrustLevelChanged, EvtPostFlagged, EvtReviewResolved:
		return true
	}
	return false
}

// Durable event payloads.

type ThreadNewPayload struct {
	ID       string `json:"id"`
	Board    string `json:"board"`
	Author   string `json:"author"`
	AuthorID string `json:"authorId,omitempty"`
	Title    string `json:"title"`
	TS       int64  `json:"ts"`
}

type PostAppendedPayload struct {
	ID          string `json:"id"`
	Thread      string `json:"thread"`
	Author      string `json:"author"`
	AuthorID    string `json:"authorId,omitempty"`
	Body        string `json:"body"`
	RawBody     string `json:"rawBody,omitempty"`
	ContentType string `json:"contentType"`
	ReplyTo     string `json:"replyTo,omitempty"`
	TS          int64  `json:"ts"`
}

type PostEditedPayload struct {
	ID      string `json:"id"`
	Thread  string `json:"thread"`
	NewBody string `json:"newBody"`
	Version int    `json:"version"`
	TS      int64  `json:"ts"`
}

type PostRedactedPayload struct {
	ID     string `json:"id"`
	Thread string `json:"thread"`
	By     string `json:"by"`
	Reason string `json:"reason,omitempty"`
	TS     int64  `json:"ts"`
}

type PostRestoredPayload struct {
	ID     string `json:"id"`
	Thread string `json:"thread"`
	By     string `json:"by"`
	TS     int64  `json:"ts"`
}

type PostPurgedPayload struct {
	ID     string `json:"id"`
	Thread string `json:"thread"`
	By     string `json:"by"`
	Reason string `json:"reason,omitempty"`
	TS     int64  `json:"ts"`
}

type ThreadLockedPayload struct {
	Thread string `json:"thread"`
	Locked bool   `json:"locked"`
	By     string `json:"by"`
	TS     int64  `json:"ts"`
}

type ThreadMovedPayload struct {
	Thread    string `json:"thread"`
	FromBoard string `json:"fromBoard"`
	ToBoard   string `json:"toBoard"`
	By        string `json:"by"`
	TS        int64  `json:"ts"`
}

type UserSanctionedPayload struct {
	User        string `json:"user"`
	Kind        string `json:"kind"`
	Scope       string `json:"scope,omitempty"`
	DurationSec int64  `json:"durationSec,omitempty"`
	By          string `json:"by"`
	Reason      string `json:"reason,omitempty"`
	TS          int64  `json:"ts"`
}

type RoleGrantedPayload struct {
	User string `json:"user"`
	Role string `json:"role"`
	By   string `json:"by"`
	TS   int64  `json:"ts"`
}

type RoleRevokedPayload struct {
	User string `json:"user"`
	Role string `json:"role"`
	By   string `json:"by"`
	TS   int64  `json:"ts"`
}

type BoardCreatedPayload struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	By          string `json:"by"`
	TS          int64  `json:"ts"`
}

// M10 — Reaction payloads.

type PostReactedPayload struct {
	PostID        string `json:"postId"`
	Thread        string `json:"thread"`
	User          string `json:"user"`
	Emoji         string `json:"emoji"`
	ReactionCount int    `json:"reactionCount"`
	TS            int64  `json:"ts"`
}

type PostUnreactedPayload struct {
	PostID        string `json:"postId"`
	Thread        string `json:"thread"`
	User          string `json:"user"`
	Emoji         string `json:"emoji"`
	ReactionCount int    `json:"reactionCount"`
	TS            int64  `json:"ts"`
}

// M11 — Poll payload.

type PollVotedPayload struct {
	Poll   string `json:"poll"`
	Option string `json:"option"`
	User   string `json:"user"`
	TS     int64  `json:"ts"`
}

// M8 — Notification payload (durable mention event).

type MentionedPayload struct {
	User     string `json:"user"` // mentioned user ID
	By       string `json:"by"`   // author username
	PostID   string `json:"postId"`
	ThreadID string `json:"threadId"`
	TS       int64  `json:"ts"`
}

// M9 — Trust level payload.

type TrustLevelChangedPayload struct {
	User     string `json:"user"`
	OldLevel int    `json:"oldLevel"`
	NewLevel int    `json:"newLevel"`
	TS       int64  `json:"ts"`
}

type PostFlaggedPayload struct {
	ReviewID string `json:"reviewId"`
	PostID   string `json:"postId"`
	Thread   string `json:"thread"`
	Reporter string `json:"reporter"`
	Reason   string `json:"reason,omitempty"`
	TS       int64  `json:"ts"`
}

type ReviewResolvedPayload struct {
	ReviewID   string `json:"reviewId"`
	Resolution string `json:"resolution"`
	By         string `json:"by"`
	TS         int64  `json:"ts"`
}

// Ephemeral event payloads.

type ChatLinePayload struct {
	ID   string `json:"id"`
	Room string `json:"room"`
	User string `json:"user"`
	Text string `json:"text"`
	TS   int64  `json:"ts"`
}

type PresenceUpdatePayload struct {
	User   string `json:"user"`
	Status string `json:"status"`
	TS     int64  `json:"ts"`
}

type UserJoinedPayload struct {
	User string `json:"user"`
	TS   int64  `json:"ts"`
}

type UserLeftPayload struct {
	User string `json:"user"`
	TS   int64  `json:"ts"`
}
