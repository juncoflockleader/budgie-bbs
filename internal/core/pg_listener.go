package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/lib/pq"
)

// pgNotifyChannel is the Postgres LISTEN/NOTIFY channel name for cross-node wakeups.
const pgNotifyChannel = "budgie_events"

// pgWakeupPayload is the JSON payload sent via pg_notify.
type pgWakeupPayload struct {
	Seq    int64  `json:"seq"`
	Event  string `json:"event"`
	NodeID string `json:"node_id"`
	Scopes string `json:"scopes"` // comma-separated scope list
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

	// Fetch the authoritative event from Postgres by seq.
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
