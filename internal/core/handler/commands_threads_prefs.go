package handler

import (
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func (h *Handler) setThreadPref(actor *User, p proto.SetThreadPrefPayload) Reply {
	if p.Thread == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "thread is required", false)}
	}
	if p.Level != "watch" && p.Level != "normal" && p.Level != "mute" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, `level must be "watch", "normal", or "mute"`, false)}
	}
	// Verify thread exists.
	thread, err := getThread(h.db, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	if err := setThreadPref(h.db, actor.ID, p.Thread, p.Level); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Thread}}
}
