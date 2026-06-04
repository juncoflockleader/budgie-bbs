package handler

import (
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// ── M10: Reactions ──────────────────────────────────────────────────────────

func (h *Handler) reactPost(actor *User, p proto.ReactPostPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	emoji := p.Emoji
	if emoji == "" {
		emoji = "heart"
	}
	ts := nowMS()

	// Read before TX.
	post, err := getPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot react to a redacted post", false)}
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

	if err := upsertReaction(tx, post.ID, actor.ID, emoji, ts); err != nil {
		return internalErr(err)
	}
	count, err := reactionCountTx(tx, post.ID)
	if err != nil {
		return internalErr(err)
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostReacted, scopes, &proto.PostReactedPayload{
		PostID: post.ID, Thread: post.Thread, User: actor.ID, Emoji: emoji, ReactionCount: count, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	postAuthorID := post.AuthorID
	if postAuthorID == "" {
		postAuthorID = post.Author
	}
	// Update activity for post author (best-effort).
	if postAuthorID != actor.ID {
		go recordReactionReceived(h.db, postAuthorID) //nolint
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostReacted, Seq: seq, Scopes: scopes,
		Payload: &proto.PostReactedPayload{PostID: post.ID, Thread: post.Thread, User: actor.Name, Emoji: emoji, ReactionCount: count, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) unreactPost(actor *User, p proto.ReactPostPayload) Reply {
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

	// Check user actually reacted.
	reacted, err := userReacted(h.db, post.ID, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if !reacted {
		return Reply{Err: errDetail(proto.ErrConflict, "you have not reacted to this post", false)}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if err := deleteReaction(tx, post.ID, actor.ID); err != nil {
		return internalErr(err)
	}
	count, err := reactionCountTx(tx, post.ID)
	if err != nil {
		return internalErr(err)
	}
	emoji := p.Emoji
	if emoji == "" {
		emoji = "heart"
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostUnreacted, scopes, &proto.PostUnreactedPayload{
		PostID: post.ID, Thread: post.Thread, User: actor.ID, Emoji: emoji, ReactionCount: count, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	postAuthorID := post.AuthorID
	if postAuthorID == "" {
		postAuthorID = post.Author
	}
	if postAuthorID != actor.ID {
		go recordReactionRemoved(h.db, postAuthorID) //nolint
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostUnreacted, Seq: seq, Scopes: scopes,
		Payload: &proto.PostUnreactedPayload{PostID: post.ID, Thread: post.Thread, User: actor.Name, Emoji: emoji, ReactionCount: count, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}
