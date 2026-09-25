package commandrules

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/favoritemodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func FavoriteTreeFromImportPayload(p proto.ImportFavoriteTreePayload) *projections.FavoriteTree {
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
	return tree
}

func RequireBoardZapAllowed(zapped bool, settings *projections.BoardSettings) *proto.ErrorDetail {
	zapAllowed := settings == nil || settings.ZapAllowed
	if !favoritemodel.BoardZapAllowed(zapped, zapAllowed) {
		return newErrDetail(proto.ErrConflict, "board cannot be zapped", false)
	}
	return nil
}

func RequireFavoriteFolderNotSelfParent(folderID, parentID string) *proto.ErrorDetail {
	if favoritemodel.FolderSelfParentFailure(folderID, parentID) == favoritemodel.FolderParentSelf {
		return newErrDetail(proto.ErrValidationFailed, "folder cannot be its own parent", false)
	}
	return nil
}

func RequireFavoriteFolderNotDescendantParent(containsParent bool) *proto.ErrorDetail {
	if favoritemodel.FolderDescendantParentFailure(containsParent) == favoritemodel.FolderParentDescendant {
		return newErrDetail(proto.ErrValidationFailed, "folder cannot move under its descendant", false)
	}
	return nil
}
