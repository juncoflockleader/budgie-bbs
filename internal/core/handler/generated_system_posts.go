package handler

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandevents"
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
		boardScopes, payload := commandevents.BoardCreated(spec.BoardID, spec.BoardName, spec.Description, "", position, actor.ID, ts)
		boardSeq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, payload)
		if err != nil {
			return nil, err
		}
		if err := currentRuntime().InsertBoard(tx, spec.BoardID, spec.BoardName, spec.Description, "", position); err != nil {
			return nil, err
		}
		_, publicPayload := commandevents.BoardCreated(spec.BoardID, spec.BoardName, spec.Description, "", 0, actor.Name, ts)
		out = append(out, &proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: boardScopes, Payload: publicPayload, TS: ts})
	}
	if spec.AfterEnsureBoard != nil {
		if err := spec.AfterEnsureBoard(); err != nil {
			return nil, err
		}
	}

	scopes, threadPayload := commandevents.ThreadNew(spec.ThreadID, spec.BoardID, actor.Name, actor.ID, spec.Title, ts)
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, threadPayload)
	if err != nil {
		return nil, err
	}
	threadScopes, postPayload := commandevents.PostAppended(spec.BoardID, commandevents.PostAppendedSpec{
		ID:          spec.PostID,
		Thread:      spec.ThreadID,
		Author:      actor.Name,
		AuthorID:    actor.ID,
		Body:        spec.Body,
		RawBody:     spec.Body,
		ContentType: "markup",
		TS:          ts,
	})
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, postPayload)
	if err != nil {
		return nil, err
	}
	if err := currentRuntime().InsertThread(tx, &projections.Thread{
		ID: spec.ThreadID, Board: spec.BoardID, Author: actor.Name, AuthorID: actor.ID, Title: spec.Title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := currentRuntime().InsertPost(tx, &projections.Post{
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
