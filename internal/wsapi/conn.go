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
	core      *core.Core
	jwtSecret []byte
}

// New returns a WebSocket server.
func New(c *core.Core, jwtSecret []byte) *Server {
	return &Server{core: c, jwtSecret: jwtSecret}
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

	c := &wsConn{ws: ws, core: s.core, actor: actor}
	c.run(r.Context())
}

// wsConn is a single WebSocket connection holding its cursor and subscription.
type wsConn struct {
	ws             *websocket.Conn
	core           *core.Core
	actor          *core.User
	sub            *core.Subscription
	scopes         []string // active subscription scopes; used for gap replay
	lastDurableSeq int64    // highest durable seq delivered; 0 = none yet
}

func (c *wsConn) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 1. Send welcome.
	head, _ := c.core.Head()
	if err := c.write(proto.OutboundMessage{
		Kind:    "control",
		Control: "welcome",
		Payload: proto.WelcomePayload{
			Protocol:     "1",
			Server:       "budgied/0.1.0",
			Head:         head,
			Capabilities: []string{"ws", "sse", "longpoll", "poll", "edit", "redact"},
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

	scopes := resume.Subscriptions
	if len(scopes) == 0 {
		scopes = defaultScopes()
	}
	c.scopes = scopes
	c.sub = c.core.Subscribe(scopes)
	defer c.core.Unsubscribe(c.sub)

	// 3. Replay backlog since the client's cursor.
	events, err := c.core.Replay(resume.After, scopes, 500)
	if err != nil {
		slog.Error("ws: replay error", "err", err)
		return
	}
	for _, evt := range events {
		if err := c.write(proto.EventToOutbound(evt)); err != nil {
			return
		}
		if evt.IsDurable() && evt.Seq > c.lastDurableSeq {
			c.lastDurableSeq = evt.Seq
		}
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
			// mid-session scope update
			var resume proto.ResumePayload
			if json.Unmarshal(msg.Payload, &resume) != nil {
				return
			}
			scopes := resume.Subscriptions
			if len(scopes) == 0 {
				scopes = defaultScopes()
			}
			c.core.Unsubscribe(c.sub)
			c.scopes = scopes
			c.sub = c.core.Subscribe(scopes)
		}

	case "command":
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

func (c *wsConn) write(v any) error {
	c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.ws.WriteJSON(v)
}

// deliverEvent writes an event to the client, handling gap detection and
// deduplication for durable events.
//
// For ephemeral events (no Seq) it writes directly. For durable events it:
//   - skips events the client has already seen (Seq <= lastDurableSeq)
//   - replays any gap from lastDurableSeq before writing the current event
//   - updates lastDurableSeq on each delivered durable event
func (c *wsConn) deliverEvent(evt *proto.Event) error {
	if !evt.IsDurable() {
		return c.write(proto.EventToOutbound(evt))
	}
	// Duplicate: already delivered (can happen when cross-node NOTIFY arrives
	// for an event the command handler already published locally).
	if evt.Seq <= c.lastDurableSeq {
		return nil
	}
	// Gap: one or more durable events were dropped by MemBus (slow-consumer path).
	if c.lastDurableSeq > 0 && evt.Seq > c.lastDurableSeq+1 {
		missed, err := c.core.Replay(c.lastDurableSeq, c.scopes, 1000)
		if err != nil {
			slog.Warn("ws: gap replay error", "from", c.lastDurableSeq, "to", evt.Seq-1, "err", err)
		}
		for _, m := range missed {
			if m.Seq >= evt.Seq {
				break // stop before re-delivering the event we're about to deliver
			}
			if m.Seq <= c.lastDurableSeq {
				continue // already delivered (shouldn't happen, but be safe)
			}
			if err := c.write(proto.EventToOutbound(m)); err != nil {
				return err
			}
			c.lastDurableSeq = m.Seq
		}
	}
	if err := c.write(proto.EventToOutbound(evt)); err != nil {
		return err
	}
	c.lastDurableSeq = evt.Seq
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
