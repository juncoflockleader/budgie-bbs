package handler

import (
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandevents"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandrules"
	"github.com/juncoflockleader/budgie-bbs/internal/core/eventwakeup"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func (h *Handler) votePoll(actor *projections.User, p proto.VotePollPayload) Reply {
	if p.Poll == "" || p.Option == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "poll and option are required", false)}
	}
	ts := nowMS()

	// Verify poll + option exist (reads before TX).
	poll, err := currentRuntime().GetPollWithVotes(h.db, p.Poll, actor.ID)
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
	post, err := currentRuntime().GetPost(h.db, poll.PostID)
	if err != nil || post == nil {
		return internalErr(err)
	}
	thread, err := currentRuntime().GetThread(h.db, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}

	counters, err := beginCounterMutation()
	if err != nil {
		return internalErr(err)
	}
	defer counters.Rollback() //nolint

	if err := counters.CastVote(p.Poll, p.Option, actor.ID, ts); err != nil {
		return internalErr(err)
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	if err := counters.Commit(); err != nil {
		return internalErr(err)
	}

	scopes, eventPayload := commandevents.PollVoted(p.Poll, p.Option, post.Thread, thread.Board, actor.Name, ts)
	h.bus.Publish(&proto.Event{Kind: proto.EvtPollVoted, Scopes: scopes, Payload: eventPayload, TS: ts})
	pgNotifyEphemeral(h.db, string(proto.EvtPollVoted), eventwakeup.EncodePollVote(p.Poll, actor.ID, ts), strings.Join(scopes, ","))

	return Reply{Result: &proto.AckResult{ID: p.Poll}}
}

func (h *Handler) publishPollResult(actor *projections.User, p proto.PublishPollResultPayload) Reply {
	p, msg := proto.NormalizePublishPollResultPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	poll, err := currentRuntime().GetPollWithVotes(h.db, p.Poll, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if poll == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "poll not found", false)}
	}
	post, err := currentRuntime().GetPost(h.db, poll.PostID)
	if err != nil || post == nil {
		return internalErr(err)
	}
	thread, err := currentRuntime().GetThread(h.db, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}
	canManagePolls := commandrules.ActorCanManageBoardPolls(h.db, actor, thread.Board)
	if errDetail := commandrules.RequirePollResultPublisher(canManagePolls, post.AuthorID == actor.ID, thread.AuthorID == actor.ID); errDetail != nil {
		return Reply{Err: errDetail}
	}
	emit, err := currentRuntime().BoardAllowsPublicSystemPost(h.db, thread.Board)
	if err != nil {
		return internalErr(err)
	}
	if errDetail := commandrules.RequirePollResultPublicBoard(emit); errDetail != nil {
		return Reply{Err: errDetail}
	}
	threadID, seq, err := h.ensurePollResultSystemPost(actor, thread, poll)
	if err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: threadID, Seq: seq}}
}

func (h *Handler) ensurePollResultSystemPost(actor *projections.User, sourceThread *projections.Thread, poll *projections.Poll) (string, int64, error) {
	threadID, postID := proto.PollResultPostIDs(poll.ID)
	if existingSeq, found, err := projections.ThreadLastSeq(h.db, threadID); err != nil {
		return "", 0, err
	} else if found {
		return threadID, existingSeq, nil
	}

	ts := nowMS()
	title := proto.PollResultTitle(poll.Question)
	body := proto.FormatPollResultBody(sourceThread.Title, sourceThread.Board, poll.Question, projections.PollResultOptions(poll))

	tx, err := h.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback() //nolint

	events, err := h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:     proto.VoteSystemBoardID,
		BoardName:   proto.VoteSystemBoardName,
		Description: proto.VoteSystemBoardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
	}, ts)
	if err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}

	h.publishGeneratedEvents(events)
	return threadID, events[len(events)-1].Seq, nil
}
