package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandrules"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/core/statsplan"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func (h *Handler) setBoardFavorite(actor *User, p proto.SetBoardFavoritePayload) Reply {
	p, msg := proto.NormalizeSetBoardFavoritePayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if p.Favorite {
		if errReply := h.requireFavoriteFolder(actor.ID, p.FolderID); errReply.Err != nil {
			return errReply
		}
	}
	if err := currentRuntime().SetBoardFavorite(h.db, actor.ID, p.Board, p.FolderID, p.Position, p.Favorite); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) setBoardZap(actor *User, p proto.SetBoardZapPayload) Reply {
	p, msg := proto.NormalizeSetBoardZapPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if p.Zapped {
		settings, err := currentRuntime().GetBoardSettings(h.db, p.Board)
		if err != nil {
			return internalErr(err)
		}
		if errDetail := commandrules.RequireBoardZapAllowed(p.Zapped, settings); errDetail != nil {
			return Reply{Err: errDetail}
		}
	}
	if err := currentRuntime().SetBoardZap(h.db, actor.ID, p.Board, p.Zapped); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) createFavoriteFolder(actor *User, p proto.CreateFavoriteFolderPayload) Reply {
	p, msg := proto.NormalizeCreateFavoriteFolderPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireFavoriteFolder(actor.ID, p.ParentID); errReply.Err != nil {
		return errReply
	}
	folderID := newID("favfld_")
	if err := currentRuntime().CreateFavoriteFolder(h.db, actor.ID, folderID, p.ParentID, p.Name, p.Position); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: folderID}}
}

func (h *Handler) updateFavoriteFolder(actor *User, p proto.UpdateFavoriteFolderPayload) Reply {
	p, msg := proto.NormalizeUpdateFavoriteFolderPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireFavoriteFolder(actor.ID, p.Folder); errReply.Err != nil {
		return errReply
	}
	if p.ParentID != nil {
		if *p.ParentID == p.Folder {
			return Reply{Err: errDetail(proto.ErrValidationFailed, "folder cannot be its own parent", false)}
		}
		if errReply := h.requireFavoriteFolder(actor.ID, *p.ParentID); errReply.Err != nil {
			return errReply
		}
		if contains, err := projections.FavoriteFolderContains(h.db, actor.ID, p.Folder, *p.ParentID); err == nil && contains {
			return Reply{Err: errDetail(proto.ErrValidationFailed, "folder cannot move under its descendant", false)}
		}
	}
	if err := currentRuntime().UpdateFavoriteFolder(h.db, actor.ID, p.Folder, p.Name, p.ParentID, p.Position); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Folder}}
}

func (h *Handler) deleteFavoriteFolder(actor *User, p proto.DeleteFavoriteFolderPayload) Reply {
	p, msg := proto.NormalizeDeleteFavoriteFolderPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireFavoriteFolder(actor.ID, p.Folder); errReply.Err != nil {
		return errReply
	}
	if err := currentRuntime().DeleteFavoriteFolder(h.db, actor.ID, p.Folder); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Folder}}
}

func (h *Handler) moveBoardFavorite(actor *User, p proto.MoveBoardFavoritePayload) Reply {
	p, msg := proto.NormalizeMoveBoardFavoritePayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if errReply := h.requireFavoriteFolder(actor.ID, p.FolderID); errReply.Err != nil {
		return errReply
	}
	if err := currentRuntime().MoveBoardFavorite(h.db, actor.ID, p.Board, p.FolderID, p.Position); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) importFavoriteTree(actor *User, p proto.ImportFavoriteTreePayload) Reply {
	p, msg := proto.NormalizeImportFavoriteTreePayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	replace := true
	if p.Replace != nil {
		replace = *p.Replace
	}
	tree := commandrules.FavoriteTreeFromImportPayload(p)
	if err := currentRuntime().ImportFavoriteTree(h.db, actor.ID, tree, replace); err != nil {
		return Reply{Err: errDetail(proto.ErrValidationFailed, err.Error(), false)}
	}
	return Reply{Result: &proto.AckResult{}}
}

func (h *Handler) setBoardSettings(actor *User, p proto.SetBoardSettingsPayload) Reply {
	p, msg := proto.NormalizeSetBoardSettingsPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if errDetail := commandrules.RequireBoardSettingsPermission(commandrules.ActorCanSetBoardSettings(h.db, actor, p.Board)); errDetail != nil {
		return Reply{Err: errDetail}
	}
	if err := currentRuntime().SetBoardSettings(h.db, p.Board, projections.BoardSettingsPatchFromPayload(p)); err != nil {
		return internalErr(err)
	}
	settingLines := proto.BoardSettingsAuditLines(p)
	if len(settingLines) > 0 {
		lines := []string{
			"Action: board settings changed",
			"Board: " + p.Board,
			"Actor: " + actor.Name,
		}
		lines = append(lines, settingLines...)
		if err := h.ensureSyssecuritySystemPost(actor, "Board settings changed: "+p.Board, lines, p.Board); err != nil {
			return internalErr(err)
		}
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) setRecommendedBoard(actor *User, p proto.SetRecommendedBoardPayload) Reply {
	if errDetail := commandrules.RequireAdminRole(actor.IsAdmin()); errDetail != nil {
		return Reply{Err: errDetail}
	}
	p, msg := proto.NormalizeSetRecommendedBoardPayload(p)
	if msg != "" && p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if p.Recommended {
		if ok, reason, err := projections.BoardCanBePubliclyRecommended(h.db, p.Board); err != nil {
			return internalErr(err)
		} else if !ok {
			return Reply{Err: errDetail(proto.ErrValidationFailed, reason, false)}
		}
	}
	if err := currentRuntime().SetRecommendedBoard(h.db, p.Board, p.Note, actor.ID, p.Position, p.Recommended); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) setBoardModerator(actor *User, p proto.SetBoardModeratorPayload) Reply {
	if errDetail := commandrules.RequireAdminRole(actor.IsAdmin()); errDetail != nil {
		return Reply{Err: errDetail}
	}
	p, msg := proto.NormalizeSetBoardModeratorPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	user, ruleErr := commandrules.ResolveUserRef(h.db, p.User)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	if err := currentRuntime().SetBoardModerator(h.db, p.Board, user.ID, actor.ID, p.Moderator, p.Position); err != nil {
		return internalErr(err)
	}
	action := "moderator removed"
	if p.Moderator {
		action = "moderator appointed"
	}
	if err := h.ensureSyssecuritySystemPost(actor, "Board "+action+": "+p.Board, []string{
		"Action: board " + action,
		"Board: " + p.Board,
		"User: " + user.Name,
		"Actor: " + actor.Name,
	}, p.Board); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) setBoardMember(actor *User, p proto.SetBoardMemberPayload) Reply {
	p, msg := proto.NormalizeSetBoardMemberPayload(p)
	if msg != "" && (p.Board == "" || p.User == "") {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	canModerateBoard := commandrules.ActorCanModerateBoard(h.db, actor, p.Board)
	canManageMembers := commandrules.ActorCanManageBoardMembers(h.db, actor, p.Board)
	if failure := proto.CheckBoardMemberManagerPermission(canModerateBoard, canManageMembers); failure != nil {
		return Reply{Err: errDetail(failure.Code, failure.Message, false)}
	}
	user, ruleErr := commandrules.ResolveUserRef(h.db, p.User)
	if ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	userID := user.ID
	if !canModerateBoard {
		if failure := proto.CheckSetBoardMemberPermissionChange(p, canModerateBoard); failure != nil {
			return Reply{Err: errDetail(failure.Code, failure.Message, false)}
		}
		targetIsModerator := commandrules.BoardPermissionAllowed(projections.BoardModeratorExists(h.db, p.Board, userID))
		if failure := proto.CheckSetBoardMemberTargetPermission(canModerateBoard, targetIsModerator, false); failure != nil {
			return Reply{Err: errDetail(failure.Code, failure.Message, false)}
		}
		privilegedMember, err := projections.BoardMemberHasDelegatedPermissions(h.db, p.Board, userID)
		if err != nil {
			return internalErr(err)
		}
		if failure := proto.CheckSetBoardMemberTargetPermission(canModerateBoard, false, privilegedMember); failure != nil {
			return Reply{Err: errDetail(failure.Code, failure.Message, false)}
		}
	}
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if err := currentRuntime().SetBoardMember(h.db, p.Board, userID, p.Member, projections.BoardMemberPatchFromPayload(p)); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) setBoardMemberRequirements(actor *User, p proto.SetBoardMemberRequirementsPayload) Reply {
	p, msg := proto.NormalizeSetBoardMemberRequirementsPayload(p)
	if msg != "" && p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if errDetail := commandrules.RequireBoardSettingsPermission(commandrules.ActorCanSetBoardSettings(h.db, actor, p.Board)); errDetail != nil {
		return Reply{Err: errDetail}
	}
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if err := currentRuntime().SetBoardMemberRequirements(h.db, p.Board, projections.BoardMemberRequirementsPatchFromPayload(p)); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) applyBoardMembership(actor *User, p proto.ApplyBoardMembershipPayload) Reply {
	p, msg := proto.NormalizeApplyBoardMembershipPayload(p)
	if msg != "" && p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	isMember := commandrules.BoardPermissionAllowed(projections.BoardMemberExists(h.db, p.Board, actor.ID))
	if errDetail := commandrules.RequireBoardMembershipApplicantNotMember(isMember); errDetail != nil {
		return Reply{Err: errDetail}
	}
	status, err := projections.LatestBoardMemberApplicationStatus(h.db, p.Board, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if errDetail := commandrules.RequireBoardMembershipApplicationCanStart(status); errDetail != nil {
		return Reply{Err: errDetail}
	}
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	requirements, err := currentRuntime().GetBoardMemberRequirements(h.db, p.Board)
	if err != nil {
		return internalErr(err)
	}
	if ruleErr := commandrules.RequireBoardMembershipAdmission(h.db, counterStore(), p.Board, actor.ID, requirements); ruleErr != nil {
		return Reply{Err: ruleErr}
	}
	appID := newID("bmap_")
	if err := currentRuntime().InsertBoardMemberApplication(h.db, appID, p.Board, actor.ID, p.Note); err != nil {
		return internalErr(err)
	}
	if requirements != nil && requirements.ApprovalMode == "auto" {
		if err := currentRuntime().ReviewBoardMemberApplication(h.db, appID, actor.ID, "approved", "", proto.BoardMembershipAutoApprovalNote); err != nil {
			return internalErr(err)
		}
		if err := h.ensureBoardRegistrationSystemPost(actor, appID, "approved", p.Board, actor.ID); err != nil {
			return internalErr(err)
		}
	}
	return Reply{Result: &proto.AckResult{ID: appID}}
}

func (h *Handler) reviewBoardMembership(actor *User, p proto.ReviewBoardMembershipPayload) Reply {
	p, msg := proto.NormalizeReviewBoardMembershipTargetPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	app, err := currentRuntime().GetBoardMemberApplication(h.db, p.Application)
	if err != nil {
		return internalErr(err)
	}
	if app == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "membership application not found", false)}
	}
	if errDetail := commandrules.RequireBoardMembershipApplicationPending(app.Status); errDetail != nil {
		return Reply{Err: errDetail}
	}
	canModerateBoard := commandrules.ActorCanModerateBoard(h.db, actor, app.BoardID)
	canManageMembers := commandrules.ActorCanManageBoardMembers(h.db, actor, app.BoardID)
	if failure := proto.CheckReviewBoardMembershipPermission(canModerateBoard, canManageMembers, actor.ID, app.UserID, p.Status); failure != nil {
		return Reply{Err: errDetail(failure.Code, failure.Message, false)}
	}
	p, msg = proto.NormalizeReviewBoardMembershipContent(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if p.Status == "approved" {
		requirements, err := currentRuntime().GetBoardMemberRequirements(h.db, app.BoardID)
		if err != nil {
			return internalErr(err)
		}
		if ruleErr := commandrules.RequireBoardMembershipAdmission(h.db, counterStore(), app.BoardID, app.UserID, requirements); ruleErr != nil {
			return Reply{Err: ruleErr}
		}
	}
	if err := currentRuntime().ReviewBoardMemberApplication(h.db, p.Application, actor.ID, p.Status, p.Title, p.Note); err != nil {
		return internalErr(err)
	}
	if err := h.ensureBoardRegistrationSystemPost(actor, p.Application, p.Status, app.BoardID, app.UserID); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Application}}
}

func (h *Handler) leaveBoardMembership(actor *User, p proto.LeaveBoardMembershipPayload) Reply {
	p, msg := proto.NormalizeLeaveBoardMembershipPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if !commandrules.BoardPermissionAllowed(projections.BoardMemberExists(h.db, p.Board, actor.ID)) {
		return Reply{Err: errDetail(proto.ErrNotFound, "board membership not found", false)}
	}
	if err := currentRuntime().SetBoardMember(h.db, p.Board, actor.ID, false, BoardMemberPatch{}); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) curatePost(actor *User, p proto.CuratePostPayload) Reply {
	p, msg := proto.NormalizeCuratePostTargetPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	post, err := currentRuntime().GetPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if errDetail := commandrules.RequirePostNotRedacted(post.Redacted, "cannot curate a redacted post"); errDetail != nil {
		return Reply{Err: errDetail}
	}
	thread, err := currentRuntime().GetThread(h.db, post.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	kind, title, path, note, msg := proto.NormalizeDigestCurationFields(p.Kind, p.Title, p.Path, p.Note)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if !commandrules.ActorCanCurateBoardKind(h.db, actor, thread.Board, kind) {
		return Reply{Err: errDetail(proto.ErrForbidden, proto.DigestCurationPermissionMessage(kind), false)}
	}
	if title == "" {
		title = fmt.Sprintf("%s #%d", thread.Title, post.CreatedSeq)
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	entryID, err := currentRuntime().UpsertDigestEntryTx(
		tx,
		newID("dig_"),
		thread.Board,
		"post",
		post.ID,
		kind,
		title,
		path,
		note,
		actor.ID,
		ts,
	)
	if err != nil {
		return internalErr(err)
	}
	eventPayload := &proto.DigestEntryUpsertedPayload{
		ID:         entryID,
		Board:      thread.Board,
		TargetKind: "post",
		TargetID:   post.ID,
		Kind:       kind,
		Title:      title,
		Path:       path,
		Note:       note,
		CreatedBy:  actor.ID,
		TS:         ts,
	}
	scopes := proto.DigestEventScopes(thread.Board)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtDigestEntryUpserted, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtDigestEntryUpserted, seq, scopes, eventPayload, ts)
	if kind == "announcement" {
		if err := h.ensureAnnouncementSystemPost(actor, entryID); err != nil {
			return internalErr(err)
		}
	}
	if kind == "recommended" {
		if err := h.ensureRecommendSystemPost(actor, entryID); err != nil {
			return internalErr(err)
		}
	}
	return Reply{Result: &proto.AckResult{ID: entryID, Seq: seq}}
}

func (h *Handler) curateThread(actor *User, p proto.CurateThreadPayload) Reply {
	p, msg := proto.NormalizeCurateThreadTargetPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	thread, err := currentRuntime().GetThread(h.db, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	kind, title, path, note, msg := proto.NormalizeDigestCurationFields(p.Kind, p.Title, p.Path, p.Note)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if !commandrules.ActorCanCurateBoardKind(h.db, actor, thread.Board, kind) {
		return Reply{Err: errDetail(proto.ErrForbidden, proto.DigestCurationPermissionMessage(kind), false)}
	}
	if title == "" {
		title = thread.Title
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	entryID, err := currentRuntime().UpsertDigestEntryTx(
		tx,
		newID("dig_"),
		thread.Board,
		"thread",
		thread.ID,
		kind,
		title,
		path,
		note,
		actor.ID,
		ts,
	)
	if err != nil {
		return internalErr(err)
	}
	eventPayload := &proto.DigestEntryUpsertedPayload{
		ID:         entryID,
		Board:      thread.Board,
		TargetKind: "thread",
		TargetID:   thread.ID,
		Kind:       kind,
		Title:      title,
		Path:       path,
		Note:       note,
		CreatedBy:  actor.ID,
		TS:         ts,
	}
	scopes := proto.DigestEventScopes(thread.Board)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtDigestEntryUpserted, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtDigestEntryUpserted, seq, scopes, eventPayload, ts)
	if kind == "announcement" {
		if err := h.ensureAnnouncementSystemPost(actor, entryID); err != nil {
			return internalErr(err)
		}
	}
	if kind == "recommended" {
		if err := h.ensureRecommendSystemPost(actor, entryID); err != nil {
			return internalErr(err)
		}
	}
	return Reply{Result: &proto.AckResult{ID: entryID, Seq: seq}}
}

func (h *Handler) removeDigestEntry(actor *User, p proto.RemoveDigestEntryPayload) Reply {
	p, msg := proto.NormalizeRemoveDigestEntryPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	entry, errReply := h.digestEntryForCuration(actor, p.Entry)
	if errReply.Err != nil {
		return errReply
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	eventPayload := &proto.DigestEntryRemovedPayload{
		ID:    entry.ID,
		Board: entry.BoardID,
		Kind:  entry.Kind,
		By:    actor.ID,
		TS:    ts,
	}
	if err := currentRuntime().RemoveDigestEntryFinalTx(tx, p.Entry, eventPayload.Board, eventPayload.Kind, eventPayload.By, eventPayload.TS); err != nil {
		return internalErr(err)
	}
	scopes := proto.DigestEventScopes(entry.BoardID)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtDigestEntryRemoved, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtDigestEntryRemoved, seq, scopes, eventPayload, ts)
	return Reply{Result: &proto.AckResult{ID: entry.ID, Seq: seq}}
}

func (h *Handler) updateDigestEntry(actor *User, p proto.UpdateDigestEntryPayload) Reply {
	p, msg := proto.NormalizeUpdateDigestEntryTargetPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	entry, errReply := h.digestEntryForCuration(actor, p.Entry)
	if errReply.Err != nil {
		return errReply
	}
	p, msg = proto.NormalizeUpdateDigestEntryPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	title := entry.Title
	if p.Title != nil {
		title = *p.Title
	}
	path := entry.Path
	if p.Path != nil {
		path = *p.Path
	}
	note := entry.Note
	if p.Note != nil {
		note = *p.Note
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	if errReply := replyFromCommandRule(commandrules.RequireDigestEntryPathAvailable(tx, entry, path)); errReply.Err != nil {
		return errReply
	}
	if err := currentRuntime().UpdateDigestEntryTx(tx, entry.ID, title, path, note, ts); err != nil {
		return internalErr(err)
	}
	eventPayload := &proto.DigestEntryUpdatedPayload{
		ID:         entry.ID,
		Board:      entry.BoardID,
		TargetKind: entry.TargetKind,
		TargetID:   entry.TargetID,
		Kind:       entry.Kind,
		Title:      title,
		Path:       path,
		Note:       note,
		By:         actor.ID,
		TS:         ts,
	}
	scopes := proto.DigestEventScopes(entry.BoardID)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtDigestEntryUpdated, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtDigestEntryUpdated, seq, scopes, eventPayload, ts)
	return Reply{Result: &proto.AckResult{ID: entry.ID, Seq: seq}}
}

func (h *Handler) setDigestEntryBody(actor *User, p proto.SetDigestEntryBodyPayload) Reply {
	p, msg := proto.NormalizeSetDigestEntryBodyPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	entry, errReply := h.digestEntryForCuration(actor, p.Entry)
	if errReply.Err != nil {
		return errReply
	}
	body := p.Body
	edited := !p.Reset
	if p.Reset {
		body = ""
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	if err := currentRuntime().SetDigestEntryBodyTx(tx, entry.ID, body, edited, ts); err != nil {
		return internalErr(err)
	}
	eventPayload := &proto.DigestEntryBodySetPayload{
		ID:     entry.ID,
		Board:  entry.BoardID,
		Kind:   entry.Kind,
		Body:   body,
		Edited: edited,
		By:     actor.ID,
		TS:     ts,
	}
	scopes := proto.DigestEventScopes(entry.BoardID)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtDigestEntryBodySet, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtDigestEntryBodySet, seq, scopes, eventPayload, ts)
	return Reply{Result: &proto.AckResult{ID: entry.ID, Seq: seq}}
}

func (h *Handler) createDigestDirectory(actor *User, p proto.CreateDigestDirectoryPayload) Reply {
	boardID, kind, path, _, errReply := h.prepareDigestPathMutation(actor, p.Board, p.Kind, p.Path, "")
	if errReply.Err != nil {
		return errReply
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	directoryID, err := currentRuntime().UpsertDigestDirectoryTx(tx, newID("dir_"), boardID, kind, path, actor.ID, ts)
	if err != nil {
		return internalErr(err)
	}
	eventPayload := &proto.DigestDirectorySetPayload{
		ID:        directoryID,
		Board:     boardID,
		Kind:      kind,
		Path:      path,
		CreatedBy: actor.ID,
		TS:        ts,
	}
	scopes := proto.DigestEventScopes(boardID)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtDigestDirectorySet, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtDigestDirectorySet, seq, scopes, eventPayload, ts)
	return Reply{Result: &proto.AckResult{ID: directoryID, Seq: seq}}
}

func (h *Handler) moveDigestPath(actor *User, p proto.MoveDigestPathPayload) Reply {
	boardID, kind, fromPath, toPath, errReply := h.prepareDigestPathMutation(actor, p.Board, p.Kind, p.FromPath, p.ToPath)
	if errReply.Err != nil {
		return errReply
	}
	if fromPath == toPath {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "destination path must differ from source path", false)}
	}
	if toPath != "" && (toPath == fromPath || strings.HasPrefix(toPath, fromPath+"/")) {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "cannot move an archive path into itself", false)}
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	eventID := newID("evt_")
	count, err := currentRuntime().MoveDigestPathFinalTx(tx, eventID, boardID, kind, fromPath, toPath, actor.ID, ts)
	if err != nil {
		if errors.Is(err, projections.ErrDigestPathConflict) {
			return Reply{Err: errDetail(proto.ErrConflict, "digest path move would overwrite an existing entry", false)}
		}
		return internalErr(err)
	}
	eventPayload := &proto.DigestPathMovedPayload{
		Board:    boardID,
		Kind:     kind,
		FromPath: fromPath,
		ToPath:   toPath,
		Count:    count,
		By:       actor.ID,
		TS:       ts,
	}
	scopes := proto.DigestEventScopes(boardID)
	seq, err := appendEvent(tx, eventID, proto.EvtDigestPathMoved, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtDigestPathMoved, seq, scopes, eventPayload, ts)
	return Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%s:%s:%d", boardID, kind, count), Seq: seq}}
}

func (h *Handler) copyDigestPath(actor *User, p proto.CopyDigestPathPayload) Reply {
	boardID, kind, fromPath, toPath, errReply := h.prepareDigestPathMutation(actor, p.Board, p.Kind, p.FromPath, p.ToPath)
	if errReply.Err != nil {
		return errReply
	}
	if fromPath == toPath {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "destination path must differ from source path", false)}
	}
	count, err := currentRuntime().CountDigestPathEntries(h.db, boardID, kind, fromPath)
	if err != nil {
		return internalErr(err)
	}
	dirCount, err := currentRuntime().CountDigestPathDirectories(h.db, boardID, kind, fromPath)
	if err != nil {
		return internalErr(err)
	}
	ids := make([]string, count)
	for i := range ids {
		ids[i] = newID("dig_")
	}
	dirIDs := make([]string, dirCount)
	for i := range dirIDs {
		dirIDs[i] = newID("dir_")
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	count, err = currentRuntime().CopyDigestPathTx(tx, boardID, kind, fromPath, toPath, actor.ID, ids, dirIDs, ts)
	if err != nil {
		if errors.Is(err, projections.ErrDigestPathConflict) {
			return Reply{Err: errDetail(proto.ErrConflict, "digest path copy would overwrite an existing entry", false)}
		}
		return internalErr(err)
	}
	eventPayload := &proto.DigestPathCopiedPayload{
		Board:        boardID,
		Kind:         kind,
		FromPath:     fromPath,
		ToPath:       toPath,
		EntryIDs:     ids,
		DirectoryIDs: dirIDs,
		Count:        count,
		CreatedBy:    actor.ID,
		TS:           ts,
	}
	scopes := proto.DigestEventScopes(boardID)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtDigestPathCopied, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtDigestPathCopied, seq, scopes, eventPayload, ts)
	return Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%s:%s:%d", boardID, kind, count), Seq: seq}}
}

func (h *Handler) deleteDigestPath(actor *User, p proto.DeleteDigestPathPayload) Reply {
	boardID, kind, path, _, errReply := h.prepareDigestPathMutation(actor, p.Board, p.Kind, p.Path, "")
	if errReply.Err != nil {
		return errReply
	}
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	eventID := newID("evt_")
	count, err := currentRuntime().DeleteDigestPathFinalTx(tx, eventID, boardID, kind, path, actor.ID, ts)
	if err != nil {
		return internalErr(err)
	}
	eventPayload := &proto.DigestPathDeletedPayload{
		Board: boardID,
		Kind:  kind,
		Path:  path,
		Count: count,
		By:    actor.ID,
		TS:    ts,
	}
	scopes := proto.DigestEventScopes(boardID)
	seq, err := appendEvent(tx, eventID, proto.EvtDigestPathDeleted, scopes, eventPayload)
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtDigestPathDeleted, seq, scopes, eventPayload, ts)
	return Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%s:%s:%d", boardID, kind, count), Seq: seq}}
}

func (h *Handler) markBoardRead(actor *User, p proto.MarkBoardReadPayload) Reply {
	p, msg := proto.NormalizeMarkBoardReadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return h.applyReadMarker(p.Board, func() *proto.ErrorDetail {
		return commandrules.RequireBoard(h.db, p.Board)
	}, func() error {
		return currentRuntime().MarkBoardRead(h.db, actor.ID, p.Board)
	})
}

func (h *Handler) restoreBoardRead(actor *User, p proto.RestoreBoardReadPayload) Reply {
	p, msg := proto.NormalizeRestoreBoardReadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return h.applyReadMarker(p.Board, func() *proto.ErrorDetail {
		return commandrules.RequireBoard(h.db, p.Board)
	}, func() error {
		return currentRuntime().RestoreBoardRead(h.db, actor.ID, p.Board)
	})
}

func (h *Handler) markFavoriteFolderRead(actor *User, p proto.MarkFavoriteFolderReadPayload) Reply {
	return h.applyReadMarker(p.Folder, func() *proto.ErrorDetail {
		return commandrules.RequireFavoriteFolder(h.db, actor.ID, p.Folder)
	}, func() error {
		return currentRuntime().MarkFavoriteFolderRead(h.db, actor.ID, p.Folder)
	})
}

func (h *Handler) restoreFavoriteFolderRead(actor *User, p proto.RestoreFavoriteFolderReadPayload) Reply {
	return h.applyReadMarker(p.Folder, func() *proto.ErrorDetail {
		return commandrules.RequireFavoriteFolder(h.db, actor.ID, p.Folder)
	}, func() error {
		return currentRuntime().RestoreFavoriteFolderRead(h.db, actor.ID, p.Folder)
	})
}

func (h *Handler) markThreadRead(actor *User, p proto.MarkThreadReadPayload) Reply {
	p, msg := proto.NormalizeMarkThreadReadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return h.applyReadMarker(p.Thread, func() *proto.ErrorDetail {
		return commandrules.RequireThread(h.db, p.Thread)
	}, func() error {
		return currentRuntime().MarkThreadRead(h.db, actor.ID, p.Thread)
	})
}

func (h *Handler) restoreThreadRead(actor *User, p proto.RestoreThreadReadPayload) Reply {
	p, msg := proto.NormalizeRestoreThreadReadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return h.applyReadMarker(p.Thread, func() *proto.ErrorDetail {
		return commandrules.RequireThread(h.db, p.Thread)
	}, func() error {
		return currentRuntime().RestoreThreadRead(h.db, actor.ID, p.Thread)
	})
}

func (h *Handler) markPostRead(actor *User, p proto.MarkPostReadPayload) Reply {
	p, msg := proto.NormalizeMarkPostReadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return h.applyReadMarker(p.Post, func() *proto.ErrorDetail {
		return commandrules.RequirePost(h.db, p.Post)
	}, func() error {
		return currentRuntime().MarkPostRead(h.db, actor.ID, p.Post)
	})
}

func (h *Handler) applyReadMarker(targetID string, require func() *proto.ErrorDetail, update func() error) Reply {
	return replyFromCommandResult(commandrules.ApplyReadMarker(targetID, require, update))
}

func (h *Handler) digestEntryForCuration(actor *User, entryID string) (*commandrules.DigestEntryForCommand, Reply) {
	entry, errDetail := commandrules.DigestEntryForCuration(h.db, actor, entryID)
	if errDetail != nil {
		return nil, Reply{Err: errDetail}
	}
	return entry, Reply{}
}

func (h *Handler) prepareDigestPathMutation(actor *User, boardID, kind, fromPath, toPath string) (string, string, string, string, Reply) {
	prepared, errDetail := commandrules.PrepareDigestPathMutation(h.db, actor, boardID, kind, fromPath, toPath)
	if errDetail != nil {
		return "", "", "", "", Reply{Err: errDetail}
	}
	return prepared.BoardID, prepared.Kind, prepared.FromPath, prepared.ToPath, Reply{}
}

func (h *Handler) publishSystemNotice(actor *User, p proto.PublishSystemNoticePayload) Reply {
	if errDetail := commandrules.RequireAdminRole(actor.IsAdmin()); errDetail != nil {
		return Reply{Err: errDetail}
	}
	p, board, msg := proto.NormalizePublishSystemNoticePayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	threadID, seq, err := h.appendSystemNoticePost(actor, board, p.Title, p.Body, p.Source, nowMS())
	if err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: threadID, Seq: seq}}
}

func (h *Handler) appendSystemNoticePost(actor *User, board proto.SystemNoticeBoard, title, noticeBody, source string, ts int64) (string, int64, error) {
	threadID := newID("notice_thr_")
	postID := newID("notice_pst_")
	body := proto.FormatSystemNoticeBody(board, title, noticeBody, source, actor.Name)

	tx, err := h.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback() //nolint

	events, err := h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:     board.ID,
		BoardName:   board.Name,
		Description: board.Description,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
	}, ts)
	if err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}

	h.publishGeneratedEvents(events)
	return threadID, events[len(events)-1].Seq, nil
}

func (h *Handler) ensureBlessingSystemPost(actor, target *User, blessingID, message string, ts int64) error {
	threadID, postID := proto.BlessingSystemPostIDs(blessingID)
	exists, err := projections.ThreadExists(h.db, threadID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	title := proto.BlessingSystemTitle(actor.Name, target.Name)
	body := proto.FormatBlessingSystemBody(actor.Name, target.Name, message)

	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	events, err := h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:     proto.BlessingSystemBoardID,
		BoardName:   proto.BlessingSystemBoardName,
		Description: proto.BlessingSystemBoardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
	}, ts)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	h.publishGeneratedEvents(events)
	return nil
}

func (h *Handler) publishStatsSnapshot(actor *User, p proto.PublishStatsSnapshotPayload) Reply {
	if errDetail := commandrules.RequireAdminRole(actor.IsAdmin()); errDetail != nil {
		return Reply{Err: errDetail}
	}
	ts := nowMS()
	dateLabel, _, msg := proto.NormalizeStatsSnapshotDate(p.Date, ts)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	plan, err := statsplan.PlanStatsSnapshotSystemPosts(h.db, dateLabel, ts)
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().RecordCommunityStatSnapshot(h.db, ts); err != nil {
		return internalErr(err)
	}

	seq := plan.MainExistingSeq
	for _, post := range plan.Posts {
		_, postSeq, err := h.ensureStatsSystemPost(actor, post.ThreadID, post.PostID, post.Title, post.Body, ts)
		if err != nil {
			return internalErr(err)
		}
		if post.ThreadID == plan.MainThreadID {
			seq = postSeq
		}
	}
	return Reply{Result: &proto.AckResult{ID: plan.MainThreadID, Seq: seq}}
}

func (h *Handler) ensureStatsSystemPost(actor *User, threadID, postID, title, body string, ts int64) (string, int64, error) {
	tx, err := h.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback() //nolint

	events, err := h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:     statsplan.SystemBoardID,
		BoardName:   statsplan.SystemBoardName,
		Description: statsplan.SystemBoardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
	}, ts)
	if err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}

	h.publishGeneratedEvents(events)
	return threadID, events[len(events)-1].Seq, nil
}

func (h *Handler) ensureAnnouncementSystemPost(actor *User, entryID string) error {
	mirror, _ := projections.DigestMirrorSystemBoardForKind("announcement")
	return h.ensureDigestMirrorSystemPost(actor, entryID, mirror)
}

func (h *Handler) ensureRecommendSystemPost(actor *User, entryID string) error {
	mirror, _ := projections.DigestMirrorSystemBoardForKind("recommended")
	return h.ensureDigestMirrorSystemPost(actor, entryID, mirror)
}

func (h *Handler) ensureDigestMirrorSystemPost(actor *User, entryID string, mirror projections.DigestMirrorSystemBoard) error {
	export, err := currentRuntime().GetDigestExport(h.db, entryID)
	if err != nil || export == nil {
		return err
	}
	if export.Entry.Kind != mirror.Kind || export.Entry.BoardID == mirror.BoardID {
		return nil
	}
	emit, err := currentRuntime().BoardAllowsPublicSystemPost(h.db, export.Entry.BoardID)
	if err != nil {
		return err
	}
	if !emit {
		return nil
	}

	threadID := mirror.ThreadID + entryID
	postID := mirror.PostID + entryID
	exists, err := projections.ThreadExists(h.db, threadID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	title := strings.TrimSpace(export.Entry.Title)
	if title == "" {
		title = mirror.Default
	}
	body := projections.FormatDigestExportText(export)
	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	events, err := h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:     mirror.BoardID,
		BoardName:   mirror.Name,
		Description: mirror.Description,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
	}, ts)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	h.publishGeneratedEvents(events)
	return nil
}

func (h *Handler) ensureBoardRegistrationSystemPost(actor *User, applicationID, status, boardID, userID string) error {
	boardIDOut, boardDescription, threadID, postID, ok := proto.BoardRegistrationSystemPlan(status, applicationID)
	if !ok {
		return nil
	}
	emit, err := currentRuntime().BoardAllowsPublicSystemPost(h.db, boardID)
	if err != nil {
		return err
	}
	if !emit {
		return nil
	}

	exists, err := projections.ThreadExists(h.db, threadID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	sourceBoardName, found, err := projections.BoardName(h.db, boardID)
	if err != nil {
		return err
	}
	if !found {
		return sql.ErrNoRows
	}
	applicantName, err := projections.UserName(h.db, userID)
	if err != nil {
		return err
	}

	ts := nowMS()
	title, body := proto.BoardRegistrationSystemContent(status, applicationID, sourceBoardName, boardID, applicantName, actor.Name)

	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	events, err := h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:     boardIDOut,
		BoardName:   boardIDOut,
		Description: boardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
	}, ts)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	h.publishGeneratedEvents(events)
	return nil
}

func (h *Handler) ensureSyssecuritySystemPost(actor *User, title string, lines []string, sourceBoardID string) error {
	emit, err := currentRuntime().BoardAllowsPublicSystemPost(h.db, sourceBoardID)
	if err != nil {
		return err
	}
	if !emit {
		return nil
	}

	title = proto.NormalizeSyssecuritySystemTitle(title)
	body := proto.FormatSyssecuritySystemBody(title, lines)

	ts := nowMS()
	threadID := newID("syssecurity_thr_")
	postID := newID("syssecurity_pst_")
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	events, err := h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:     proto.SyssecuritySystemBoardID,
		BoardName:   proto.SyssecuritySystemBoardID,
		Description: proto.SyssecuritySystemBoardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
	}, ts)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	h.publishGeneratedEvents(events)
	return nil
}

func (h *Handler) requireFavoriteFolder(userID, folderID string) Reply {
	return replyFromCommandRule(commandrules.RequireFavoriteFolder(h.db, userID, folderID))
}

func (h *Handler) requireBoard(boardID string) Reply {
	return replyFromCommandRule(commandrules.RequireBoard(h.db, boardID))
}

func (h *Handler) requirePost(postID string) Reply {
	return replyFromCommandRule(commandrules.RequirePost(h.db, postID))
}

func (h *Handler) requireThread(threadID string) Reply {
	return replyFromCommandRule(commandrules.RequireThread(h.db, threadID))
}

func replyFromCommandRule(errDetail *proto.ErrorDetail) Reply {
	if errDetail != nil {
		return Reply{Err: errDetail}
	}
	return Reply{}
}
