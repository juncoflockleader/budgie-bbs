package commandrules

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type DigestEntryForCommand struct {
	ID         string
	BoardID    string
	TargetKind string
	TargetID   string
	Kind       string
	Title      string
	Path       string
	Note       string
}

func DigestEntryForCuration(queryable Queryable, actor *projections.User, entryID string) (*DigestEntryForCommand, *proto.ErrorDetail) {
	var entry DigestEntryForCommand
	err := projections.QQueryRow(
		queryable,
		`SELECT id, board_id, target_kind, target_id, kind, title, path, note
		   FROM digest_entries
		  WHERE id=?`,
		entryID,
	).Scan(&entry.ID, &entry.BoardID, &entry.TargetKind, &entry.TargetID, &entry.Kind, &entry.Title, &entry.Path, &entry.Note)
	if err == sql.ErrNoRows {
		return nil, newErrDetail(proto.ErrNotFound, "digest entry not found", false)
	}
	if err != nil {
		return nil, internalErr(err)
	}
	if !ActorCanCurateBoardKind(queryable, actor, entry.BoardID, entry.Kind) {
		return nil, newErrDetail(proto.ErrForbidden, proto.DigestCurationPermissionMessage(entry.Kind), false)
	}
	return &entry, nil
}
