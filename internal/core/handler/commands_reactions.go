package handler

import (
	"encoding/json"
	"strings"

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

	alreadyReacted, err := userReacted(post.ID, actor.ID)
	if err != nil {
		return internalErr(err)
	}

	counters, err := beginCounterMutation()
	if err != nil {
		return internalErr(err)
	}
	defer counters.Rollback() //nolint

	if err := counters.UpsertReaction(post.ID, actor.ID, emoji, ts); err != nil {
		return internalErr(err)
	}
	count, err := counters.ReactionCount(post.ID)
	if err != nil {
		return internalErr(err)
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	postAuthorID := post.AuthorID
	if postAuthorID == "" {
		postAuthorID = post.Author
	}
	if !alreadyReacted && postAuthorID != actor.ID {
		if err := counters.RecordReactionReceived(postAuthorID); err != nil {
			return internalErr(err)
		}
	}
	if err := counters.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostReacted, Scopes: scopes,
		Payload: &proto.PostReactedPayload{PostID: post.ID, Thread: post.Thread, User: actor.Name, Emoji: emoji, ReactionCount: count, TS: ts}, TS: ts})
	pgNotifyEphemeral(h.db, string(proto.EvtPostReacted), encodePostReactionWakeup(post.ID, actor.ID, emoji, ts), stringsJoinScopes(scopes))

	return Reply{Result: &proto.AckResult{ID: post.ID}}
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
	reacted, err := userReacted(post.ID, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if !reacted {
		return Reply{Err: errDetail(proto.ErrConflict, "you have not reacted to this post", false)}
	}

	counters, err := beginCounterMutation()
	if err != nil {
		return internalErr(err)
	}
	defer counters.Rollback() //nolint

	if err := counters.DeleteReaction(post.ID, actor.ID); err != nil {
		return internalErr(err)
	}
	count, err := counters.ReactionCount(post.ID)
	if err != nil {
		return internalErr(err)
	}
	emoji := p.Emoji
	if emoji == "" {
		emoji = "heart"
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	postAuthorID := post.AuthorID
	if postAuthorID == "" {
		postAuthorID = post.Author
	}
	if postAuthorID != actor.ID {
		if err := counters.RecordReactionRemoved(postAuthorID); err != nil {
			return internalErr(err)
		}
	}
	if err := counters.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostUnreacted, Scopes: scopes,
		Payload: &proto.PostUnreactedPayload{PostID: post.ID, Thread: post.Thread, User: actor.Name, Emoji: emoji, ReactionCount: count, TS: ts}, TS: ts})
	pgNotifyEphemeral(h.db, string(proto.EvtPostUnreacted), encodePostReactionWakeup(post.ID, actor.ID, emoji, ts), stringsJoinScopes(scopes))

	return Reply{Result: &proto.AckResult{ID: post.ID}}
}

type postReactionWakeup struct {
	Post  string `json:"post"`
	User  string `json:"user"`
	Emoji string `json:"emoji,omitempty"`
	TS    int64  `json:"ts"`
}

func encodePostReactionWakeup(postID, userID, emoji string, ts int64) string {
	raw, err := json.Marshal(postReactionWakeup{Post: postID, User: userID, Emoji: emoji, TS: ts})
	if err != nil {
		return ""
	}
	return string(raw)
}

func stringsJoinScopes(scopes []string) string {
	return strings.Join(scopes, ",")
}
