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
		EvtPostRestored, EvtThreadLocked, EvtThreadMoved,
		EvtUserSanctioned, EvtRoleGranted, EvtRoleRevoked:
		return true
	}
	return false
}

// Durable event payloads.

type ThreadNewPayload struct {
	ID     string `json:"id"`
	Board  string `json:"board"`
	Author string `json:"author"`
	Title  string `json:"title"`
	TS     int64  `json:"ts"`
}

type PostAppendedPayload struct {
	ID          string `json:"id"`
	Thread      string `json:"thread"`
	Author      string `json:"author"`
	Body        string `json:"body"`
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
