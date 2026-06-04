package handler

import (
	"encoding/json"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// --- Idempotency wrapper ---

func (h *Handler) dispatch(actor *User, name proto.CommandName, payload json.RawMessage, cid string) Reply {
	actorID := ""
	if actor != nil {
		actorID = actor.ID
	}
	commandHash := hashCommand(name, payload)
	if cid != "" {
		if cached, ok, conflict := checkProcessed(h.db, actorID, cid, commandHash); conflict {
			return Reply{Err: errDetail(proto.ErrConflict, "command id was already used with a different payload", false)}
		} else if ok {
			var r proto.AckResult
			_ = json.Unmarshal([]byte(cached), &r)
			return Reply{Result: &r}
		}
	}

	reply := h.route(actor, name, payload)

	if cid != "" && reply.Err == nil && reply.Result != nil {
		raw, _ := json.Marshal(reply.Result)
		// Record inside its own tiny tx; non-fatal if it fails.
		tx, err := h.db.Begin()
		if err == nil {
			_ = recordProcessed(tx, actorID, cid, commandHash, string(raw))
			_ = tx.Commit()
		}
	}
	return reply
}

func (h *Handler) route(actor *User, name proto.CommandName, payload json.RawMessage) Reply {
	switch name {
	case proto.CmdCreateThread:
		var p proto.CreateThreadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.createThread(actor, p)

	case proto.CmdAppendPost:
		var p proto.AppendPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.appendPost(actor, p)

	case proto.CmdRepostPost:
		var p proto.RepostPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.repostPost(actor, p)

	case proto.CmdPostBoardMail:
		var p proto.PostBoardMailPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.postBoardMail(actor, p)

	case proto.CmdAttachPost:
		var p proto.AttachPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.attachPost(actor, p)

	case proto.CmdEditPost:
		var p proto.EditPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.editPost(actor, p)

	case proto.CmdSetPostFlag:
		var p proto.SetPostFlagPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setPostFlag(actor, p)

	case proto.CmdRedactPost:
		var p proto.RedactPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.redactPost(actor, p)

	case proto.CmdRestorePost:
		var p proto.RestorePostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.restorePost(actor, p)

	case proto.CmdSetThreadTitle:
		var p proto.SetThreadTitlePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setThreadTitle(actor, p)

	case proto.CmdLockThread:
		var p proto.LockThreadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.lockThread(actor, p)

	case proto.CmdMoveThread:
		var p proto.MoveThreadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.moveThread(actor, p)

	case proto.CmdGrantRole:
		var p proto.GrantRolePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.grantRole(actor, p)

	case proto.CmdRevokeRole:
		var p proto.RevokeRolePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.revokeRole(actor, p)

	case proto.CmdPublishStatsSnapshot:
		var p proto.PublishStatsSnapshotPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.publishStatsSnapshot(actor, p)

	case proto.CmdPublishSystemNotice:
		var p proto.PublishSystemNoticePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.publishSystemNotice(actor, p)

	case proto.CmdSendChatLine:
		var p proto.SendChatLinePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.sendChatLine(actor, p)

	case proto.CmdSetPresence:
		var p proto.SetPresencePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setPresence(actor, p)

	case proto.CmdSanctionUser:
		var p proto.SanctionUserPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.sanctionUser(actor, p)

	case proto.CmdClearUserSanction:
		var p proto.ClearUserSanctionPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.clearUserSanction(actor, p)

	case proto.CmdSetContentFilter:
		var p proto.SetContentFilterPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setContentFilter(actor, p)

	case proto.CmdCreateBoard:
		var p proto.CreateBoardPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.createBoard(actor, p)

	case proto.CmdSetBoardSettings:
		var p proto.SetBoardSettingsPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setBoardSettings(actor, p)

	case proto.CmdSetBoardModerator:
		var p proto.SetBoardModeratorPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setBoardModerator(actor, p)

	case proto.CmdSetBoardMember:
		var p proto.SetBoardMemberPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setBoardMember(actor, p)

	case proto.CmdSetBoardMemberRequirements:
		var p proto.SetBoardMemberRequirementsPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setBoardMemberRequirements(actor, p)

	case proto.CmdSetRecommendedBoard:
		var p proto.SetRecommendedBoardPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setRecommendedBoard(actor, p)

	case proto.CmdApplyBoardMembership:
		var p proto.ApplyBoardMembershipPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.applyBoardMembership(actor, p)

	case proto.CmdReviewBoardMembership:
		var p proto.ReviewBoardMembershipPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.reviewBoardMembership(actor, p)

	case proto.CmdLeaveBoardMembership:
		var p proto.LeaveBoardMembershipPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.leaveBoardMembership(actor, p)

	case proto.CmdCuratePost:
		var p proto.CuratePostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.curatePost(actor, p)

	case proto.CmdCurateThread:
		var p proto.CurateThreadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.curateThread(actor, p)

	case proto.CmdRemoveDigestEntry:
		var p proto.RemoveDigestEntryPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.removeDigestEntry(actor, p)

	case proto.CmdUpdateDigestEntry:
		var p proto.UpdateDigestEntryPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.updateDigestEntry(actor, p)

	case proto.CmdSetDigestEntryBody:
		var p proto.SetDigestEntryBodyPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setDigestEntryBody(actor, p)

	case proto.CmdCreateDigestDirectory:
		var p proto.CreateDigestDirectoryPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.createDigestDirectory(actor, p)

	case proto.CmdMoveDigestPath:
		var p proto.MoveDigestPathPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.moveDigestPath(actor, p)

	case proto.CmdCopyDigestPath:
		var p proto.CopyDigestPathPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.copyDigestPath(actor, p)

	case proto.CmdDeleteDigestPath:
		var p proto.DeleteDigestPathPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.deleteDigestPath(actor, p)

	case proto.CmdSendDigestEntryMail:
		var p proto.SendDigestEntryMailPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.sendDigestEntryMail(actor, p)

	case proto.CmdMailPostAuthor:
		var p proto.MailPostAuthorPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.mailPostAuthor(actor, p)

	case proto.CmdSendMail:
		var p proto.SendMailPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.sendMail(actor, p)

	case proto.CmdSetMailGroup:
		var p proto.SetMailGroupPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setMailGroup(actor, p)

	case proto.CmdDeleteMailGroup:
		var p proto.DeleteMailGroupPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.deleteMailGroup(actor, p)

	case proto.CmdAttachMail:
		var p proto.AttachMailPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.attachMail(actor, p)

	case proto.CmdUpdateMail:
		var p proto.UpdateMailPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.updateMail(actor, p)

	case proto.CmdDeleteMail:
		var p proto.DeleteMailPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.deleteMail(actor, p)

	case proto.CmdSendDirectMessage:
		var p proto.SendDirectMessagePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.sendDirectMessage(actor, p)

	case proto.CmdSetDirectMessageSettings:
		var p proto.SetDirectMessageSettingsPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setDirectMessageSettings(actor, p)

	case proto.CmdMarkDirectMessageRead:
		var p proto.MarkDirectMessageReadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.markDirectMessageRead(actor, p)

	case proto.CmdDeleteDirectMessage:
		var p proto.DeleteDirectMessagePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.deleteDirectMessage(actor, p)

	case proto.CmdSetUserRelationship:
		var p proto.SetUserRelationshipPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setUserRelationship(actor, p)

	case proto.CmdSetLoginWatch:
		var p proto.SetLoginWatchPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setLoginWatch(actor, p)

	case proto.CmdBlessUser:
		var p proto.BlessUserPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.blessUser(actor, p)

	case proto.CmdSetBoardFavorite:
		var p proto.SetBoardFavoritePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setBoardFavorite(actor, p)

	case proto.CmdCreateFavoriteFolder:
		var p proto.CreateFavoriteFolderPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.createFavoriteFolder(actor, p)

	case proto.CmdUpdateFavoriteFolder:
		var p proto.UpdateFavoriteFolderPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.updateFavoriteFolder(actor, p)

	case proto.CmdDeleteFavoriteFolder:
		var p proto.DeleteFavoriteFolderPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.deleteFavoriteFolder(actor, p)

	case proto.CmdMoveBoardFavorite:
		var p proto.MoveBoardFavoritePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.moveBoardFavorite(actor, p)

	case proto.CmdImportFavoriteTree:
		var p proto.ImportFavoriteTreePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.importFavoriteTree(actor, p)

	case proto.CmdMarkBoardRead:
		var p proto.MarkBoardReadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.markBoardRead(actor, p)

	case proto.CmdRestoreBoardRead:
		var p proto.RestoreBoardReadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.restoreBoardRead(actor, p)

	case proto.CmdMarkFavoriteFolderRead:
		var p proto.MarkFavoriteFolderReadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.markFavoriteFolderRead(actor, p)

	case proto.CmdRestoreFavoriteFolderRead:
		var p proto.RestoreFavoriteFolderReadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.restoreFavoriteFolderRead(actor, p)

	case proto.CmdMarkThreadRead:
		var p proto.MarkThreadReadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.markThreadRead(actor, p)

	case proto.CmdRestoreThreadRead:
		var p proto.RestoreThreadReadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.restoreThreadRead(actor, p)

	case proto.CmdMarkPostRead:
		var p proto.MarkPostReadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.markPostRead(actor, p)

	case proto.CmdPurgePost:
		var p proto.PurgePostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.purgePost(actor, p)

	case proto.CmdReactPost:
		var p proto.ReactPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.reactPost(actor, p)

	case proto.CmdUnreactPost:
		var p proto.ReactPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.unreactPost(actor, p)

	case proto.CmdVotePoll:
		var p proto.VotePollPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.votePoll(actor, p)

	case proto.CmdPublishPollResult:
		var p proto.PublishPollResultPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.publishPollResult(actor, p)

	case proto.CmdSetThreadPref:
		var p proto.SetThreadPrefPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setThreadPref(actor, p)

	case proto.CmdFlagPost:
		var p proto.FlagPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.flagPost(actor, p)

	case proto.CmdResolveReview:
		var p proto.ResolveReviewPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.resolveReview(actor, p)

	default:
		return Reply{Err: errDetail(proto.ErrValidationFailed, fmt.Sprintf("unknown command: %s", name), false)}
	}
}
