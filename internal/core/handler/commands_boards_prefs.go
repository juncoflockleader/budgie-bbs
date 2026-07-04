package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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
		if settings != nil && !settings.ZapAllowed {
			return Reply{Err: errDetail(proto.ErrConflict, "board cannot be zapped", false)}
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
	tree := &projections.FavoriteTree{
		Folders: make([]projections.FavoriteFolder, 0, len(p.Folders)),
		Boards:  make([]projections.FavoriteBoardEntry, 0, len(p.Boards)),
	}
	for _, folder := range p.Folders {
		tree.Folders = append(tree.Folders, projections.FavoriteFolder{
			ID:       folder.ID,
			ParentID: folder.ParentID,
			Name:     folder.Name,
			Position: folder.Position,
		})
	}
	for _, board := range p.Boards {
		tree.Boards = append(tree.Boards, projections.FavoriteBoardEntry{
			ID:       board.ID,
			FolderID: board.FolderID,
			Position: board.Position,
		})
	}
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
	if !h.actorCanSetBoardSettings(actor, p.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board settings permission required", false)}
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
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
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
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
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
	canModerateBoard := h.actorCanModerateBoard(actor, p.Board)
	canManageMembers := h.actorCanManageBoardMembers(actor, p.Board)
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
		targetIsModerator := boardPermissionAllowed(projections.BoardModeratorExists(h.db, p.Board, userID))
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
	if !h.actorCanSetBoardSettings(actor, p.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board settings permission required", false)}
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
	if boardPermissionAllowed(projections.BoardMemberExists(h.db, p.Board, actor.ID)) {
		return Reply{Err: errDetail(proto.ErrConflict, "already a board member", false)}
	}
	status, err := projections.LatestBoardMemberApplicationStatus(h.db, p.Board, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	switch status {
	case "pending":
		return Reply{Err: errDetail(proto.ErrConflict, "membership application already pending", false)}
	case "blacklisted":
		return Reply{Err: errDetail(proto.ErrForbidden, "membership application is blocked", false)}
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
	if app.Status != "pending" {
		return Reply{Err: errDetail(proto.ErrConflict, "membership application is already reviewed", false)}
	}
	canModerateBoard := h.actorCanModerateBoard(actor, app.BoardID)
	canManageMembers := h.actorCanManageBoardMembers(actor, app.BoardID)
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
	if !boardPermissionAllowed(projections.BoardMemberExists(h.db, p.Board, actor.ID)) {
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
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot curate a redacted post", false)}
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
	if !h.actorCanCurateBoardKind(actor, thread.Board, kind) {
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
	if !h.actorCanCurateBoardKind(actor, thread.Board, kind) {
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
	if path != entry.Path {
		var conflictID string
		err := qQueryRow(
			tx,
			`SELECT id
			   FROM digest_entries
			  WHERE board_id=? AND target_kind=? AND target_id=? AND kind=? AND path=? AND id<>?
			  LIMIT 1`,
			entry.BoardID, entry.TargetKind, entry.TargetID, entry.Kind, path, entry.ID,
		).Scan(&conflictID)
		if err == nil {
			return Reply{Err: errDetail(proto.ErrConflict, "digest entry already exists at that path", false)}
		}
		if err != sql.ErrNoRows {
			return internalErr(err)
		}
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
	return h.applyReadMarker(p.Board, func() Reply {
		return h.requireBoard(p.Board)
	}, func() error {
		return currentRuntime().MarkBoardRead(h.db, actor.ID, p.Board)
	})
}

func (h *Handler) restoreBoardRead(actor *User, p proto.RestoreBoardReadPayload) Reply {
	p, msg := proto.NormalizeRestoreBoardReadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return h.applyReadMarker(p.Board, func() Reply {
		return h.requireBoard(p.Board)
	}, func() error {
		return currentRuntime().RestoreBoardRead(h.db, actor.ID, p.Board)
	})
}

func (h *Handler) markFavoriteFolderRead(actor *User, p proto.MarkFavoriteFolderReadPayload) Reply {
	return h.applyReadMarker(p.Folder, func() Reply {
		return h.requireFavoriteFolder(actor.ID, p.Folder)
	}, func() error {
		return currentRuntime().MarkFavoriteFolderRead(h.db, actor.ID, p.Folder)
	})
}

func (h *Handler) restoreFavoriteFolderRead(actor *User, p proto.RestoreFavoriteFolderReadPayload) Reply {
	return h.applyReadMarker(p.Folder, func() Reply {
		return h.requireFavoriteFolder(actor.ID, p.Folder)
	}, func() error {
		return currentRuntime().RestoreFavoriteFolderRead(h.db, actor.ID, p.Folder)
	})
}

func (h *Handler) markThreadRead(actor *User, p proto.MarkThreadReadPayload) Reply {
	p, msg := proto.NormalizeMarkThreadReadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return h.applyReadMarker(p.Thread, func() Reply {
		return h.requireThread(p.Thread)
	}, func() error {
		return currentRuntime().MarkThreadRead(h.db, actor.ID, p.Thread)
	})
}

func (h *Handler) restoreThreadRead(actor *User, p proto.RestoreThreadReadPayload) Reply {
	p, msg := proto.NormalizeRestoreThreadReadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return h.applyReadMarker(p.Thread, func() Reply {
		return h.requireThread(p.Thread)
	}, func() error {
		return currentRuntime().RestoreThreadRead(h.db, actor.ID, p.Thread)
	})
}

func (h *Handler) markPostRead(actor *User, p proto.MarkPostReadPayload) Reply {
	p, msg := proto.NormalizeMarkPostReadPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return h.applyReadMarker(p.Post, func() Reply {
		return h.requirePost(p.Post)
	}, func() error {
		return currentRuntime().MarkPostRead(h.db, actor.ID, p.Post)
	})
}

func (h *Handler) applyReadMarker(targetID string, require func() Reply, update func() error) Reply {
	if errReply := require(); errReply.Err != nil {
		return errReply
	}
	if err := update(); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: targetID}}
}

type digestEntryForCommand struct {
	ID         string
	BoardID    string
	TargetKind string
	TargetID   string
	Kind       string
	Title      string
	Path       string
	Note       string
}

func (h *Handler) digestEntryForCuration(actor *User, entryID string) (*digestEntryForCommand, Reply) {
	var entry digestEntryForCommand
	err := qQueryRow(
		h.db,
		`SELECT id, board_id, target_kind, target_id, kind, title, path, note
		   FROM digest_entries
		  WHERE id=?`,
		entryID,
	).Scan(&entry.ID, &entry.BoardID, &entry.TargetKind, &entry.TargetID, &entry.Kind, &entry.Title, &entry.Path, &entry.Note)
	if err == sql.ErrNoRows {
		return nil, Reply{Err: errDetail(proto.ErrNotFound, "digest entry not found", false)}
	}
	if err != nil {
		return nil, internalErr(err)
	}
	if !h.actorCanCurateBoardKind(actor, entry.BoardID, entry.Kind) {
		return nil, Reply{Err: errDetail(proto.ErrForbidden, proto.DigestCurationPermissionMessage(entry.Kind), false)}
	}
	return &entry, Reply{}
}

func (h *Handler) prepareDigestPathMutation(actor *User, boardID, kind, fromPath, toPath string) (string, string, string, string, Reply) {
	boardID, msg := proto.NormalizeDigestPathMutationBoard(boardID)
	if msg != "" {
		return "", "", "", "", Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if errReply := h.requireBoard(boardID); errReply.Err != nil {
		return "", "", "", "", errReply
	}
	normalizedKind, msg := proto.NormalizeDigestPathMutationKind(kind)
	if msg != "" {
		return "", "", "", "", Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if !h.actorCanCurateBoardKind(actor, boardID, normalizedKind) {
		return "", "", "", "", Reply{Err: errDetail(proto.ErrForbidden, proto.DigestCurationPermissionMessage(normalizedKind), false)}
	}
	normalizedFrom, normalizedTo, msg := proto.NormalizeDigestPathMutationPaths(fromPath, toPath)
	if msg != "" {
		return "", "", "", "", Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	return boardID, normalizedKind, normalizedFrom, normalizedTo, Reply{}
}

func (h *Handler) publishSystemNotice(actor *User, p proto.PublishSystemNoticePayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
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
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	ts := nowMS()
	dateLabel, _, msg := proto.NormalizeStatsSnapshotDate(p.Date, ts)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	plan, err := PlanStatsSnapshotSystemPosts(h.db, actor, dateLabel, ts)
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

func PlanStatsSnapshotSystemPosts(db *sql.DB, actor *User, dateLabel string, ts int64) (*statsplan.SnapshotPlan, error) {
	dateLabel = strings.TrimSpace(dateLabel)
	day, err := time.Parse("2006-01-02", dateLabel)
	if err != nil {
		return nil, err
	}
	dateID := day.Format("20060102")
	h := &Handler{db: db}
	snapshot, stats, err := h.planCurrentCommunityStatSnapshot(dateLabel, ts)
	if err != nil {
		return nil, err
	}
	plan := &statsplan.SnapshotPlan{
		MainThreadID: "bbslists_stats_" + dateID,
		Snapshot:     snapshot,
	}
	addPost := func(threadID, postID, title string, build func() (string, error)) error {
		existingSeq, exists, err := projections.ThreadLastSeq(h.db, threadID)
		if err != nil {
			return err
		}
		if exists {
			if threadID == plan.MainThreadID {
				plan.MainExistingSeq = existingSeq
			}
			return nil
		}
		body, err := build()
		if err != nil {
			return err
		}
		plan.Posts = append(plan.Posts, statsplan.SystemPostPlan{
			ThreadID: threadID,
			PostID:   postID,
			Title:    title,
			Body:     body,
		})
		return nil
	}
	if err := addPost(plan.MainThreadID, "bbslists_stats_post_"+dateID, "Community stats "+dateLabel, func() (string, error) {
		boards, err := projections.ListBoardRankings(db, "", false, 5, 0)
		if err != nil {
			return "", err
		}
		threads, err := projections.ListThreadRankings(db, "", false, "", 5, 0)
		if err != nil {
			return "", err
		}
		replies, err := projections.ListReplyRankings(db, "", false, 5, 0)
		if err != nil {
			return "", err
		}
		users, err := projections.ListUserRankings(db, 5, 0)
		if err != nil {
			return "", err
		}
		archives, err := projections.ListArchiveRankings(db, "", false, "", 5, 0)
		if err != nil {
			return "", err
		}
		blessings, err := projections.ListBlessingRankings(db, 5, 0)
		if err != nil {
			return "", err
		}
		history, err := h.planCommunityStatHistoryRecent(snapshot, 7)
		if err != nil {
			return "", err
		}
		return formatStatsSnapshotBody(dateLabel, stats, boards, threads, replies, users, archives, blessings, history), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_countlogins_"+dateID, "bbslists_countlogins_post_"+dateID, "Login count history "+dateLabel, func() (string, error) {
		history, err := h.planCommunityStatHistoryRecent(snapshot, 30)
		if err != nil {
			return "", err
		}
		hourly, err := projections.ListLoginHourlyStats(db, dateLabel)
		if err != nil {
			return "", err
		}
		return formatStatsLoginHistoryBody(dateLabel, stats, history, hourly), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_statguy_"+dateID, "bbslists_statguy_post_"+dateID, "User activity rankings "+dateLabel, func() (string, error) {
		users, err := projections.ListUserRankings(db, 100, 0)
		if err != nil {
			return "", err
		}
		return formatStatsUserActivityRankListBody(dateLabel, users), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_bonline_"+dateID, "bbslists_bonline_post_"+dateID, "Board online occupancy "+dateLabel, func() (string, error) {
		boards, err := projections.ListBoardRankings(db, "", false, 100, 0)
		if err != nil {
			return "", err
		}
		return formatStatsBoardOnlineListBody(dateLabel, stats, boards), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_uonline_"+dateID, "bbslists_uonline_post_"+dateID, "Online user roster "+dateLabel, func() (string, error) {
		users, err := projections.ListOnlineUsers(db, "", "", 200, 0)
		if err != nil {
			return "", err
		}
		if err := h.maskStatsOnlineUserRosterLocations(users); err != nil {
			return "", err
		}
		return formatStatsOnlineUserRosterBody(dateLabel, stats, users), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_statbm_"+dateID, "bbslists_statbm_post_"+dateID, "Board moderator activity "+dateLabel, func() (string, error) {
		boards, err := projections.ListBoardRankings(db, "", false, 100, 0)
		if err != nil {
			return "", err
		}
		onlineUsers, err := projections.ListOnlineUsers(db, "", "", 200, 0)
		if err != nil {
			return "", err
		}
		activity, err := h.listStatsBoardModeratorActivity(boards, onlineUsers)
		if err != nil {
			return "", err
		}
		return formatStatsBoardModeratorActivityBody(dateLabel, activity), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_bms_"+dateID, "bbslists_bms_post_"+dateID, "Board moderator tenure history "+dateLabel, func() (string, error) {
		terms, err := projections.ListPublicBoardModeratorTerms(db, 100, 0)
		if err != nil {
			return "", err
		}
		return formatStatsBoardModeratorHistoryBody(dateLabel, terms), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_boardlog_"+dateID, "bbslists_boardlog_post_"+dateID, "Board activity history "+dateLabel, func() (string, error) {
		boards, err := projections.ListBoardRankings(db, "", false, 30, 0)
		if err != nil {
			return "", err
		}
		history, err := h.planCommunityStatHistoryRecent(snapshot, 30)
		if err != nil {
			return "", err
		}
		return formatStatsBoardActivityHistoryBody(dateLabel, stats, boards, history), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_boardrank_"+dateID, "bbslists_boardrank_post_"+dateID, "Board popularity list "+dateLabel, func() (string, error) {
		boards, err := projections.ListBoardRankings(db, "", false, 100, 0)
		if err != nil {
			return "", err
		}
		activeBoards := boards[:0]
		for _, board := range boards {
			if board.PostCount > 0 || board.ThreadCount > 0 || board.OnlineUsers > 0 {
				activeBoards = append(activeBoards, board)
			}
		}
		return formatStatsBoardRankListBody(dateLabel, activeBoards), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_newboards_"+dateID, "bbslists_newboards_post_"+dateID, "New board list "+dateLabel, func() (string, error) {
		endAt := day.UTC().AddDate(0, 0, 1).Add(-time.Millisecond).UnixMilli()
		startAt := day.UTC().AddDate(0, 0, -29).UnixMilli()
		boards, err := projections.ListRecentPublicBoards(db, startAt, endAt, 100)
		if err != nil {
			return "", err
		}
		return formatStatsNewBoardListBody(dateLabel, boards, startAt, endAt), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_rcmdbrd_"+dateID, "bbslists_rcmdbrd_post_"+dateID, "Recommended board list "+dateLabel, func() (string, error) {
		boards, err := projections.ListRecommendedBoards(db, 100, 0)
		if err != nil {
			return "", err
		}
		return formatStatsRecommendedBoardListBody(dateLabel, boards), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_commend_"+dateID, "bbslists_commend_post_"+dateID, "Recommended article list "+dateLabel, func() (string, error) {
		entries, err := projections.ListPublicRecommendedDigestEntries(db, 100, 0)
		if err != nil {
			return "", err
		}
		return formatStatsRecommendedArticleListBody(dateLabel, entries), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_toplog_"+dateID, "bbslists_toplog_post_"+dateID, "Hot topic history "+dateLabel, func() (string, error) {
		threads, err := projections.ListThreadRankings(db, "", false, "", 30, 0)
		if err != nil {
			return "", err
		}
		categories, err := projections.ListCategories(db)
		if err != nil {
			return "", err
		}
		return formatStatsHotTopicHistoryBody(dateLabel, stats, threads, categories), nil
	}); err != nil {
		return nil, err
	}
	if err := addPost("bbslists_bless_"+dateID, "bbslists_bless_post_"+dateID, "Daily blessing list "+dateLabel, func() (string, error) {
		startAt, endAt, err := statsPeriodBounds(dateLabel, dateLabel)
		if err != nil {
			return "", err
		}
		rankings, err := projections.ListBlessingRankingsRange(db, startAt, endAt, 10, 0)
		if err != nil {
			return "", err
		}
		recent, err := projections.ListBlessingsRange(db, startAt, endAt, 10, 0)
		if err != nil {
			return "", err
		}
		return formatStatsBlessingListBody(dateLabel, rankings, recent, startAt, endAt), nil
	}); err != nil {
		return nil, err
	}
	for _, spec := range statsPeriodHistorySpecs(day) {
		spec := spec
		if err := addPost(spec.ThreadID, spec.PostID, spec.Title, func() (string, error) {
			history, err := h.planCommunityStatHistoryRange(snapshot, spec.StartDay, spec.EndDay)
			if err != nil {
				return "", err
			}
			return formatStatsPeriodHistoryBody(spec, history), nil
		}); err != nil {
			return nil, err
		}
	}
	for _, spec := range statsHotTopicPeriodHistorySpecs(day) {
		spec := spec
		if err := addPost(spec.ThreadID, spec.PostID, spec.Title, func() (string, error) {
			start, end, err := statsPeriodBounds(spec.StartDay, spec.EndDay)
			if err != nil {
				return "", err
			}
			threads, err := projections.ListThreadRankingsRange(db, "", false, "", start, end, 100, 0)
			if err != nil {
				return "", err
			}
			categories, err := projections.ListCategories(db)
			if err != nil {
				return "", err
			}
			return formatStatsHotTopicPeriodHistoryBody(spec, threads, categories), nil
		}); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func (h *Handler) planCurrentCommunityStatSnapshot(dateLabel string, ts int64) (projections.CommunityStatHistory, *projections.CommunityStats, error) {
	stats, err := projections.GetCommunityStats(h.db)
	if err != nil {
		return projections.CommunityStatHistory{}, nil, err
	}
	snapshot := projections.CommunityStatHistory{
		Day:                 dateLabel,
		SnapshotAt:          ts,
		TotalUsers:          stats.TotalUsers,
		TotalBoards:         stats.TotalBoards,
		TotalThreads:        stats.TotalThreads,
		TotalPosts:          stats.TotalPosts,
		TotalReactions:      stats.TotalReactions,
		TotalMail:           stats.TotalMail,
		TotalDirectMessages: stats.TotalDirectMessages,
		TotalLogins:         stats.TotalLogins,
		TotalLogouts:        stats.TotalLogouts,
		TotalWebLogins:      stats.TotalWebLogins,
		TotalWebLogouts:     stats.TotalWebLogouts,
		TotalGuestLogins:    stats.TotalGuestLogins,
		TotalGuestLogouts:   stats.TotalGuestLogouts,
		TotalOnlineSeconds:  stats.TotalOnlineSeconds,
		OnlineUsers:         stats.OnlineUsers,
		OnlineGuests:        stats.OnlineGuests,
		MaxOnlineUsers:      stats.OnlineUsers,
		MaxOnlineGuests:     stats.OnlineGuests,
		HeadSeq:             stats.HeadSeq,
	}
	if snapshot.OnlineUsers > 0 {
		snapshot.MaxOnlineAt = ts
	}
	if snapshot.OnlineGuests > 0 {
		snapshot.MaxOnlineGuestsAt = ts
	}
	existing, exists, err := h.planCommunityStatHistoryDay(dateLabel)
	if err != nil {
		return projections.CommunityStatHistory{}, nil, err
	}
	if exists {
		snapshot = mergeCurrentCommunityStatSnapshot(existing, snapshot)
	}
	if snapshot.MaxOnlineUsers > stats.MaxOnlineUsers {
		stats.MaxOnlineUsers = snapshot.MaxOnlineUsers
		stats.MaxOnlineAt = snapshot.MaxOnlineAt
	}
	if snapshot.MaxOnlineGuests > stats.MaxOnlineGuests {
		stats.MaxOnlineGuests = snapshot.MaxOnlineGuests
		stats.MaxOnlineGuestsAt = snapshot.MaxOnlineGuestsAt
	}
	return snapshot, stats, nil
}

func (h *Handler) planCommunityStatHistoryDay(day string) (projections.CommunityStatHistory, bool, error) {
	rows, err := projections.QQuery(h.db, planCommunityStatHistorySelectSQL()+` WHERE day=? LIMIT 1`, day)
	if err != nil {
		return projections.CommunityStatHistory{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return projections.CommunityStatHistory{}, false, err
		}
		return projections.CommunityStatHistory{}, false, nil
	}
	history, err := scanPlanCommunityStatHistory(rows)
	if err != nil {
		return projections.CommunityStatHistory{}, false, err
	}
	if err := rows.Err(); err != nil {
		return projections.CommunityStatHistory{}, false, err
	}
	return history, true, nil
}

func (h *Handler) planCommunityStatHistoryRecent(current projections.CommunityStatHistory, limit int) ([]projections.CommunityStatHistory, error) {
	fetchLimit := limit + 1
	rows, err := projections.QQuery(h.db, planCommunityStatHistorySelectSQL()+` ORDER BY day DESC LIMIT ?`, fetchLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history, err := scanPlanCommunityStatHistoryRows(rows)
	if err != nil {
		return nil, err
	}
	history = appendPlanCommunityStatHistoryIfMissing(history, current, true)
	sort.Slice(history, func(i, j int) bool { return history[i].Day > history[j].Day })
	if len(history) > fetchLimit {
		history = history[:fetchLimit]
	}
	applyPlanCommunityStatHistoryDeltas(history)
	if len(history) > limit {
		history = history[:limit]
	}
	return history, nil
}

func (h *Handler) planCommunityStatHistoryRange(current projections.CommunityStatHistory, startDay, endDay string) ([]projections.CommunityStatHistory, error) {
	rows, err := projections.QQuery(
		h.db,
		planCommunityStatHistorySelectSQL()+` WHERE day >= ? AND day <= ? ORDER BY day DESC`,
		startDay,
		endDay,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history, err := scanPlanCommunityStatHistoryRows(rows)
	if err != nil {
		return nil, err
	}
	if current.Day >= startDay && current.Day <= endDay {
		history = appendPlanCommunityStatHistoryIfMissing(history, current, true)
	}
	sort.Slice(history, func(i, j int) bool { return history[i].Day > history[j].Day })
	previous, exists, err := h.planCommunityStatHistoryPrevious(startDay)
	if err != nil {
		return nil, err
	}
	if exists {
		withPrevious := append(append([]projections.CommunityStatHistory(nil), history...), previous)
		applyPlanCommunityStatHistoryDeltas(withPrevious)
		return withPrevious[:len(history)], nil
	}
	applyPlanCommunityStatHistoryDeltas(history)
	return history, nil
}

func (h *Handler) planCommunityStatHistoryPrevious(startDay string) (projections.CommunityStatHistory, bool, error) {
	rows, err := projections.QQuery(
		h.db,
		planCommunityStatHistorySelectSQL()+` WHERE day < ? ORDER BY day DESC LIMIT 1`,
		startDay,
	)
	if err != nil {
		return projections.CommunityStatHistory{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return projections.CommunityStatHistory{}, false, err
		}
		return projections.CommunityStatHistory{}, false, nil
	}
	history, err := scanPlanCommunityStatHistory(rows)
	if err != nil {
		return projections.CommunityStatHistory{}, false, err
	}
	if err := rows.Err(); err != nil {
		return projections.CommunityStatHistory{}, false, err
	}
	return history, true, nil
}

func appendPlanCommunityStatHistoryIfMissing(history []projections.CommunityStatHistory, current projections.CommunityStatHistory, include bool) []projections.CommunityStatHistory {
	if !include {
		return history
	}
	for i := range history {
		if history[i].Day == current.Day {
			return history
		}
	}
	return append(history, current)
}

func mergeCurrentCommunityStatSnapshot(existing, current projections.CommunityStatHistory) projections.CommunityStatHistory {
	if existing.MaxOnlineUsers > current.MaxOnlineUsers {
		current.MaxOnlineUsers = existing.MaxOnlineUsers
		current.MaxOnlineAt = existing.MaxOnlineAt
	}
	if existing.MaxOnlineGuests > current.MaxOnlineGuests {
		current.MaxOnlineGuests = existing.MaxOnlineGuests
		current.MaxOnlineGuestsAt = existing.MaxOnlineGuestsAt
	}
	return current
}

func applyPlanCommunityStatHistoryDeltas(history []projections.CommunityStatHistory) {
	for i := range history {
		history[i].DeltaUsers = 0
		history[i].DeltaBoards = 0
		history[i].DeltaThreads = 0
		history[i].DeltaPosts = 0
		history[i].DeltaReactions = 0
		history[i].DeltaMail = 0
		history[i].DeltaDirectMessages = 0
		history[i].DeltaLogins = 0
		history[i].DeltaLogouts = 0
		history[i].DeltaWebLogins = 0
		history[i].DeltaWebLogouts = 0
		history[i].DeltaGuestLogins = 0
		history[i].DeltaGuestLogouts = 0
		history[i].DeltaOnlineSeconds = 0
		history[i].DeltaGuests = 0
		if i+1 >= len(history) {
			continue
		}
		previous := history[i+1]
		history[i].DeltaUsers = history[i].TotalUsers - previous.TotalUsers
		history[i].DeltaBoards = history[i].TotalBoards - previous.TotalBoards
		history[i].DeltaThreads = history[i].TotalThreads - previous.TotalThreads
		history[i].DeltaPosts = history[i].TotalPosts - previous.TotalPosts
		history[i].DeltaReactions = history[i].TotalReactions - previous.TotalReactions
		history[i].DeltaMail = history[i].TotalMail - previous.TotalMail
		history[i].DeltaDirectMessages = history[i].TotalDirectMessages - previous.TotalDirectMessages
		history[i].DeltaLogins = history[i].TotalLogins - previous.TotalLogins
		history[i].DeltaLogouts = history[i].TotalLogouts - previous.TotalLogouts
		history[i].DeltaWebLogins = history[i].TotalWebLogins - previous.TotalWebLogins
		history[i].DeltaWebLogouts = history[i].TotalWebLogouts - previous.TotalWebLogouts
		history[i].DeltaGuestLogins = history[i].TotalGuestLogins - previous.TotalGuestLogins
		history[i].DeltaGuestLogouts = history[i].TotalGuestLogouts - previous.TotalGuestLogouts
		history[i].DeltaOnlineSeconds = history[i].TotalOnlineSeconds - previous.TotalOnlineSeconds
		history[i].DeltaGuests = history[i].OnlineGuests - previous.OnlineGuests
	}
}

func scanPlanCommunityStatHistoryRows(rows *sql.Rows) ([]projections.CommunityStatHistory, error) {
	out := []projections.CommunityStatHistory{}
	for rows.Next() {
		history, err := scanPlanCommunityStatHistory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, history)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanPlanCommunityStatHistory(row interface {
	Scan(dest ...any) error
}) (projections.CommunityStatHistory, error) {
	var h projections.CommunityStatHistory
	err := row.Scan(
		&h.Day,
		&h.SnapshotAt,
		&h.TotalUsers,
		&h.TotalBoards,
		&h.TotalThreads,
		&h.TotalPosts,
		&h.TotalReactions,
		&h.TotalMail,
		&h.TotalDirectMessages,
		&h.TotalLogins,
		&h.TotalLogouts,
		&h.TotalWebLogins,
		&h.TotalWebLogouts,
		&h.TotalGuestLogins,
		&h.TotalGuestLogouts,
		&h.TotalOnlineSeconds,
		&h.OnlineUsers,
		&h.OnlineGuests,
		&h.MaxOnlineUsers,
		&h.MaxOnlineAt,
		&h.MaxOnlineGuests,
		&h.MaxOnlineGuestsAt,
		&h.HeadSeq,
	)
	return h, err
}

func planCommunityStatHistorySelectSQL() string {
	return `SELECT day, snapshot_at, total_users, total_boards, total_threads, total_posts,
		        total_reactions, total_mail, total_direct_messages, total_logins,
		        total_logouts, total_web_logins, total_web_logouts,
		        total_guest_logins, total_guest_logouts,
		        total_online_seconds, online_users, online_guests,
		        max_online_users, max_online_at, max_online_guests,
		        max_online_guests_at, head_seq
		   FROM community_stat_history`
}

func (h *Handler) maskStatsOnlineUserRosterLocations(users []projections.SocialUser) error {
	masked := make(map[string]bool)
	for i := range users {
		boardID := users[i].BoardID
		if boardID == "" {
			continue
		}
		shouldMask, ok := masked[boardID]
		if !ok {
			var err error
			shouldMask, err = h.shouldMaskStatsOnlineUserBoard(boardID)
			if err != nil {
				return err
			}
			masked[boardID] = shouldMask
		}
		if !shouldMask {
			continue
		}
		users[i].BoardID = ""
		users[i].BoardName = ""
		users[i].ThreadID = ""
		users[i].LocationLabel = "hidden board"
		if users[i].Mode != "" {
			users[i].Status = users[i].Mode
		} else {
			users[i].Status = "online"
		}
	}
	return nil
}

func (h *Handler) shouldMaskStatsOnlineUserBoard(boardID string) (bool, error) {
	if projections.IsGeneratedSystemBoardID(boardID) {
		return true, nil
	}
	var memberReadMode, statsExcluded int
	err := qQueryRow(
		h.db,
		`SELECT COALESCE(s.member_read_mode, 0), COALESCE(s.stats_excluded, 0)
		   FROM boards b
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  WHERE b.id=?`,
		boardID,
	).Scan(&memberReadMode, &statsExcluded)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return memberReadMode != 0 || statsExcluded != 0, nil
}

type statsBoardModeratorActivity struct {
	Board      projections.BoardRanking
	Moderators []statsBoardModerator
}

type statsBoardModerator struct {
	UserID             string
	Name               string
	Position           int
	CreatedAt          int64
	UpdatedAt          int64
	LoginCount         int
	PostsCreated       int
	TotalOnlineSeconds int64
	LastVisitDay       string
	Online             bool
}

func (h *Handler) listStatsBoardModeratorActivity(boards []projections.BoardRanking, onlineUsers []projections.SocialUser) ([]statsBoardModeratorActivity, error) {
	online := make(map[string]bool)
	for _, user := range onlineUsers {
		if user.UserID != "" {
			online[user.UserID] = true
		}
	}
	out := make([]statsBoardModeratorActivity, 0)
	for _, board := range boards {
		if board.ModeratorCount <= 0 {
			continue
		}
		moderators, err := projections.ListBoardModerators(h.db, board.ID)
		if err != nil {
			return nil, err
		}
		entry := statsBoardModeratorActivity{Board: board, Moderators: make([]statsBoardModerator, 0, len(moderators))}
		for _, moderator := range moderators {
			stat, err := h.getStatsBoardModerator(moderator, online[moderator.UserID])
			if err != nil {
				return nil, err
			}
			entry.Moderators = append(entry.Moderators, stat)
		}
		if len(entry.Moderators) > 0 {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (h *Handler) getStatsBoardModerator(moderator projections.BoardModerator, online bool) (statsBoardModerator, error) {
	stat := statsBoardModerator{
		UserID:    moderator.UserID,
		Name:      moderator.Name,
		Position:  moderator.Position,
		CreatedAt: moderator.CreatedAt,
		UpdatedAt: moderator.UpdatedAt,
		Online:    online,
	}
	err := qQueryRow(
		h.db,
		`SELECT COALESCE(ua.login_count, 0),
		        COALESCE(ua.total_online_seconds, 0),
		        COALESCE(ua.last_visit_day, '')
		   FROM users u
		   LEFT JOIN user_activity ua ON ua.user_id=u.id
		  WHERE u.id=?`,
		moderator.UserID,
	).Scan(&stat.LoginCount, &stat.TotalOnlineSeconds, &stat.LastVisitDay)
	if err == sql.ErrNoRows {
		return stat, nil
	}
	if err != nil {
		return stat, err
	}
	postCount, lastPostAt, err := projections.GetPublicUserPostActivity(h.db, moderator.UserID)
	if err != nil {
		return stat, err
	}
	stat.PostsCreated = postCount
	if strings.TrimSpace(stat.LastVisitDay) == "" && lastPostAt > 0 {
		stat.LastVisitDay = time.UnixMilli(lastPostAt).UTC().Format("2006-01-02")
	}
	return stat, nil
}

type statsPeriodHistorySpec struct {
	ThreadID string
	PostID   string
	Title    string
	Label    string
	StartDay string
	EndDay   string
}

func statsPeriodHistorySpecs(day time.Time) []statsPeriodHistorySpec {
	day = day.UTC()
	out := []statsPeriodHistorySpec{}
	if day.Weekday() == time.Sunday {
		isoYear, isoWeek := day.ISOWeek()
		start := day.AddDate(0, 0, -6)
		label := fmt.Sprintf("%04d-W%02d", isoYear, isoWeek)
		id := fmt.Sprintf("%04dw%02d", isoYear, isoWeek)
		out = append(out, statsPeriodHistorySpec{
			ThreadID: "bbslists_week_" + id,
			PostID:   "bbslists_week_post_" + id,
			Title:    "Weekly activity history " + label,
			Label:    label,
			StartDay: start.Format("2006-01-02"),
			EndDay:   day.Format("2006-01-02"),
		})
	}
	tomorrow := day.AddDate(0, 0, 1)
	if tomorrow.Day() == 1 {
		label := day.Format("2006-01")
		id := day.Format("200601")
		out = append(out, statsPeriodHistorySpec{
			ThreadID: "bbslists_month_" + id,
			PostID:   "bbslists_month_post_" + id,
			Title:    "Monthly activity history " + label,
			Label:    label,
			StartDay: time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
			EndDay:   day.Format("2006-01-02"),
		})
	}
	if day.Month() == time.December && day.Day() == 31 {
		label := day.Format("2006")
		out = append(out, statsPeriodHistorySpec{
			ThreadID: "bbslists_year_" + label,
			PostID:   "bbslists_year_post_" + label,
			Title:    "Yearly activity history " + label,
			Label:    label,
			StartDay: time.Date(day.Year(), time.January, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
			EndDay:   day.Format("2006-01-02"),
		})
	}
	return out
}

func statsHotTopicPeriodHistorySpecs(day time.Time) []statsPeriodHistorySpec {
	out := []statsPeriodHistorySpec{}
	for _, spec := range statsPeriodHistorySpecs(day) {
		switch {
		case strings.HasPrefix(spec.ThreadID, "bbslists_week_"):
			id := strings.TrimPrefix(spec.ThreadID, "bbslists_week_")
			out = append(out, statsPeriodHistorySpec{
				ThreadID: "bbslists_toplog_week_" + id,
				PostID:   "bbslists_toplog_week_post_" + id,
				Title:    "Weekly hot-topic history " + spec.Label,
				Label:    spec.Label,
				StartDay: spec.StartDay,
				EndDay:   spec.EndDay,
			})
		case strings.HasPrefix(spec.ThreadID, "bbslists_month_"):
			id := strings.TrimPrefix(spec.ThreadID, "bbslists_month_")
			out = append(out, statsPeriodHistorySpec{
				ThreadID: "bbslists_toplog_month_" + id,
				PostID:   "bbslists_toplog_month_post_" + id,
				Title:    "Monthly hot-topic history " + spec.Label,
				Label:    spec.Label,
				StartDay: spec.StartDay,
				EndDay:   spec.EndDay,
			})
		case strings.HasPrefix(spec.ThreadID, "bbslists_year_"):
			id := strings.TrimPrefix(spec.ThreadID, "bbslists_year_")
			out = append(out, statsPeriodHistorySpec{
				ThreadID: "bbslists_toplog_year_" + id,
				PostID:   "bbslists_toplog_year_post_" + id,
				Title:    "Yearly hot-topic history " + spec.Label,
				Label:    spec.Label,
				StartDay: spec.StartDay,
				EndDay:   spec.EndDay,
			})
		}
	}
	return out
}

func statsPeriodBounds(startDay, endDay string) (int64, int64, error) {
	start, err := time.Parse("2006-01-02", startDay)
	if err != nil {
		return 0, 0, err
	}
	end, err := time.Parse("2006-01-02", endDay)
	if err != nil {
		return 0, 0, err
	}
	return start.UTC().UnixMilli(), end.UTC().AddDate(0, 0, 1).Add(-time.Millisecond).UnixMilli(), nil
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

func formatStatsSnapshotBody(dateLabel string, stats *projections.CommunityStats, boards []projections.BoardRanking, threads []projections.ThreadRanking, replies []projections.ReplyRanking, users []projections.UserRanking, archives []projections.ArchiveRanking, blessings []projections.BlessingRanking, history []projections.CommunityStatHistory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Community stats %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Total users: %d\n", stats.TotalUsers)
	fmt.Fprintf(&b, "- Total logins: %d\n", stats.TotalLogins)
	fmt.Fprintf(&b, "- Total logouts: %d\n", stats.TotalLogouts)
	fmt.Fprintf(&b, "- Web logins: %d\n", stats.TotalWebLogins)
	fmt.Fprintf(&b, "- Web logouts: %d\n", stats.TotalWebLogouts)
	fmt.Fprintf(&b, "- Guest logins: %d\n", stats.TotalGuestLogins)
	fmt.Fprintf(&b, "- Guest logouts: %d\n", stats.TotalGuestLogouts)
	fmt.Fprintf(&b, "- Total boards: %d\n", stats.TotalBoards)
	fmt.Fprintf(&b, "- Total threads: %d\n", stats.TotalThreads)
	fmt.Fprintf(&b, "- Total posts: %d\n", stats.TotalPosts)
	fmt.Fprintf(&b, "- Total reactions: %d\n", stats.TotalReactions)
	fmt.Fprintf(&b, "- Total mail messages: %d\n", stats.TotalMail)
	fmt.Fprintf(&b, "- Total direct messages: %d\n", stats.TotalDirectMessages)
	fmt.Fprintf(&b, "- Total online time: %s\n", formatStatsDuration(stats.TotalOnlineSeconds))
	fmt.Fprintf(&b, "- Online users: %d\n", stats.OnlineUsers)
	fmt.Fprintf(&b, "- Online guests: %d\n", stats.OnlineGuests)
	fmt.Fprintf(&b, "- Max online users: %d", stats.MaxOnlineUsers)
	if stats.MaxOnlineAt > 0 {
		fmt.Fprintf(&b, " at %s UTC", time.UnixMilli(stats.MaxOnlineAt).UTC().Format("2006-01-02 15:04"))
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "- Max online guests: %d", stats.MaxOnlineGuests)
	if stats.MaxOnlineGuestsAt > 0 {
		fmt.Fprintf(&b, " at %s UTC", time.UnixMilli(stats.MaxOnlineGuestsAt).UTC().Format("2006-01-02 15:04"))
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "- Event head: %d\n\n", stats.HeadSeq)

	b.WriteString("## Recent daily history\n")
	if len(history) == 0 {
		b.WriteString("- No daily stat history yet.\n")
	}
	for _, day := range history {
		maxAt := "n/a"
		if day.MaxOnlineAt > 0 {
			maxAt = time.UnixMilli(day.MaxOnlineAt).UTC().Format("2006-01-02 15:04")
		}
		guestMaxAt := "n/a"
		if day.MaxOnlineGuestsAt > 0 {
			guestMaxAt = time.UnixMilli(day.MaxOnlineGuestsAt).UTC().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&b, "- %s: %d users%s, %s%s, %s%s, web %d in%s/%d out%s, guests %d in%s/%d out%s, %d guests%s, %d posts%s, %d reactions%s, %s online time%s, %d users online now, max %d users at %s UTC, max %d guests at %s UTC\n",
			day.Day,
			day.TotalUsers,
			formatStatsDelta(day.DeltaUsers),
			formatStatsCount(day.TotalLogins, "login", "logins"),
			formatStatsDelta(day.DeltaLogins),
			formatStatsCount(day.TotalLogouts, "logout", "logouts"),
			formatStatsDelta(day.DeltaLogouts),
			day.TotalWebLogins,
			formatStatsDelta(day.DeltaWebLogins),
			day.TotalWebLogouts,
			formatStatsDelta(day.DeltaWebLogouts),
			day.TotalGuestLogins,
			formatStatsDelta(day.DeltaGuestLogins),
			day.TotalGuestLogouts,
			formatStatsDelta(day.DeltaGuestLogouts),
			day.OnlineGuests,
			formatStatsDelta(day.DeltaGuests),
			day.TotalPosts,
			formatStatsDelta(day.DeltaPosts),
			day.TotalReactions,
			formatStatsDelta(day.DeltaReactions),
			formatStatsDuration(day.TotalOnlineSeconds),
			formatStatsDurationDelta(day.DeltaOnlineSeconds),
			day.OnlineUsers,
			day.MaxOnlineUsers,
			maxAt,
			day.MaxOnlineGuests,
			guestMaxAt)
	}
	b.WriteByte('\n')

	b.WriteString("## Active boards\n")
	if len(boards) == 0 {
		b.WriteString("- No public board activity yet.\n")
	}
	for i, board := range boards {
		fmt.Fprintf(&b, "%d. %s (%s): %d posts, %d threads\n", i+1, board.Name, board.ID, board.PostCount, board.ThreadCount)
	}
	b.WriteString("\n## Hot threads\n")
	if len(threads) == 0 {
		b.WriteString("- No public thread activity yet.\n")
	}
	for i, thread := range threads {
		fmt.Fprintf(&b, "%d. %s / %s: %d participants, %d posts, %d reactions, score %d\n", i+1, thread.BoardName, thread.Title, thread.ParticipantCount, thread.PostCount, thread.ReactionCount, thread.Score)
	}
	b.WriteString("\n## Latest replies\n")
	if len(replies) == 0 {
		b.WriteString("- No public replies yet.\n")
	}
	for i, reply := range replies {
		fmt.Fprintf(&b, "%d. %s / %s by %s: %s\n", i+1, reply.BoardName, reply.Title, reply.Author, reply.Excerpt)
	}
	b.WriteString("\n## Top users\n")
	if len(users) == 0 {
		b.WriteString("- No user activity yet.\n")
	}
	for i, user := range users {
		fmt.Fprintf(&b, "%d. %s: %d posts, %d reactions received, %d logins\n", i+1, user.Name, user.PostsCreated, user.ReactionsReceived, user.LoginCount)
	}
	b.WriteString("\n## Blessings\n")
	if len(blessings) == 0 {
		b.WriteString("- No blessing rituals yet.\n")
	}
	for i, blessing := range blessings {
		fmt.Fprintf(&b, "%d. %s: %d blessings\n", i+1, blessing.Name, blessing.BlessingCount)
	}
	b.WriteString("\n## Archive paths\n")
	if len(archives) == 0 {
		b.WriteString("- No public archive paths yet.\n")
	}
	for i, archive := range archives {
		fmt.Fprintf(&b, "%d. %s / %s / %s: %d entries\n", i+1, archive.BoardName, archive.Kind, archive.Path, archive.EntryCount)
	}
	return b.String()
}

func formatStatsLoginHistoryBody(dateLabel string, stats *projections.CommunityStats, history []projections.CommunityStatHistory, hourly []projections.LoginHourlyStat) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Login count history %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Total logins: %d\n", stats.TotalLogins)
	fmt.Fprintf(&b, "- Total logouts: %d\n", stats.TotalLogouts)
	fmt.Fprintf(&b, "- Web logins: %d\n", stats.TotalWebLogins)
	fmt.Fprintf(&b, "- Web logouts: %d\n", stats.TotalWebLogouts)
	fmt.Fprintf(&b, "- Guest logins: %d\n", stats.TotalGuestLogins)
	fmt.Fprintf(&b, "- Guest logouts: %d\n", stats.TotalGuestLogouts)
	fmt.Fprintf(&b, "- Total users: %d\n", stats.TotalUsers)
	fmt.Fprintf(&b, "- Online users: %d\n", stats.OnlineUsers)
	fmt.Fprintf(&b, "- Online guests: %d\n", stats.OnlineGuests)
	fmt.Fprintf(&b, "- Total online time: %s\n", formatStatsDuration(stats.TotalOnlineSeconds))
	fmt.Fprintf(&b, "- Max online users: %d", stats.MaxOnlineUsers)
	if stats.MaxOnlineAt > 0 {
		fmt.Fprintf(&b, " at %s UTC", time.UnixMilli(stats.MaxOnlineAt).UTC().Format("2006-01-02 15:04"))
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "- Max online guests: %d", stats.MaxOnlineGuests)
	if stats.MaxOnlineGuestsAt > 0 {
		fmt.Fprintf(&b, " at %s UTC", time.UnixMilli(stats.MaxOnlineGuestsAt).UTC().Format("2006-01-02 15:04"))
	}
	b.WriteString("\n\n## Recent login and guest history\n")
	if len(history) == 0 {
		b.WriteString("- No daily stat history yet.\n")
	}
	for _, day := range history {
		fmt.Fprintf(&b, "- %s: %s%s, %s%s, web %d in%s/%d out%s, guests %d in%s/%d out%s, %d users%s, %d online users, %d guests%s, %s online time%s\n",
			day.Day,
			formatStatsCount(day.TotalLogins, "login", "logins"),
			formatStatsDelta(day.DeltaLogins),
			formatStatsCount(day.TotalLogouts, "logout", "logouts"),
			formatStatsDelta(day.DeltaLogouts),
			day.TotalWebLogins,
			formatStatsDelta(day.DeltaWebLogins),
			day.TotalWebLogouts,
			formatStatsDelta(day.DeltaWebLogouts),
			day.TotalGuestLogins,
			formatStatsDelta(day.DeltaGuestLogins),
			day.TotalGuestLogouts,
			formatStatsDelta(day.DeltaGuestLogouts),
			day.TotalUsers,
			formatStatsDelta(day.DeltaUsers),
			day.OnlineUsers,
			day.OnlineGuests,
			formatStatsDelta(day.DeltaGuests),
			formatStatsDuration(day.TotalOnlineSeconds),
			formatStatsDurationDelta(day.DeltaOnlineSeconds))
	}
	b.WriteString("\n## Hourly login histogram\n")
	peakHour, peakCount, total := statsLoginHourlySummary(hourly)
	if total == 0 {
		b.WriteString("- No hourly login samples for this day yet.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "- Day login samples: %d\n", total)
	fmt.Fprintf(&b, "- Peak hour: %02d:00 UTC (%s)\n\n", peakHour, formatStatsCount(peakCount, "login", "logins"))
	b.WriteString("| Hour | Logins | Bar |\n")
	b.WriteString("| --- | ---: | --- |\n")
	for _, hour := range hourly {
		fmt.Fprintf(&b, "| %02d:00 | %d | %s |\n", hour.Hour, hour.LoginCount, formatStatsHistogramBar(hour.LoginCount, peakCount))
	}
	return b.String()
}

func statsLoginHourlySummary(hourly []projections.LoginHourlyStat) (peakHour, peakCount, total int) {
	for _, hour := range hourly {
		total += hour.LoginCount
		if hour.LoginCount > peakCount {
			peakHour = hour.Hour
			peakCount = hour.LoginCount
		}
	}
	return peakHour, peakCount, total
}

func formatStatsHistogramBar(count, peak int) string {
	if count <= 0 || peak <= 0 {
		return ""
	}
	width := (count * 20) / peak
	if (count*20)%peak != 0 {
		width++
	}
	if width < 1 {
		width = 1
	}
	return strings.Repeat("#", width)
}

func formatStatsUserActivityRankListBody(dateLabel string, users []projections.UserRanking) string {
	active := activeUserRankings(users)
	var b strings.Builder
	fmt.Fprintf(&b, "# User activity rankings %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Ranked active users: %d\n", len(active))
	totalPosts := 0
	totalReactions := 0
	totalLogins := 0
	var totalOnlineSeconds int64
	for _, user := range active {
		totalPosts += user.PostsCreated
		totalReactions += user.ReactionsReceived
		totalLogins += user.LoginCount
		totalOnlineSeconds += user.TotalOnlineSeconds
	}
	fmt.Fprintf(&b, "- Ranked posts: %d\n", totalPosts)
	fmt.Fprintf(&b, "- Ranked reactions received: %d\n", totalReactions)
	fmt.Fprintf(&b, "- Ranked logins: %d\n", totalLogins)
	fmt.Fprintf(&b, "- Ranked stay time: %s\n\n", formatStatsDuration(totalOnlineSeconds))

	if len(active) == 0 {
		b.WriteString("- No user activity yet.\n")
		return b.String()
	}
	formatStatsUserRankingSection(&b, "Top posters", sortUserRankings(active, func(a, b projections.UserRanking) bool {
		if a.PostsCreated != b.PostsCreated {
			return a.PostsCreated > b.PostsCreated
		}
		if a.ReactionsReceived != b.ReactionsReceived {
			return a.ReactionsReceived > b.ReactionsReceived
		}
		if a.LoginCount != b.LoginCount {
			return a.LoginCount > b.LoginCount
		}
		if a.TotalOnlineSeconds != b.TotalOnlineSeconds {
			return a.TotalOnlineSeconds > b.TotalOnlineSeconds
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	}), func(user projections.UserRanking) string {
		return fmt.Sprintf("%d posts, %d reactions received, %s, %s stay time",
			user.PostsCreated,
			user.ReactionsReceived,
			formatStatsCount(user.LoginCount, "login", "logins"),
			formatStatsDuration(user.TotalOnlineSeconds))
	})
	formatStatsUserRankingSection(&b, "Top login counts", sortUserRankings(active, func(a, b projections.UserRanking) bool {
		if a.LoginCount != b.LoginCount {
			return a.LoginCount > b.LoginCount
		}
		if a.PostsCreated != b.PostsCreated {
			return a.PostsCreated > b.PostsCreated
		}
		if a.TotalOnlineSeconds != b.TotalOnlineSeconds {
			return a.TotalOnlineSeconds > b.TotalOnlineSeconds
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	}), func(user projections.UserRanking) string {
		return fmt.Sprintf("%s, %d posts, %s stay time",
			formatStatsCount(user.LoginCount, "login", "logins"),
			user.PostsCreated,
			formatStatsDuration(user.TotalOnlineSeconds))
	})
	formatStatsUserRankingSection(&b, "Top stay time", sortUserRankings(active, func(a, b projections.UserRanking) bool {
		if a.TotalOnlineSeconds != b.TotalOnlineSeconds {
			return a.TotalOnlineSeconds > b.TotalOnlineSeconds
		}
		if a.LoginCount != b.LoginCount {
			return a.LoginCount > b.LoginCount
		}
		if a.PostsCreated != b.PostsCreated {
			return a.PostsCreated > b.PostsCreated
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	}), func(user projections.UserRanking) string {
		return fmt.Sprintf("%s stay time, %s, %d posts",
			formatStatsDuration(user.TotalOnlineSeconds),
			formatStatsCount(user.LoginCount, "login", "logins"),
			user.PostsCreated)
	})
	formatStatsUserRankingSection(&b, "Top community score", sortUserRankings(active, func(a, b projections.UserRanking) bool {
		aScore := userActivityScore(a)
		bScore := userActivityScore(b)
		if aScore != bScore {
			return aScore > bScore
		}
		if a.PostsCreated != b.PostsCreated {
			return a.PostsCreated > b.PostsCreated
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	}), func(user projections.UserRanking) string {
		return fmt.Sprintf("score %d (%d posts, %d reactions received, %s, %s stay time, trust %d)",
			userActivityScore(user),
			user.PostsCreated,
			user.ReactionsReceived,
			formatStatsCount(user.LoginCount, "login", "logins"),
			formatStatsDuration(user.TotalOnlineSeconds),
			user.TrustLevel)
	})
	return b.String()
}

func activeUserRankings(users []projections.UserRanking) []projections.UserRanking {
	active := make([]projections.UserRanking, 0, len(users))
	for _, user := range users {
		if user.PostsCreated > 0 || user.ReactionsReceived > 0 || user.LoginCount > 0 || user.TotalOnlineSeconds > 0 || user.TrustLevel > 0 {
			active = append(active, user)
		}
	}
	return active
}

func sortUserRankings(users []projections.UserRanking, less func(a, b projections.UserRanking) bool) []projections.UserRanking {
	out := append([]projections.UserRanking(nil), users...)
	sort.SliceStable(out, func(i, j int) bool {
		return less(out[i], out[j])
	})
	if len(out) > 20 {
		return out[:20]
	}
	return out
}

func formatStatsUserRankingSection(b *strings.Builder, title string, users []projections.UserRanking, detail func(projections.UserRanking) string) {
	fmt.Fprintf(b, "## %s\n", title)
	for i, user := range users {
		fmt.Fprintf(b, "%d. %s: %s\n", i+1, user.Name, detail(user))
	}
	b.WriteByte('\n')
}

func userActivityScore(user projections.UserRanking) int64 {
	return int64(user.PostsCreated*10+user.ReactionsReceived*3+user.LoginCount+user.TrustLevel*25) + user.TotalOnlineSeconds/3600
}

func formatStatsBoardOnlineListBody(dateLabel string, stats *projections.CommunityStats, boards []projections.BoardRanking) string {
	onlineBoards := onlineBoardRankings(boards)
	var b strings.Builder
	fmt.Fprintf(&b, "# Board online occupancy %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Online users: %d\n", stats.OnlineUsers)
	fmt.Fprintf(&b, "- Online guests: %d\n", stats.OnlineGuests)
	fmt.Fprintf(&b, "- Boards with online users: %d\n\n", len(onlineBoards))

	b.WriteString("## Public board online ranking\n")
	if len(onlineBoards) == 0 {
		b.WriteString("- No public boards currently have online users.\n")
		return b.String()
	}
	for i, board := range onlineBoards {
		lastActivity := "no posts yet"
		if board.LastPostAt > 0 {
			lastActivity = time.UnixMilli(board.LastPostAt).UTC().Format("2006-01-02 15:04") + " UTC"
		}
		fmt.Fprintf(&b, "%d. %s (%s): %d users online, %d posts, %d threads, last activity %s\n",
			i+1,
			board.Name,
			board.ID,
			board.OnlineUsers,
			board.PostCount,
			board.ThreadCount,
			lastActivity)
	}
	return b.String()
}

func onlineBoardRankings(boards []projections.BoardRanking) []projections.BoardRanking {
	out := make([]projections.BoardRanking, 0, len(boards))
	for _, board := range boards {
		if board.OnlineUsers > 0 {
			out = append(out, board)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].OnlineUsers != out[j].OnlineUsers {
			return out[i].OnlineUsers > out[j].OnlineUsers
		}
		if out[i].PostCount != out[j].PostCount {
			return out[i].PostCount > out[j].PostCount
		}
		if out[i].LastSeq != out[j].LastSeq {
			return out[i].LastSeq > out[j].LastSeq
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func formatStatsOnlineUserRosterBody(dateLabel string, stats *projections.CommunityStats, users []projections.SocialUser) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Online user roster %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Online user sessions: %d\n", len(users))
	fmt.Fprintf(&b, "- Distinct online users: %d\n", distinctOnlineUserCount(users))
	fmt.Fprintf(&b, "- Online guests: %d\n\n", stats.OnlineGuests)

	b.WriteString("## Visible online users\n")
	if len(users) == 0 {
		b.WriteString("- No visible users are online.\n")
		return b.String()
	}
	for i, user := range users {
		location := formatStatsOnlineUserLocation(user)
		lastSeen := "unknown"
		if user.LastSeen > 0 {
			lastSeen = time.UnixMilli(user.LastSeen).UTC().Format("2006-01-02 15:04") + " UTC"
		}
		host := strings.TrimSpace(user.FromHost)
		if host == "" {
			host = "unknown host"
		}
		fmt.Fprintf(&b, "%d. %s: %s, idle %s, last seen %s, from %s\n",
			i+1,
			user.Name,
			location,
			formatStatsDuration(user.IdleSeconds),
			lastSeen,
			host)
	}
	return b.String()
}

func distinctOnlineUserCount(users []projections.SocialUser) int {
	seen := make(map[string]bool)
	for _, user := range users {
		if user.UserID == "" {
			continue
		}
		seen[user.UserID] = true
	}
	return len(seen)
}

func formatStatsOnlineUserLocation(user projections.SocialUser) string {
	mode := strings.TrimSpace(user.Mode)
	status := strings.TrimSpace(user.Status)
	if mode == "" {
		mode = status
	}
	if mode == "" {
		mode = "online"
	}
	if user.BoardID != "" {
		board := user.BoardID
		if user.BoardName != "" {
			board = fmt.Sprintf("%s (%s)", user.BoardName, user.BoardID)
		}
		parts := []string{mode + " on " + board}
		if user.ThreadID != "" {
			parts = append(parts, "thread "+user.ThreadID)
		}
		if label := strings.TrimSpace(user.LocationLabel); label != "" {
			parts = append(parts, label)
		}
		return strings.Join(parts, ", ")
	}
	if label := strings.TrimSpace(user.LocationLabel); label != "" {
		return mode + " in " + label
	}
	return mode
}

func formatStatsBoardModeratorActivityBody(dateLabel string, activity []statsBoardModeratorActivity) string {
	boardCount, assignmentCount, onlineCount, dormantCount := statsBoardModeratorActivitySummary(activity)
	var b strings.Builder
	fmt.Fprintf(&b, "# Board moderator activity %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Public boards with moderators: %d\n", boardCount)
	fmt.Fprintf(&b, "- Moderator assignments: %d\n", assignmentCount)
	fmt.Fprintf(&b, "- Online moderators: %d\n", onlineCount)
	fmt.Fprintf(&b, "- Dormant moderator assignments: %d\n\n", dormantCount)

	b.WriteString("## Public board moderator roster\n")
	if len(activity) == 0 {
		b.WriteString("- No public board moderators are assigned.\n")
		return b.String()
	}
	for i, entry := range activity {
		fmt.Fprintf(&b, "%d. %s (%s): %d moderators, %d posts, %d threads, %d users online\n",
			i+1,
			entry.Board.Name,
			entry.Board.ID,
			len(entry.Moderators),
			entry.Board.PostCount,
			entry.Board.ThreadCount,
			entry.Board.OnlineUsers)
		for _, moderator := range entry.Moderators {
			fmt.Fprintf(&b, "   - %s: position %d, %s, %s, %s, %s stay time, last activity %s\n",
				moderator.Name,
				moderator.Position,
				formatStatsModeratorOnline(moderator.Online),
				formatStatsCount(moderator.LoginCount, "login", "logins"),
				formatStatsCount(moderator.PostsCreated, "post", "posts"),
				formatStatsDuration(moderator.TotalOnlineSeconds),
				formatStatsModeratorLastActivity(moderator.LastVisitDay))
		}
	}
	return b.String()
}

func statsBoardModeratorActivitySummary(activity []statsBoardModeratorActivity) (boardCount, assignmentCount, onlineCount, dormantCount int) {
	for _, entry := range activity {
		boardCount++
		for _, moderator := range entry.Moderators {
			assignmentCount++
			if moderator.Online {
				onlineCount++
			}
			if strings.TrimSpace(moderator.LastVisitDay) == "" {
				dormantCount++
			}
		}
	}
	return boardCount, assignmentCount, onlineCount, dormantCount
}

func formatStatsBoardModeratorHistoryBody(dateLabel string, terms []projections.BoardModeratorTerm) string {
	activeCount := 0
	for _, term := range terms {
		if term.Active {
			activeCount++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Board moderator tenure history %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Public moderator terms: %d\n", len(terms))
	fmt.Fprintf(&b, "- Active terms: %d\n", activeCount)
	fmt.Fprintf(&b, "- Closed terms: %d\n\n", len(terms)-activeCount)

	b.WriteString("## Public board moderator terms\n")
	if len(terms) == 0 {
		b.WriteString("- No public board moderator terms are recorded.\n")
		return b.String()
	}
	for i, term := range terms {
		fmt.Fprintf(&b, "%d. %s (%s) / %s: position %d, %s",
			i+1,
			term.BoardName,
			term.BoardID,
			term.Name,
			term.Position,
			formatStatsModeratorTermWindow(term))
		if by := strings.TrimSpace(term.AppointedByName); by != "" {
			fmt.Fprintf(&b, ", appointed by %s", by)
		}
		if by := strings.TrimSpace(term.RemovedByName); by != "" {
			fmt.Fprintf(&b, ", removed by %s", by)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatStatsModeratorTermWindow(term projections.BoardModeratorTerm) string {
	started := formatStatsTermDate(term.StartedAt)
	if term.Active {
		return "active since " + started
	}
	return "served " + started + " to " + formatStatsTermDate(term.EndedAt)
}

func formatStatsTermDate(ts int64) string {
	if ts <= 0 {
		return "unknown"
	}
	return time.UnixMilli(ts).UTC().Format("2006-01-02")
}

func formatStatsModeratorOnline(online bool) string {
	if online {
		return "online"
	}
	return "offline"
}

func formatStatsModeratorLastActivity(day string) string {
	day = strings.TrimSpace(day)
	if day == "" {
		return "not recorded"
	}
	return day
}

func formatStatsBoardActivityHistoryBody(dateLabel string, stats *projections.CommunityStats, boards []projections.BoardRanking, history []projections.CommunityStatHistory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Board activity history %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Total boards: %d\n", stats.TotalBoards)
	fmt.Fprintf(&b, "- Total threads: %d\n", stats.TotalThreads)
	fmt.Fprintf(&b, "- Total posts: %d\n", stats.TotalPosts)
	fmt.Fprintf(&b, "- Ranked public boards: %d\n", len(boards))
	fmt.Fprintf(&b, "- Event head: %d\n\n", stats.HeadSeq)

	b.WriteString("## Top public boards\n")
	if len(boards) == 0 {
		b.WriteString("- No public board activity yet.\n")
	}
	for i, board := range boards {
		lastActivity := "no posts yet"
		if board.LastPostAt > 0 {
			lastActivity = time.UnixMilli(board.LastPostAt).UTC().Format("2006-01-02 15:04") + " UTC"
		}
		fmt.Fprintf(&b, "%d. %s (%s): %d posts, %d threads, last activity %s\n", i+1, board.Name, board.ID, board.PostCount, board.ThreadCount, lastActivity)
	}

	b.WriteString("\n## Recent board activity history\n")
	if len(history) == 0 {
		b.WriteString("- No daily stat history yet.\n")
	}
	for _, day := range history {
		fmt.Fprintf(&b, "- %s: %d boards%s, %d threads%s, %d posts%s, %d reactions%s\n",
			day.Day,
			day.TotalBoards,
			formatStatsDelta(day.DeltaBoards),
			day.TotalThreads,
			formatStatsDelta(day.DeltaThreads),
			day.TotalPosts,
			formatStatsDelta(day.DeltaPosts),
			day.TotalReactions,
			formatStatsDelta(day.DeltaReactions))
	}
	return b.String()
}

func formatStatsBoardRankListBody(dateLabel string, boards []projections.BoardRanking) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Board popularity list %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Ranked public boards: %d\n", len(boards))
	onlineTotal := 0
	for _, board := range boards {
		onlineTotal += board.OnlineUsers
	}
	fmt.Fprintf(&b, "- Users currently on ranked boards: %d\n\n", onlineTotal)

	b.WriteString("## Public board ranking\n")
	if len(boards) == 0 {
		b.WriteString("- No public board activity yet.\n")
		return b.String()
	}
	for i, board := range boards {
		lastActivity := "no posts yet"
		if board.LastPostAt > 0 {
			lastActivity = time.UnixMilli(board.LastPostAt).UTC().Format("2006-01-02 15:04") + " UTC"
		}
		fmt.Fprintf(&b, "%d. %s (%s): %d posts, %d threads, %d users online, %d moderators, last activity %s\n",
			i+1,
			board.Name,
			board.ID,
			board.PostCount,
			board.ThreadCount,
			board.OnlineUsers,
			board.ModeratorCount,
			lastActivity)
		if strings.TrimSpace(board.Description) != "" {
			fmt.Fprintf(&b, "   - %s\n", board.Description)
		}
	}
	return b.String()
}

func formatStatsNewBoardListBody(dateLabel string, boards []projections.BoardSummary, startAt, endAt int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# New board list %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Window: %s to %s UTC\n", time.UnixMilli(startAt).UTC().Format("2006-01-02"), time.UnixMilli(endAt).UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "- New public boards: %d\n\n", len(boards))

	if len(boards) == 0 {
		b.WriteString("- No public boards opened in this 30-day window.\n")
		return b.String()
	}
	b.WriteString("## Newly opened public boards\n")
	for i, board := range boards {
		created := "unknown"
		if board.CreatedAt > 0 {
			created = time.UnixMilli(board.CreatedAt).UTC().Format("2006-01-02")
		}
		lastActivity := "no posts yet"
		if board.LastSeq > 0 {
			lastActivity = fmt.Sprintf("event seq %d", board.LastSeq)
		}
		fmt.Fprintf(&b, "%d. %s (%s): opened %s, %d threads, %d posts, %d moderators, %s\n",
			i+1,
			board.Name,
			board.ID,
			created,
			board.ThreadCount,
			board.PostCount,
			board.ModeratorCount,
			lastActivity)
		if strings.TrimSpace(board.Description) != "" {
			fmt.Fprintf(&b, "   - %s\n", board.Description)
		}
	}
	return b.String()
}

func formatStatsRecommendedBoardListBody(dateLabel string, boards []projections.RecommendedBoard) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Recommended board list %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Recommended public boards: %d\n", len(boards))
	onlineTotal := 0
	for _, board := range boards {
		onlineTotal += board.OnlineUsers
	}
	fmt.Fprintf(&b, "- Users currently on recommended boards: %d\n\n", onlineTotal)

	if len(boards) == 0 {
		b.WriteString("- No public boards are currently recommended.\n")
		return b.String()
	}
	b.WriteString("## Recommended public boards\n")
	for i, board := range boards {
		lastActivity := "no posts yet"
		if board.LastPostAt > 0 {
			lastActivity = time.UnixMilli(board.LastPostAt).UTC().Format("2006-01-02 15:04") + " UTC"
		}
		fmt.Fprintf(&b, "%d. %s (%s): %d posts, %d threads, %d users online, %d moderators, last activity %s\n",
			i+1,
			board.Name,
			board.ID,
			board.PostCount,
			board.ThreadCount,
			board.OnlineUsers,
			board.ModeratorCount,
			lastActivity)
		if strings.TrimSpace(board.Description) != "" {
			fmt.Fprintf(&b, "   - %s\n", board.Description)
		}
		if strings.TrimSpace(board.Note) != "" {
			fmt.Fprintf(&b, "   - Curator note: %s\n", board.Note)
		}
		if strings.TrimSpace(board.CuratedByName) != "" {
			fmt.Fprintf(&b, "   - Curated by %s\n", board.CuratedByName)
		}
	}
	return b.String()
}

func formatStatsRecommendedArticleListBody(dateLabel string, entries []projections.DigestEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Recommended article list %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Recommended public articles: %d\n\n", len(entries))

	if len(entries) == 0 {
		b.WriteString("- No public articles are currently recommended.\n")
		return b.String()
	}
	b.WriteString("## Recommended public articles\n")
	for i, entry := range entries {
		boardName := strings.TrimSpace(entry.BoardName)
		if boardName == "" {
			boardName = entry.BoardID
		}
		title := strings.TrimSpace(entry.Title)
		if title == "" {
			title = "(untitled)"
		}
		source := entry.TargetKind
		if source == "" {
			source = "article"
		}
		updated := "unknown"
		if entry.UpdatedAt > 0 {
			updated = time.UnixMilli(entry.UpdatedAt).UTC().Format("2006-01-02 15:04") + " UTC"
		}
		fmt.Fprintf(&b, "%d. %s / %s: %s recommendation, updated %s\n",
			i+1,
			boardName,
			title,
			source,
			updated)
		if strings.TrimSpace(entry.Author) != "" {
			fmt.Fprintf(&b, "   - Author: %s\n", entry.Author)
		}
		if strings.TrimSpace(entry.Path) != "" {
			fmt.Fprintf(&b, "   - Path: %s\n", entry.Path)
		}
		if strings.TrimSpace(entry.Note) != "" {
			fmt.Fprintf(&b, "   - Curator note: %s\n", entry.Note)
		}
		if strings.TrimSpace(entry.CreatedByName) != "" {
			fmt.Fprintf(&b, "   - Curated by %s\n", entry.CreatedByName)
		}
		if excerpt := strings.Join(strings.Fields(entry.Excerpt), " "); excerpt != "" {
			fmt.Fprintf(&b, "   - Excerpt: %s\n", excerpt)
		}
	}
	return b.String()
}

func formatStatsHotTopicHistoryBody(dateLabel string, stats *projections.CommunityStats, threads []projections.ThreadRanking, categories []projections.Category) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Hot topic history %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Total threads: %d\n", stats.TotalThreads)
	fmt.Fprintf(&b, "- Total posts: %d\n", stats.TotalPosts)
	fmt.Fprintf(&b, "- Ranked public hot topics: %d\n", len(threads))
	fmt.Fprintf(&b, "- Event head: %d\n\n", stats.HeadSeq)

	b.WriteString("## Top public hot topics\n")
	if len(threads) == 0 {
		b.WriteString("- No public hot topics yet.\n")
	}
	for i, thread := range threads {
		lastActivity := "no posts yet"
		if thread.UpdatedAt > 0 {
			lastActivity = time.UnixMilli(thread.UpdatedAt).UTC().Format("2006-01-02 15:04") + " UTC"
		}
		fmt.Fprintf(&b, "%d. %s / %s: %d participants, %d posts, %d reactions, score %d, last activity %s\n",
			i+1,
			thread.BoardName,
			thread.Title,
			thread.ParticipantCount,
			thread.PostCount,
			thread.ReactionCount,
			thread.Score,
			lastActivity)
	}
	formatStatsCategoryHotTopicGroups(&b, threads, categories, "Category hot topics", 10)
	return b.String()
}

func formatStatsBlessingListBody(dateLabel string, rankings []projections.BlessingRanking, recent []projections.Blessing, startAt, endAt int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Daily blessing list %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Window: %s to %s UTC\n", time.UnixMilli(startAt).UTC().Format("2006-01-02"), time.UnixMilli(endAt).UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "- Blessed users: %d\n", len(rankings))
	fmt.Fprintf(&b, "- Recent blessings: %d\n\n", len(recent))

	b.WriteString("## Top blessed users\n")
	if len(rankings) == 0 {
		b.WriteString("- No blessings recorded for this day.\n")
	}
	for i, ranking := range rankings {
		lastBlessed := "unknown"
		if ranking.LastBlessedAt > 0 {
			lastBlessed = time.UnixMilli(ranking.LastBlessedAt).UTC().Format("2006-01-02 15:04") + " UTC"
		}
		fmt.Fprintf(&b, "%d. %s: %d blessings, last blessed %s\n", i+1, ranking.Name, ranking.BlessingCount, lastBlessed)
	}

	b.WriteString("\n## Recent blessing messages\n")
	if len(recent) == 0 {
		b.WriteString("- No blessing messages for this day.\n")
	}
	for i, blessing := range recent {
		created := "unknown"
		if blessing.CreatedAt > 0 {
			created = time.UnixMilli(blessing.CreatedAt).UTC().Format("2006-01-02 15:04") + " UTC"
		}
		message := strings.TrimSpace(blessing.Message)
		if message == "" {
			message = "public blessing"
		}
		fmt.Fprintf(&b, "%d. %s -> %s at %s: %s\n", i+1, blessing.FromName, blessing.ToName, created, message)
	}
	return b.String()
}

func formatStatsHotTopicPeriodHistoryBody(spec statsPeriodHistorySpec, threads []projections.ThreadRanking, categories []projections.Category) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", spec.Title)
	fmt.Fprintf(&b, "- Period: %s to %s\n", spec.StartDay, spec.EndDay)
	fmt.Fprintf(&b, "- Ranked public hot topics: %d\n\n", len(threads))

	b.WriteString("## Top public hot topics\n")
	if len(threads) == 0 {
		b.WriteString("- No public hot topics were active in this completed period.\n")
	}
	for i, thread := range threads {
		lastActivity := "no period activity"
		if thread.UpdatedAt > 0 {
			lastActivity = time.UnixMilli(thread.UpdatedAt).UTC().Format("2006-01-02 15:04") + " UTC"
		}
		fmt.Fprintf(&b, "%d. %s / %s: %d participants, %d period posts, %d reactions, period score %d, last period activity %s\n",
			i+1,
			thread.BoardName,
			thread.Title,
			thread.ParticipantCount,
			thread.PostCount,
			thread.ReactionCount,
			thread.Score,
			lastActivity)
	}
	formatStatsCategoryHotTopicGroups(&b, threads, categories, "Category period hot topics", 10)
	return b.String()
}

type statsCategoryHotTopicGroup struct {
	ID      string
	Name    string
	Order   int
	Threads []projections.ThreadRanking
}

func formatStatsCategoryHotTopicGroups(b *strings.Builder, threads []projections.ThreadRanking, categories []projections.Category, title string, perCategory int) {
	groups := statsCategoryHotTopicGroups(threads, categories, perCategory)
	fmt.Fprintf(b, "\n## %s\n", title)
	if len(groups) == 0 {
		b.WriteString("- No category hot topics yet.\n")
		return
	}
	for _, group := range groups {
		fmt.Fprintf(b, "\n### %s\n", group.Name)
		for i, thread := range group.Threads {
			lastActivity := "no activity"
			if thread.UpdatedAt > 0 {
				lastActivity = time.UnixMilli(thread.UpdatedAt).UTC().Format("2006-01-02 15:04") + " UTC"
			}
			fmt.Fprintf(b, "%d. %s / %s: %d participants, %d posts, %d reactions, score %d, last activity %s\n",
				i+1,
				thread.BoardName,
				thread.Title,
				thread.ParticipantCount,
				thread.PostCount,
				thread.ReactionCount,
				thread.Score,
				lastActivity)
		}
	}
}

func statsCategoryHotTopicGroups(threads []projections.ThreadRanking, categories []projections.Category, perCategory int) []statsCategoryHotTopicGroup {
	if perCategory <= 0 {
		perCategory = 10
	}
	categoryByID := map[string]projections.Category{}
	categoryOrder := map[string]int{}
	for i, category := range categories {
		categoryByID[category.ID] = category
		categoryOrder[category.ID] = i
	}
	groupsByID := map[string]*statsCategoryHotTopicGroup{}
	for _, thread := range threads {
		category := statsRootCategoryForBoard(thread.Board, categoryByID)
		id := category.ID
		name := category.Name
		if id == "" {
			id = thread.Board
			name = thread.BoardName
		}
		if strings.TrimSpace(name) == "" {
			name = id
		}
		group := groupsByID[id]
		if group == nil {
			order, ok := categoryOrder[id]
			if !ok {
				order = len(categoryOrder) + len(groupsByID)
			}
			group = &statsCategoryHotTopicGroup{ID: id, Name: name, Order: order}
			groupsByID[id] = group
		}
		if len(group.Threads) < perCategory {
			group.Threads = append(group.Threads, thread)
		}
	}
	out := make([]statsCategoryHotTopicGroup, 0, len(groupsByID))
	for _, group := range groupsByID {
		out = append(out, *group)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func statsRootCategoryForBoard(boardID string, categoryByID map[string]projections.Category) projections.Category {
	current, ok := categoryByID[boardID]
	if !ok {
		return projections.Category{}
	}
	seen := map[string]bool{current.ID: true}
	for strings.TrimSpace(current.ParentID) != "" {
		parent, ok := categoryByID[current.ParentID]
		if !ok || seen[parent.ID] {
			break
		}
		current = parent
		seen[current.ID] = true
	}
	return current
}

func formatStatsPeriodHistoryBody(spec statsPeriodHistorySpec, history []projections.CommunityStatHistory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", spec.Title)
	fmt.Fprintf(&b, "- Period: %s to %s\n", spec.StartDay, spec.EndDay)
	fmt.Fprintf(&b, "- Days captured: %d\n", len(history))
	if len(history) == 0 {
		b.WriteString("\n- No daily stat history exists for this completed period yet.\n")
		return b.String()
	}

	newest := history[0]
	var posts, threads, boards, users, reactions, mail, messages, logins, logouts int
	var webLogins, webLogouts, guestLogins, guestLogouts int
	var onlineSeconds int64
	var guestDelta int
	maxOnlineUsers := newest.MaxOnlineUsers
	maxOnlineAt := newest.MaxOnlineAt
	maxOnlineGuests := newest.MaxOnlineGuests
	maxOnlineGuestsAt := newest.MaxOnlineGuestsAt
	for _, day := range history {
		posts += day.DeltaPosts
		threads += day.DeltaThreads
		boards += day.DeltaBoards
		users += day.DeltaUsers
		reactions += day.DeltaReactions
		mail += day.DeltaMail
		messages += day.DeltaDirectMessages
		logins += day.DeltaLogins
		logouts += day.DeltaLogouts
		webLogins += day.DeltaWebLogins
		webLogouts += day.DeltaWebLogouts
		guestLogins += day.DeltaGuestLogins
		guestLogouts += day.DeltaGuestLogouts
		onlineSeconds += day.DeltaOnlineSeconds
		guestDelta += day.DeltaGuests
		if day.MaxOnlineUsers > maxOnlineUsers {
			maxOnlineUsers = day.MaxOnlineUsers
			maxOnlineAt = day.MaxOnlineAt
		}
		if day.MaxOnlineGuests > maxOnlineGuests {
			maxOnlineGuests = day.MaxOnlineGuests
			maxOnlineGuestsAt = day.MaxOnlineGuestsAt
		}
	}

	b.WriteString("\n## Period totals\n")
	fmt.Fprintf(&b, "- New users: %d\n", users)
	fmt.Fprintf(&b, "- New boards: %d\n", boards)
	fmt.Fprintf(&b, "- New threads: %d\n", threads)
	fmt.Fprintf(&b, "- New posts: %d\n", posts)
	fmt.Fprintf(&b, "- New reactions: %d\n", reactions)
	fmt.Fprintf(&b, "- New mail messages: %d\n", mail)
	fmt.Fprintf(&b, "- New direct messages: %d\n", messages)
	fmt.Fprintf(&b, "- Logins: %d\n", logins)
	fmt.Fprintf(&b, "- Logouts: %d\n", logouts)
	fmt.Fprintf(&b, "- Web logins: %d\n", webLogins)
	fmt.Fprintf(&b, "- Web logouts: %d\n", webLogouts)
	fmt.Fprintf(&b, "- Guest logins: %d\n", guestLogins)
	fmt.Fprintf(&b, "- Guest logouts: %d\n", guestLogouts)
	fmt.Fprintf(&b, "- Online time added: %s\n", formatStatsDuration(onlineSeconds))
	fmt.Fprintf(&b, "- Guest delta: %+d\n", guestDelta)
	fmt.Fprintf(&b, "- Ending users: %d\n", newest.TotalUsers)
	fmt.Fprintf(&b, "- Ending boards: %d\n", newest.TotalBoards)
	fmt.Fprintf(&b, "- Ending threads: %d\n", newest.TotalThreads)
	fmt.Fprintf(&b, "- Ending posts: %d\n", newest.TotalPosts)
	fmt.Fprintf(&b, "- Peak online users: %d", maxOnlineUsers)
	if maxOnlineAt > 0 {
		fmt.Fprintf(&b, " at %s UTC", time.UnixMilli(maxOnlineAt).UTC().Format("2006-01-02 15:04"))
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "- Peak online guests: %d", maxOnlineGuests)
	if maxOnlineGuestsAt > 0 {
		fmt.Fprintf(&b, " at %s UTC", time.UnixMilli(maxOnlineGuestsAt).UTC().Format("2006-01-02 15:04"))
	}
	b.WriteString("\n\n## Daily rows\n")
	for _, day := range history {
		fmt.Fprintf(&b, "- %s: %d posts%s, %d threads%s, %d boards%s, %d users%s, %s%s, %s%s, web %d in%s/%d out%s, guests %d in%s/%d out%s, %d reactions%s, %s online time%s\n",
			day.Day,
			day.TotalPosts,
			formatStatsDelta(day.DeltaPosts),
			day.TotalThreads,
			formatStatsDelta(day.DeltaThreads),
			day.TotalBoards,
			formatStatsDelta(day.DeltaBoards),
			day.TotalUsers,
			formatStatsDelta(day.DeltaUsers),
			formatStatsCount(day.TotalLogins, "login", "logins"),
			formatStatsDelta(day.DeltaLogins),
			formatStatsCount(day.TotalLogouts, "logout", "logouts"),
			formatStatsDelta(day.DeltaLogouts),
			day.TotalWebLogins,
			formatStatsDelta(day.DeltaWebLogins),
			day.TotalWebLogouts,
			formatStatsDelta(day.DeltaWebLogouts),
			day.TotalGuestLogins,
			formatStatsDelta(day.DeltaGuestLogins),
			day.TotalGuestLogouts,
			formatStatsDelta(day.DeltaGuestLogouts),
			day.TotalReactions,
			formatStatsDelta(day.DeltaReactions),
			formatStatsDuration(day.TotalOnlineSeconds),
			formatStatsDurationDelta(day.DeltaOnlineSeconds))
	}
	return b.String()
}

func formatStatsDelta(value int) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf(" (%+d)", value)
}

func formatStatsCount(value int, singular, plural string) string {
	word := plural
	if value == 1 {
		word = singular
	}
	return fmt.Sprintf("%d %s", value, word)
}

func formatStatsDurationDelta(value int64) string {
	if value == 0 {
		return ""
	}
	sign := "+"
	if value < 0 {
		sign = "-"
		value = -value
	}
	return fmt.Sprintf(" (%s%s)", sign, formatStatsDuration(value))
}

func formatStatsDuration(seconds int64) string {
	if seconds < 0 {
		seconds = -seconds
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	minutes = minutes % 60
	if hours < 24 {
		if minutes == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	days := hours / 24
	hours = hours % 24
	if hours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, hours)
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

func boardPermissionAllowed(ok bool, err error) bool {
	return err == nil && ok
}

func (h *Handler) actorCanModerateBoard(actor *User, boardID string) bool {
	return boardPermissionAllowed(projections.ActorCanModerateBoard(h.db, actor, boardID))
}

func (h *Handler) actorCanUseMemberBoard(actor *User, boardID string) bool {
	return boardPermissionAllowed(projections.ActorCanUseMemberBoard(h.db, actor, boardID))
}

func (h *Handler) actorCanManageBoardMembers(actor *User, boardID string) bool {
	return boardPermissionAllowed(projections.ActorCanManageBoardMembers(h.db, actor, boardID))
}

func (h *Handler) actorCanSetBoardSettings(actor *User, boardID string) bool {
	return boardPermissionAllowed(projections.ActorCanSetBoardSettings(h.db, actor, boardID))
}

func (h *Handler) actorCanCurateBoard(actor *User, boardID string) bool {
	return boardPermissionAllowed(projections.ActorCanCurateBoard(h.db, actor, boardID))
}

func (h *Handler) actorCanModerateBoardThreads(actor *User, boardID string) bool {
	return boardPermissionAllowed(projections.ActorCanModerateBoardThreads(h.db, actor, boardID))
}

func (h *Handler) actorCanManageBoardPolls(actor *User, boardID string) bool {
	return boardPermissionAllowed(projections.ActorCanManageBoardPolls(h.db, actor, boardID))
}

func (h *Handler) actorCanCurateBoardKind(actor *User, boardID, kind string) bool {
	return boardPermissionAllowed(projections.ActorCanCurateBoardKind(h.db, actor, boardID, kind))
}

func (h *Handler) actorCanModerateBoardPostsTx(tx *sql.Tx, actor *User, boardID string) bool {
	return boardPermissionAllowed(projections.ActorCanModerateBoardPosts(tx, actor, boardID))
}

func (h *Handler) actorCanModerateBoardThreadsTx(tx *sql.Tx, actor *User, boardID string) bool {
	return boardPermissionAllowed(projections.ActorCanModerateBoardThreads(tx, actor, boardID))
}

func (h *Handler) requireFavoriteFolder(userID, folderID string) Reply {
	exists, err := projections.FavoriteFolderExists(h.db, userID, folderID)
	if err != nil {
		return internalErr(err)
	}
	if !exists {
		return Reply{Err: errDetail(proto.ErrNotFound, "favorite folder not found", false)}
	}
	return Reply{}
}

func (h *Handler) requireBoard(boardID string) Reply {
	exists, err := projections.BoardExists(h.db, boardID)
	if err != nil {
		return internalErr(err)
	}
	if !exists {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}
	return Reply{}
}

func (h *Handler) requirePost(postID string) Reply {
	exists, err := projections.PostExists(h.db, postID)
	if err != nil {
		return internalErr(err)
	}
	if !exists {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	return Reply{}
}

func (h *Handler) requireThread(threadID string) Reply {
	exists, err := projections.ThreadExists(h.db, threadID)
	if err != nil {
		return internalErr(err)
	}
	if !exists {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	return Reply{}
}
