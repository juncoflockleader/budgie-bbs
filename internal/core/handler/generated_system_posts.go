package handler

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type generatedSystemPostSpec struct {
	BoardID          string
	BoardName        string
	Description      string
	ThreadID         string
	PostID           string
	Title            string
	Body             string
	AfterEnsureBoard func() error
}

func (h *Handler) appendGeneratedSystemPostTx(tx *sql.Tx, actor *User, spec generatedSystemPostSpec, ts int64) ([]*proto.Event, error) {
	out := []*proto.Event{}
	exists, err := projections.BoardExists(tx, spec.BoardID)
	if err != nil {
		return nil, err
	}
	if !exists {
		position, err := projections.NextCategoryPosition(tx, "")
		if err != nil {
			return nil, err
		}
		boardScopes := []string{"board:" + spec.BoardID}
		boardSeq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          spec.BoardID,
			Name:        spec.BoardName,
			Description: spec.Description,
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return nil, err
		}
		if err := currentRuntime().InsertBoard(tx, spec.BoardID, spec.BoardName, spec.Description, "", position); err != nil {
			return nil, err
		}
		out = append(out, &proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: boardScopes,
			Payload: &proto.BoardCreatedPayload{ID: spec.BoardID, Name: spec.BoardName, Description: spec.Description, By: actor.Name, TS: ts}, TS: ts})
	}
	if spec.AfterEnsureBoard != nil {
		if err := spec.AfterEnsureBoard(); err != nil {
			return nil, err
		}
	}

	scopes := []string{"board:" + spec.BoardID}
	threadPayload := &proto.ThreadNewPayload{
		ID: spec.ThreadID, Board: spec.BoardID, Author: actor.Name, AuthorID: actor.ID, Title: spec.Title, TS: ts,
	}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, threadPayload)
	if err != nil {
		return nil, err
	}
	threadScopes := append(scopes, "thread:"+spec.ThreadID)
	postPayload := &proto.PostAppendedPayload{
		ID: spec.PostID, Thread: spec.ThreadID, Author: actor.Name, AuthorID: actor.ID, Body: spec.Body, RawBody: spec.Body, ContentType: "markup", TS: ts,
	}
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, postPayload)
	if err != nil {
		return nil, err
	}
	if err := currentRuntime().InsertThread(tx, &Thread{
		ID: spec.ThreadID, Board: spec.BoardID, Author: actor.Name, AuthorID: actor.ID, Title: spec.Title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := currentRuntime().InsertPost(tx, &Post{
		ID: spec.PostID, Thread: spec.ThreadID, Author: actor.Name, AuthorID: actor.ID,
		Body: spec.Body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := currentRuntime().BumpThread(tx, spec.ThreadID, pseq); err != nil {
		return nil, err
	}
	if err := currentRuntime().FtsInsertPost(tx, spec.PostID, spec.ThreadID, spec.BoardID, actor.Name, spec.Body); err != nil {
		return nil, err
	}
	out = append(out,
		&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes, Payload: threadPayload, TS: ts},
		&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes, Payload: postPayload, TS: ts},
	)
	return out, nil
}
