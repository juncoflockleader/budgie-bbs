package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// handleEvents serves GET /api/v1/events
// Supports poll (wait=0 or omitted) and long-poll (wait=N seconds, max 60).
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	cursor := parseCursorParam(q.Get("cursor"))
	replayAfter := cursor.AfterSeq(after)
	limitN, _ := strconv.Atoi(q.Get("limit"))
	if limitN <= 0 || limitN > 200 {
		limitN = 50
	}
	waitSec, _ := strconv.Atoi(q.Get("wait"))
	scopes := s.authorizedScopes(userFromCtx(r.Context()), q["scope"])

	head, err := s.core.Head()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	headCursor, err := s.core.HeadCursor()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}

	var events []*proto.Event
	if cursor.Seq > 0 || len(cursor.Partitions) > 0 {
		events, err = s.core.ReplayCursor(cursor, scopes, limitN)
	} else {
		events, err = s.core.Replay(replayAfter, scopes, limitN)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}

	if len(events) > 0 || waitSec <= 0 {
		deliveredCursor := deliveredCursorFromEvents(cursor, after, scopes, events)
		writeEventsResponse(w, head, headCursor, deliveredCursor, events)
		return
	}

	// Long-poll: wait up to waitSec seconds for new events.
	timeout := time.Duration(waitSec) * time.Second
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// scopes is already authorized and never nil; an empty list (a request for
	// only forbidden scopes) correctly subscribes to nothing.
	subScopes := scopes
	sub := s.core.Subscribe(subScopes)
	defer s.core.Unsubscribe(sub)

	select {
	case evt, ok := <-sub.Ch:
		if !ok {
			deliveredCursor := deliveredCursorFromEvents(cursor, after, scopes, nil)
			writeEventsResponse(w, head, headCursor, deliveredCursor, nil)
			return
		}
		batch := []*proto.Event{evt}
		for len(batch) < limitN {
			select {
			case e, ok := <-sub.Ch:
				if !ok {
					goto done
				}
				batch = append(batch, e)
			default:
				goto done
			}
		}
	done:
		newHead, _ := s.core.Head()
		newHeadCursor, err := s.core.HeadCursor()
		if err != nil {
			newHeadCursor = proto.CursorFromHead(newHead)
		}
		deliveredCursor := deliveredCursorFromEvents(cursor, after, subScopes, batch)
		writeEventsResponse(w, newHead, newHeadCursor, deliveredCursor, batch)
	case <-ctx.Done():
		deliveredCursor := deliveredCursorFromEvents(cursor, after, scopes, nil)
		writeEventsResponse(w, head, headCursor, deliveredCursor, nil)
	}
}

// handleEventsStream serves GET /api/v1/events/stream (SSE).
func (s *Server) handleEventsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	q := r.URL.Query()
	scopes := s.authorizedScopes(userFromCtx(r.Context()), q["scope"])

	// Honour Last-Event-ID header as the cursor (SSE auto-reconnect).
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if a := q.Get("after"); a != "" {
		after, _ = strconv.ParseInt(a, 10, 64)
	}
	cursor := parseCursorParam(q.Get("cursor"))
	after = cursor.AfterSeq(after)
	if cursor.Empty() && after > 0 {
		cursor = proto.CursorFromHead(after)
	}
	if after > 0 || !cursor.Empty() {
		metrics.GatewayReconnects.Inc()
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Replay backlog first.
	events, _ := s.core.ReplayCursor(cursor, scopes, 200)
	metrics.ReplayTotal.Inc()
	metrics.ReplayBatchSize.Observe(float64(len(events)))
	for _, evt := range events {
		if err := writeSSEEvent(w, evt); err != nil {
			return
		}
		cursor.ObserveEvent(evt)
	}
	flusher.Flush()

	// scopes is already authorized and never nil; an empty list subscribes to
	// nothing (a request for only forbidden scopes).
	subScopes := scopes
	sub := s.core.Subscribe(subScopes)
	defer s.core.Unsubscribe(sub)

	for {
		select {
		case evt, ok := <-sub.Ch:
			if !ok {
				return
			}
			if err := s.deliverSSEEvent(w, evt, subScopes, &cursor); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writeEventsResponse(w http.ResponseWriter, head int64, headCursor, deliveredCursor proto.Cursor, events []*proto.Event) {
	w.Header().Set("X-Log-Head", strconv.FormatInt(head, 10))
	if raw, err := json.Marshal(headCursor); err == nil {
		w.Header().Set("X-Log-Cursor", string(raw))
	}
	if raw, err := json.Marshal(deliveredCursor); err == nil {
		w.Header().Set("X-Log-Delivered-Cursor", string(raw))
	}
	type resp struct {
		Events          []*proto.Event `json:"events"`
		Head            int64          `json:"head"`
		Cursor          proto.Cursor   `json:"cursor"`
		DeliveredCursor proto.Cursor   `json:"deliveredCursor"`
	}
	if events == nil {
		events = []*proto.Event{}
	}
	writeJSON(w, http.StatusOK, resp{Events: events, Head: head, Cursor: headCursor, DeliveredCursor: deliveredCursor})
}

func deliveredCursorFromEvents(start proto.Cursor, after int64, scopes []string, events []*proto.Event) proto.Cursor {
	cursor := start
	if cursor.Empty() && after > 0 {
		cursor = proto.CursorFromHead(after)
	}
	if len(events) == 0 {
		return cursor
	}
	if len(scopes) == 0 {
		for _, evt := range events {
			cursor.ObserveEvent(evt)
		}
		return cursor
	}

	scopedCursor := cursor.PartitionOnly()
	observedPartition := false
	for _, evt := range events {
		if observeEventPartition(&scopedCursor, evt) {
			observedPartition = true
		}
	}
	if observedPartition {
		return scopedCursor
	}

	for _, evt := range events {
		cursor.ObserveEvent(evt)
	}
	return cursor
}

func observeEventPartition(cursor *proto.Cursor, evt *proto.Event) bool {
	if cursor == nil || evt == nil || !evt.IsDurable() {
		return false
	}
	if evt.PartitionKind == "" || evt.PartitionKey == "" || evt.PartitionOffset <= 0 {
		return false
	}
	seq := cursor.Seq
	cursor.ObserveEvent(evt)
	cursor.Seq = seq
	return true
}

// deliverSSEEvent delivers via the shared gap-repair path; a failed replay
// aborts the stream so the client reconnects with its Last-Event-ID cursor.
func (s *Server) deliverSSEEvent(w http.ResponseWriter, evt *proto.Event, scopes []string, cursor *proto.Cursor) error {
	return s.core.DeliverWithGapRepair(cursor, evt, scopes,
		func(e *proto.Event) error { return writeSSEEvent(w, e) },
		func(err error) error { return err })
}

func writeSSEEvent(w http.ResponseWriter, evt *proto.Event) error {
	start := time.Now()
	msg := proto.EventToOutbound(evt)
	raw, _ := json.Marshal(msg)
	if evt.IsDurable() {
		if _, err := fmt.Fprintf(w, "id: %d\n", evt.Seq); err != nil {
			metrics.GatewaySSESendLatency.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
			return err
		}
	}
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Kind, raw)
	metrics.GatewaySSESendLatency.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
	return err
}

// authorizedScopes resolves the scopes a stream request may use. With no scope
// requested it falls back to the public defaults; otherwise it filters the
// requested scopes to those the actor is authorized to receive (private boards,
// other users' account scopes, and moderation scopes are dropped). The result
// is never nil, so replay/subscribe never degrade to "all events".
func (s *Server) authorizedScopes(actor *projections.User, requested []string) []string {
	if len(requested) == 0 {
		return defaultScopes()
	}
	return s.core.AuthorizedScopes(actor, requested)
}

func defaultScopes() []string {
	return []string{"board:general", "chat:lobby", "presence:global"}
}

func parseCursorParam(raw string) proto.Cursor {
	if raw == "" {
		return proto.Cursor{}
	}
	var cursor proto.Cursor
	if err := json.Unmarshal([]byte(raw), &cursor); err != nil {
		return proto.Cursor{}
	}
	return cursor
}
