package proto

// EventKind identifies which event type the server emitted.
type EventKind string

const (
	// Durable events — carry seq, persist permanently, replayable.
	EvtThreadNew           EventKind = "thread.new"
	EvtPostAppended        EventKind = "post.appended"
	EvtPostAttachmentAdded EventKind = "post.attachment_added"
	EvtPostEdited          EventKind = "post.edited"
	EvtPostFlagsSet        EventKind = "post.flags_set"
	EvtPostRedacted        EventKind = "post.redacted"
	EvtPostRestored        EventKind = "post.restored"
	EvtThreadLocked        EventKind = "thread.locked"
	EvtThreadMoved         EventKind = "thread.moved"
	EvtUserSanctioned      EventKind = "user.sanctioned"
	EvtUserSanctionCleared EventKind = "user.sanction_cleared"
	EvtContentFilterSet    EventKind = "content_filter.set"
	EvtRoleGranted         EventKind = "role.granted"
	EvtRoleRevoked         EventKind = "role.revoked"
	EvtBoardCreated        EventKind = "board.created"
	EvtPostPurged          EventKind = "post.purged" // GDPR hard-delete; body removed from projection
	EvtMailSent            EventKind = "mail.sent"
	EvtMailAttachmentAdded EventKind = "mail.attachment_added"
	EvtDirectMessageSent   EventKind = "direct_message.sent"
	EvtUserBlessed         EventKind = "user.blessed"

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
	case EvtThreadNew, EvtPostAppended, EvtPostAttachmentAdded, EvtPostEdited, EvtPostRedacted,
		EvtPostFlagsSet, EvtPostRestored, EvtPostPurged, EvtThreadLocked, EvtThreadMoved,
		EvtUserSanctioned, EvtUserSanctionCleared, EvtContentFilterSet, EvtRoleGranted, EvtRoleRevoked, EvtBoardCreated,
		EvtMailSent, EvtMailAttachmentAdded, EvtDirectMessageSent,
		EvtUserBlessed, EvtPostReacted, EvtPostUnreacted, EvtPollVoted,
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
	ID             string              `json:"id"`
	Thread         string              `json:"thread"`
	Author         string              `json:"author"`
	AuthorID       string              `json:"authorId,omitempty"`
	Body           string              `json:"body"`
	RawBody        string              `json:"rawBody,omitempty"`
	Signature      string              `json:"signature,omitempty"`
	ContentType    string              `json:"contentType"`
	ReplyTo        string              `json:"replyTo,omitempty"`
	TeX            bool                `json:"tex,omitempty"`
	MailBack       bool                `json:"mailBack,omitempty"`
	SourcePost     string              `json:"sourcePost,omitempty"`
	SourceThread   string              `json:"sourceThread,omitempty"`
	SourceBoard    string              `json:"sourceBoard,omitempty"`
	SourceAuthor   string              `json:"sourceAuthor,omitempty"`
	SourceAuthorID string              `json:"sourceAuthorId,omitempty"`
	SourceTitle    string              `json:"sourceTitle,omitempty"`
	Attachments    []AttachmentPayload `json:"attachments,omitempty"`
	TS             int64               `json:"ts"`
}

type PostAttachmentAddedPayload struct {
	ID          string `json:"id"`
	Post        string `json:"post"`
	Thread      string `json:"thread"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	AuthorID    string `json:"authorId,omitempty"`
	TS          int64  `json:"ts"`
}

type PostEditedPayload struct {
	ID      string `json:"id"`
	Thread  string `json:"thread"`
	NewBody string `json:"newBody"`
	Version int    `json:"version"`
	TS      int64  `json:"ts"`
}

type PostFlagsSetPayload struct {
	ID          string `json:"id"`
	Thread      string `json:"thread"`
	Marked      bool   `json:"marked"`
	Recommended bool   `json:"recommended"`
	NoReply     bool   `json:"noReply"`
	TeX         bool   `json:"tex"`
	MailBack    bool   `json:"mailBack"`
	By          string `json:"by"`
	TS          int64  `json:"ts"`
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

type UserSanctionClearedPayload struct {
	User   string `json:"user"`
	Kind   string `json:"kind,omitempty"`
	Scope  string `json:"scope,omitempty"`
	By     string `json:"by"`
	Reason string `json:"reason,omitempty"`
	TS     int64  `json:"ts"`
}

type ContentFilterSetPayload struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"`
	Scope   string `json:"scope,omitempty"`
	Active  bool   `json:"active"`
	By      string `json:"by"`
	TS      int64  `json:"ts"`
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
	ParentID    string `json:"parentId,omitempty"`
	Position    int    `json:"position,omitempty"`
	By          string `json:"by"`
	TS          int64  `json:"ts"`
}

type MailSentPayload struct {
	ID          string              `json:"id"`
	FromUserID  string              `json:"fromUserId"`
	From        string              `json:"from"`
	ToUserIDs   []string            `json:"toUserIds"`
	To          []string            `json:"to"`
	Subject     string              `json:"subject"`
	Body        string              `json:"body"`
	ParentID    string              `json:"parentId,omitempty"`
	SaveSent    bool                `json:"saveSent"`
	Attachments []AttachmentPayload `json:"attachments,omitempty"`
	TS          int64               `json:"ts"`
}

type MailAttachmentAddedPayload struct {
	ID          string `json:"id"`
	Mail        string `json:"mail"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	AuthorID    string `json:"authorId,omitempty"`
	Author      string `json:"author,omitempty"`
	TS          int64  `json:"ts"`
}

type DirectMessageSentPayload struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	FromUserID     string `json:"fromUserId"`
	From           string `json:"from"`
	ToUserID       string `json:"toUserId"`
	To             string `json:"to"`
	Body           string `json:"body"`
	TS             int64  `json:"ts"`
}

type UserBlessedPayload struct {
	ID         string `json:"id"`
	FromUserID string `json:"fromUserId"`
	From       string `json:"from"`
	ToUserID   string `json:"toUserId"`
	To         string `json:"to"`
	Message    string `json:"message,omitempty"`
	TS         int64  `json:"ts"`
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
	Kind     string `json:"kind,omitempty"`
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
	User      string `json:"user"`
	UserID    string `json:"userId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Status    string `json:"status"`
	Mode      string `json:"mode,omitempty"`
	Board     string `json:"board,omitempty"`
	Thread    string `json:"thread,omitempty"`
	Location  string `json:"location,omitempty"`
	FromHost  string `json:"fromHost,omitempty"`
	TS        int64  `json:"ts"`
}

type UserJoinedPayload struct {
	User string `json:"user"`
	TS   int64  `json:"ts"`
}

type UserLeftPayload struct {
	User string `json:"user"`
	TS   int64  `json:"ts"`
}
