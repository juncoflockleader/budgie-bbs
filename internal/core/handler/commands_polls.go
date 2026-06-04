package handler

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const voteSystemBoardID = "vote"

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

func (h *Handler) publishPollResult(actor *User, p proto.PublishPollResultPayload) Reply {
	if strings.TrimSpace(p.Poll) == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "poll is required", false)}
	}
	poll, err := getPollWithVotes(h.db, p.Poll, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if poll == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "poll not found", false)}
	}
	post, err := getPost(h.db, poll.PostID)
	if err != nil || post == nil {
		return internalErr(err)
	}
	thread, err := getThread(h.db, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}
	if !h.actorCanManageBoardPolls(actor, thread.Board) && post.AuthorID != actor.ID && thread.AuthorID != actor.ID {
		return Reply{Err: errDetail(proto.ErrForbidden, "poll author or board poll manager required", false)}
	}
	settings, err := getBoardSettings(h.db, thread.Board)
	if err != nil {
		return internalErr(err)
	}
	if settings != nil && settings.MemberReadMode {
		return Reply{Err: errDetail(proto.ErrForbidden, "member-read poll results stay on the source board", false)}
	}
	threadID, seq, err := h.ensurePollResultSystemPost(actor, thread, poll)
	if err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: threadID, Seq: seq}}
}

func (h *Handler) ensurePollResultSystemPost(actor *User, sourceThread *Thread, poll *Poll) (string, int64, error) {
	threadID := "vote_poll_" + poll.ID
	postID := "vote_poll_post_" + poll.ID
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		return threadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}

	ts := nowMS()
	question := strings.TrimSpace(poll.Question)
	title := "Poll result"
	if question != "" {
		title = "Poll result: " + question
	}
	body := formatPollResultBody(sourceThread, poll)

	tx, err := h.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback() //nolint

	boardCreated := false
	var boardSeq int64
	var exists int
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, voteSystemBoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, err := boardCategoryPosition(tx, "", nil)
		if err != nil {
			return "", 0, err
		}
		boardScopes := []string{"board:" + voteSystemBoardID}
		boardSeq, err = appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          voteSystemBoardID,
			Name:        "vote",
			Description: "Generated poll results",
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return "", 0, err
		}
		if err := insertBoard(tx, voteSystemBoardID, "vote", "Generated poll results", "", position); err != nil {
			return "", 0, err
		}
		boardCreated = true
	} else if err != nil {
		return "", 0, err
	}

	scopes := []string{"board:" + voteSystemBoardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: voteSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts,
	})
	if err != nil {
		return "", 0, err
	}
	threadScopes := append(scopes, "thread:"+threadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return "", 0, err
	}
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: voteSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return "", 0, err
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return "", 0, err
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return "", 0, err
	}
	if err := ftsInsertPost(tx, postID, threadID, voteSystemBoardID, actor.Name, body); err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}

	if boardCreated {
		h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: []string{"board:" + voteSystemBoardID},
			Payload: &proto.BoardCreatedPayload{ID: voteSystemBoardID, Name: "vote", Description: "Generated poll results", By: actor.Name, TS: ts}, TS: ts})
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: voteSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts})
	return threadID, pseq, nil
}

func formatPollResultBody(sourceThread *Thread, poll *Poll) string {
	total := 0
	for _, option := range poll.Options {
		total += option.VoteCount
	}
	var b strings.Builder
	question := strings.TrimSpace(poll.Question)
	if question == "" {
		question = "Untitled poll"
	}
	fmt.Fprintf(&b, "# Poll result: %s\n\n", question)
	fmt.Fprintf(&b, "- Source thread: %s\n", sourceThread.Title)
	fmt.Fprintf(&b, "- Source board: %s\n", sourceThread.Board)
	fmt.Fprintf(&b, "- Total votes: %d\n\n", total)
	for i, option := range poll.Options {
		percent := 0
		if total > 0 {
			percent = option.VoteCount * 100 / total
		}
		fmt.Fprintf(&b, "%d. %s: %d vote(s), %d%%\n", i+1, option.Text, option.VoteCount, percent)
	}
	b.WriteString("\nGenerated public poll result.\n")
	return b.String()
}
