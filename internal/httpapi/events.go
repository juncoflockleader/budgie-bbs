package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// handleEvents serves GET /api/v1/events
// Supports poll (wait=0 or omitted) and long-poll (wait=N seconds, max 60).
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	limitN, _ := strconv.Atoi(q.Get("limit"))
	if limitN <= 0 || limitN > 200 {
		limitN = 50
	}
	waitSec, _ := strconv.Atoi(q.Get("wait"))
	scopes := q["scope"]

	head, err := s.core.Head()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}

	events, err := s.core.Replay(after, scopes, limitN)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}

	if len(events) > 0 || waitSec <= 0 {
		writeEventsResponse(w, head, events)
		return
	}

	// Long-poll: wait up to waitSec seconds for new events.
	timeout := time.Duration(waitSec) * time.Second
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	subScopes := scopes
	if len(subScopes) == 0 {
		subScopes = defaultScopes()
	}
	sub := s.core.Subscribe(subScopes)
	defer s.core.Unsubscribe(sub)

	select {
	case evt, ok := <-sub.Ch:
		if !ok {
			writeEventsResponse(w, head, nil)
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
		writeEventsResponse(w, newHead, batch)
	case <-ctx.Done():
		writeEventsResponse(w, head, nil)
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
	scopes := q["scope"]

	// Honour Last-Event-ID header as the cursor (SSE auto-reconnect).
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if a := q.Get("after"); a != "" {
		after, _ = strconv.ParseInt(a, 10, 64)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Replay backlog first.
	events, _ := s.core.Replay(after, scopes, 200)
	for _, evt := range events {
		writeSSEEvent(w, evt)
		if evt.IsDurable() {
			after = evt.Seq
		}
	}
	flusher.Flush()

	subScopes := scopes
	if len(subScopes) == 0 {
		subScopes = defaultScopes()
	}
	sub := s.core.Subscribe(subScopes)
	defer s.core.Unsubscribe(sub)

	for {
		select {
		case evt, ok := <-sub.Ch:
			if !ok {
				return
			}
			writeSSEEvent(w, evt)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writeEventsResponse(w http.ResponseWriter, head int64, events []*proto.Event) {
	w.Header().Set("X-Log-Head", strconv.FormatInt(head, 10))
	type resp struct {
		Events []*proto.Event `json:"events"`
		Head   int64          `json:"head"`
	}
	if events == nil {
		events = []*proto.Event{}
	}
	writeJSON(w, http.StatusOK, resp{Events: events, Head: head})
}

func writeSSEEvent(w http.ResponseWriter, evt *proto.Event) {
	msg := proto.EventToOutbound(evt)
	raw, _ := json.Marshal(msg)
	if evt.IsDurable() {
		fmt.Fprintf(w, "id: %d\n", evt.Seq)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Kind, raw)
}

func defaultScopes() []string {
	return []string{"board:general", "chat:lobby", "presence:global"}
}
