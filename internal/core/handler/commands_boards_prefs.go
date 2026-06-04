package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func (h *Handler) setBoardFavorite(actor *User, p proto.SetBoardFavoritePayload) Reply {
	if p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if p.Favorite {
		if errReply := h.requireFavoriteFolder(actor.ID, p.FolderID); errReply.Err != nil {
			return errReply
		}
	}
	if err := setBoardFavorite(h.db, actor.ID, p.Board, p.FolderID, p.Position, p.Favorite); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) createFavoriteFolder(actor *User, p proto.CreateFavoriteFolderPayload) Reply {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "name is required", false)}
	}
	if len(name) > 80 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "folder name must be 80 characters or less", false)}
	}
	if errReply := h.requireFavoriteFolder(actor.ID, p.ParentID); errReply.Err != nil {
		return errReply
	}
	folderID := newID("favfld_")
	if err := createFavoriteFolder(h.db, actor.ID, folderID, p.ParentID, name, p.Position); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: folderID}}
}

func (h *Handler) updateFavoriteFolder(actor *User, p proto.UpdateFavoriteFolderPayload) Reply {
	if p.Folder == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "folder is required", false)}
	}
	name := strings.TrimSpace(p.Name)
	if len(name) > 80 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "folder name must be 80 characters or less", false)}
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
		if h.favoriteFolderContains(actor.ID, p.Folder, *p.ParentID) {
			return Reply{Err: errDetail(proto.ErrValidationFailed, "folder cannot move under its descendant", false)}
		}
	}
	if err := updateFavoriteFolder(h.db, actor.ID, p.Folder, name, p.ParentID, p.Position); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Folder}}
}

func (h *Handler) deleteFavoriteFolder(actor *User, p proto.DeleteFavoriteFolderPayload) Reply {
	if p.Folder == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "folder is required", false)}
	}
	if errReply := h.requireFavoriteFolder(actor.ID, p.Folder); errReply.Err != nil {
		return errReply
	}
	if err := deleteFavoriteFolder(h.db, actor.ID, p.Folder); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Folder}}
}

func (h *Handler) moveBoardFavorite(actor *User, p proto.MoveBoardFavoritePayload) Reply {
	if p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if errReply := h.requireFavoriteFolder(actor.ID, p.FolderID); errReply.Err != nil {
		return errReply
	}
	if err := moveBoardFavorite(h.db, actor.ID, p.Board, p.FolderID, p.Position); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) importFavoriteTree(actor *User, p proto.ImportFavoriteTreePayload) Reply {
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
	if err := importFavoriteTree(h.db, actor.ID, tree, replace); err != nil {
		return Reply{Err: errDetail(proto.ErrValidationFailed, err.Error(), false)}
	}
	return Reply{Result: &proto.AckResult{}}
}

func (h *Handler) setBoardSettings(actor *User, p proto.SetBoardSettingsPayload) Reply {
	if p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if !h.actorCanSetBoardSettings(actor, p.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board settings permission required", false)}
	}
	patch := BoardSettingsPatch{
		AnonymousAllowed:   p.AnonymousAllowed,
		ReadOnly:           p.ReadOnly,
		NoReply:            p.NoReply,
		AttachmentsAllowed: p.AttachmentsAllowed,
		MailInAllowed:      p.MailInAllowed,
		RelayEnabled:       p.RelayEnabled,
		MemberReadMode:     p.MemberReadMode,
		MemberPostMode:     p.MemberPostMode,
		StatsExcluded:      p.StatsExcluded,
	}
	if err := setBoardSettings(h.db, p.Board, patch); err != nil {
		return internalErr(err)
	}
	settingLines := boardSettingsAuditLines(p)
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
	boardID := strings.TrimSpace(p.Board)
	if boardID == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if errReply := h.requireBoard(boardID); errReply.Err != nil {
		return errReply
	}
	if p.Position != nil && *p.Position < 0 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "position cannot be negative", false)}
	}
	note := strings.TrimSpace(p.Note)
	if len(note) > 500 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "recommendation note must be 500 characters or less", false)}
	}
	if p.Recommended {
		if ok, reason, err := h.boardCanBePubliclyRecommended(boardID); err != nil {
			return internalErr(err)
		} else if !ok {
			return Reply{Err: errDetail(proto.ErrValidationFailed, reason, false)}
		}
	}
	if err := setRecommendedBoard(h.db, boardID, note, actor.ID, p.Position, p.Recommended); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: boardID}}
}

func (h *Handler) boardCanBePubliclyRecommended(boardID string) (bool, string, error) {
	if generatedSystemBoardIDSet[boardID] {
		return false, "generated system boards cannot be recommended", nil
	}
	var visibility string
	var memberReadMode, statsExcluded int
	err := qQueryRow(
		h.db,
		`SELECT COALESCE(c.visibility, 'public'),
		        COALESCE(s.member_read_mode, 0),
		        COALESCE(s.stats_excluded, 0)
		   FROM boards b
		   LEFT JOIN categories c ON c.id=b.id
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  WHERE b.id=?`,
		boardID,
	).Scan(&visibility, &memberReadMode, &statsExcluded)
	if err != nil {
		return false, "", err
	}
	if strings.ToLower(strings.TrimSpace(visibility)) != "public" {
		return false, "only public directory boards can be recommended", nil
	}
	if memberReadMode != 0 {
		return false, "member-read boards cannot be publicly recommended", nil
	}
	if statsExcluded != 0 {
		return false, "stats-excluded boards cannot be publicly recommended", nil
	}
	return true, "", nil
}

var generatedSystemBoardIDSet = map[string]bool{
	"0announce":       true,
	"0moderation":     true,
	"BBSLists":        true,
	"Blessing":        true,
	"Filter":          true,
	"Goodbye":         true,
	"GiveupNotice":    true,
	"Recommend":       true,
	"Registry":        true,
	"bbsnet":          true,
	"denypost":        true,
	"newcomers":       true,
	"notepad":         true,
	"reject_registry": true,
	"sysmail":         true,
	"syssecurity":     true,
	"undenypost":      true,
	"vote":            true,
}

func (h *Handler) setBoardModerator(actor *User, p proto.SetBoardModeratorPayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	if p.Board == "" || p.User == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board and user are required", false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	userID, userName, errReply := h.resolveUserRef(p.User)
	if errReply.Err != nil {
		return errReply
	}
	if err := setBoardModerator(h.db, p.Board, userID, p.Moderator, p.Position); err != nil {
		return internalErr(err)
	}
	action := "moderator removed"
	if p.Moderator {
		action = "moderator appointed"
	}
	if err := h.ensureSyssecuritySystemPost(actor, "Board "+action+": "+p.Board, []string{
		"Action: board " + action,
		"Board: " + p.Board,
		"User: " + userName,
		"Actor: " + actor.Name,
	}, p.Board); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) setBoardMember(actor *User, p proto.SetBoardMemberPayload) Reply {
	if p.Board == "" || p.User == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board and user are required", false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	canModerateBoard := h.actorCanModerateBoard(actor, p.Board)
	canManageMembers := h.actorCanManageBoardMembers(actor, p.Board)
	if !canModerateBoard && !canManageMembers {
		return Reply{Err: errDetail(proto.ErrForbidden, "board member manager permission required", false)}
	}
	userID, _, errReply := h.resolveUserRef(p.User)
	if errReply.Err != nil {
		return errReply
	}
	if !canModerateBoard {
		if boardMemberPermissionsChanged(p) {
			return Reply{Err: errDetail(proto.ErrForbidden, "board moderator role required to change member permissions", false)}
		}
		if h.isBoardModerator(userID, p.Board) {
			return Reply{Err: errDetail(proto.ErrForbidden, "board moderator role required to manage board moderators", false)}
		}
		privilegedMember, err := h.boardMemberHasDelegatedPermissions(p.Board, userID)
		if err != nil {
			return internalErr(err)
		}
		if privilegedMember {
			return Reply{Err: errDetail(proto.ErrForbidden, "board moderator role required to manage delegated board members", false)}
		}
	}
	title := strings.TrimSpace(p.Title)
	if len(title) > 80 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "member title must be 80 characters or less", false)}
	}
	if p.Position != nil && *p.Position < 0 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "member position cannot be negative", false)}
	}
	patch := BoardMemberPatch{
		Title:               title,
		Position:            p.Position,
		CanManageMembers:    p.CanManageMembers,
		CanCurate:           p.CanCurate,
		CanModeratePosts:    p.CanModeratePosts,
		CanModerateThreads:  p.CanModerateThreads,
		CanAnnounce:         p.CanAnnounce,
		CanManagePolls:      p.CanManagePolls,
		CanSetBoardSettings: p.CanSetBoardSettings,
	}
	if err := setBoardMember(h.db, p.Board, userID, p.Member, patch); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) setBoardMemberRequirements(actor *User, p proto.SetBoardMemberRequirementsPayload) Reply {
	if p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if !h.actorCanSetBoardSettings(actor, p.Board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board settings permission required", false)}
	}
	for _, field := range []struct {
		name  string
		value *int
	}{
		{"minLoginCount", p.MinLoginCount},
		{"minPostCount", p.MinPostCount},
		{"minTrustLevel", p.MinTrustLevel},
		{"minScore", p.MinScore},
		{"minBoardPostCount", p.MinBoardPostCount},
		{"minBoardOriginalPostCount", p.MinBoardOriginalPostCount},
		{"minBoardDigestCount", p.MinBoardDigestCount},
		{"minBoardMarkCount", p.MinBoardMarkCount},
		{"maxMembers", p.MaxMembers},
	} {
		if field.value != nil && *field.value < 0 {
			return Reply{Err: errDetail(proto.ErrValidationFailed, field.name+" must be non-negative", false)}
		}
	}
	patch := BoardMemberRequirementsPatch{
		MinLoginCount:             p.MinLoginCount,
		MinPostCount:              p.MinPostCount,
		MinTrustLevel:             p.MinTrustLevel,
		MinScore:                  p.MinScore,
		MinBoardPostCount:         p.MinBoardPostCount,
		MinBoardOriginalPostCount: p.MinBoardOriginalPostCount,
		MinBoardDigestCount:       p.MinBoardDigestCount,
		MinBoardMarkCount:         p.MinBoardMarkCount,
		MaxMembers:                p.MaxMembers,
	}
	if p.ApprovalMode != nil {
		mode, errReply := normalizeBoardMemberApprovalMode(*p.ApprovalMode)
		if errReply.Err != nil {
			return errReply
		}
		patch.ApprovalMode = &mode
	}
	if err := setBoardMemberRequirements(h.db, p.Board, patch); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) applyBoardMembership(actor *User, p proto.ApplyBoardMembershipPayload) Reply {
	if p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if h.isBoardMember(actor.ID, p.Board) {
		return Reply{Err: errDetail(proto.ErrConflict, "already a board member", false)}
	}
	status, err := h.latestBoardMembershipApplicationStatus(p.Board, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	switch status {
	case "pending":
		return Reply{Err: errDetail(proto.ErrConflict, "membership application already pending", false)}
	case "blacklisted":
		return Reply{Err: errDetail(proto.ErrForbidden, "membership application is blocked", false)}
	}
	note := strings.TrimSpace(p.Note)
	if len(note) > 500 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "application note must be 500 characters or less", false)}
	}
	requirements, err := getBoardMemberRequirements(h.db, p.Board)
	if err != nil {
		return internalErr(err)
	}
	if errReply := h.requireBoardMembershipAdmission(p.Board, actor.ID, requirements); errReply.Err != nil {
		return errReply
	}
	appID := newID("bmap_")
	if err := insertBoardMemberApplication(h.db, appID, p.Board, actor.ID, note); err != nil {
		return internalErr(err)
	}
	if requirements != nil && requirements.ApprovalMode == "auto" {
		if err := reviewBoardMemberApplication(h.db, appID, actor.ID, "approved", "", "auto-approved by board membership rules"); err != nil {
			return internalErr(err)
		}
		if err := h.ensureBoardRegistrationSystemPost(actor, appID, "approved", p.Board, actor.ID); err != nil {
			return internalErr(err)
		}
	}
	return Reply{Result: &proto.AckResult{ID: appID}}
}

func (h *Handler) reviewBoardMembership(actor *User, p proto.ReviewBoardMembershipPayload) Reply {
	if p.Application == "" || p.Status == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "application and status are required", false)}
	}
	status, errReply := normalizeMemberApplicationStatus(p.Status)
	if errReply.Err != nil {
		return errReply
	}
	var boardID, userID, currentStatus string
	err := qQueryRow(h.db, `SELECT board_id, user_id, status FROM board_member_applications WHERE id=?`, p.Application).Scan(&boardID, &userID, &currentStatus)
	if err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "membership application not found", false)}
	}
	if err != nil {
		return internalErr(err)
	}
	if currentStatus != "pending" {
		return Reply{Err: errDetail(proto.ErrConflict, "membership application is already reviewed", false)}
	}
	canModerateBoard := h.actorCanModerateBoard(actor, boardID)
	if !canModerateBoard && !h.actorCanManageBoardMembers(actor, boardID) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board member manager permission required", false)}
	}
	if !canModerateBoard && actor.ID == userID {
		return Reply{Err: errDetail(proto.ErrForbidden, "board moderator role required to review your own application", false)}
	}
	if !canModerateBoard && status == "blacklisted" {
		return Reply{Err: errDetail(proto.ErrForbidden, "board moderator role required to blacklist membership applications", false)}
	}
	title := strings.TrimSpace(p.Title)
	if len(title) > 80 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "member title must be 80 characters or less", false)}
	}
	note := strings.TrimSpace(p.Note)
	if len(note) > 500 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "review note must be 500 characters or less", false)}
	}
	if status == "approved" {
		requirements, err := getBoardMemberRequirements(h.db, boardID)
		if err != nil {
			return internalErr(err)
		}
		if errReply := h.requireBoardMembershipAdmission(boardID, userID, requirements); errReply.Err != nil {
			return errReply
		}
	}
	if err := reviewBoardMemberApplication(h.db, p.Application, actor.ID, status, title, note); err != nil {
		return internalErr(err)
	}
	if err := h.ensureBoardRegistrationSystemPost(actor, p.Application, status, boardID, userID); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Application}}
}

func (h *Handler) leaveBoardMembership(actor *User, p proto.LeaveBoardMembershipPayload) Reply {
	if p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if !h.isBoardMember(actor.ID, p.Board) {
		return Reply{Err: errDetail(proto.ErrNotFound, "board membership not found", false)}
	}
	if err := setBoardMember(h.db, p.Board, actor.ID, false, BoardMemberPatch{}); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) curatePost(actor *User, p proto.CuratePostPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	post, err := getPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot curate a redacted post", false)}
	}
	thread, err := getThread(h.db, post.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	kind, errReply := normalizeDigestKind(p.Kind)
	if errReply.Err != nil {
		return errReply
	}
	if !h.actorCanCurateBoardKind(actor, thread.Board, kind) {
		return Reply{Err: errDetail(proto.ErrForbidden, boardCurationPermissionMessage(kind), false)}
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = fmt.Sprintf("%s #%d", thread.Title, post.CreatedSeq)
	}
	entryID, err := upsertDigestEntry(
		h.db,
		newID("dig_"),
		thread.Board,
		"post",
		post.ID,
		kind,
		title,
		normalizeDigestPath(p.Path),
		strings.TrimSpace(p.Note),
		actor.ID,
	)
	if err != nil {
		return internalErr(err)
	}
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
	return Reply{Result: &proto.AckResult{ID: entryID}}
}

func (h *Handler) curateThread(actor *User, p proto.CurateThreadPayload) Reply {
	if p.Thread == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "thread is required", false)}
	}
	thread, err := getThread(h.db, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	kind, errReply := normalizeDigestKind(p.Kind)
	if errReply.Err != nil {
		return errReply
	}
	if !h.actorCanCurateBoardKind(actor, thread.Board, kind) {
		return Reply{Err: errDetail(proto.ErrForbidden, boardCurationPermissionMessage(kind), false)}
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = thread.Title
	}
	entryID, err := upsertDigestEntry(
		h.db,
		newID("dig_"),
		thread.Board,
		"thread",
		thread.ID,
		kind,
		title,
		normalizeDigestPath(p.Path),
		strings.TrimSpace(p.Note),
		actor.ID,
	)
	if err != nil {
		return internalErr(err)
	}
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
	return Reply{Result: &proto.AckResult{ID: entryID}}
}

func (h *Handler) removeDigestEntry(actor *User, p proto.RemoveDigestEntryPayload) Reply {
	if p.Entry == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "entry is required", false)}
	}
	entry, errReply := h.digestEntryForCuration(actor, p.Entry)
	if errReply.Err != nil {
		return errReply
	}
	if err := removeDigestEntry(h.db, p.Entry); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: entry.ID}}
}

func (h *Handler) updateDigestEntry(actor *User, p proto.UpdateDigestEntryPayload) Reply {
	if p.Entry == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "entry is required", false)}
	}
	entry, errReply := h.digestEntryForCuration(actor, p.Entry)
	if errReply.Err != nil {
		return errReply
	}
	title := entry.Title
	if p.Title != nil {
		title = strings.TrimSpace(*p.Title)
		if title == "" {
			return Reply{Err: errDetail(proto.ErrValidationFailed, "title is required", false)}
		}
	}
	path := entry.Path
	if p.Path != nil {
		path = normalizeDigestPath(*p.Path)
	}
	note := entry.Note
	if p.Note != nil {
		note = strings.TrimSpace(*p.Note)
	}
	if path != entry.Path {
		var conflictID string
		err := qQueryRow(
			h.db,
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
	if err := updateDigestEntry(h.db, entry.ID, title, path, note); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: entry.ID}}
}

func (h *Handler) setDigestEntryBody(actor *User, p proto.SetDigestEntryBodyPayload) Reply {
	if p.Entry == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "entry is required", false)}
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
	if err := setDigestEntryBody(h.db, entry.ID, body, edited); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: entry.ID}}
}

func (h *Handler) createDigestDirectory(actor *User, p proto.CreateDigestDirectoryPayload) Reply {
	boardID, kind, path, _, errReply := h.prepareDigestPathMutation(actor, p.Board, p.Kind, p.Path, "")
	if errReply.Err != nil {
		return errReply
	}
	directoryID, err := upsertDigestDirectory(h.db, newID("dir_"), boardID, kind, path, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: directoryID}}
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
	count, err := moveDigestPath(h.db, boardID, kind, fromPath, toPath)
	if err != nil {
		if errors.Is(err, projections.ErrDigestPathConflict) {
			return Reply{Err: errDetail(proto.ErrConflict, "digest path move would overwrite an existing entry", false)}
		}
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%s:%s:%d", boardID, kind, count)}}
}

func (h *Handler) copyDigestPath(actor *User, p proto.CopyDigestPathPayload) Reply {
	boardID, kind, fromPath, toPath, errReply := h.prepareDigestPathMutation(actor, p.Board, p.Kind, p.FromPath, p.ToPath)
	if errReply.Err != nil {
		return errReply
	}
	if fromPath == toPath {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "destination path must differ from source path", false)}
	}
	count, err := countDigestPathEntries(h.db, boardID, kind, fromPath)
	if err != nil {
		return internalErr(err)
	}
	dirCount, err := countDigestPathDirectories(h.db, boardID, kind, fromPath)
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
	count, err = copyDigestPath(h.db, boardID, kind, fromPath, toPath, actor.ID, ids, dirIDs)
	if err != nil {
		if errors.Is(err, projections.ErrDigestPathConflict) {
			return Reply{Err: errDetail(proto.ErrConflict, "digest path copy would overwrite an existing entry", false)}
		}
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%s:%s:%d", boardID, kind, count)}}
}

func (h *Handler) deleteDigestPath(actor *User, p proto.DeleteDigestPathPayload) Reply {
	boardID, kind, path, _, errReply := h.prepareDigestPathMutation(actor, p.Board, p.Kind, p.Path, "")
	if errReply.Err != nil {
		return errReply
	}
	count, err := deleteDigestPath(h.db, boardID, kind, path)
	if err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: fmt.Sprintf("%s:%s:%d", boardID, kind, count)}}
}

func (h *Handler) markBoardRead(actor *User, p proto.MarkBoardReadPayload) Reply {
	if p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if err := markBoardRead(h.db, actor.ID, p.Board); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) restoreBoardRead(actor *User, p proto.RestoreBoardReadPayload) Reply {
	if p.Board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if errReply := h.requireBoard(p.Board); errReply.Err != nil {
		return errReply
	}
	if err := restoreBoardRead(h.db, actor.ID, p.Board); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Board}}
}

func (h *Handler) markFavoriteFolderRead(actor *User, p proto.MarkFavoriteFolderReadPayload) Reply {
	if errReply := h.requireFavoriteFolder(actor.ID, p.Folder); errReply.Err != nil {
		return errReply
	}
	if err := markFavoriteFolderRead(h.db, actor.ID, p.Folder); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Folder}}
}

func (h *Handler) restoreFavoriteFolderRead(actor *User, p proto.RestoreFavoriteFolderReadPayload) Reply {
	if errReply := h.requireFavoriteFolder(actor.ID, p.Folder); errReply.Err != nil {
		return errReply
	}
	if err := restoreFavoriteFolderRead(h.db, actor.ID, p.Folder); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Folder}}
}

func (h *Handler) markThreadRead(actor *User, p proto.MarkThreadReadPayload) Reply {
	if p.Thread == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "thread is required", false)}
	}
	if errReply := h.requireThread(p.Thread); errReply.Err != nil {
		return errReply
	}
	if err := markThreadRead(h.db, actor.ID, p.Thread); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Thread}}
}

func (h *Handler) restoreThreadRead(actor *User, p proto.RestoreThreadReadPayload) Reply {
	if p.Thread == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "thread is required", false)}
	}
	if errReply := h.requireThread(p.Thread); errReply.Err != nil {
		return errReply
	}
	if err := restoreThreadRead(h.db, actor.ID, p.Thread); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Thread}}
}

func (h *Handler) markPostRead(actor *User, p proto.MarkPostReadPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	if errReply := h.requirePost(p.Post); errReply.Err != nil {
		return errReply
	}
	if err := markPostRead(h.db, actor.ID, p.Post); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Post}}
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
		return nil, Reply{Err: errDetail(proto.ErrForbidden, boardCurationPermissionMessage(entry.Kind), false)}
	}
	return &entry, Reply{}
}

func (h *Handler) prepareDigestPathMutation(actor *User, boardID, kind, fromPath, toPath string) (string, string, string, string, Reply) {
	boardID = strings.TrimSpace(boardID)
	if boardID == "" {
		return "", "", "", "", Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if errReply := h.requireBoard(boardID); errReply.Err != nil {
		return "", "", "", "", errReply
	}
	if strings.TrimSpace(kind) == "" {
		kind = "archive"
	}
	normalizedKind, errReply := normalizeDigestKind(kind)
	if errReply.Err != nil {
		return "", "", "", "", errReply
	}
	if !h.actorCanCurateBoardKind(actor, boardID, normalizedKind) {
		return "", "", "", "", Reply{Err: errDetail(proto.ErrForbidden, boardCurationPermissionMessage(normalizedKind), false)}
	}
	normalizedFrom := normalizeDigestPath(fromPath)
	if normalizedFrom == "" {
		return "", "", "", "", Reply{Err: errDetail(proto.ErrValidationFailed, "source path is required", false)}
	}
	return boardID, normalizedKind, normalizedFrom, normalizeDigestPath(toPath), Reply{}
}

const announcementSystemBoardID = "0announce"
const recommendSystemBoardID = "Recommend"
const statsSystemBoardID = "BBSLists"
const registrySystemBoardID = "Registry"
const rejectRegistrySystemBoardID = "reject_registry"
const syssecuritySystemBoardID = "syssecurity"
const blessingSystemBoardID = "Blessing"

type systemNoticeBoard struct {
	ID          string
	Name        string
	Description string
}

func (h *Handler) publishSystemNotice(actor *User, p proto.PublishSystemNoticePayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	board, ok := normalizeSystemNoticeBoard(p.Board)
	if !ok {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "notice board must be notepad, GiveupNotice, or bbsnet", false)}
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "title is required", false)}
	}
	if len(title) > 160 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "title must be 160 characters or less", false)}
	}
	body := strings.TrimSpace(p.Body)
	if body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "body is required", false)}
	}
	if len(body) > 20000 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "body must be 20000 characters or less", false)}
	}
	source := strings.TrimSpace(p.Source)
	if len(source) > 160 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "source must be 160 characters or less", false)}
	}
	threadID, seq, err := h.appendSystemNoticePost(actor, board, title, body, source, nowMS())
	if err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: threadID, Seq: seq}}
}

func normalizeSystemNoticeBoard(raw string) (systemNoticeBoard, bool) {
	board := strings.TrimSpace(raw)
	if board == "" || strings.EqualFold(board, "notepad") {
		return systemNoticeBoard{ID: "notepad", Name: "notepad", Description: "Generated public system notes"}, true
	}
	switch strings.ToLower(board) {
	case "giveupnotice", "giveup_notice":
		return systemNoticeBoard{ID: "GiveupNotice", Name: "GiveupNotice", Description: "Generated give-up-net notices"}, true
	case "bbsnet":
		return systemNoticeBoard{ID: "bbsnet", Name: "bbsnet", Description: "Generated site-hop and network notices"}, true
	default:
		return systemNoticeBoard{}, false
	}
}

func (h *Handler) appendSystemNoticePost(actor *User, board systemNoticeBoard, title, noticeBody, source string, ts int64) (string, int64, error) {
	threadID := newID("notice_thr_")
	postID := newID("notice_pst_")
	body := formatSystemNoticeBody(board, title, noticeBody, source, actor.Name)

	tx, err := h.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback() //nolint

	boardCreated := false
	var boardSeq int64
	var exists int
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, board.ID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, err := boardCategoryPosition(tx, "", nil)
		if err != nil {
			return "", 0, err
		}
		boardScopes := []string{"board:" + board.ID}
		boardSeq, err = appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          board.ID,
			Name:        board.Name,
			Description: board.Description,
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return "", 0, err
		}
		if err := insertBoard(tx, board.ID, board.Name, board.Description, "", position); err != nil {
			return "", 0, err
		}
		boardCreated = true
	} else if err != nil {
		return "", 0, err
	}

	scopes := []string{"board:" + board.ID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: board.ID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts,
	})
	if err != nil {
		return "", 0, err
	}
	threadScopes := append(scopes, "thread:"+threadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return "", 0, err
	}
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: board.ID, Author: actor.Name, AuthorID: actor.ID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return "", 0, err
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return "", 0, err
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return "", 0, err
	}
	if err := ftsInsertPost(tx, postID, threadID, board.ID, actor.Name, body); err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}

	if boardCreated {
		h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: []string{"board:" + board.ID},
			Payload: &proto.BoardCreatedPayload{ID: board.ID, Name: board.Name, Description: board.Description, By: actor.Name, TS: ts}, TS: ts})
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: board.ID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts})
	return threadID, pseq, nil
}

func formatSystemNoticeBody(board systemNoticeBoard, title, noticeBody, source, actorName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "- Notice board: %s\n", board.Name)
	fmt.Fprintf(&b, "- Actor: %s\n", actorName)
	if source != "" {
		fmt.Fprintf(&b, "- Source: %s\n", source)
	}
	b.WriteString("\n")
	b.WriteString(noticeBody)
	if !strings.HasSuffix(noticeBody, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\nGenerated public system notice.\n")
	return b.String()
}

func (h *Handler) ensureBlessingSystemPost(actor, target *User, blessingID, message string, ts int64) error {
	threadID := "blessing_thr_" + blessingID
	postID := "blessing_pst_" + blessingID
	var exists int
	err := qQueryRow(h.db, `SELECT 1 FROM threads WHERE id=?`, threadID).Scan(&exists)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}

	title := "Blessing: " + actor.Name + " -> " + target.Name
	body := formatBlessingSystemBody(actor.Name, target.Name, message)

	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	boardCreated := false
	var boardSeq int64
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, blessingSystemBoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, err := boardCategoryPosition(tx, "", nil)
		if err != nil {
			return err
		}
		boardScopes := []string{"board:" + blessingSystemBoardID}
		boardSeq, err = appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          blessingSystemBoardID,
			Name:        "Blessing",
			Description: "Generated blessing rituals and rankings",
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return err
		}
		if err := insertBoard(tx, blessingSystemBoardID, "Blessing", "Generated blessing rituals and rankings", "", position); err != nil {
			return err
		}
		boardCreated = true
	} else if err != nil {
		return err
	}

	scopes := []string{"board:" + blessingSystemBoardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: blessingSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts,
	})
	if err != nil {
		return err
	}
	threadScopes := append(scopes, "thread:"+threadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return err
	}
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: blessingSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return err
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return err
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return err
	}
	if err := ftsInsertPost(tx, postID, threadID, blessingSystemBoardID, actor.Name, body); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if boardCreated {
		h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: []string{"board:" + blessingSystemBoardID},
			Payload: &proto.BoardCreatedPayload{ID: blessingSystemBoardID, Name: "Blessing", Description: "Generated blessing rituals and rankings", By: actor.Name, TS: ts}, TS: ts})
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: blessingSystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts})
	return nil
}

func formatBlessingSystemBody(fromName, toName, message string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Blessing for %s\n\n", toName)
	fmt.Fprintf(&b, "- From: %s\n", fromName)
	fmt.Fprintf(&b, "- To: %s\n\n", toName)
	if strings.TrimSpace(message) == "" {
		b.WriteString("A public blessing was sent.\n")
	} else {
		b.WriteString(strings.TrimSpace(message))
		if !strings.HasSuffix(message, "\n") {
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nGenerated public blessing record.\n")
	return b.String()
}

func (h *Handler) publishStatsSnapshot(actor *User, p proto.PublishStatsSnapshotPayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	ts := nowMS()
	dateLabel, dateID, errReply := normalizeStatsSnapshotDate(p.Date, ts)
	if errReply.Err != nil {
		return errReply
	}
	threadID, seq, err := h.ensureStatsSnapshotSystemPost(actor, dateLabel, dateID, ts)
	if err != nil {
		return internalErr(err)
	}
	if _, _, err := h.ensureStatsLoginHistorySystemPost(actor, dateLabel, dateID, ts); err != nil {
		return internalErr(err)
	}
	if _, _, err := h.ensureStatsBoardActivityHistorySystemPost(actor, dateLabel, dateID, ts); err != nil {
		return internalErr(err)
	}
	if _, _, err := h.ensureStatsBoardRankListSystemPost(actor, dateLabel, dateID, ts); err != nil {
		return internalErr(err)
	}
	if _, _, err := h.ensureStatsNewBoardListSystemPost(actor, dateLabel, dateID, ts); err != nil {
		return internalErr(err)
	}
	if _, _, err := h.ensureStatsRecommendedBoardListSystemPost(actor, dateLabel, dateID, ts); err != nil {
		return internalErr(err)
	}
	if _, _, err := h.ensureStatsRecommendedArticleListSystemPost(actor, dateLabel, dateID, ts); err != nil {
		return internalErr(err)
	}
	if _, _, err := h.ensureStatsHotTopicHistorySystemPost(actor, dateLabel, dateID, ts); err != nil {
		return internalErr(err)
	}
	if _, _, err := h.ensureStatsBlessingListSystemPost(actor, dateLabel, dateID, ts); err != nil {
		return internalErr(err)
	}
	day, err := time.Parse("2006-01-02", dateLabel)
	if err != nil {
		return internalErr(err)
	}
	if err := h.ensureStatsPeriodHistorySystemPosts(actor, day, ts); err != nil {
		return internalErr(err)
	}
	if err := h.ensureStatsHotTopicPeriodHistorySystemPosts(actor, day, ts); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: threadID, Seq: seq}}
}

func normalizeStatsSnapshotDate(raw string, ts int64) (dateLabel, dateID string, reply Reply) {
	raw = strings.TrimSpace(raw)
	var day time.Time
	var err error
	if raw == "" {
		day = time.UnixMilli(ts).UTC()
	} else {
		day, err = time.Parse("2006-01-02", raw)
		if err != nil {
			return "", "", Reply{Err: errDetail(proto.ErrValidationFailed, "date must be YYYY-MM-DD", false)}
		}
	}
	return day.Format("2006-01-02"), day.Format("20060102"), Reply{}
}

func (h *Handler) ensureStatsSnapshotSystemPost(actor *User, dateLabel, dateID string, ts int64) (string, int64, error) {
	threadID := "bbslists_stats_" + dateID
	postID := "bbslists_stats_post_" + dateID
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return threadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}

	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	stats, err := projections.GetCommunityStats(h.db)
	if err != nil {
		return "", 0, err
	}
	boards, err := projections.ListBoardRankings(h.db, "", false, 5, 0)
	if err != nil {
		return "", 0, err
	}
	threads, err := projections.ListThreadRankings(h.db, "", false, "", 5, 0)
	if err != nil {
		return "", 0, err
	}
	replies, err := projections.ListReplyRankings(h.db, "", false, 5, 0)
	if err != nil {
		return "", 0, err
	}
	users, err := projections.ListUserRankings(h.db, 5, 0)
	if err != nil {
		return "", 0, err
	}
	archives, err := projections.ListArchiveRankings(h.db, "", false, "", 5, 0)
	if err != nil {
		return "", 0, err
	}
	blessings, err := projections.ListBlessingRankings(h.db, 5, 0)
	if err != nil {
		return "", 0, err
	}
	history, err := projections.ListCommunityStatHistory(h.db, 7, 0)
	if err != nil {
		return "", 0, err
	}
	body := formatStatsSnapshotBody(dateLabel, stats, boards, threads, replies, users, archives, blessings, history)
	return h.ensureStatsSystemPost(actor, threadID, postID, "Community stats "+dateLabel, body, ts)
}

func (h *Handler) ensureStatsLoginHistorySystemPost(actor *User, dateLabel, dateID string, ts int64) (string, int64, error) {
	threadID := "bbslists_countlogins_" + dateID
	postID := "bbslists_countlogins_post_" + dateID
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return threadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	stats, err := projections.GetCommunityStats(h.db)
	if err != nil {
		return "", 0, err
	}
	history, err := projections.ListCommunityStatHistory(h.db, 30, 0)
	if err != nil {
		return "", 0, err
	}
	hourly, err := projections.ListLoginHourlyStats(h.db, dateLabel)
	if err != nil {
		return "", 0, err
	}
	body := formatStatsLoginHistoryBody(dateLabel, stats, history, hourly)
	return h.ensureStatsSystemPost(actor, threadID, postID, "Login count history "+dateLabel, body, ts)
}

func (h *Handler) ensureStatsBoardActivityHistorySystemPost(actor *User, dateLabel, dateID string, ts int64) (string, int64, error) {
	threadID := "bbslists_boardlog_" + dateID
	postID := "bbslists_boardlog_post_" + dateID
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return threadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	stats, err := projections.GetCommunityStats(h.db)
	if err != nil {
		return "", 0, err
	}
	boards, err := projections.ListBoardRankings(h.db, "", false, 30, 0)
	if err != nil {
		return "", 0, err
	}
	history, err := projections.ListCommunityStatHistory(h.db, 30, 0)
	if err != nil {
		return "", 0, err
	}
	body := formatStatsBoardActivityHistoryBody(dateLabel, stats, boards, history)
	return h.ensureStatsSystemPost(actor, threadID, postID, "Board activity history "+dateLabel, body, ts)
}

func (h *Handler) ensureStatsBoardRankListSystemPost(actor *User, dateLabel, dateID string, ts int64) (string, int64, error) {
	threadID := "bbslists_boardrank_" + dateID
	postID := "bbslists_boardrank_post_" + dateID
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return threadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	boards, err := projections.ListBoardRankings(h.db, "", false, 100, 0)
	if err != nil {
		return "", 0, err
	}
	activeBoards := boards[:0]
	for _, board := range boards {
		if board.PostCount > 0 || board.ThreadCount > 0 || board.OnlineUsers > 0 {
			activeBoards = append(activeBoards, board)
		}
	}
	body := formatStatsBoardRankListBody(dateLabel, activeBoards)
	return h.ensureStatsSystemPost(actor, threadID, postID, "Board popularity list "+dateLabel, body, ts)
}

func (h *Handler) ensureStatsNewBoardListSystemPost(actor *User, dateLabel, dateID string, ts int64) (string, int64, error) {
	threadID := "bbslists_newboards_" + dateID
	postID := "bbslists_newboards_post_" + dateID
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return threadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	end, err := time.Parse("2006-01-02", dateLabel)
	if err != nil {
		return "", 0, err
	}
	endAt := end.UTC().AddDate(0, 0, 1).Add(-time.Millisecond).UnixMilli()
	startAt := end.UTC().AddDate(0, 0, -29).UnixMilli()
	boards, err := projections.ListRecentPublicBoards(h.db, startAt, endAt, 100)
	if err != nil {
		return "", 0, err
	}
	body := formatStatsNewBoardListBody(dateLabel, boards, startAt, endAt)
	return h.ensureStatsSystemPost(actor, threadID, postID, "New board list "+dateLabel, body, ts)
}

func (h *Handler) ensureStatsRecommendedBoardListSystemPost(actor *User, dateLabel, dateID string, ts int64) (string, int64, error) {
	threadID := "bbslists_rcmdbrd_" + dateID
	postID := "bbslists_rcmdbrd_post_" + dateID
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return threadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	boards, err := projections.ListRecommendedBoards(h.db, 100, 0)
	if err != nil {
		return "", 0, err
	}
	body := formatStatsRecommendedBoardListBody(dateLabel, boards)
	return h.ensureStatsSystemPost(actor, threadID, postID, "Recommended board list "+dateLabel, body, ts)
}

func (h *Handler) ensureStatsRecommendedArticleListSystemPost(actor *User, dateLabel, dateID string, ts int64) (string, int64, error) {
	threadID := "bbslists_commend_" + dateID
	postID := "bbslists_commend_post_" + dateID
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return threadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	entries, err := projections.ListPublicRecommendedDigestEntries(h.db, 100, 0)
	if err != nil {
		return "", 0, err
	}
	body := formatStatsRecommendedArticleListBody(dateLabel, entries)
	return h.ensureStatsSystemPost(actor, threadID, postID, "Recommended article list "+dateLabel, body, ts)
}

func (h *Handler) ensureStatsHotTopicHistorySystemPost(actor *User, dateLabel, dateID string, ts int64) (string, int64, error) {
	threadID := "bbslists_toplog_" + dateID
	postID := "bbslists_toplog_post_" + dateID
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return threadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	stats, err := projections.GetCommunityStats(h.db)
	if err != nil {
		return "", 0, err
	}
	threads, err := projections.ListThreadRankings(h.db, "", false, "", 30, 0)
	if err != nil {
		return "", 0, err
	}
	categories, err := projections.ListCategories(h.db)
	if err != nil {
		return "", 0, err
	}
	body := formatStatsHotTopicHistoryBody(dateLabel, stats, threads, categories)
	return h.ensureStatsSystemPost(actor, threadID, postID, "Hot topic history "+dateLabel, body, ts)
}

func (h *Handler) ensureStatsBlessingListSystemPost(actor *User, dateLabel, dateID string, ts int64) (string, int64, error) {
	threadID := "bbslists_bless_" + dateID
	postID := "bbslists_bless_post_" + dateID
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return threadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	startAt, endAt, err := statsPeriodBounds(dateLabel, dateLabel)
	if err != nil {
		return "", 0, err
	}
	rankings, err := projections.ListBlessingRankingsRange(h.db, startAt, endAt, 10, 0)
	if err != nil {
		return "", 0, err
	}
	recent, err := projections.ListBlessingsRange(h.db, startAt, endAt, 10, 0)
	if err != nil {
		return "", 0, err
	}
	body := formatStatsBlessingListBody(dateLabel, rankings, recent, startAt, endAt)
	return h.ensureStatsSystemPost(actor, threadID, postID, "Daily blessing list "+dateLabel, body, ts)
}

type statsPeriodHistorySpec struct {
	ThreadID string
	PostID   string
	Title    string
	Label    string
	StartDay string
	EndDay   string
}

func (h *Handler) ensureStatsPeriodHistorySystemPosts(actor *User, day time.Time, ts int64) error {
	for _, spec := range statsPeriodHistorySpecs(day) {
		if _, _, err := h.ensureStatsPeriodHistorySystemPost(actor, spec, ts); err != nil {
			return err
		}
	}
	return nil
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

func (h *Handler) ensureStatsPeriodHistorySystemPost(actor *User, spec statsPeriodHistorySpec, ts int64) (string, int64, error) {
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, spec.ThreadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return spec.ThreadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	history, err := projections.ListCommunityStatHistoryRange(h.db, spec.StartDay, spec.EndDay)
	if err != nil {
		return "", 0, err
	}
	body := formatStatsPeriodHistoryBody(spec, history)
	return h.ensureStatsSystemPost(actor, spec.ThreadID, spec.PostID, spec.Title, body, ts)
}

func (h *Handler) ensureStatsHotTopicPeriodHistorySystemPosts(actor *User, day time.Time, ts int64) error {
	for _, spec := range statsHotTopicPeriodHistorySpecs(day) {
		if _, _, err := h.ensureStatsHotTopicPeriodHistorySystemPost(actor, spec, ts); err != nil {
			return err
		}
	}
	return nil
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

func (h *Handler) ensureStatsHotTopicPeriodHistorySystemPost(actor *User, spec statsPeriodHistorySpec, ts int64) (string, int64, error) {
	var existingSeq int64
	err := qQueryRow(h.db, `SELECT last_seq FROM threads WHERE id=?`, spec.ThreadID).Scan(&existingSeq)
	if err == nil {
		if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
			return "", 0, err
		}
		return spec.ThreadID, existingSeq, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(h.db, ts); err != nil {
		return "", 0, err
	}
	start, end, err := statsPeriodBounds(spec.StartDay, spec.EndDay)
	if err != nil {
		return "", 0, err
	}
	threads, err := projections.ListThreadRankingsRange(h.db, "", false, "", start, end, 100, 0)
	if err != nil {
		return "", 0, err
	}
	categories, err := projections.ListCategories(h.db)
	if err != nil {
		return "", 0, err
	}
	body := formatStatsHotTopicPeriodHistoryBody(spec, threads, categories)
	return h.ensureStatsSystemPost(actor, spec.ThreadID, spec.PostID, spec.Title, body, ts)
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

	boardCreated := false
	var boardSeq int64
	var exists int
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, statsSystemBoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, err := boardCategoryPosition(tx, "", nil)
		if err != nil {
			return "", 0, err
		}
		boardScopes := []string{"board:" + statsSystemBoardID}
		boardSeq, err = appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          statsSystemBoardID,
			Name:        "BBSLists",
			Description: "Generated community rankings and statistics",
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return "", 0, err
		}
		if err := insertBoard(tx, statsSystemBoardID, "BBSLists", "Generated community rankings and statistics", "", position); err != nil {
			return "", 0, err
		}
		boardCreated = true
	} else if err != nil {
		return "", 0, err
	}

	authorName := actor.Name
	authorID := actor.ID
	scopes := []string{"board:" + statsSystemBoardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: statsSystemBoardID, Author: authorName, AuthorID: authorID, Title: title, TS: ts,
	})
	if err != nil {
		return "", 0, err
	}
	threadScopes := append(scopes, "thread:"+threadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return "", 0, err
	}
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: statsSystemBoardID, Author: authorName, AuthorID: authorID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return "", 0, err
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return "", 0, err
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return "", 0, err
	}
	if err := ftsInsertPost(tx, postID, threadID, statsSystemBoardID, authorName, body); err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}

	if boardCreated {
		h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: []string{"board:" + statsSystemBoardID},
			Payload: &proto.BoardCreatedPayload{ID: statsSystemBoardID, Name: "BBSLists", Description: "Generated community rankings and statistics", By: actor.Name, TS: ts}, TS: ts})
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: statsSystemBoardID, Author: authorName, AuthorID: authorID, Title: title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts})
	return threadID, pseq, nil
}

func formatStatsSnapshotBody(dateLabel string, stats *projections.CommunityStats, boards []projections.BoardRanking, threads []projections.ThreadRanking, replies []projections.ReplyRanking, users []projections.UserRanking, archives []projections.ArchiveRanking, blessings []projections.BlessingRanking, history []projections.CommunityStatHistory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Community stats %s\n\n", dateLabel)
	fmt.Fprintf(&b, "- Total users: %d\n", stats.TotalUsers)
	fmt.Fprintf(&b, "- Total logins: %d\n", stats.TotalLogins)
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
		fmt.Fprintf(&b, "- %s: %d users%s, %s%s, %d guests%s, %d posts%s, %d reactions%s, %s online time%s, %d users online now, max %d users at %s UTC, max %d guests at %s UTC\n",
			day.Day,
			day.TotalUsers,
			formatStatsDelta(day.DeltaUsers),
			formatStatsCount(day.TotalLogins, "login", "logins"),
			formatStatsDelta(day.DeltaLogins),
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
		fmt.Fprintf(&b, "- %s: %s%s, %d users%s, %d online users, %d guests%s, %s online time%s\n",
			day.Day,
			formatStatsCount(day.TotalLogins, "login", "logins"),
			formatStatsDelta(day.DeltaLogins),
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
	var posts, threads, boards, users, reactions, mail, messages, logins int
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
		fmt.Fprintf(&b, "- %s: %d posts%s, %d threads%s, %d boards%s, %d users%s, %s%s, %d reactions%s, %s online time%s\n",
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

type digestMirrorSystemBoard struct {
	Kind        string
	BoardID     string
	Name        string
	Description string
	ThreadID    string
	PostID      string
	Default     string
}

func (h *Handler) ensureAnnouncementSystemPost(actor *User, entryID string) error {
	return h.ensureDigestMirrorSystemPost(actor, entryID, digestMirrorSystemBoard{
		Kind:        "announcement",
		BoardID:     announcementSystemBoardID,
		Name:        "0Announce",
		Description: "Generated site-wide announcements",
		ThreadID:    "ann_thr_",
		PostID:      "ann_pst_",
		Default:     "Announcement",
	})
}

func (h *Handler) ensureRecommendSystemPost(actor *User, entryID string) error {
	return h.ensureDigestMirrorSystemPost(actor, entryID, digestMirrorSystemBoard{
		Kind:        "recommended",
		BoardID:     recommendSystemBoardID,
		Name:        "Recommend",
		Description: "Generated recommended articles and homepage recommendations",
		ThreadID:    "recommend_thr_",
		PostID:      "recommend_pst_",
		Default:     "Recommended article",
	})
}

func (h *Handler) ensureDigestMirrorSystemPost(actor *User, entryID string, mirror digestMirrorSystemBoard) error {
	export, err := getDigestExport(h.db, entryID)
	if err != nil || export == nil {
		return err
	}
	if export.Entry.Kind != mirror.Kind || export.Entry.BoardID == mirror.BoardID {
		return nil
	}
	settings, err := getBoardSettings(h.db, export.Entry.BoardID)
	if err != nil {
		return err
	}
	if settings != nil && settings.MemberReadMode {
		return nil
	}

	threadID := mirror.ThreadID + entryID
	postID := mirror.PostID + entryID
	var exists int
	err = qQueryRow(h.db, `SELECT 1 FROM threads WHERE id=?`, threadID).Scan(&exists)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}

	ts := nowMS()
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	boardCreated := false
	var boardSeq int64
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, mirror.BoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, err := boardCategoryPosition(tx, "", nil)
		if err != nil {
			return err
		}
		boardScopes := []string{"board:" + mirror.BoardID}
		boardSeq, err = appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          mirror.BoardID,
			Name:        mirror.Name,
			Description: mirror.Description,
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return err
		}
		if err := insertBoard(tx, mirror.BoardID, mirror.Name, mirror.Description, "", position); err != nil {
			return err
		}
		boardCreated = true
	} else if err != nil {
		return err
	}

	title := strings.TrimSpace(export.Entry.Title)
	if title == "" {
		title = mirror.Default
	}
	body := projections.FormatDigestExportText(export)
	authorName := actor.Name
	authorID := actor.ID
	scopes := []string{"board:" + mirror.BoardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: mirror.BoardID, Author: authorName, AuthorID: authorID, Title: title, TS: ts,
	})
	if err != nil {
		return err
	}
	threadScopes := append(scopes, "thread:"+threadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return err
	}
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: mirror.BoardID, Author: authorName, AuthorID: authorID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return err
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return err
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return err
	}
	if err := ftsInsertPost(tx, postID, threadID, mirror.BoardID, authorName, body); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if boardCreated {
		h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: []string{"board:" + mirror.BoardID},
			Payload: &proto.BoardCreatedPayload{ID: mirror.BoardID, Name: mirror.Name, Description: mirror.Description, By: actor.Name, TS: ts}, TS: ts})
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: mirror.BoardID, Author: authorName, AuthorID: authorID, Title: title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: authorName, AuthorID: authorID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts})
	return nil
}

func (h *Handler) ensureBoardRegistrationSystemPost(actor *User, applicationID, status, boardID, userID string) error {
	switch status {
	case "approved", "rejected", "blacklisted":
	default:
		return nil
	}
	settings, err := getBoardSettings(h.db, boardID)
	if err != nil {
		return err
	}
	if settings != nil && settings.MemberReadMode {
		return nil
	}

	boardIDOut := registrySystemBoardID
	boardNameOut := "Registry"
	boardDescription := "Generated board registration approvals"
	if status != "approved" {
		boardIDOut = rejectRegistrySystemBoardID
		boardNameOut = "reject_registry"
		boardDescription = "Generated rejected board registrations"
	}
	threadID := "registry_" + status + "_thr_" + applicationID
	postID := "registry_" + status + "_pst_" + applicationID
	var exists int
	err = qQueryRow(h.db, `SELECT 1 FROM threads WHERE id=?`, threadID).Scan(&exists)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}

	var sourceBoardName string
	if err := qQueryRow(h.db, `SELECT name FROM boards WHERE id=?`, boardID).Scan(&sourceBoardName); err != nil {
		return err
	}
	var applicantName string
	if err := qQueryRow(h.db, `SELECT name FROM users WHERE id=?`, userID).Scan(&applicantName); err != nil {
		return err
	}

	ts := nowMS()
	title := "Board registration " + status + " " + applicationID
	body := fmt.Sprintf("# %s\n\n- Application: %s\n- Status: %s\n- Board: %s (%s)\n- Applicant: %s\n- Reviewer: %s\n\nApplication and review notes are kept in the board member manager queue.\n",
		title, applicationID, status, sourceBoardName, boardID, applicantName, actor.Name)

	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	boardCreated := false
	var boardSeq int64
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, boardIDOut).Scan(&exists)
	if err == sql.ErrNoRows {
		position, err := boardCategoryPosition(tx, "", nil)
		if err != nil {
			return err
		}
		boardScopes := []string{"board:" + boardIDOut}
		boardSeq, err = appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          boardIDOut,
			Name:        boardNameOut,
			Description: boardDescription,
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return err
		}
		if err := insertBoard(tx, boardIDOut, boardNameOut, boardDescription, "", position); err != nil {
			return err
		}
		boardCreated = true
	} else if err != nil {
		return err
	}

	scopes := []string{"board:" + boardIDOut}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: boardIDOut, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts,
	})
	if err != nil {
		return err
	}
	threadScopes := append(scopes, "thread:"+threadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return err
	}
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: boardIDOut, Author: actor.Name, AuthorID: actor.ID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return err
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return err
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return err
	}
	if err := ftsInsertPost(tx, postID, threadID, boardIDOut, actor.Name, body); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if boardCreated {
		h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: []string{"board:" + boardIDOut},
			Payload: &proto.BoardCreatedPayload{ID: boardIDOut, Name: boardNameOut, Description: boardDescription, By: actor.Name, TS: ts}, TS: ts})
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: boardIDOut, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts})
	return nil
}

func (h *Handler) ensureSyssecuritySystemPost(actor *User, title string, lines []string, sourceBoardID string) error {
	if sourceBoardID != "" {
		settings, err := getBoardSettings(h.db, sourceBoardID)
		if err != nil {
			return err
		}
		if settings != nil && settings.MemberReadMode {
			return nil
		}
	}

	title = strings.TrimSpace(title)
	if title == "" {
		title = "Security notice"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s\n", line)
	}
	b.WriteString("\nGenerated security notices omit private notes and article content.\n")
	body := b.String()

	ts := nowMS()
	threadID := newID("syssecurity_thr_")
	postID := newID("syssecurity_pst_")
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	boardCreated := false
	var boardSeq int64
	var exists int
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, syssecuritySystemBoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, err := boardCategoryPosition(tx, "", nil)
		if err != nil {
			return err
		}
		boardScopes := []string{"board:" + syssecuritySystemBoardID}
		boardSeq, err = appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          syssecuritySystemBoardID,
			Name:        "syssecurity",
			Description: "Generated security and administration audit log",
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return err
		}
		if err := insertBoard(tx, syssecuritySystemBoardID, "syssecurity", "Generated security and administration audit log", "", position); err != nil {
			return err
		}
		boardCreated = true
	} else if err != nil {
		return err
	}

	scopes := []string{"board:" + syssecuritySystemBoardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: syssecuritySystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts,
	})
	if err != nil {
		return err
	}
	threadScopes := append(scopes, "thread:"+threadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return err
	}
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: syssecuritySystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return err
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return err
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return err
	}
	if err := ftsInsertPost(tx, postID, threadID, syssecuritySystemBoardID, actor.Name, body); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if boardCreated {
		h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: []string{"board:" + syssecuritySystemBoardID},
			Payload: &proto.BoardCreatedPayload{ID: syssecuritySystemBoardID, Name: "syssecurity", Description: "Generated security and administration audit log", By: actor.Name, TS: ts}, TS: ts})
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: syssecuritySystemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts})
	return nil
}

func normalizeDigestKind(kind string) (string, Reply) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "digest"
	}
	switch kind {
	case "digest", "archive", "recommended", "pinned", "announcement":
		return kind, Reply{}
	default:
		return "", Reply{Err: errDetail(proto.ErrValidationFailed, `kind must be "digest", "archive", "recommended", "pinned", or "announcement"`, false)}
	}
}

func boardCurationPermissionMessage(kind string) string {
	if kind == "announcement" {
		return "board announcement permission required"
	}
	return "board curator permission required"
}

func normalizeDigestPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "/")
	if len(path) > 120 {
		return path[:120]
	}
	return path
}

func (h *Handler) actorCanModerateBoard(actor *User, boardID string) bool {
	if actor == nil {
		return false
	}
	if actor.IsMod() {
		return true
	}
	return h.isBoardModerator(actor.ID, boardID)
}

func (h *Handler) actorCanUseMemberBoard(actor *User, boardID string) bool {
	if h.actorCanModerateBoard(actor, boardID) {
		return true
	}
	if actor == nil {
		return false
	}
	var exists int
	err := qQueryRow(h.db, `SELECT 1 FROM board_members WHERE board_id=? AND user_id=?`, boardID, actor.ID).Scan(&exists)
	return err == nil
}

func (h *Handler) actorCanManageBoardMembers(actor *User, boardID string) bool {
	if h.actorCanModerateBoard(actor, boardID) {
		return true
	}
	return h.actorHasBoardMemberPermission(actor, boardID, "can_manage_members")
}

func (h *Handler) actorCanSetBoardSettings(actor *User, boardID string) bool {
	if h.actorCanModerateBoard(actor, boardID) {
		return true
	}
	return h.actorHasBoardMemberPermission(actor, boardID, "can_set_board_settings")
}

func (h *Handler) actorCanCurateBoard(actor *User, boardID string) bool {
	if h.actorCanModerateBoard(actor, boardID) {
		return true
	}
	return h.actorHasBoardMemberPermission(actor, boardID, "can_curate")
}

func (h *Handler) actorCanAnnounceBoard(actor *User, boardID string) bool {
	if h.actorCanModerateBoard(actor, boardID) {
		return true
	}
	return h.actorHasBoardMemberPermission(actor, boardID, "can_announce")
}

func (h *Handler) actorCanModerateBoardThreads(actor *User, boardID string) bool {
	if h.actorCanModerateBoard(actor, boardID) {
		return true
	}
	return h.actorHasBoardMemberPermission(actor, boardID, "can_moderate_threads")
}

func (h *Handler) actorCanManageBoardPolls(actor *User, boardID string) bool {
	if h.actorCanModerateBoard(actor, boardID) {
		return true
	}
	return h.actorHasBoardMemberPermission(actor, boardID, "can_manage_polls")
}

func (h *Handler) actorCanCurateBoardKind(actor *User, boardID, kind string) bool {
	if kind == "announcement" {
		return h.actorCanCurateBoard(actor, boardID) || h.actorCanAnnounceBoard(actor, boardID)
	}
	return h.actorCanCurateBoard(actor, boardID)
}

func (h *Handler) actorHasBoardMemberPermission(actor *User, boardID, column string) bool {
	if actor == nil {
		return false
	}
	switch column {
	case "can_manage_members", "can_curate", "can_moderate_posts", "can_moderate_threads", "can_announce", "can_manage_polls", "can_set_board_settings":
	default:
		return false
	}
	var allowed int
	err := qQueryRow(h.db, `SELECT `+column+` FROM board_members WHERE board_id=? AND user_id=?`, boardID, actor.ID).Scan(&allowed)
	return err == nil && allowed != 0
}

func (h *Handler) actorCanModerateBoardTx(tx *sql.Tx, actor *User, boardID string) bool {
	if actor == nil {
		return false
	}
	if actor.IsMod() {
		return true
	}
	var exists int
	err := qQueryRow(tx, `SELECT 1 FROM board_moderators WHERE board_id=? AND user_id=?`, boardID, actor.ID).Scan(&exists)
	return err == nil
}

func (h *Handler) actorCanModerateBoardPostsTx(tx *sql.Tx, actor *User, boardID string) bool {
	if h.actorCanModerateBoardTx(tx, actor, boardID) {
		return true
	}
	return h.actorHasBoardMemberPermissionTx(tx, actor, boardID, "can_moderate_posts")
}

func (h *Handler) actorCanModerateBoardThreadsTx(tx *sql.Tx, actor *User, boardID string) bool {
	if h.actorCanModerateBoardTx(tx, actor, boardID) {
		return true
	}
	return h.actorHasBoardMemberPermissionTx(tx, actor, boardID, "can_moderate_threads")
}

func (h *Handler) actorHasBoardMemberPermissionTx(tx *sql.Tx, actor *User, boardID, column string) bool {
	if actor == nil {
		return false
	}
	switch column {
	case "can_moderate_posts", "can_moderate_threads":
	default:
		return false
	}
	var allowed int
	err := qQueryRow(tx, `SELECT `+column+` FROM board_members WHERE board_id=? AND user_id=?`, boardID, actor.ID).Scan(&allowed)
	return err == nil && allowed != 0
}

func (h *Handler) isBoardModerator(userID, boardID string) bool {
	var exists int
	err := qQueryRow(h.db, `SELECT 1 FROM board_moderators WHERE board_id=? AND user_id=?`, boardID, userID).Scan(&exists)
	return err == nil
}

func (h *Handler) isBoardMember(userID, boardID string) bool {
	var exists int
	err := qQueryRow(h.db, `SELECT 1 FROM board_members WHERE board_id=? AND user_id=?`, boardID, userID).Scan(&exists)
	return err == nil
}

func (h *Handler) boardMemberHasDelegatedPermissions(boardID, userID string) (bool, error) {
	var canManageMembers, canCurate, canModeratePosts, canModerateThreads, canAnnounce, canManagePolls, canSetBoardSettings int
	err := qQueryRow(h.db,
		`SELECT can_manage_members, can_curate, can_moderate_posts, can_moderate_threads, can_announce, can_manage_polls, can_set_board_settings
		   FROM board_members WHERE board_id=? AND user_id=?`,
		boardID, userID,
	).Scan(&canManageMembers, &canCurate, &canModeratePosts, &canModerateThreads, &canAnnounce, &canManagePolls, &canSetBoardSettings)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return canManageMembers != 0 ||
		canCurate != 0 ||
		canModeratePosts != 0 ||
		canModerateThreads != 0 ||
		canAnnounce != 0 ||
		canManagePolls != 0 ||
		canSetBoardSettings != 0, nil
}

func (h *Handler) latestBoardMembershipApplicationStatus(boardID, userID string) (string, error) {
	var status string
	err := qQueryRow(h.db,
		`SELECT status FROM board_member_applications
		  WHERE board_id=? AND user_id=?
		    AND status IN ('pending', 'blacklisted')
		  ORDER BY CASE status WHEN 'pending' THEN 0 ELSE 1 END, updated_at DESC, created_at DESC LIMIT 1`,
		boardID, userID,
	).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return status, err
}

func boardMemberPermissionsChanged(p proto.SetBoardMemberPayload) bool {
	return p.CanManageMembers != nil ||
		p.CanCurate != nil ||
		p.CanModeratePosts != nil ||
		p.CanModerateThreads != nil ||
		p.CanAnnounce != nil ||
		p.CanManagePolls != nil ||
		p.CanSetBoardSettings != nil
}

func boardSettingsAuditLines(p proto.SetBoardSettingsPayload) []string {
	fields := []struct {
		name  string
		value *bool
	}{
		{"anonymousAllowed", p.AnonymousAllowed},
		{"readOnly", p.ReadOnly},
		{"noReply", p.NoReply},
		{"attachmentsAllowed", p.AttachmentsAllowed},
		{"mailInAllowed", p.MailInAllowed},
		{"relayEnabled", p.RelayEnabled},
		{"memberReadMode", p.MemberReadMode},
		{"memberPostMode", p.MemberPostMode},
		{"statsExcluded", p.StatsExcluded},
	}
	out := []string{}
	for _, field := range fields {
		if field.value == nil {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %t", field.name, *field.value))
	}
	return out
}

func (h *Handler) requireBoardMembershipAdmission(boardID, userID string, requirements *BoardMemberRequirements) Reply {
	if requirements == nil {
		return Reply{}
	}
	if requirements.MaxMembers > 0 && !h.isBoardMember(userID, boardID) {
		var currentMembers int
		if err := qQueryRow(h.db, `SELECT COUNT(*) FROM board_members WHERE board_id=?`, boardID).Scan(&currentMembers); err != nil {
			return internalErr(err)
		}
		if currentMembers >= requirements.MaxMembers {
			return Reply{Err: errDetail(proto.ErrConflict, "board membership is full", false)}
		}
	}
	if requirements.MinLoginCount > 0 {
		loginCount, err := h.userLoginCount(userID)
		if err != nil {
			return internalErr(err)
		}
		if loginCount < requirements.MinLoginCount {
			return Reply{Err: errDetail(proto.ErrForbidden, fmt.Sprintf("minimum login count is %d", requirements.MinLoginCount), false)}
		}
	}
	if requirements.MinPostCount > 0 {
		postsCreated, err := h.userPostsCreated(userID)
		if err != nil {
			return internalErr(err)
		}
		if postsCreated < requirements.MinPostCount {
			return Reply{Err: errDetail(proto.ErrForbidden, fmt.Sprintf("minimum post count is %d", requirements.MinPostCount), false)}
		}
	}
	if requirements.MinScore > 0 {
		score, err := h.userReactionScore(userID)
		if err != nil {
			return internalErr(err)
		}
		if score < requirements.MinScore {
			return Reply{Err: errDetail(proto.ErrForbidden, fmt.Sprintf("minimum score is %d", requirements.MinScore), false)}
		}
	}
	if requirements.MinBoardPostCount > 0 {
		boardPosts, err := h.userBoardPostCount(boardID, userID)
		if err != nil {
			return internalErr(err)
		}
		if boardPosts < requirements.MinBoardPostCount {
			return Reply{Err: errDetail(proto.ErrForbidden, fmt.Sprintf("minimum board post count is %d", requirements.MinBoardPostCount), false)}
		}
	}
	if requirements.MinBoardOriginalPostCount > 0 {
		boardOriginalPosts, err := h.userBoardOriginalPostCount(boardID, userID)
		if err != nil {
			return internalErr(err)
		}
		if boardOriginalPosts < requirements.MinBoardOriginalPostCount {
			return Reply{Err: errDetail(proto.ErrForbidden, fmt.Sprintf("minimum board original post count is %d", requirements.MinBoardOriginalPostCount), false)}
		}
	}
	if requirements.MinBoardDigestCount > 0 {
		boardDigests, err := h.userBoardDigestCount(boardID, userID)
		if err != nil {
			return internalErr(err)
		}
		if boardDigests < requirements.MinBoardDigestCount {
			return Reply{Err: errDetail(proto.ErrForbidden, fmt.Sprintf("minimum board digest count is %d", requirements.MinBoardDigestCount), false)}
		}
	}
	if requirements.MinBoardMarkCount > 0 {
		boardMarks, err := h.userBoardMarkCount(boardID, userID)
		if err != nil {
			return internalErr(err)
		}
		if boardMarks < requirements.MinBoardMarkCount {
			return Reply{Err: errDetail(proto.ErrForbidden, fmt.Sprintf("minimum board mark count is %d", requirements.MinBoardMarkCount), false)}
		}
	}
	if requirements.MinTrustLevel > 0 {
		level, err := userTrustLevel(h.db, userID)
		if err != nil {
			return internalErr(err)
		}
		if level < requirements.MinTrustLevel {
			return Reply{Err: errDetail(proto.ErrForbidden, fmt.Sprintf("minimum trust level is %d", requirements.MinTrustLevel), false)}
		}
	}
	return Reply{}
}

func (h *Handler) userLoginCount(userID string) (int, error) {
	var loginCount int
	err := qQueryRow(h.db, `SELECT login_count FROM user_activity WHERE user_id=?`, userID).Scan(&loginCount)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return loginCount, err
}

func (h *Handler) userPostsCreated(userID string) (int, error) {
	var postsCreated int
	err := qQueryRow(h.db, `SELECT COUNT(*) FROM posts WHERE author_id=? AND redacted=0`, userID).Scan(&postsCreated)
	return postsCreated, err
}

func (h *Handler) userReactionScore(userID string) (int, error) {
	var count int
	err := qQueryRow(h.db,
		`SELECT COUNT(*)
		   FROM post_reactions r
		   JOIN posts p ON p.id=r.post_id
		  WHERE p.author_id=? AND p.redacted=0`,
		userID,
	).Scan(&count)
	return count, err
}

func (h *Handler) userBoardPostCount(boardID, userID string) (int, error) {
	var count int
	err := qQueryRow(h.db,
		`SELECT COUNT(*)
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		  WHERE t.board=? AND p.author_id=? AND p.redacted=0`,
		boardID, userID,
	).Scan(&count)
	return count, err
}

func (h *Handler) userBoardOriginalPostCount(boardID, userID string) (int, error) {
	var count int
	err := qQueryRow(h.db,
		`SELECT COUNT(*) FROM threads WHERE board=? AND author_id=?`,
		boardID, userID,
	).Scan(&count)
	return count, err
}

func (h *Handler) userBoardMarkCount(boardID, userID string) (int, error) {
	var count int
	err := qQueryRow(h.db,
		`SELECT COUNT(*)
		   FROM post_reactions r
		   JOIN posts p ON p.id=r.post_id
		   JOIN threads t ON t.id=p.thread
		  WHERE t.board=? AND p.author_id=? AND p.redacted=0`,
		boardID, userID,
	).Scan(&count)
	return count, err
}

func (h *Handler) userBoardDigestCount(boardID, userID string) (int, error) {
	var count int
	err := qQueryRow(h.db,
		`SELECT COUNT(*)
		   FROM digest_entries d
		   LEFT JOIN posts p ON d.target_kind='post' AND p.id=d.target_id
		   LEFT JOIN threads tt ON d.target_kind='thread' AND tt.id=d.target_id
		  WHERE d.board_id=?
		    AND (
		      (d.target_kind='post' AND p.author_id=?)
		      OR (d.target_kind='thread' AND tt.author_id=?)
		    )`,
		boardID, userID, userID,
	).Scan(&count)
	return count, err
}

func normalizeBoardMemberApprovalMode(mode string) (string, Reply) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "manual":
		return "manual", Reply{}
	case "auto", "automatic":
		return "auto", Reply{}
	default:
		return "", Reply{Err: errDetail(proto.ErrValidationFailed, `approvalMode must be "manual" or "auto"`, false)}
	}
}

func normalizeMemberApplicationStatus(status string) (string, Reply) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approved", "approve":
		return "approved", Reply{}
	case "rejected", "reject":
		return "rejected", Reply{}
	case "blacklisted", "blacklist":
		return "blacklisted", Reply{}
	default:
		return "", Reply{Err: errDetail(proto.ErrValidationFailed, `status must be "approved", "rejected", or "blacklisted"`, false)}
	}
}

func (h *Handler) resolveUserRef(ref string) (string, string, Reply) {
	var userID, name string
	err := qQueryRow(h.db, `SELECT id, name FROM users WHERE id=? OR name=?`, ref, ref).Scan(&userID, &name)
	if err == sql.ErrNoRows {
		return "", "", Reply{Err: errDetail(proto.ErrNotFound, "user not found", false)}
	}
	if err != nil {
		return "", "", internalErr(err)
	}
	return userID, name, Reply{}
}

func (h *Handler) requireFavoriteFolder(userID, folderID string) Reply {
	if folderID == "" {
		return Reply{}
	}
	var exists int
	err := qQueryRow(h.db, `SELECT 1 FROM favorite_folders WHERE user_id=? AND id=?`, userID, folderID).Scan(&exists)
	if err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "favorite folder not found", false)}
	}
	if err != nil {
		return internalErr(err)
	}
	return Reply{}
}

func (h *Handler) favoriteFolderContains(userID, ancestorID, folderID string) bool {
	for folderID != "" {
		if folderID == ancestorID {
			return true
		}
		var parentID string
		err := qQueryRow(h.db, `SELECT parent_id FROM favorite_folders WHERE user_id=? AND id=?`, userID, folderID).Scan(&parentID)
		if err != nil {
			return false
		}
		folderID = parentID
	}
	return false
}

func (h *Handler) requireBoard(boardID string) Reply {
	var exists int
	err := qQueryRow(h.db, `SELECT 1 FROM boards WHERE id=?`, boardID).Scan(&exists)
	if err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	}
	if err != nil {
		return internalErr(err)
	}
	return Reply{}
}

func (h *Handler) requirePost(postID string) Reply {
	var exists int
	err := qQueryRow(h.db, `SELECT 1 FROM posts WHERE id=?`, postID).Scan(&exists)
	if err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if err != nil {
		return internalErr(err)
	}
	return Reply{}
}

func (h *Handler) requireThread(threadID string) Reply {
	var exists int
	err := qQueryRow(h.db, `SELECT 1 FROM threads WHERE id=?`, threadID).Scan(&exists)
	if err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	if err != nil {
		return internalErr(err)
	}
	return Reply{}
}
