package handler

import (
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func (h *Handler) votePoll(actor *User, p proto.VotePollPayload) Reply {
	if p.Poll == "" || p.Option == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "poll and option are required", false)}
	}
	ts := nowMS()

	// Verify poll + option exist (reads before TX).
	poll, err := getPollWithVotes(h.db, p.Poll, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if poll == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "poll not found", false)}
	}
	optionValid := false
	for _, opt := range poll.Options {
		if opt.ID == p.Option {
			optionValid = true
			break
		}
	}
	if !optionValid {
		return Reply{Err: errDetail(proto.ErrNotFound, "option not found", false)}
	}
	if poll.ExpiresAt > 0 && ts > poll.ExpiresAt {
		return Reply{Err: errDetail(proto.ErrConflict, "poll has expired", false)}
	}

	// Look up the post for scoping.
	post, err := getPost(h.db, poll.PostID)
	if err != nil || post == nil {
		return internalErr(err)
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

	if err := castVote(tx, p.Poll, p.Option, actor.ID, ts); err != nil {
		return internalErr(err)
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPollVoted, scopes, &proto.PollVotedPayload{
		Poll: p.Poll, Option: p.Option, User: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPollVoted, Seq: seq, Scopes: scopes,
		Payload: &proto.PollVotedPayload{Poll: p.Poll, Option: p.Option, User: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: p.Poll, Seq: seq}}
}
