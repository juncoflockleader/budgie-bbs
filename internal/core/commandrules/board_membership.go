package commandrules

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type ReactionCounter interface {
	ReactionCount(postID string) (int, error)
}

func RequireBoardMembershipApplicantNotMember(isMember bool) *proto.ErrorDetail {
	if isMember {
		return newErrDetail(proto.ErrConflict, "already a board member", false)
	}
	return nil
}

func RequireBoardMembershipApplicationCanStart(latestStatus string) *proto.ErrorDetail {
	switch latestStatus {
	case "pending":
		return newErrDetail(proto.ErrConflict, "membership application already pending", false)
	case "blacklisted":
		return newErrDetail(proto.ErrForbidden, "membership application is blocked", false)
	default:
		return nil
	}
}

func RequireBoardMembershipApplicationPending(status string) *proto.ErrorDetail {
	if status != "pending" {
		return newErrDetail(proto.ErrConflict, "membership application is already reviewed", false)
	}
	return nil
}

func RequireBoardMembershipAdmission(db *sql.DB, store ReactionCounter, boardID, userID string, requirements *projections.BoardMemberRequirements) *proto.ErrorDetail {
	if requirements == nil {
		return nil
	}
	stats, err := projections.BoardMembershipAdmissionStatsForUser(db, boardID, userID, requirements)
	if err != nil {
		return internalErr(err)
	}
	if requirements.MinScore > 0 {
		score, err := projections.UserReactionScore(db, store, userID)
		if err != nil {
			return internalErr(err)
		}
		stats.ReactionScore = score
	}
	if requirements.MinBoardMarkCount > 0 {
		boardMarks, err := projections.UserBoardMarkCount(db, store, boardID, userID)
		if err != nil {
			return internalErr(err)
		}
		stats.BoardMarkCount = boardMarks
	}
	if failure := projections.CheckBoardMembershipAdmission(requirements, stats); failure != nil {
		return newErrDetail(failure.Code, failure.Message, false)
	}
	return nil
}
