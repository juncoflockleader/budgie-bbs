package commandrules

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type ReactionCounter interface {
	ReactionCount(postID string) (int, error)
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
