package proto

import "encoding/json"

// InboundMessage is the envelope for any client-to-server message.
type InboundMessage struct {
	Kind    string          `json:"kind"`              // "command" | "control"
	CID     string          `json:"cid,omitempty"`     // client correlation id
	Command CommandName     `json:"command,omitempty"` // when kind=="command"
	Payload json.RawMessage `json:"payload,omitempty"`
	Control string          `json:"control,omitempty"` // when kind=="control"
	TS      int64           `json:"ts,omitempty"`
}

// OutboundMessage is the envelope for any server-to-client message.
type OutboundMessage struct {
	Kind string `json:"kind"` // "event" | "ack" | "control"

	// event fields
	Event   EventKind `json:"event,omitempty"`
	Seq     int64     `json:"seq,omitempty"`
	ESeq    int64     `json:"eseq,omitempty"`
	Payload any       `json:"payload,omitempty"`
	TS      int64     `json:"ts,omitempty"`

	// ack fields
	CID    string       `json:"cid,omitempty"`
	OK     bool         `json:"ok,omitempty"`
	Result any          `json:"result,omitempty"`
	Error  *ErrorDetail `json:"error,omitempty"`

	// control fields
	Control string `json:"control,omitempty"`
}

// ErrorDetail describes a command rejection.
type ErrorDetail struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Retryable   bool   `json:"retryable"`
	RetryAfterMs int64 `json:"retryAfterMs,omitempty"`
}

// AckResult carries the stable ID and seq of a successfully created resource.
type AckResult struct {
	ID  string `json:"id,omitempty"`
	Seq int64  `json:"seq,omitempty"`
}

// Standard error codes (see protocol-definition.md §8).
const (
	ErrUnauthenticated   = "unauthenticated"
	ErrForbidden         = "forbidden"
	ErrRateLimited       = "rate_limited"
	ErrValidationFailed  = "validation_failed"
	ErrNotFound          = "not_found"
	ErrThreadLocked      = "thread_locked"
	ErrEditWindowExpired = "edit_window_expired"
	ErrConflict          = "conflict"
	ErrMuted             = "muted"
	ErrBanned            = "banned"
)

// WelcomePayload is sent by the server on a new WS/SSE connection.
type WelcomePayload struct {
	Protocol     string   `json:"protocol"`
	Server       string   `json:"server"`
	Head         int64    `json:"head"`
	Capabilities []string `json:"capabilities"`
	WireFormats  []string `json:"wireFormats"`
	HeartbeatSec int      `json:"heartbeatSec"`
}

// ResumePayload is sent by the client after receiving welcome.
type ResumePayload struct {
	After         int64    `json:"after"`
	Subscriptions []string `json:"subscriptions"`
}

// BackfillRequired is sent when the client's cursor is too old to replay.
type BackfillRequiredPayload struct {
	Head int64 `json:"head"`
}

// EventToOutbound converts an Event to the wire OutboundMessage.
func EventToOutbound(e *Event) OutboundMessage {
	msg := OutboundMessage{
		Kind:    "event",
		Event:   e.Kind,
		Payload: e.Payload,
		TS:      e.TS,
	}
	if e.IsDurable() {
		msg.Seq = e.Seq
	} else {
		msg.ESeq = e.ESeq
	}
	return msg
}
