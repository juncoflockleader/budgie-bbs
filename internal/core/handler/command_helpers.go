package handler

import (
	"database/sql"
	"strconv"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func errDetail(code, msg string, retryable bool) *proto.ErrorDetail {
	return &proto.ErrorDetail{Code: code, Message: msg, Retryable: retryable}
}

func badPayload() Reply {
	return Reply{Err: errDetail(proto.ErrValidationFailed, "invalid payload", false)}
}

func internalErr(err error) Reply {
	return Reply{Err: errDetail("internal_error", err.Error(), true)}
}

func (h *Handler) publishEvent(kind proto.EventKind, seq int64, scopes []string, payload any, ts int64) {
	h.bus.Publish(&proto.Event{Kind: kind, Seq: seq, Scopes: scopes, Payload: payload, TS: ts})
}

func (h *Handler) publishGeneratedEvents(events []*proto.Event) {
	for _, evt := range events {
		h.bus.Publish(evt)
	}
}

// requireMinTrustForPoll blocks poll creation for actors below the requested
// trust level. Mod/admin actors bypass this gate.
func RequireMinTrustForPoll(db *sql.DB, actor *User, minLevel int, action string, userTrustLevel func(*sql.DB, string) (int, error)) Reply {
	if actor.IsMod() {
		return Reply{}
	}
	trustLevel, err := userTrustLevel(db, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if trustLevel < minLevel {
		return Reply{Err: errDetail(proto.ErrForbidden, action+" with poll requires trust level "+strconv.Itoa(minLevel), false)}
	}
	return Reply{}
}
