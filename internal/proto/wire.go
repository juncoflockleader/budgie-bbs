package proto

import (
	"encoding/json"
	"sort"
)

// InboundMessage is the envelope for any client-to-server message.
type InboundMessage struct {
	Kind    string          `json:"kind"`              // "command" | "control"
	CID     string          `json:"cid,omitempty"`     // client correlation id
	Command CommandName     `json:"command,omitempty"` // when kind=="command"
	Payload json.RawMessage `json:"payload,omitempty"`
	Control string          `json:"control,omitempty"` // when kind=="control"
	TS      int64           `json:"ts,omitempty"`
}

// Cursor is the durable resume position. Seq preserves the v1 scalar cursor;
// Partitions lets newer clients remember per-partition offsets before the
// server fully switches to partition-native replay.
type Cursor struct {
	Seq        int64             `json:"seq,omitempty"`
	Partitions []PartitionCursor `json:"partitions,omitempty"`
}

type PartitionCursor struct {
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Offset int64  `json:"offset"`
}

func (c Cursor) AfterSeq(fallback int64) int64 {
	if c.Seq > 0 {
		return c.Seq
	}
	return fallback
}

func CursorFromHead(head int64) Cursor {
	if head <= 0 {
		return Cursor{}
	}
	return Cursor{Seq: head}
}

func CursorFromEvent(e *Event) Cursor {
	if e == nil || !e.IsDurable() {
		return Cursor{}
	}
	c := Cursor{Seq: e.Seq}
	if e.PartitionKind != "" && e.PartitionKey != "" && e.PartitionOffset > 0 {
		c.Partitions = []PartitionCursor{{
			Kind:   e.PartitionKind,
			Key:    e.PartitionKey,
			Offset: e.PartitionOffset,
		}}
	}
	return c
}

func (c Cursor) Empty() bool {
	return c.Seq == 0 && len(c.Partitions) == 0
}

// PartitionOnly returns a cursor that keeps per-partition offsets but drops the
// scalar compatibility seq. It is used when a partition gap must be repaired
// even though the compatibility seq may have advanced on another partition.
func (c Cursor) PartitionOnly() Cursor {
	c.Seq = 0
	return c
}

// PartitionOffset returns the highest known offset for a partition.
func (c Cursor) PartitionOffset(kind, key string) (int64, bool) {
	var max int64
	for _, part := range c.Partitions {
		if part.Kind != kind || part.Key != key {
			continue
		}
		if part.Offset > max {
			max = part.Offset
		}
	}
	return max, max > 0
}

// ObserveEvent advances the cursor to include a delivered durable event.
func (c *Cursor) ObserveEvent(e *Event) {
	if c == nil || e == nil || !e.IsDurable() {
		return
	}
	if e.Seq > c.Seq {
		c.Seq = e.Seq
	}
	if !eventHasPartitionOffset(e) {
		return
	}
	c.observePartition(e.PartitionKind, e.PartitionKey, e.PartitionOffset)
}

func (c *Cursor) observePartition(kind, key string, offset int64) {
	if kind == "" || key == "" || offset <= 0 {
		return
	}
	for i := range c.Partitions {
		part := &c.Partitions[i]
		if part.Kind != kind || part.Key != key {
			continue
		}
		if offset > part.Offset {
			part.Offset = offset
		}
		return
	}
	c.Partitions = append(c.Partitions, PartitionCursor{Kind: kind, Key: key, Offset: offset})
	sort.Slice(c.Partitions, func(i, j int) bool {
		if c.Partitions[i].Kind == c.Partitions[j].Kind {
			return c.Partitions[i].Key < c.Partitions[j].Key
		}
		return c.Partitions[i].Kind < c.Partitions[j].Kind
	})
}

// SeenEvent reports whether a durable event is at or behind this cursor.
func (c Cursor) SeenEvent(e *Event) bool {
	if e == nil || !e.IsDurable() {
		return false
	}
	if eventHasPartitionOffset(e) {
		if offset, ok := c.PartitionOffset(e.PartitionKind, e.PartitionKey); ok {
			return e.PartitionOffset <= offset
		}
	}
	return c.Seq > 0 && e.Seq > 0 && e.Seq <= c.Seq
}

// PartitionGapBeforeEvent reports whether the event's partition offset has
// advanced beyond this cursor. This catches gaps even when the compatibility
// seq has already advanced on another partition.
func (c Cursor) PartitionGapBeforeEvent(e *Event) bool {
	if e == nil || !e.IsDurable() || !eventHasPartitionOffset(e) {
		return false
	}
	if offset, ok := c.PartitionOffset(e.PartitionKind, e.PartitionKey); ok {
		return e.PartitionOffset > offset+1
	}
	return len(c.Partitions) > 0 && e.PartitionOffset > 1
}

// ScalarGapBeforeEvent preserves v1 gap detection for scalar-only cursors.
func (c Cursor) ScalarGapBeforeEvent(e *Event) bool {
	if e == nil || !e.IsDurable() || c.Seq <= 0 || e.Seq <= 0 {
		return false
	}
	return e.Seq > c.Seq+1
}

// DurableEventAtOrAfter reports whether candidate is the current durable event
// or a later one. Partition offsets are preferred when both events are in the
// same partition; scalar seq remains the cross-partition compatibility order.
func DurableEventAtOrAfter(candidate, current *Event) bool {
	if candidate == nil || current == nil || !candidate.IsDurable() || !current.IsDurable() {
		return false
	}
	if eventHasPartitionOffset(candidate) && eventHasPartitionOffset(current) &&
		candidate.PartitionKind == current.PartitionKind &&
		candidate.PartitionKey == current.PartitionKey {
		return candidate.PartitionOffset >= current.PartitionOffset
	}
	if candidate.Seq > 0 && current.Seq > 0 {
		return candidate.Seq >= current.Seq
	}
	return false
}

func eventHasPartitionOffset(e *Event) bool {
	return e.PartitionKind != "" && e.PartitionKey != "" && e.PartitionOffset > 0
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

	PartitionKind   string  `json:"partitionKind,omitempty"`
	PartitionKey    string  `json:"partitionKey,omitempty"`
	PartitionOffset int64   `json:"partitionOffset,omitempty"`
	Cursor          *Cursor `json:"cursor,omitempty"`

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
	Code         string `json:"code"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable"`
	RetryAfterMs int64  `json:"retryAfterMs,omitempty"`
}

// AckResult carries the stable ID and seq of a successfully created resource.
// In authoritative command-log mode it can instead acknowledge that a command
// was durably accepted and is pending writer execution.
type AckResult struct {
	ID                   string `json:"id,omitempty"`
	Seq                  int64  `json:"seq,omitempty"`
	Status               string `json:"status,omitempty"`
	CommandID            string `json:"commandId,omitempty"`
	CommandPartitionKind string `json:"commandPartitionKind,omitempty"`
	CommandPartitionKey  string `json:"commandPartitionKey,omitempty"`
	CommandOffset        int64  `json:"commandOffset,omitempty"`
}

const AckStatusPending = "pending"

// Standard error codes (see protocol-definition.md §8).
const (
	ErrUnauthenticated        = "unauthenticated"
	ErrForbidden              = "forbidden"
	ErrRateLimited            = "rate_limited"
	ErrValidationFailed       = "validation_failed"
	ErrNotFound               = "not_found"
	ErrThreadLocked           = "thread_locked"
	ErrEditWindowExpired      = "edit_window_expired"
	ErrConflict               = "conflict"
	ErrMuted                  = "muted"
	ErrBanned                 = "banned"
	ErrCommandLogUnavailable  = "command_log_unavailable"
	ErrProjectionStale        = "projection_stale"
	ErrWriteRegionUnavailable = "write_region_unavailable"
	ErrBlobStagingRequired    = "blob_staging_required"
)

// WelcomePayload is sent by the server on a new WS/SSE connection.
type WelcomePayload struct {
	Protocol     string   `json:"protocol"`
	Server       string   `json:"server"`
	Head         int64    `json:"head"`
	HeadCursor   Cursor   `json:"headCursor,omitempty"`
	Capabilities []string `json:"capabilities"`
	WireFormats  []string `json:"wireFormats"`
	HeartbeatSec int      `json:"heartbeatSec"`
}

// ResumePayload is sent by the client after receiving welcome.
type ResumePayload struct {
	After         int64    `json:"after"`
	Cursor        *Cursor  `json:"cursor,omitempty"`
	Subscriptions []string `json:"subscriptions"`
}

// BackfillRequired is sent when the client's cursor is too old to replay.
type BackfillRequiredPayload struct {
	Head       int64  `json:"head"`
	HeadCursor Cursor `json:"headCursor,omitempty"`
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
		msg.PartitionKind = e.PartitionKind
		msg.PartitionKey = e.PartitionKey
		msg.PartitionOffset = e.PartitionOffset
		cursor := CursorFromEvent(e)
		if !cursor.Empty() {
			msg.Cursor = &cursor
		}
	} else {
		msg.ESeq = e.ESeq
	}
	return msg
}
