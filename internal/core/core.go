// Package core implements the append-only event log, command handler, and
// pub/sub bus that form the server's single source of truth.
//
// Design invariant: all state mutation flows through the Handler's single-writer
// goroutine. Transports (HTTP, WebSocket, SSH) are read-heavy and stateless;
// they submit commands and read projections but never touch the log directly.
package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/bcrypt"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	_ "modernc.org/sqlite"
)

// Core is the central server object. Transports embed or reference it.
type Core struct {
	DB      *sql.DB
	Bus     Bus
	handler *Handler
}

// New opens the SQLite database, runs migrations, and returns a ready Core.
func New(dbPath string) (*Core, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=on", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite WAL: one writer is plenty
	if _, err := db.Exec(ddl); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	bus := NewMemBus()
	c := &Core{
		DB:      db,
		Bus:     bus,
		handler: newHandler(db, bus),
	}
	return c, nil
}

// Run starts the single-writer goroutine. Returns when ctx is cancelled.
func (c *Core) Run(ctx context.Context) {
	c.handler.Run(ctx)
}

// ExecCmd submits a command for the actor and returns the result.
// payload is the raw JSON of the command-specific payload object.
func (c *Core) ExecCmd(ctx context.Context, actor *User, name proto.CommandName, payload json.RawMessage, cid string) Reply {
	return c.handler.Execute(ctx, actor, name, payload, cid)
}

// Head returns the current highest seq in the event log.
func (c *Core) Head() (int64, error) {
	return headSeq(c.DB)
}

// Replay returns events with seq > after, filtered to the given scopes.
func (c *Core) Replay(after int64, scopes []string, limit int) ([]*proto.Event, error) {
	return replayEvents(c.DB, after, scopes, limit)
}

// Subscribe creates a new subscription on the bus.
func (c *Core) Subscribe(scopes []string) *Subscription {
	return c.Bus.Subscribe(scopes)
}

// Unsubscribe removes a subscription.
func (c *Core) Unsubscribe(s *Subscription) {
	c.Bus.Unsubscribe(s)
}

// --- User management (used by the auth layer) ---

// RegisterUser creates a new account. The very first user becomes admin.
func (c *Core) RegisterUser(name, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	id := newID("usr_")
	ts := nowMS()

	n, err := countUsers(c.DB)
	if err != nil {
		return nil, err
	}
	role := "user"
	if n == 0 {
		role = "admin"
	}

	_, err = c.DB.Exec(
		`INSERT INTO users (id, name, role, password, created) VALUES (?,?,?,?,?)`,
		id, name, role, string(hash), ts,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	slog.Info("user registered", "id", id, "name", name, "role", role)
	return &User{ID: id, Name: name, Role: role, Created: ts}, nil
}

// AuthenticateUser verifies credentials and returns the user on success.
func (c *Core) AuthenticateUser(name, password string) (*User, error) {
	u, err := getUserByName(c.DB, name)
	if err != nil || u == nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	return u, nil
}

// AddPubkey registers an SSH public key for the given user.
func (c *Core) AddPubkey(userID, pubkey string) error {
	_, err := c.DB.Exec(
		`INSERT OR IGNORE INTO auth_pubkeys (user_id, pubkey) VALUES (?,?)`,
		userID, pubkey,
	)
	return err
}

// UserByPubkey looks up a user by their SSH public key fingerprint.
func (c *Core) UserByPubkey(pubkey string) (*User, error) {
	return getUserByPubkey(c.DB, pubkey)
}

// UserByID returns the user for the given ID.
func (c *Core) UserByID(id string) (*User, error) {
	return getUserByID(c.DB, id)
}

// --- Projection readers (safe for concurrent access) ---

func (c *Core) ListBoards() ([]Board, error) { return listBoards(c.DB) }
func (c *Core) GetBoard(id string) (*Board, error) { return getBoard(c.DB, id) }
func (c *Core) ListThreads(board string, limit, offset int) ([]Thread, error) {
	return listThreads(c.DB, board, limit, offset)
}
func (c *Core) GetThread(id string) (*Thread, error) { return getThread(c.DB, id) }
func (c *Core) ListPosts(thread string, limit, offset int) ([]Post, error) {
	return listPosts(c.DB, thread, limit, offset)
}
