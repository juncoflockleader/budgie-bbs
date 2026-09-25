package core

import (
	"context"
	"encoding/json"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// EventStore is the durable event log contract. SQLite currently satisfies the
// behavior through package helpers; Postgres will implement this interface as
// the production event store without changing transports.
type EventStore interface {
	Append(ctx context.Context, kind proto.EventKind, scopes []string, payload any) (seq int64, id string, err error)
	Head(ctx context.Context) (int64, error)
	Replay(ctx context.Context, after int64, scopes []string, limit int) ([]*proto.Event, error)
}

// ProjectionStore exposes rebuildable read models. It is intentionally broad:
// projection implementations can split this into smaller concrete stores while
// keeping API and transport code on the interface boundary.
type ProjectionStore interface {
	ListBoards(ctx context.Context) ([]Board, error)
	GetBoard(ctx context.Context, id string) (*Board, error)
	ListThreads(ctx context.Context, boardID string, limit, offset int) ([]Thread, error)
	GetThread(ctx context.Context, id string) (*Thread, error)
	ListPosts(ctx context.Context, threadID string, limit, offset int) ([]Post, error)
}

// CommandReceiptStore owns command idempotency. The command hash makes retries
// safe while rejecting accidental cid reuse with a different payload.
type CommandReceiptStore interface {
	Load(ctx context.Context, actorID, cid, commandHash string) (result json.RawMessage, ok bool, conflict bool, err error)
	Record(ctx context.Context, actorID, cid, commandHash string, result json.RawMessage) error
}

// Migrator applies storage-specific migrations.
type Migrator interface {
	Apply(ctx context.Context) error
}
