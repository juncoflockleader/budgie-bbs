package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type commandRequest struct {
	CID     string              `json:"cid"`
	Command proto.CommandName   `json:"command"`
	Payload json.RawMessage     `json:"payload"`
}

// handleCommand serves POST /api/v1/commands (uniform endpoint).
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())

	var req commandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "command field required", false)
		return
	}
	cid := req.CID
	if cid == "" {
		cid = r.Header.Get("X-Command-Id")
	}

	reply := s.core.ExecCmd(r.Context(), actor, req.Command, req.Payload, cid)
	writeAck(w, cid, reply)
}

// --- RESTful alias handlers ---

func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	var p proto.CreateThreadPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Board = boardID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdCreateThread, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleAppendPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	threadID := r.PathValue("thread")

	var p proto.AppendPostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Thread = threadID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdAppendPost, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleEditPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	var p proto.EditPostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Post = postID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdEditPost, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleRedactPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	var p proto.RedactPostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		p = proto.RedactPostPayload{}
	}
	p.Post = postID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdRedactPost, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleRestorePost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	p := proto.RestorePostPayload{Post: postID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdRestorePost, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleLockThread(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	threadID := r.PathValue("thread")

	var p proto.LockThreadPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Thread = threadID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdLockThread, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleSendChatLine(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	room := r.PathValue("room")

	var p proto.SendChatLinePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Room = room

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSendChatLine, raw, cid)
	writeAck(w, cid, reply)
}

// writeAck serialises a handler Reply into the wire ack envelope.
func writeAck(w http.ResponseWriter, cid string, reply core.Reply) {
	type ack struct {
		Kind   string             `json:"kind"`
		CID    string             `json:"cid,omitempty"`
		OK     bool               `json:"ok"`
		Result *proto.AckResult   `json:"result"`
		Error  *proto.ErrorDetail `json:"error,omitempty"`
	}
	a := ack{Kind: "ack", CID: cid, OK: reply.Err == nil, Result: reply.Result, Error: reply.Err}
	status := http.StatusOK
	if reply.Err != nil {
		status = errorCode(reply.Err.Code)
	} else if reply.Result != nil && reply.Result.ID != "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, a)
}
