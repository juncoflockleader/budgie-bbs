package handler

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// ── Modern moderation review queue ──────────────────────────────────────────

func (h *Handler) flagPost(actor *User, p proto.FlagPostPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	ts := nowMS()

	post, err := getPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	thread, err := getThread(h.db, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	reviewID := newID("rev_")
	if err := insertModerationReview(tx, reviewID, "post_flag", post.ID, "post", actor.ID, p.Reason, ts); err != nil {
		return internalErr(err)
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board, "moderation:global"}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostFlagged, scopes, &proto.PostFlaggedPayload{
		ReviewID: reviewID, PostID: post.ID, Thread: post.Thread, Reporter: actor.ID, Reason: p.Reason, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostFlagged, Seq: seq, Scopes: scopes,
		Payload: &proto.PostFlaggedPayload{ReviewID: reviewID, PostID: post.ID, Thread: post.Thread, Reporter: actor.Name, Reason: p.Reason, TS: ts}, TS: ts})
	return Reply{Result: &proto.AckResult{ID: reviewID, Seq: seq}}
}

func (h *Handler) resolveReview(actor *User, p proto.ResolveReviewPayload) Reply {
	if !actor.IsMod() {
		return Reply{Err: errDetail(proto.ErrForbidden, "moderator role required", false)}
	}
	if p.Review == "" || p.Resolution == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "review and resolution are required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if err := resolveModerationReview(tx, p.Review, actor.ID, p.Resolution, ts); err != nil {
		if err == sql.ErrNoRows {
			return Reply{Err: errDetail(proto.ErrNotFound, "review not found", false)}
		}
		return internalErr(err)
	}
	scopes := []string{"moderation:global"}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtReviewResolved, scopes, &proto.ReviewResolvedPayload{
		ReviewID: p.Review, Resolution: p.Resolution, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtReviewResolved, Seq: seq, Scopes: scopes,
		Payload: &proto.ReviewResolvedPayload{ReviewID: p.Review, Resolution: p.Resolution, By: actor.Name, TS: ts}, TS: ts})
	return Reply{Result: &proto.AckResult{ID: p.Review, Seq: seq}}
}
