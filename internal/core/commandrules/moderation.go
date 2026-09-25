package commandrules

import "github.com/juncoflockleader/budgie-bbs/internal/proto"

func RequireOpenModerationReview(found bool, status string) *proto.ErrorDetail {
	if !found || status != "open" {
		return newErrDetail(proto.ErrNotFound, "review not found", false)
	}
	return nil
}
