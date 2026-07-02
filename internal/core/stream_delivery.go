package core

import (
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// gapReplayLimit bounds how many missed durable events a single gap repair
// replays before the current event is delivered.
const gapReplayLimit = 1000

// DeliverWithGapRepair writes one event to a stream consumer, repairing any
// cursor gap by replaying missed durable events first. It is the shared
// delivery path for all live transports (SSE, WebSocket, and future ones), so
// gap detection and cursor bookkeeping behave identically everywhere.
//
// Ephemeral events (no Seq) are delivered directly. For durable events it:
//   - skips events the consumer has already seen
//   - replays any scalar or partition gap before writing the current event
//   - advances the cursor for each delivered event
//
// deliver writes a single event to the transport. onReplayErr decides the
// transport's policy when the gap replay itself fails: return the error to
// abort the stream (SSE, where the client reconnects with its cursor), or
// return nil to log-and-continue delivering the current event (WebSocket).
func (c *Core) DeliverWithGapRepair(cursor *proto.Cursor, evt *proto.Event, scopes []string, deliver func(*proto.Event) error, onReplayErr func(error) error) error {
	if cursor == nil {
		cursor = &proto.Cursor{}
	}
	if !evt.IsDurable() {
		return deliver(evt)
	}
	if cursor.SeenEvent(evt) {
		return nil
	}
	if cursor.PartitionGapBeforeEvent(evt) || cursor.ScalarGapBeforeEvent(evt) {
		replayCursor := *cursor
		if cursor.PartitionGapBeforeEvent(evt) {
			replayCursor = replayCursor.PartitionOnly()
		}
		missed, err := c.ReplayCursor(replayCursor, scopes, gapReplayLimit)
		if err != nil {
			if err := onReplayErr(err); err != nil {
				return err
			}
		} else {
			metrics.GatewayReplayRepairs.Inc()
			metrics.ReplayTotal.Inc()
			metrics.ReplayBatchSize.Observe(float64(len(missed)))
			for _, m := range missed {
				if cursor.SeenEvent(m) || proto.DurableEventAtOrAfter(m, evt) {
					continue
				}
				if err := deliver(m); err != nil {
					return err
				}
				cursor.ObserveEvent(m)
			}
		}
	}
	if err := deliver(evt); err != nil {
		return err
	}
	cursor.ObserveEvent(evt)
	return nil
}
