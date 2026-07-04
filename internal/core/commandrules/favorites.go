package commandrules

import (
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
