// Package wsapi implements the WebSocket transport (Tier 1 of the transport
// ladder). The connection lifecycle follows the welcome/resume handshake
// defined in protocol-definition.md §4.
package wsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const (
	heartbeatSec = 30
	writeTimeout = 10 * time.Second
	readLimit    = 64 * 1024
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Server handles WebSocket connections.
type Server struct {
	core          *core.Core
	jwtSecret     []byte
	allowCommands bool
}

// New returns a WebSocket server.
func New(c *core.Core, jwtSecret []byte) *Server {
	return &Server{core: c, jwtSecret: jwtSecret, allowCommands: true}
}

// NewGateway returns a live-transport gateway WebSocket server. Command frames
// are accepted only when the node is configured to submit them to the
// authoritative command log instead of executing local writes.
func NewGateway(c *core.Core, jwtSecret []byte, allowCommands bool) *Server {
	return &Server{core: c, jwtSecret: jwtSecret, allowCommands: allowCommands}
}

// ServeHTTP upgrades the connection and drives the conn lifecycle.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok := bearerToken(r)
	actor, err := s.validateToken(tok)
	if err != nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("ws upgrade failed", "err", err)
		return
	}
	defer ws.Close()

	metrics.WSConnections.Inc()
	defer metrics.WSConnections.Dec()

	c := &wsConn{ws: ws, core: s.core, actor: actor, allowCommands: s.allowCommands}
	c.run(r.Context())
}

// wsConn is a single WebSocket connection holding its cursor and subscription.
type wsConn struct {
	ws            *websocket.Conn
	core          *core.Core
	actor         *core.User
	sub           *core.Subscription
	scopes        []string // active subscription scopes; used for gap replay
	cursor        proto.Cursor
	allowCommands bool
	writeJSON     func(any) error
}

func (c *wsConn) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 1. Send welcome.
	head, _ := c.core.Head()
	headCursor, err := c.core.HeadCursor()
	if err != nil {
		headCursor = proto.CursorFromHead(head)
	}
	if err := c.write(proto.OutboundMessage{
		Kind:    "control",
		Control: "welcome",
		Payload: proto.WelcomePayload{
			Protocol:     "1",
			Server:       "budgied/0.1.0",
			Head:         head,
			HeadCursor:   headCursor,
			Capabilities: []string{"ws", "sse", "longpoll", "poll", "edit", "redact", "partition-cursors"},
			WireFormats:  []string{"json"},
			HeartbeatSec: heartbeatSec,
		},
	}); err != nil {
		return
	}

	// 2. Wait for resume.
	c.ws.SetReadLimit(readLimit)
	var inbound proto.InboundMessage
	if err := c.ws.ReadJSON(&inbound); err != nil {
		return
	}
	if inbound.Kind != "control" || inbound.Control != "resume" {
		slog.Warn("ws: expected resume control message")
		return
	}
	var resume proto.ResumePayload
	if err := json.Unmarshal(inbound.Payload, &resume); err != nil {
		return
	}
	resumeCursor := cursorFromResumePayload(resume, proto.Cursor{})
	after := resumeCursor.AfterSeq(resume.After)
	if after > 0 || !resumeCursor.Empty() {
		metrics.GatewayReconnects.Inc()
	}

	scopes := resume.Subscriptions
	if len(scopes) == 0 {
		scopes = defaultScopes()
	}
	c.scopes = scopes
	c.cursor = resumeCursor
	c.sub = c.core.Subscribe(scopes)
	defer func() {
		if c.sub != nil {
			c.core.Unsubscribe(c.sub)
		}
	}()

	// 3. Replay backlog since the client's cursor.
	if err := c.replayBacklog(500); err != nil {
		slog.Error("ws: replay error", "err", err)
		return
	}

	// 4. Main loop: heartbeats + live events + inbound commands.
	ticker := time.NewTicker(heartbeatSec * time.Second)
	defer ticker.Stop()

	inCh := make(chan proto.InboundMessage, 64)
	go func() {
		defer close(inCh)
		for {
			var msg proto.InboundMessage
			if err := c.ws.ReadJSON(&msg); err != nil {
				cancel()
				return
			}
			select {
			case inCh <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			if err := c.write(proto.OutboundMessage{Kind: "control", Control: "ping"}); err != nil {
				return
			}

		case evt, ok := <-c.sub.Ch:
			if !ok {
				return
			}
			if err := c.deliverEvent(evt); err != nil {
				return
			}

		case msg, ok := <-inCh:
			if !ok {
				return
			}
			c.handleInbound(ctx, msg)
		}
	}
}

func (c *wsConn) handleInbound(ctx context.Context, msg proto.InboundMessage) {
	switch msg.Kind {
	case "control":
		switch msg.Control {
		case "pong":
			// heartbeat reply
		case "resume":
			var resume proto.ResumePayload
			if json.Unmarshal(msg.Payload, &resume) != nil {
				return
			}
			scopes := resume.Subscriptions
			if len(scopes) == 0 {
				scopes = defaultScopes()
			}
			if c.sub != nil {
				c.core.Unsubscribe(c.sub)
			}
			c.scopes = scopes
			c.cursor = cursorFromResumePayload(resume, c.cursor)
			c.sub = c.core.Subscribe(scopes)
			if err := c.replayBacklog(500); err != nil {
				slog.Warn("ws: mid-session resume replay error", "err", err)
			}
		}

	case "command":
		if !c.allowCommands {
			_ = c.write(proto.OutboundMessage{
				Kind: "ack",
				CID:  msg.CID,
				OK:   false,
				Error: &proto.ErrorDetail{
					Code:      proto.ErrForbidden,
					Message:   "commands are not enabled on this gateway",
					Retryable: false,
				},
			})
			return
		}
		reply := c.core.ExecCmd(ctx, c.actor, msg.Command, msg.Payload, msg.CID)
		_ = c.write(proto.OutboundMessage{
			Kind:   "ack",
			CID:    msg.CID,
			OK:     reply.Err == nil,
			Result: reply.Result,
			Error:  reply.Err,
		})
	}
}

func cursorFromResumePayload(resume proto.ResumePayload, fallback proto.Cursor) proto.Cursor {
	if resume.Cursor != nil {
		cursor := *resume.Cursor
		if cursor.Empty() && resume.After > 0 {
			return proto.CursorFromHead(resume.After)
		}
		return cursor
	}
	if resume.After > 0 {
		return proto.CursorFromHead(resume.After)
	}
	return fallback
}

func (c *wsConn) write(v any) error {
	start := time.Now()
	var err error
	if c.writeJSON != nil {
		err = c.writeJSON(v)
	} else {
		c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
		err = c.ws.WriteJSON(v)
	}
	metrics.GatewayWSSendLatency.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
	return err
}

func (c *wsConn) replayBacklog(limit int) error {
	events, err := c.core.ReplayCursor(c.cursor, c.scopes, limit)
	if err != nil {
		return err
	}
	metrics.ReplayTotal.Inc()
	metrics.ReplayBatchSize.Observe(float64(len(events)))
	for _, evt := range events {
		if err := c.write(proto.EventToOutbound(evt)); err != nil {
			return err
		}
		c.cursor.ObserveEvent(evt)
	}
	return nil
}

// deliverEvent writes an event to the client, handling gap detection and cursor
// deduplication for durable events.
//
// For ephemeral events (no Seq) it writes directly. For durable events it:
//   - skips events the client has already seen
//   - replays any scalar or partition gap before writing the current event
//   - updates the durable cursor on each delivered event
func (c *wsConn) deliverEvent(evt *proto.Event) error {
	if !evt.IsDurable() {
		return c.write(proto.EventToOutbound(evt))
	}
	// Duplicate: already delivered (can happen when cross-node NOTIFY arrives
	// for an event the command handler already published locally).
	if c.cursor.SeenEvent(evt) {
		return nil
	}
	// Gap: one or more durable events were dropped by MemBus (slow-consumer path).
	if c.cursor.PartitionGapBeforeEvent(evt) || c.cursor.ScalarGapBeforeEvent(evt) {
		if err := c.repairGapBefore(evt); err != nil {
			return err
		}
	}
	if err := c.write(proto.EventToOutbound(evt)); err != nil {
		return err
	}
	c.cursor.ObserveEvent(evt)
	return nil
}

func (c *wsConn) repairGapBefore(evt *proto.Event) error {
	replayCursor := c.cursor
	if c.cursor.PartitionGapBeforeEvent(evt) {
		replayCursor = replayCursor.PartitionOnly()
	}
	missed, err := c.core.ReplayCursor(replayCursor, c.scopes, 1000)
	if err != nil {
		slog.Warn("ws: gap replay error",
			"seq", evt.Seq,
			"cursor_seq", c.cursor.Seq,
			"partition_kind", evt.PartitionKind,
			"partition_key", evt.PartitionKey,
			"partition_offset", evt.PartitionOffset,
			"err", err)
		return nil
	}
	metrics.GatewayReplayRepairs.Inc()
	metrics.ReplayTotal.Inc()
	metrics.ReplayBatchSize.Observe(float64(len(missed)))
	for _, m := range missed {
		if c.cursor.SeenEvent(m) || proto.DurableEventAtOrAfter(m, evt) {
			continue
		}
		if err := c.write(proto.EventToOutbound(m)); err != nil {
			return err
		}
		c.cursor.ObserveEvent(m)
	}
	return nil
}

func (s *Server) validateToken(tok string) (*core.User, error) {
	if tok == "" {
		return nil, fmt.Errorf("missing token")
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(tok, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	// Reject non-session tokens (e.g. the pre-verification 2FA challenge token,
	// typ:"2fa") so they cannot authenticate a WS connection and bypass 2FA.
	if typ, _ := claims["typ"].(string); typ != "" && typ != "session" {
		return nil, fmt.Errorf("invalid token type")
	}
	uid, _ := claims["sub"].(string)
	if uid == "" {
		return nil, fmt.Errorf("missing sub claim")
	}
	return s.core.UserByID(uid)
}

func bearerToken(r *http.Request) string {
	if q := r.URL.Query().Get("token"); q != "" {
		return q
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func defaultScopes() []string {
	return []string{"board:general", "chat:lobby", "presence:global"}
}
