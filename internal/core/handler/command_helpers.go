package handler

import (
	"strconv"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func contentType(ct string) string {
	if ct == "ansi-art" {
		return "ansi-art"
	}
	return "markup"
}

func errDetail(code, msg string, retryable bool) *proto.ErrorDetail {
	return &proto.ErrorDetail{Code: code, Message: msg, Retryable: retryable}
}

func badPayload() Reply {
	return Reply{Err: errDetail(proto.ErrValidationFailed, "invalid payload", false)}
}

func internalErr(err error) Reply {
	return Reply{Err: errDetail("internal_error", err.Error(), true)}
}

// requireMinTrustForPoll blocks poll creation for actors below the requested
// trust level. Mod/admin actors bypass this gate.
func (h *Handler) requireMinTrustForPoll(actor *User, minLevel int, action string) Reply {
	if actor.IsMod() {
		return Reply{}
	}
	trustLevel, err := userTrustLevel(h.db, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if trustLevel < minLevel {
		return Reply{Err: errDetail(proto.ErrForbidden, action+" with poll requires trust level "+strconv.Itoa(minLevel), false)}
	}
	return Reply{}
}

// isValidSlug returns true if s is a non-empty lowercase alphanumeric / hyphen / underscore
// string of at most 64 characters (suitable as a board ID).
func isValidSlug(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
