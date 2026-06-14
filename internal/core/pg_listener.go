package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
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
	Scopes string `json:"scopes"`        // comma-separated scope list
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
	recordRemoteWakeup(events[0].TS)
	publishLocal(bus, events[0])
}

// recordRemoteWakeup observes ingestion of a sibling-node event: counts it and
// records the delay between the event's timestamp and its local receipt.
func recordRemoteWakeup(eventTS int64) {
	metrics.EventsIngestedRemote.Inc()
	if eventTS > 0 {
		if lag := time.Now().UnixMilli() - eventTS; lag >= 0 {
			metrics.RemoteWakeupLag.Observe(float64(lag))
		}
	}
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
		recordRemoteWakeup(line.TS)
		publishLocal(bus, &proto.Event{
			Kind:    proto.EvtChatLine,
			Scopes:  scopes,
			Payload: &proto.ChatLinePayload{ID: line.ID, Room: line.Room, User: line.User, Text: line.Text, TS: line.TS},
			TS:      line.TS,
		})
	case proto.EvtPostReacted, proto.EvtPostUnreacted:
		var wakeup postReactionWakeup
		if err := json.Unmarshal([]byte(p.EID), &wakeup); err != nil {
			slog.Warn("pg listener: malformed reaction wakeup", "eid", p.EID, "err", err)
			return
		}
		evt, err := postReactionWakeupEvent(db, proto.EventKind(p.Event), strings.Split(p.Scopes, ","), wakeup)
		if err != nil {
			slog.Warn("pg listener: reaction wakeup failed", "eid", p.EID, "err", err)
			return
		}
		recordRemoteWakeup(wakeup.TS)
		publishLocal(bus, evt)
	case proto.EvtPollVoted:
		var wakeup pollVoteWakeup
		if err := json.Unmarshal([]byte(p.EID), &wakeup); err != nil {
			slog.Warn("pg listener: malformed poll vote wakeup", "eid", p.EID, "err", err)
			return
		}
		evt, err := pollVoteWakeupEvent(db, strings.Split(p.Scopes, ","), wakeup)
		if err != nil {
			slog.Warn("pg listener: poll vote wakeup failed", "eid", p.EID, "err", err)
			return
		}
		recordRemoteWakeup(wakeup.TS)
		publishLocal(bus, evt)
	default:
		slog.Debug("pg listener: unknown ephemeral event kind", "event", p.Event, "eid", p.EID)
	}
}

type postReactionWakeup struct {
	Post  string `json:"post"`
	User  string `json:"user"`
	Emoji string `json:"emoji,omitempty"`
	TS    int64  `json:"ts"`
}

type pollVoteWakeup struct {
	Poll string `json:"poll"`
	User string `json:"user"`
	TS   int64  `json:"ts"`
}

func postReactionWakeupEvent(db *sql.DB, kind proto.EventKind, scopes []string, wakeup postReactionWakeup) (*proto.Event, error) {
	emoji := strings.TrimSpace(wakeup.Emoji)
	if emoji == "" {
		emoji = "heart"
	}
	var (
		thread string
		user   string
	)
	if err := qQueryRow(db,
		`SELECT p.thread, u.name
		   FROM posts p
		   JOIN users u ON u.id=?
		  WHERE p.id=?`,
		wakeup.User, wakeup.Post,
	).Scan(&thread, &user); err != nil {
		return nil, err
	}
	count, err := reactionCount(db, wakeup.Post)
	if err != nil {
		return nil, err
	}
	switch kind {
	case proto.EvtPostReacted:
		return &proto.Event{
			Kind:    proto.EvtPostReacted,
			Scopes:  scopes,
			Payload: &proto.PostReactedPayload{PostID: wakeup.Post, Thread: thread, User: user, Emoji: emoji, ReactionCount: count, TS: wakeup.TS},
			TS:      wakeup.TS,
		}, nil
	case proto.EvtPostUnreacted:
		return &proto.Event{
			Kind:    proto.EvtPostUnreacted,
			Scopes:  scopes,
			Payload: &proto.PostUnreactedPayload{PostID: wakeup.Post, Thread: thread, User: user, Emoji: emoji, ReactionCount: count, TS: wakeup.TS},
			TS:      wakeup.TS,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported reaction wakeup event %s", kind)
	}
}

func pollVoteWakeupEvent(db *sql.DB, scopes []string, wakeup pollVoteWakeup) (*proto.Event, error) {
	var (
		option string
		user   string
	)
	if err := qQueryRow(db,
		`SELECT pv.option_id, u.name
		   FROM poll_votes pv
		   JOIN users u ON u.id=pv.user_id
		  WHERE pv.poll_id=? AND pv.user_id=?`,
		wakeup.Poll, wakeup.User,
	).Scan(&option, &user); err != nil {
		return nil, err
	}
	return &proto.Event{
		Kind:    proto.EvtPollVoted,
		Scopes:  scopes,
		Payload: &proto.PollVotedPayload{Poll: wakeup.Poll, Option: option, User: user, TS: wakeup.TS},
		TS:      wakeup.TS,
	}, nil
}
