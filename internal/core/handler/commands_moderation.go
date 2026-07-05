package handler

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandevents"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandrules"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// ── Modern moderation review queue ──────────────────────────────────────────

func (h *Handler) flagPost(actor *User, p proto.FlagPostPayload) Reply {
	p, msg := proto.NormalizeFlagPostPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	ts := nowMS()

	post, err := currentRuntime().GetPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	thread, err := currentRuntime().GetThread(h.db, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}
	publicBoard, err := currentRuntime().BoardAllowsPublicSystemPost(h.db, thread.Board)
	if err != nil {
		return internalErr(err)
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	reviewID := newID("rev_")
	if err := currentRuntime().InsertModerationReview(tx, reviewID, "post_flag", post.ID, "post", actor.ID, p.Reason, ts); err != nil {
		return internalErr(err)
	}
	// Moderation-only: this event carries the reporter and reason, so it must
	// not be delivered/replayed on board/thread scopes (that exposes the
	// reporter's identity to every board member — M8). Moderators receive it via
	// moderation:global; the review projection consumes it regardless of scope.
	scopes := []string{"moderation:global"}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostFlagged, scopes, &proto.PostFlaggedPayload{
		ReviewID: reviewID, Kind: "post_flag", PostID: post.ID, Thread: post.Thread, Reporter: actor.ID, Reason: p.Reason, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	generatedEvents := []*proto.Event{}
	if publicBoard {
		generatedEvents, err = h.appendModerationSystemPostTx(tx, actor, proto.ModerationLogFlag, reviewID, post.ID, post.Thread, thread.Board, ts)
		if err != nil {
			return internalErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostFlagged, Seq: seq, Scopes: scopes,
		Payload: &proto.PostFlaggedPayload{ReviewID: reviewID, Kind: "post_flag", PostID: post.ID, Thread: post.Thread, Reporter: actor.Name, Reason: p.Reason, TS: ts}, TS: ts})
	h.publishGeneratedEvents(generatedEvents)
	return Reply{Result: &proto.AckResult{ID: reviewID, Seq: seq}}
}

func (h *Handler) resolveReview(actor *User, p proto.ResolveReviewPayload) Reply {
	if errDetail := commandrules.RequireModeratorRole(actor.IsMod()); errDetail != nil {
		return Reply{Err: errDetail}
	}
	p, msg := proto.NormalizeResolveReviewPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	ts := nowMS()
	target, found, err := projections.GetModerationReviewLogTarget(h.db, p.Review)
	if err != nil {
		return internalErr(err)
	}
	if errDetail := commandrules.RequireOpenModerationReview(found, target.Status); errDetail != nil {
		return Reply{Err: errDetail}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if err := currentRuntime().ResolveModerationReview(tx, p.Review, actor.ID, p.Resolution, ts); err != nil {
		if err == sql.ErrNoRows {
			return Reply{Err: commandrules.RequireOpenModerationReview(false, "")}
		}
		return internalErr(err)
	}
	scopes, payload := commandevents.ReviewResolved(p.Review, p.Resolution, actor.ID, ts)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtReviewResolved, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	generatedEvents := []*proto.Event{}
	if target.Public {
		generatedEvents, err = h.appendModerationSystemPostTx(tx, actor, proto.ModerationLogResolve, p.Review, target.PostID, target.ThreadID, target.BoardID, ts)
		if err != nil {
			return internalErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	_, publicPayload := commandevents.ReviewResolved(p.Review, p.Resolution, actor.Name, ts)
	h.bus.Publish(&proto.Event{Kind: proto.EvtReviewResolved, Seq: seq, Scopes: scopes, Payload: publicPayload, TS: ts})
	h.publishGeneratedEvents(generatedEvents)
	return Reply{Result: &proto.AckResult{ID: p.Review, Seq: seq}}
}

func (h *Handler) appendModerationSystemPostTx(tx *sql.Tx, actor *User, action, reviewID, postID, threadID, boardID string, ts int64) ([]*proto.Event, error) {
	threadIDOut, postIDOut := proto.ModerationSystemPostIDs(action, reviewID)
	exists, err := projections.ThreadExists(tx, threadIDOut)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, nil
	}

	title := proto.ModerationSystemTitle(action, reviewID)
	body := proto.FormatModerationSystemBody(action, reviewID, boardID, threadID, postID, actor.Name)
	return h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:     proto.ModerationSystemBoardID,
		BoardName:   proto.ModerationSystemBoardName,
		Description: proto.ModerationSystemBoardDescription,
		ThreadID:    threadIDOut,
		PostID:      postIDOut,
		Title:       title,
		Body:        body,
	}, ts)
}

func (h *Handler) appendContentFilterReviewTx(tx *sql.Tx, actor *User, publicAuthor string, filter *ContentFilter, postID, threadID, boardID string, publicBoard bool, ts int64) (*proto.Event, []*proto.Event, error) {
	if filter == nil {
		return nil, nil, nil
	}
	reviewID := newID("rev_")
	reason := "Matched content filter " + filter.ID
	if err := currentRuntime().InsertModerationReview(tx, reviewID, "content_filter", postID, "post", actor.ID, reason, ts); err != nil {
		return nil, nil, err
	}
	// Moderation-only (reporter/reason must not reach board subscribers — M8).
	scopes := []string{"moderation:global"}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostFlagged, scopes, &proto.PostFlaggedPayload{
		ReviewID: reviewID, Kind: "content_filter", PostID: postID, Thread: threadID, Reporter: actor.ID, Reason: reason, TS: ts,
	})
	if err != nil {
		return nil, nil, err
	}
	evt := &proto.Event{Kind: proto.EvtPostFlagged, Seq: seq, Scopes: scopes,
		Payload: &proto.PostFlaggedPayload{ReviewID: reviewID, Kind: "content_filter", PostID: postID, Thread: threadID, Reporter: actor.Name, Reason: reason, TS: ts}, TS: ts}

	if !publicBoard {
		return evt, nil, nil
	}
	generated, err := h.appendFilterSystemPostTx(tx, actor, publicAuthor, filter, reviewID, postID, threadID, boardID, ts)
	if err != nil {
		return nil, nil, err
	}
	return evt, generated, nil
}

func (h *Handler) appendFilterSystemPostTx(tx *sql.Tx, actor *User, publicAuthor string, filter *ContentFilter, reviewID, postID, threadID, boardID string, ts int64) ([]*proto.Event, error) {
	threadIDOut, postIDOut := proto.ContentFilterReviewPostIDs(reviewID)
	exists, err := projections.ThreadExists(tx, threadIDOut)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, nil
	}

	title := proto.ContentFilterReviewTitle(reviewID)
	body := proto.FormatContentFilterReviewBody(title, reviewID, filter.ID, filter.Scope, boardID, threadID, postID, publicAuthor)
	return h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:     proto.ContentFilterSystemBoardID,
		BoardName:   proto.ContentFilterSystemBoardName,
		Description: proto.ContentFilterSystemBoardDescription,
		ThreadID:    threadIDOut,
		PostID:      postIDOut,
		Title:       title,
		Body:        body,
	}, ts)
}
