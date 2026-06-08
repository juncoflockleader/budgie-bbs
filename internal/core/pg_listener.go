package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// pgNotifyChannel is the Postgres LISTEN/NOTIFY channel name for cross-node wakeups.
const pgNotifyChannel = "budgie_events"

// pgWakeupPayload is the JSON payload sent via pg_notify.
// For durable events: Seq > 0, EID is empty.
// For ephemeral events (e.g. chat.line): Seq = 0, EID is the record's ID.
type pgWakeupPayload struct {
	Seq    int64  `json:"seq"`
	Event  string `json:"event"`
	NodeID string `json:"node_id"`
	Scopes string `json:"scopes"` // comma-separated scope list
	EID    string `json:"eid,omitempty"` // ephemeral record ID (non-durable events)
}

// startPGListener starts a goroutine that listens for events committed by other
// Postgres nodes and re-publishes them to the local Bus. Returns immediately.
// The goroutine runs until ctx is cancelled.
//
// Notifications from this node (matching nodeID) are skipped because the
// command handler already published those events to Bus directly.
func startPGListener(ctx context.Context, dsn, nodeID string, db *sql.DB, bus Bus) {
	l := pq.NewListener(dsn,
		200*time.Millisecond,
		5*time.Second,
		func(ev pq.ListenerEventType, err error) {
			if err != nil {
				slog.Warn("pg listener: connection event", "err", err)
			}
		},
	)
	if err := l.Listen(pgNotifyChannel); err != nil {
		slog.Error("pg listener: Listen failed", "channel", pgNotifyChannel, "err", err)
		_ = l.Close()
		return
	}
	slog.Info("pg listener: listening for cross-node events", "channel", pgNotifyChannel)

	go func() {
		defer func() {
			_ = l.Close()
			slog.Info("pg listener: stopped")
		}()

		pingTicker := time.NewTicker(90 * time.Second)
		defer pingTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case n := <-l.Notify:
				if n == nil {
					// Reconnect event; listener will re-establish automatically.
					slog.Warn("pg listener: nil notification — reconnecting")
					continue
				}
				handlePGNotification(n, nodeID, db, bus)

			case <-pingTicker.C:
				// Keep the connection alive.
				go func() {
					if err := l.Ping(); err != nil {
						slog.Warn("pg listener: ping failed", "err", err)
					}
				}()
			}
		}
	}()
}

// handlePGNotification processes a single notification from the pg_notify channel.
func handlePGNotification(n *pq.Notification, nodeID string, db *sql.DB, bus Bus) {
	var p pgWakeupPayload
	if err := json.Unmarshal([]byte(n.Extra), &p); err != nil {
		slog.Warn("pg listener: malformed notification", "extra", n.Extra, "err", err)
		return
	}
	// Skip notifications from this node — already published by the command handler.
	if p.NodeID == nodeID {
		return
	}

	if p.EID != "" {
		// Ephemeral event: fetch the record by ID and publish to local bus.
		handlePGEphemeralNotification(p, db, bus)
		return
	}

	// Durable event: fetch the authoritative event from Postgres by seq.
	events, err := replayEvents(db, p.Seq-1, nil, 1)
	if err != nil {
		slog.Warn("pg listener: replay failed", "seq", p.Seq, "err", err)
		return
	}
	if len(events) == 0 || events[0].Seq != p.Seq {
		slog.Warn("pg listener: event not found at seq", "seq", p.Seq)
		return
	}
	bus.Publish(events[0])
}

// handlePGEphemeralNotification handles cross-node wakeups for ephemeral events
// (e.g. chat lines) that have no durable seq.
func handlePGEphemeralNotification(p pgWakeupPayload, db *sql.DB, bus Bus) {
	switch proto.EventKind(p.Event) {
	case proto.EvtChatLine:
		line, err := projections.GetChatLineByID(db, p.EID)
		if err != nil {
			slog.Warn("pg listener: chat line not found", "eid", p.EID, "err", err)
			return
		}
		scopes := strings.Split(p.Scopes, ",")
		bus.Publish(&proto.Event{
			Kind:    proto.EvtChatLine,
			Scopes:  scopes,
			Payload: &proto.ChatLinePayload{ID: line.ID, Room: line.Room, User: line.User, Text: line.Text, TS: line.TS},
			TS:      line.TS,
		})
	default:
		slog.Debug("pg listener: unknown ephemeral event kind", "event", p.Event, "eid", p.EID)
	}
}
