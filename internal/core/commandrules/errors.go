package commandrules

import "github.com/juncoflockleader/budgie-bbs/internal/proto"

func newErrDetail(code, message string, retryable bool) *proto.ErrorDetail {
	return &proto.ErrorDetail{Code: code, Message: message, Retryable: retryable}
}

func internalErr(err error) *proto.ErrorDetail {
	return newErrDetail("internal_error", err.Error(), true)
}
