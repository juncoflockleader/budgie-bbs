package handler

import "github.com/juncoflockleader/budgie-bbs/internal/proto"

func errDetail(code, msg string, retryable bool) *proto.ErrorDetail {
	return &proto.ErrorDetail{Code: code, Message: msg, Retryable: retryable}
}

func badPayload() Reply {
	return Reply{Err: errDetail(proto.ErrValidationFailed, "invalid payload", false)}
}

func internalErr(err error) Reply {
	return Reply{Err: errDetail("internal_error", err.Error(), true)}
}

func replyFromCommandResult(result *proto.AckResult, errDetail *proto.ErrorDetail) Reply {
	if errDetail != nil {
		return Reply{Err: errDetail}
	}
	return Reply{Result: result}
}

func (h *Handler) publishEvent(kind proto.EventKind, seq int64, scopes []string, payload any, ts int64) {
	h.bus.Publish(&proto.Event{Kind: kind, Seq: seq, Scopes: scopes, Payload: payload, TS: ts})
}

func (h *Handler) publishGeneratedEvents(events []*proto.Event) {
	for _, evt := range events {
		h.bus.Publish(evt)
	}
}
