package handler

import (
	"database/sql"
	"fmt"

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
	publicBoard, err := h.publicBoardForModerationLog(thread.Board)
	if err != nil {
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
		ReviewID: reviewID, Kind: "post_flag", PostID: post.ID, Thread: post.Thread, Reporter: actor.ID, Reason: p.Reason, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	generatedEvents := []*proto.Event{}
	if publicBoard {
		generatedEvents, err = h.appendModerationSystemPostTx(tx, actor, moderationLogFlag, reviewID, post.ID, post.Thread, thread.Board, ts)
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
	if !actor.IsMod() {
		return Reply{Err: errDetail(proto.ErrForbidden, "moderator role required", false)}
	}
	if p.Review == "" || p.Resolution == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "review and resolution are required", false)}
	}
	ts := nowMS()
	targetPostID, targetThreadID, targetBoardID, publicBoard, err := h.moderationReviewLogTarget(p.Review)
	if err != nil {
		return internalErr(err)
	}

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
	generatedEvents := []*proto.Event{}
	if publicBoard {
		generatedEvents, err = h.appendModerationSystemPostTx(tx, actor, moderationLogResolve, p.Review, targetPostID, targetThreadID, targetBoardID, ts)
		if err != nil {
			return internalErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtReviewResolved, Seq: seq, Scopes: scopes,
		Payload: &proto.ReviewResolvedPayload{ReviewID: p.Review, Resolution: p.Resolution, By: actor.Name, TS: ts}, TS: ts})
	h.publishGeneratedEvents(generatedEvents)
	return Reply{Result: &proto.AckResult{ID: p.Review, Seq: seq}}
}

const (
	moderationSystemBoardID = "0moderation"
	filterSystemBoardID     = "Filter"
	moderationLogFlag       = "flag"
	moderationLogResolve    = "resolve"
)

func (h *Handler) moderationReviewLogTarget(reviewID string) (postID, threadID, boardID string, public bool, err error) {
	var targetKind string
	err = qQueryRow(h.db, `SELECT target_id, target_kind FROM moderation_reviews WHERE id=?`, reviewID).Scan(&postID, &targetKind)
	if err == sql.ErrNoRows {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	if targetKind != "post" {
		return postID, "", "", false, nil
	}
	post, err := getPost(h.db, postID)
	if err != nil || post == nil {
		return postID, "", "", false, err
	}
	thread, err := getThread(h.db, post.Thread)
	if err != nil || thread == nil {
		return postID, post.Thread, "", false, err
	}
	public, err = h.publicBoardForModerationLog(thread.Board)
	return postID, post.Thread, thread.Board, public, err
}

func (h *Handler) publicBoardForModerationLog(boardID string) (bool, error) {
	settings, err := getBoardSettings(h.db, boardID)
	if err != nil {
		return false, err
	}
	return settings == nil || !settings.MemberReadMode, nil
}

func (h *Handler) appendModerationSystemPostTx(tx *sql.Tx, actor *User, action, reviewID, postID, threadID, boardID string, ts int64) ([]*proto.Event, error) {
	threadIDOut := "mod_" + action + "_thr_" + reviewID
	postIDOut := "mod_" + action + "_pst_" + reviewID
	var exists int
	err := qQueryRow(tx, `SELECT 1 FROM threads WHERE id=?`, threadIDOut).Scan(&exists)
	if err == nil {
		return nil, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	out := []*proto.Event{}
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, moderationSystemBoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, err := boardCategoryPosition(tx, "", nil)
		if err != nil {
			return nil, err
		}
		boardScopes := []string{"board:" + moderationSystemBoardID}
		boardSeq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          moderationSystemBoardID,
			Name:        "0Moderation",
			Description: "Generated moderation audit log",
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return nil, err
		}
		if err := insertBoard(tx, moderationSystemBoardID, "0Moderation", "Generated moderation audit log", "", position); err != nil {
			return nil, err
		}
		out = append(out, &proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: boardScopes,
			Payload: &proto.BoardCreatedPayload{ID: moderationSystemBoardID, Name: "0Moderation", Description: "Generated moderation audit log", By: actor.Name, TS: ts}, TS: ts})
	} else if err != nil {
		return nil, err
	}

	title := "Moderation flag " + reviewID
	statusLine := "opened"
	if action == moderationLogResolve {
		title = "Moderation resolved " + reviewID
		statusLine = "resolved"
	}
	body := fmt.Sprintf("# %s\n\n- Review: %s\n- Status: %s\n- Board: %s\n- Thread: %s\n- Post: %s\n- Actor: %s\n\nSensitive report and resolution text is kept in the moderator review queue.\n",
		title, reviewID, statusLine, boardID, threadID, postID, actor.Name)
	scopes := []string{"board:" + moderationSystemBoardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadIDOut, Board: moderationSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts,
	})
	if err != nil {
		return nil, err
	}
	threadScopes := append(scopes, "thread:"+threadIDOut)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postIDOut, Thread: threadIDOut, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return nil, err
	}
	if err := insertThread(tx, &Thread{
		ID: threadIDOut, Board: moderationSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := insertPost(tx, &Post{
		ID: postIDOut, Thread: threadIDOut, Author: actor.Name, AuthorID: actor.ID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := bumpThread(tx, threadIDOut, pseq); err != nil {
		return nil, err
	}
	if err := ftsInsertPost(tx, postIDOut, threadIDOut, moderationSystemBoardID, actor.Name, body); err != nil {
		return nil, err
	}
	out = append(out,
		&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
			Payload: &proto.ThreadNewPayload{ID: threadIDOut, Board: moderationSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts}, TS: ts},
		&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
			Payload: &proto.PostAppendedPayload{ID: postIDOut, Thread: threadIDOut, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts},
	)
	return out, nil
}

func (h *Handler) appendContentFilterReviewTx(tx *sql.Tx, actor *User, publicAuthor string, filter *ContentFilter, postID, threadID, boardID string, publicBoard bool, ts int64) (*proto.Event, []*proto.Event, error) {
	if filter == nil {
		return nil, nil, nil
	}
	reviewID := newID("rev_")
	reason := "Matched content filter " + filter.ID
	if err := insertModerationReview(tx, reviewID, "content_filter", postID, "post", actor.ID, reason, ts); err != nil {
		return nil, nil, err
	}
	scopes := []string{"thread:" + threadID, "board:" + boardID, "moderation:global"}
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
	threadIDOut := "filter_thr_" + reviewID
	postIDOut := "filter_pst_" + reviewID
	var exists int
	err := qQueryRow(tx, `SELECT 1 FROM threads WHERE id=?`, threadIDOut).Scan(&exists)
	if err == nil {
		return nil, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	out := []*proto.Event{}
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, filterSystemBoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, err := boardCategoryPosition(tx, "", nil)
		if err != nil {
			return nil, err
		}
		boardScopes := []string{"board:" + filterSystemBoardID}
		boardSeq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          filterSystemBoardID,
			Name:        "Filter",
			Description: "Generated content filter review log",
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return nil, err
		}
		if err := insertBoard(tx, filterSystemBoardID, "Filter", "Generated content filter review log", "", position); err != nil {
			return nil, err
		}
		out = append(out, &proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: boardScopes,
			Payload: &proto.BoardCreatedPayload{ID: filterSystemBoardID, Name: "Filter", Description: "Generated content filter review log", By: actor.Name, TS: ts}, TS: ts})
	} else if err != nil {
		return nil, err
	}

	title := "Content filter review " + reviewID
	body := fmt.Sprintf("# %s\n\n- Review: %s\n- Status: opened\n- Filter: %s\n- Filter scope: %s\n- Board: %s\n- Thread: %s\n- Post: %s\n- Public author: %s\n\nSensitive filter pattern and article body are kept out of this generated record.\n",
		title, reviewID, filter.ID, filter.Scope, boardID, threadID, postID, publicAuthor)
	scopes := []string{"board:" + filterSystemBoardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadIDOut, Board: filterSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts,
	})
	if err != nil {
		return nil, err
	}
	threadScopes := append(scopes, "thread:"+threadIDOut)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postIDOut, Thread: threadIDOut, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return nil, err
	}
	if err := insertThread(tx, &Thread{
		ID: threadIDOut, Board: filterSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := insertPost(tx, &Post{
		ID: postIDOut, Thread: threadIDOut, Author: actor.Name, AuthorID: actor.ID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := bumpThread(tx, threadIDOut, pseq); err != nil {
		return nil, err
	}
	if err := ftsInsertPost(tx, postIDOut, threadIDOut, filterSystemBoardID, actor.Name, body); err != nil {
		return nil, err
	}
	out = append(out,
		&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
			Payload: &proto.ThreadNewPayload{ID: threadIDOut, Board: filterSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts}, TS: ts},
		&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
			Payload: &proto.PostAppendedPayload{ID: postIDOut, Thread: threadIDOut, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts},
	)
	return out, nil
}

func (h *Handler) publishGeneratedEvents(events []*proto.Event) {
	for _, evt := range events {
		h.bus.Publish(evt)
	}
}
