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
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	_ "modernc.org/sqlite"
)

var (
	ErrInvalidCredentials     = errors.New("invalid credentials")
	ErrAccountDeactivated     = errors.New("account deactivated")
	ErrAccountPending         = errors.New("account pending approval")
	ErrAccountRejected        = errors.New("account registration rejected")
	ErrLoginIPDenied          = errors.New("login host not allowed")
	ErrAccountAlreadyClosed   = errors.New("account already deactivated")
	ErrDeactivationIncomplete = errors.New("password required to deactivate account")
	ErrAccountDeleteForbidden = errors.New("account deletion forbidden")
	ErrLastAdminDeletion      = errors.New("cannot delete the last admin")
)

const (
	deletedUserID          = "usr_deleted"
	deletedUserDisplayName = "[deleted]"
)

// Core is the central server object. Transports embed or reference it.
type Core struct {
	DB      *sql.DB
	Bus     Bus
	handler *Handler
}

// New opens the SQLite database, runs migrations, and returns a ready Core.
func New(dbPath string) (*Core, error) {
	setSQLFlavor(sqliteFlavor)
	projections.SetSQLFlavor(sqliteFlavor)
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=on", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite WAL: one writer is plenty
	if err := applySQLiteMigrations(db); err != nil {
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

// NewPostgres opens a Postgres database and applies the production schema.
//
// It is intentionally explicit and minimal: single-writer semantics still live in Core,
// but SQL execution is normalized to Postgres placeholder style.
func NewPostgres(dsn string) (*Core, error) {
	setSQLFlavor(postgresFlavor)
	projections.SetSQLFlavor(postgresFlavor)
	db, err := OpenPostgres(dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres db: %w", err)
	}

	if err := ApplyPostgresMigrations(context.Background(), db); err != nil {
		db.Close()
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
	go runOutboxWorker(ctx, c.DB, c.Bus)
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

	tx, err := c.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	var n int
	if err := qQueryRow(tx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return nil, err
	}
	role := "user"
	status := "approved"
	if n == 0 {
		role = "admin"
	} else {
		var requireApproval int
		if err := qQueryRow(tx, `SELECT COALESCE(require_approval,0) FROM account_registration_settings WHERE id='default'`).Scan(&requireApproval); err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if requireApproval != 0 {
			status = "pending"
		}
	}

	_, err = qExec(tx,
		`INSERT INTO users (id, name, role, password, created, registration_status) VALUES (?,?,?,?,?,?)`,
		id, name, role, string(hash), ts, status,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	_, err = qExec(tx,
		`INSERT OR IGNORE INTO user_profiles (user_id, display_name, updated_at) VALUES (?,?,?)`,
		id, name, ts,
	)
	if err != nil {
		return nil, fmt.Errorf("create user profile: %w", err)
	}
	if _, err := qExec(tx,
		`INSERT OR IGNORE INTO user_signature_settings (user_id, selected_signature_id, random_enabled, updated_at)
		 VALUES (?, '', 0, ?)`,
		id, ts,
	); err != nil {
		return nil, fmt.Errorf("create user signature settings: %w", err)
	}
	if _, err := qExec(tx,
		`INSERT OR IGNORE INTO user_login_acl_settings (user_id, enabled, updated_at)
		 VALUES (?, 0, ?)`,
		id, ts,
	); err != nil {
		return nil, fmt.Errorf("create user login acl settings: %w", err)
	}
	if err := seedDefaultFavorites(tx, id, ts); err != nil {
		return nil, fmt.Errorf("seed default favorites: %w", err)
	}
	user := &User{ID: id, Name: name, Role: role, Created: ts, RegistrationStatus: status}
	events := []*proto.Event{}
	if status == "approved" {
		events, err = c.appendNewcomerSystemPostTx(tx, user, ts)
		if err != nil {
			return nil, fmt.Errorf("create newcomer record: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, evt := range events {
		c.Bus.Publish(evt)
	}
	slog.Info("user registered", "id", id, "name", name, "role", role, "status", status)
	return user, nil
}

func seedDefaultFavorites(db sqlLike, userID string, ts int64) error {
	_, err := qExec(db,
		`INSERT INTO board_favorites (user_id, board_id, folder_id, position, created_at, updated_at)
		 SELECT ?, id, '', 0, ?, ? FROM boards WHERE id='general'
		 ON CONFLICT(user_id, board_id) DO NOTHING`,
		userID, ts, ts,
	)
	return err
}

func (c *Core) appendNewcomerSystemPostTx(tx *sql.Tx, user *User, ts int64) ([]*proto.Event, error) {
	const boardID = "newcomers"
	threadID := "newcomer_thr_" + user.ID
	postID := "newcomer_pst_" + user.ID
	var exists int
	err := qQueryRow(tx, `SELECT 1 FROM threads WHERE id=?`, threadID).Scan(&exists)
	if err == nil {
		return nil, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	out := []*proto.Event{}
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, boardID).Scan(&exists)
	if err == sql.ErrNoRows {
		var position int
		if err := qQueryRow(tx, `SELECT COALESCE(MAX(position) + 1, 0) FROM categories WHERE parent_id=''`).Scan(&position); err != nil {
			return nil, err
		}
		boardScopes := []string{"board:" + boardID}
		boardSeq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          boardID,
			Name:        "newcomers",
			Description: "Generated new-user registration records",
			Position:    position,
			By:          user.ID,
			TS:          ts,
		})
		if err != nil {
			return nil, err
		}
		if err := insertBoard(tx, boardID, "newcomers", "Generated new-user registration records", "", position); err != nil {
			return nil, err
		}
		out = append(out, &proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: boardScopes,
			Payload: &proto.BoardCreatedPayload{ID: boardID, Name: "newcomers", Description: "Generated new-user registration records", By: user.Name, TS: ts}, TS: ts})
	} else if err != nil {
		return nil, err
	}

	title := "New user: " + user.Name
	body := fmt.Sprintf("# %s\n\n- User: %s\n- Role: %s\n- Status: registered\n\nThis generated newcomer record contains public account information only.\n",
		title, user.Name, user.Role)
	scopes := []string{"board:" + boardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: boardID, Author: user.Name, AuthorID: user.ID, Title: title, TS: ts,
	})
	if err != nil {
		return nil, err
	}
	threadScopes := append(scopes, "thread:"+threadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: user.Name, AuthorID: user.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return nil, err
	}
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: boardID, Author: user.Name, AuthorID: user.ID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: user.Name, AuthorID: user.ID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return nil, err
	}
	if err := ftsInsertPost(tx, postID, threadID, boardID, user.Name, body); err != nil {
		return nil, err
	}
	if err := markBoardReadForAllUsersTx(tx, boardID, pseq, ts); err != nil {
		return nil, err
	}
	out = append(out,
		&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
			Payload: &proto.ThreadNewPayload{ID: threadID, Board: boardID, Author: user.Name, AuthorID: user.ID, Title: title, TS: ts}, TS: ts},
		&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
			Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: user.Name, AuthorID: user.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts},
	)
	return out, nil
}

func markBoardReadForAllUsersTx(tx *sql.Tx, boardID string, seq, ts int64) error {
	_, err := qExec(tx,
		`INSERT INTO board_read_markers (user_id, board_id, last_seq, previous_seq, updated_at)
		 SELECT id, ?, ?,
		        COALESCE((SELECT last_seq FROM board_read_markers existing WHERE existing.user_id=users.id AND existing.board_id=?), 0),
		        ?
		   FROM users
		  WHERE 1=1
		 ON CONFLICT(user_id, board_id)
		 DO UPDATE SET
		    last_seq=excluded.last_seq,
		    previous_seq=excluded.previous_seq,
		    updated_at=excluded.updated_at`,
		boardID, seq, boardID, ts,
	)
	return err
}

// AuthenticateUser verifies credentials and returns the user on success.
func (c *Core) AuthenticateUser(name, password string) (*User, error) {
	u, err := getUserByName(c.DB, name)
	if err != nil || u == nil {
		return nil, ErrInvalidCredentials
	}
	if u.DeactivatedAt > 0 {
		return nil, ErrAccountDeactivated
	}
	switch u.RegistrationStatus {
	case "", "approved":
	case "pending":
		return nil, ErrAccountPending
	case "rejected":
		return nil, ErrAccountRejected
	default:
		return nil, ErrAccountPending
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

func (c *Core) AuthenticateUserFromHost(name, password, host string) (*User, error) {
	u, err := c.AuthenticateUser(name, password)
	if err != nil {
		return nil, err
	}
	allowed, err := c.UserLoginAllowed(u.ID, host)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrLoginIPDenied
	}
	return u, nil
}

func (c *Core) UserLoginAllowed(userID, host string) (bool, error) {
	bundle, err := projections.ListUserLoginACL(c.DB, userID, host)
	if err != nil {
		return false, err
	}
	return bundle.Allowed, nil
}

func (c *Core) ChangePassword(userID, currentPassword, newPassword string) error {
	if currentPassword == "" || newPassword == "" {
		return fmt.Errorf("current and new password required")
	}
	u, err := getUserByID(c.DB, userID)
	if err != nil || u == nil {
		return ErrInvalidCredentials
	}
	if u.DeactivatedAt > 0 {
		return ErrAccountDeactivated
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(currentPassword)); err != nil {
		return ErrInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := qExec(c.DB, `UPDATE users SET password=? WHERE id=?`, string(hash), userID); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

func (c *Core) DeactivateAccount(userID, password, reason string) error {
	if strings.TrimSpace(password) == "" {
		return ErrDeactivationIncomplete
	}
	u, err := getUserByID(c.DB, userID)
	if err != nil || u == nil {
		return ErrInvalidCredentials
	}
	if u.DeactivatedAt > 0 {
		return ErrAccountAlreadyClosed
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return ErrInvalidCredentials
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		reason = reason[:500]
	}
	ts := nowMS()
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	if _, err := qExec(tx,
		`UPDATE users SET deactivated_at=?, deactivated_by=?, deactivated_reason=? WHERE id=? AND deactivated_at=0`,
		ts, userID, reason, userID,
	); err != nil {
		return err
	}
	events, err := c.appendGoodbyeSystemPostTx(tx, u, ts)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, evt := range events {
		c.Bus.Publish(evt)
	}
	return nil
}

func (c *Core) DeleteUser(actorID, targetUserID, reason string) error {
	actorID = strings.TrimSpace(actorID)
	targetUserID = strings.TrimSpace(targetUserID)
	if actorID == "" || targetUserID == "" {
		return sql.ErrNoRows
	}
	if actorID == targetUserID {
		return fmt.Errorf("%w: cannot delete your own account", ErrAccountDeleteForbidden)
	}
	if targetUserID == deletedUserID {
		return fmt.Errorf("%w: cannot delete account tombstone", ErrAccountDeleteForbidden)
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		reason = reason[:500]
	}

	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	actor, err := getUserByIDTx(tx, actorID)
	if err != nil {
		return err
	}
	if actor == nil || !actor.IsAdmin() {
		return fmt.Errorf("%w: admin role required", ErrAccountDeleteForbidden)
	}
	target, err := getUserByIDTx(tx, targetUserID)
	if err != nil {
		return err
	}
	if target == nil {
		return sql.ErrNoRows
	}
	if target.Role == "admin" {
		var adminCount int
		if err := qQueryRow(tx, `SELECT COUNT(*) FROM users WHERE role='admin' AND id<>?`, targetUserID).Scan(&adminCount); err != nil {
			return err
		}
		if adminCount == 0 {
			return ErrLastAdminDeletion
		}
	}

	ts := nowMS()
	if err := ensureDeletedUserTx(tx, actorID, ts); err != nil {
		return err
	}
	if err := purgeUserTx(tx, actorID, target, ts); err != nil {
		return err
	}
	if _, err := qExec(tx, `DELETE FROM users WHERE id=?`, targetUserID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	slog.Info("user hard-deleted", "actor", actorID, "target", targetUserID, "name", target.Name, "reason", reason)
	return nil
}

func getUserByIDTx(tx *sql.Tx, id string) (*User, error) {
	u := &User{}
	err := qQueryRow(tx, `SELECT id, name, role, password, created,
	        COALESCE(NULLIF(registration_status,''), 'approved'), COALESCE(reviewed_at,0), COALESCE(reviewed_by,''), COALESCE(review_reason,''),
	        COALESCE(deactivated_at,0), COALESCE(deactivated_by,''), COALESCE(deactivated_reason,'')
	    FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created,
			&u.RegistrationStatus, &u.ReviewedAt, &u.ReviewedBy, &u.ReviewReason,
			&u.DeactivatedAt, &u.DeactivatedBy, &u.DeactivatedReason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func ensureDeletedUserTx(tx *sql.Tx, actorID string, ts int64) error {
	var exists int
	if err := qQueryRow(tx, `SELECT 1 FROM users WHERE id=?`, deletedUserID).Scan(&exists); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	name := "deleted-user"
	for i := 0; i < 10; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("deleted-user-%d", i)
		}
		var conflictingID string
		err := qQueryRow(tx, `SELECT id FROM users WHERE name=?`, candidate).Scan(&conflictingID)
		if err == sql.ErrNoRows {
			name = candidate
			break
		}
		if err != nil {
			return err
		}
		if conflictingID == deletedUserID {
			name = candidate
			break
		}
	}
	if _, err := qExec(tx,
		`INSERT INTO users (id, name, role, password, created, registration_status, reviewed_at, reviewed_by, review_reason, deactivated_at, deactivated_by, deactivated_reason)
		 VALUES (?, ?, 'user', '', ?, 'rejected', ?, ?, 'system tombstone account', ?, ?, 'system tombstone account')`,
		deletedUserID, name, ts, ts, actorID, ts, actorID,
	); err != nil {
		return err
	}
	_, err := qExec(tx,
		`INSERT OR IGNORE INTO user_profiles (user_id, display_name, updated_at) VALUES (?, ?, ?)`,
		deletedUserID, deletedUserDisplayName, ts,
	)
	return err
}

func purgeUserTx(tx *sql.Tx, actorID string, target *User, ts int64) error {
	targetID := target.ID
	oldName := target.Name
	cleanup := []struct {
		query string
		args  []any
	}{
		{`UPDATE posts_fts SET author=? WHERE post_id IN (SELECT id FROM posts WHERE author_id=?)`, []any{deletedUserDisplayName, targetID}},
		{`UPDATE threads SET author=?, author_id=? WHERE author_id=?`, []any{deletedUserDisplayName, deletedUserID, targetID}},
		{`UPDATE posts SET author=?, author_id=?, signature='' WHERE author_id=?`, []any{deletedUserDisplayName, deletedUserID, targetID}},
		{`UPDATE relay_deliveries SET author_id=?, author_name=? WHERE author_id=?`, []any{deletedUserID, deletedUserDisplayName, targetID}},
		{`UPDATE post_attachments SET created_by=? WHERE created_by=?`, []any{deletedUserID, targetID}},
		{`UPDATE mail_attachments SET created_by=? WHERE created_by=?`, []any{deletedUserID, targetID}},
		{`UPDATE digest_entries SET created_by=?, updated_at=? WHERE created_by=?`, []any{actorID, ts, targetID}},
		{`UPDATE digest_directories SET created_by=?, updated_at=? WHERE created_by=?`, []any{actorID, ts, targetID}},
		{`UPDATE users SET reviewed_by='' WHERE reviewed_by=?`, []any{targetID}},
		{`UPDATE account_registration_settings SET updated_at=? WHERE id='default' AND updated_at=0`, []any{ts}},
		{`UPDATE password_recovery_requests SET reviewer_id='', review_note='' WHERE reviewer_id=?`, []any{targetID}},
		{`UPDATE board_member_applications SET reviewer_id='', review_note='' WHERE reviewer_id=?`, []any{targetID}},
		{`UPDATE user_sanctions SET by=? WHERE by=?`, []any{deletedUserID, targetID}},
		{`UPDATE moderation_reviews SET actor=? WHERE actor=? OR actor=?`, []any{deletedUserDisplayName, targetID, oldName}},
		{`UPDATE notifications SET actor=? WHERE actor=?`, []any{deletedUserDisplayName, oldName}},
		{`DELETE FROM mail_messages WHERE from_user_id=?`, []any{targetID}},
		{`DELETE FROM direct_messages WHERE from_user_id=? OR to_user_id=?`, []any{targetID, targetID}},
		{`DELETE FROM moderation_reviews WHERE reporter=?`, []any{targetID}},
		{`DELETE FROM post_reactions WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM poll_votes WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM thread_prefs WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM notifications WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM cursors WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM auth_pubkeys WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_sanctions WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM processed_commands WHERE actor_id=?`, []any{targetID}},
		{`DELETE FROM user_activity WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM mail_copies WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM mail_messages WHERE id NOT IN (SELECT DISTINCT message_id FROM mail_copies)`, nil},
		{`DELETE FROM mail_group_members WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM mail_groups WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM password_recovery_requests WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM favorite_folders WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM board_favorites WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM board_moderators WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM board_members WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM board_member_applications WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM direct_message_settings WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_relationships WHERE user_id=? OR target_user_id=?`, []any{targetID, targetID}},
		{`DELETE FROM user_presence WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_presence_sessions WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM board_read_markers WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM thread_read_markers WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_private_profiles WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_personal_files WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_signatures WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_signature_settings WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_login_acl_rules WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_login_acl_settings WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_profiles WHERE user_id=?`, []any{targetID}},
	}
	for _, step := range cleanup {
		if _, err := qExec(tx, step.query, step.args...); err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) appendGoodbyeSystemPostTx(tx *sql.Tx, user *User, ts int64) ([]*proto.Event, error) {
	const boardID = "Goodbye"
	threadID := "goodbye_thr_" + user.ID
	postID := "goodbye_pst_" + user.ID
	var exists int
	err := qQueryRow(tx, `SELECT 1 FROM threads WHERE id=?`, threadID).Scan(&exists)
	if err == nil {
		return nil, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	out := []*proto.Event{}
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, boardID).Scan(&exists)
	if err == sql.ErrNoRows {
		var position int
		if err := qQueryRow(tx, `SELECT COALESCE(MAX(position) + 1, 0) FROM categories WHERE parent_id=''`).Scan(&position); err != nil {
			return nil, err
		}
		boardScopes := []string{"board:" + boardID}
		boardSeq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          boardID,
			Name:        "Goodbye",
			Description: "Generated account deactivation notices",
			Position:    position,
			By:          user.ID,
			TS:          ts,
		})
		if err != nil {
			return nil, err
		}
		if err := insertBoard(tx, boardID, "Goodbye", "Generated account deactivation notices", "", position); err != nil {
			return nil, err
		}
		out = append(out, &proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: boardScopes,
			Payload: &proto.BoardCreatedPayload{ID: boardID, Name: "Goodbye", Description: "Generated account deactivation notices", By: user.Name, TS: ts}, TS: ts})
	} else if err != nil {
		return nil, err
	}

	title := "Goodbye: " + user.Name
	body := fmt.Sprintf("# %s\n\n- User: %s\n- Status: deactivated\n\nThe account holder closed this account. Private deactivation notes are not published.\n",
		title, user.Name)
	scopes := []string{"board:" + boardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: boardID, Author: user.Name, AuthorID: user.ID, Title: title, TS: ts,
	})
	if err != nil {
		return nil, err
	}
	threadScopes := append(scopes, "thread:"+threadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: user.Name, AuthorID: user.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return nil, err
	}
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: boardID, Author: user.Name, AuthorID: user.ID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: user.Name, AuthorID: user.ID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return nil, err
	}
	if err := ftsInsertPost(tx, postID, threadID, boardID, user.Name, body); err != nil {
		return nil, err
	}
	out = append(out,
		&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
			Payload: &proto.ThreadNewPayload{ID: threadID, Board: boardID, Author: user.Name, AuthorID: user.ID, Title: title, TS: ts}, TS: ts},
		&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
			Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: user.Name, AuthorID: user.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts},
	)
	return out, nil
}

func (c *Core) RecordLogin(userID string) error {
	return recordLogin(c.DB, userID)
}

// AddPubkey registers an SSH public key for the given user.
func (c *Core) AddPubkey(userID, pubkey string) error {
	_, err := qExec(c.DB,
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

// UserByName returns the user for the given username.
func (c *Core) UserByName(name string) (*User, error) {
	return getUserByName(c.DB, name)
}

// --- Projection readers (safe for concurrent access) ---

func (c *Core) ListBoards() ([]Board, error)        { return listBoards(c.DB) }
func (c *Core) ListCategories() ([]Category, error) { return listCategories(c.DB) }
func (c *Core) GetBoard(id string) (*Board, error)  { return getBoard(c.DB, id) }

type CategoryUpdate struct {
	Name        *string
	Description *string
	ParentID    *string
	Position    *int
	Visibility  *string
}

func (c *Core) ListCategoriesForUser(viewer *User) ([]Category, error) {
	categories, err := c.ListCategories()
	if err != nil {
		return nil, err
	}
	if viewer != nil && viewer.IsAdmin() {
		return categories, nil
	}
	out := make([]Category, 0, len(categories))
	for _, category := range categories {
		visibility := strings.TrimSpace(strings.ToLower(category.Visibility))
		switch visibility {
		case "", "public":
			out = append(out, category)
		case "staff":
			if viewer != nil && viewer.IsMod() {
				out = append(out, category)
			}
		}
	}
	return out, nil
}

func (c *Core) UpdateCategory(actorID, categoryID string, patch CategoryUpdate) (*Category, error) {
	actorID = strings.TrimSpace(actorID)
	categoryID = strings.TrimSpace(categoryID)
	if actorID == "" || categoryID == "" {
		return nil, sql.ErrNoRows
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	actor, err := getUserByIDTx(tx, actorID)
	if err != nil {
		return nil, err
	}
	if actor == nil || !actor.IsAdmin() {
		return nil, fmt.Errorf("%w: admin role required", ErrAccountDeleteForbidden)
	}
	category, err := getCategoryTx(tx, categoryID)
	if err != nil {
		return nil, err
	}
	if category == nil {
		return nil, sql.ErrNoRows
	}

	name := category.Name
	description := category.Description
	parentID := category.ParentID
	position := category.Position
	visibility := category.Visibility
	if patch.Name != nil {
		name = strings.TrimSpace(*patch.Name)
		if name == "" {
			return nil, fmt.Errorf("category name required")
		}
	}
	if patch.Description != nil {
		description = strings.TrimSpace(*patch.Description)
	}
	parentChanged := false
	if patch.ParentID != nil {
		parentID = strings.TrimSpace(*patch.ParentID)
		parentChanged = parentID != category.ParentID
		if parentID == categoryID {
			return nil, fmt.Errorf("category cannot be its own parent")
		}
		if err := validateCategoryParentTx(tx, categoryID, parentID); err != nil {
			return nil, err
		}
	}
	if patch.Position != nil {
		if *patch.Position < 0 {
			return nil, fmt.Errorf("position cannot be negative")
		}
		position = *patch.Position
	} else if parentChanged {
		position, err = nextCategoryPositionTx(tx, parentID)
		if err != nil {
			return nil, err
		}
	}
	if patch.Visibility != nil {
		visibility = strings.TrimSpace(strings.ToLower(*patch.Visibility))
		if visibility == "" {
			visibility = "public"
		}
		switch visibility {
		case "public", "staff", "hidden":
		default:
			return nil, fmt.Errorf(`visibility must be "public", "staff", or "hidden"`)
		}
	}

	ts := nowMS()
	if _, err := qExec(tx,
		`UPDATE categories
		    SET name=?, description=?, parent_id=?, position=?, visibility=?, updated_at=?
		  WHERE id=?`,
		name, description, parentID, position, visibility, ts, categoryID,
	); err != nil {
		return nil, err
	}
	if _, err := qExec(tx, `UPDATE boards SET name=?, description=? WHERE id=?`, name, description, categoryID); err != nil {
		return nil, err
	}
	updated, err := getCategoryTx(tx, categoryID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func getCategoryTx(tx *sql.Tx, categoryID string) (*Category, error) {
	var category Category
	err := qQueryRow(tx,
		`SELECT id, name, description, parent_id, position, visibility, created_at, updated_at
		   FROM categories WHERE id=?`,
		categoryID,
	).Scan(&category.ID, &category.Name, &category.Description, &category.ParentID, &category.Position, &category.Visibility, &category.CreatedAt, &category.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &category, err
}

func nextCategoryPositionTx(tx *sql.Tx, parentID string) (int, error) {
	var next int
	err := qQueryRow(tx, `SELECT COALESCE(MAX(position) + 1, 0) FROM categories WHERE parent_id=?`, parentID).Scan(&next)
	return next, err
}

func validateCategoryParentTx(tx *sql.Tx, categoryID, parentID string) error {
	seen := map[string]bool{categoryID: true}
	for parentID != "" {
		if seen[parentID] {
			return fmt.Errorf("category parent would create a cycle")
		}
		seen[parentID] = true
		var next string
		err := qQueryRow(tx, `SELECT parent_id FROM categories WHERE id=?`, parentID).Scan(&next)
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		if err != nil {
			return err
		}
		parentID = next
	}
	return nil
}

func (c *Core) GetCommunityStats() (*CommunityStats, error) {
	return getCommunityStats(c.DB)
}

func (c *Core) ListCommunityStatHistory(limit, offset int) ([]CommunityStatHistory, error) {
	return listCommunityStatHistory(c.DB, limit, offset)
}

func (c *Core) SetGuestPresence(sessionID, status, locationLabel, fromHost string, at time.Time) error {
	ts := at.UTC().UnixMilli()
	if at.IsZero() {
		ts = time.Now().UTC().UnixMilli()
	}
	return projections.SetGuestPresence(c.DB, sessionID, status, locationLabel, fromHost, ts)
}

func (c *Core) PublishDailyStatsSnapshot(ctx context.Context, at time.Time) (*proto.AckResult, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := projections.UpsertCommunityStatHistoryFromCurrent(c.DB, at.UTC().UnixMilli()); err != nil {
		return nil, err
	}
	day := at.UTC().Format("2006-01-02")
	raw, err := json.Marshal(proto.PublishStatsSnapshotPayload{Date: day})
	if err != nil {
		return nil, err
	}
	systemActor := &User{ID: "system", Name: "system", Role: "admin", RegistrationStatus: "approved"}
	reply := c.ExecCmd(ctx, systemActor, proto.CmdPublishStatsSnapshot, raw, "auto-stats-"+day)
	if reply.Err != nil {
		return nil, fmt.Errorf("%s: %s", reply.Err.Code, reply.Err.Message)
	}
	return reply.Result, nil
}

func (c *Core) ListBoardRankings(actor *User, limit, offset int) ([]BoardRanking, error) {
	return listBoardRankings(c.DB, actor.ID, actor.IsMod(), limit, offset)
}
func (c *Core) ListThreadRankings(actor *User, boardID string, limit, offset int) ([]ThreadRanking, error) {
	return listThreadRankings(c.DB, actor.ID, actor.IsMod(), boardID, limit, offset)
}
func (c *Core) ListReplyRankings(actor *User, limit, offset int) ([]ReplyRanking, error) {
	return listReplyRankings(c.DB, actor.ID, actor.IsMod(), limit, offset)
}
func (c *Core) ListUserRankings(limit, offset int) ([]UserRanking, error) {
	return listUserRankings(c.DB, limit, offset)
}
func (c *Core) ListBlessingRankings(limit, offset int) ([]BlessingRanking, error) {
	return listBlessingRankings(c.DB, limit, offset)
}
func (c *Core) ListBlessings(limit, offset int) ([]Blessing, error) {
	return listBlessings(c.DB, limit, offset)
}
func (c *Core) ListArchiveRankings(actor *User, kind string, limit, offset int) ([]ArchiveRanking, error) {
	return listArchiveRankings(c.DB, actor.ID, actor.IsMod(), kind, limit, offset)
}
func (c *Core) ListBoardSummaries(userID string, unreadOnly bool, opts ...BoardSummaryOptions) ([]BoardSummary, error) {
	return listBoardSummaries(c.DB, userID, unreadOnly, opts...)
}
func (c *Core) GetBoardInfo(boardID string) (*BoardInfo, error) {
	return getBoardInfo(c.DB, boardID)
}
func (c *Core) GetBoardMemberRequirements(boardID string) (*BoardMemberRequirements, error) {
	return getBoardMemberRequirements(c.DB, boardID)
}
func (c *Core) ListBoardMembers(boardID string) ([]BoardMember, error) {
	return listBoardMembers(c.DB, boardID)
}
func (c *Core) UserIsBoardMember(boardID, userID string) (bool, error) {
	return userIsBoardMember(c.DB, boardID, userID)
}
func (c *Core) GetBoardMemberApplication(applicationID string) (*BoardMemberApplication, error) {
	return getBoardMemberApplication(c.DB, applicationID)
}
func (c *Core) ListBoardMemberApplications(boardID, status, userID string, limit, offset int) ([]BoardMemberApplication, error) {
	return listBoardMemberApplications(c.DB, boardID, status, userID, limit, offset)
}
func (c *Core) ListDigestEntries(boardID, kind, path string, limit, offset int) ([]DigestEntry, error) {
	return listDigestEntries(c.DB, boardID, kind, path, limit, offset)
}
func (c *Core) ListDigestPathTree(boardID, kind string) ([]DigestPathNode, error) {
	return listDigestPathTree(c.DB, boardID, kind)
}
func (c *Core) ListSiteDigestEntries(actor *User, kind, path string, limit, offset int) ([]DigestEntry, error) {
	viewerID := ""
	includePrivate := false
	if actor != nil {
		viewerID = actor.ID
		includePrivate = actor.IsMod()
	}
	return listSiteDigestEntries(c.DB, viewerID, includePrivate, kind, path, limit, offset)
}
func (c *Core) SearchDigestEntries(actor *User, boardID, kind, path, query string, limit, offset int) ([]DigestEntry, error) {
	viewerID := ""
	includePrivate := false
	if actor != nil {
		viewerID = actor.ID
		includePrivate = actor.IsMod()
	}
	return searchDigestEntries(c.DB, viewerID, includePrivate, boardID, kind, path, query, limit, offset)
}
func (c *Core) GetDigestExport(entryID string) (*DigestExport, error) {
	return getDigestExport(c.DB, entryID)
}
func FormatDigestExportText(export *DigestExport) string {
	return projections.FormatDigestExportText(export)
}
func (c *Core) ListMail(userID, mailbox string, limit, offset int, unreadOnly bool) ([]MailItem, error) {
	return listMail(c.DB, userID, mailbox, limit, offset, unreadOnly)
}
func (c *Core) GetMail(userID, messageID string) (*MailItem, error) {
	return getMail(c.DB, userID, messageID)
}
func (c *Core) CountUnreadMail(userID string) (int, error) {
	return countUnreadMail(c.DB, userID)
}
func (c *Core) GetMailUsage(userID string) (*MailUsage, error) {
	return getMailUsage(c.DB, userID)
}
func (c *Core) ListRelayDeliveries(status string, limit, offset int) ([]RelayDelivery, error) {
	return listRelayDeliveries(c.DB, status, limit, offset)
}
func (c *Core) ListMailGroups(userID string) ([]MailGroup, error) {
	groups, err := listMailGroups(c.DB, userID)
	if err != nil {
		return nil, err
	}
	friends, err := listSocialUsers(c.DB, userID, "friends", false)
	if err != nil {
		return nil, err
	}
	friendGroup := MailGroup{ID: "friends", Name: "Friends", BuiltIn: true}
	for i, friend := range friends {
		friendGroup.Members = append(friendGroup.Members, MailGroupMember{
			UserID:   friend.UserID,
			Name:     friend.Name,
			Position: i,
		})
	}
	return append([]MailGroup{friendGroup}, groups...), nil
}
func (c *Core) GetDirectMessageSettings(userID string) (*DirectMessageSettings, error) {
	return getDirectMessageSettings(c.DB, userID)
}
func (c *Core) ListDirectMessageConversations(userID string, limit, offset int) ([]DirectMessageConversation, error) {
	return listDirectMessageConversations(c.DB, userID, limit, offset)
}
func (c *Core) ListDirectMessages(userID, otherUserID string, limit, offset int) ([]DirectMessage, error) {
	return listDirectMessages(c.DB, userID, otherUserID, limit, offset)
}
func (c *Core) CountUnreadDirectMessages(userID string) (int, error) {
	return countUnreadDirectMessages(c.DB, userID)
}
func (c *Core) ListSocialUsers(userID, list string, onlineOnly bool) ([]SocialUser, error) {
	return listSocialUsers(c.DB, userID, list, onlineOnly)
}

func (c *Core) ListOnlineUsers(viewerID, boardID string, limit, offset int) ([]SocialUser, error) {
	return listOnlineUsers(c.DB, viewerID, boardID, limit, offset)
}
func (c *Core) ListFavoriteBoards(userID string) ([]Board, error) {
	return listFavoriteBoards(c.DB, userID)
}
func (c *Core) ListFavoriteTree(userID string) (*FavoriteTree, error) {
	return listFavoriteTree(c.DB, userID)
}
func (c *Core) ImportFavoriteTree(userID string, tree *FavoriteTree, replace bool) (*FavoriteTree, error) {
	if err := importFavoriteTree(c.DB, userID, tree, replace); err != nil {
		return nil, err
	}
	return c.ListFavoriteTree(userID)
}
func (c *Core) ListThreads(board string, limit, offset int) ([]Thread, error) {
	return listThreads(c.DB, board, limit, offset)
}
func (c *Core) ListThreadSummaries(userID, board string, limit, offset int, unreadOnly bool) ([]ThreadSummary, error) {
	return listThreadSummaries(c.DB, userID, board, limit, offset, unreadOnly)
}
func (c *Core) ListThreadSummariesFiltered(userID, board, titleQuery, authorQuery string, limit, offset int, unreadOnly bool) ([]ThreadSummary, error) {
	return listThreadSummariesFiltered(c.DB, userID, board, titleQuery, authorQuery, limit, offset, unreadOnly)
}
func (c *Core) ListUnreadThreadSummaries(actor *User, favoritesOnly bool, folderID string, limit, offset int) ([]ThreadSummary, error) {
	return listUnreadThreadSummaries(c.DB, actor.ID, actor.IsMod(), favoritesOnly, folderID, limit, offset)
}
func (c *Core) GetThread(id string) (*Thread, error) { return getThread(c.DB, id) }
func (c *Core) ListPosts(thread string, limit, offset int) ([]Post, error) {
	return listPosts(c.DB, thread, limit, offset)
}
func (c *Core) ListReplyTreePosts(rootPostID string, limit, offset int) ([]Post, error) {
	return listReplyTreePosts(c.DB, rootPostID, limit, offset)
}
func (c *Core) GetPostAttachment(attachmentID string) (*PostAttachment, error) {
	return getPostAttachment(c.DB, attachmentID)
}
func (c *Core) GetAttachmentBlob(attachmentID string) ([]byte, string, error) {
	return getAttachmentBlob(c.DB, attachmentID)
}
func (c *Core) StoreAttachmentBlob(attachmentID string, data []byte, contentType string) error {
	return storeAttachmentBlob(c.DB, attachmentID, data, contentType)
}
func (c *Core) GetMailAttachment(attachmentID string) (*MailAttachment, error) {
	return getMailAttachment(c.DB, attachmentID)
}
func (c *Core) GetMailAttachmentBlob(attachmentID string) ([]byte, string, error) {
	return getMailAttachmentBlob(c.DB, attachmentID)
}
func (c *Core) StoreMailAttachmentBlob(attachmentID string, data []byte, contentType string) error {
	return storeMailAttachmentBlob(c.DB, attachmentID, data, contentType)
}
func (c *Core) GetPost(id string) (*Post, error) { return getPost(c.DB, id) }
func (c *Core) SearchPosts(query, boardID string, limit int) ([]Post, error) {
	return searchPosts(c.DB, query, boardID, limit)
}

func (c *Core) SearchReadablePosts(actor *User, query, boardID string, limit int) ([]Post, error) {
	if actor == nil {
		return searchReadablePosts(c.DB, "", false, query, boardID, limit)
	}
	return searchReadablePosts(c.DB, actor.ID, actor.IsMod(), query, boardID, limit)
}

func (c *Core) ListPostsByAuthor(name string, limit, offset int) ([]Post, error) {
	return listPostsByAuthor(c.DB, name, limit, offset)
}

func (c *Core) ListReadablePostsByAuthor(actor *User, name string, limit, offset int) ([]Post, error) {
	return listReadablePostsByAuthor(c.DB, actor.ID, actor.IsMod(), name, limit, offset)
}

// AuditLog returns recent durable events (mod/admin use).
func (c *Core) AuditLog(after int64, limit int) ([]*proto.Event, error) {
	return replayEvents(c.DB, after, nil, limit)
}

// ── M10: Reactions ──────────────────────────────────────────────────────────

func (c *Core) ReactionCount(postID string) (int, error) {
	return reactionCount(c.DB, postID)
}
func (c *Core) UserReacted(postID, userID string) (bool, error) {
	return userReacted(c.DB, postID, userID)
}

// ── M11: Polls ──────────────────────────────────────────────────────────────

func (c *Core) GetPoll(pollID, viewerUserID string) (*Poll, error) {
	return getPollWithVotes(c.DB, pollID, viewerUserID)
}
func (c *Core) GetPollByPostID(postID string) (*Poll, error) {
	return getPollByPostID(c.DB, postID)
}
func (c *Core) PollsForPosts(postIDs []string, viewerUserID string) (map[string]*Poll, error) {
	return pollsForPosts(c.DB, postIDs, viewerUserID)
}

// ── M8: Notifications ───────────────────────────────────────────────────────

func (c *Core) ListNotifications(userID string, limit, offset int, unreadOnly bool) ([]Notification, error) {
	return listNotifications(c.DB, userID, limit, offset, unreadOnly)
}
func (c *Core) CountUnreadNotifications(userID string) (int, error) {
	return countUnreadNotifications(c.DB, userID)
}
func (c *Core) MarkNotificationRead(id, userID string) error {
	return markNotificationRead(c.DB, id, userID)
}
func (c *Core) MarkAllNotificationsRead(userID string) error {
	return markAllNotificationsRead(c.DB, userID)
}

// ── M9: Trust levels ────────────────────────────────────────────────────────

func (c *Core) TrustInfo(userID string) (*TrustLevelInfo, error) {
	return trustInfo(c.DB, userID)
}

// ── Modern forum projections ───────────────────────────────────────────────

func (c *Core) UserProfileByName(name string) (*UserProfile, error) {
	return getUserProfileByName(c.DB, name)
}

func (c *Core) ListUserPubkeyTitles(name string) ([]string, error) {
	return listPubkeyTitlesByUserName(c.DB, name)
}

func (c *Core) UpdateUserProfile(userID, displayName, title, bio, avatar, signature, plan, homepage string) error {
	return updateUserProfile(c.DB, userID, displayName, title, bio, avatar, signature, plan, homepage)
}

func (c *Core) UserPrivateProfile(userID string) (*UserPrivateProfile, error) {
	return getUserPrivateProfile(c.DB, userID)
}

func (c *Core) UpdateUserPrivateProfile(profile *UserPrivateProfile) error {
	return updateUserPrivateProfile(c.DB, profile)
}

func (c *Core) AccountRegistrationSettings() (*AccountRegistrationSettings, error) {
	return getAccountRegistrationSettings(c.DB)
}

func (c *Core) SetAccountRegistrationSettings(requireApproval bool) (*AccountRegistrationSettings, error) {
	return setAccountRegistrationSettings(c.DB, requireApproval)
}

func (c *Core) ListAccountRegistrations(status string, limit, offset int) ([]AccountRegistration, error) {
	return listAccountRegistrations(c.DB, status, limit, offset)
}

func (c *Core) ReviewAccountRegistration(userID, reviewerID, decision, reason string) (*AccountRegistration, error) {
	review, err := reviewAccountRegistration(c.DB, userID, reviewerID, decision, reason)
	if err != nil {
		return nil, err
	}
	if review != nil && review.Status == "approved" {
		user, err := c.UserByID(userID)
		if err != nil {
			return nil, err
		}
		if user != nil {
			tx, err := c.DB.Begin()
			if err != nil {
				return nil, err
			}
			events, err := c.appendNewcomerSystemPostTx(tx, user, nowMS())
			if err != nil {
				_ = tx.Rollback()
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			for _, evt := range events {
				c.Bus.Publish(evt)
			}
		}
	}
	return review, nil
}

func (c *Core) RequestPasswordRecovery(name, submittedName, submittedEmail, note string) (*PasswordRecoveryRequest, error) {
	u, err := c.UserByName(name)
	if err != nil || u == nil {
		return nil, err
	}
	return createPasswordRecoveryRequest(c.DB, newID("pwdrec_"), u.ID, submittedName, submittedEmail, note)
}

func (c *Core) ListPasswordRecoveryRequests(status string, limit, offset int) ([]PasswordRecoveryRequest, error) {
	return listPasswordRecoveryRequests(c.DB, status, limit, offset)
}

func (c *Core) ReviewPasswordRecoveryRequest(requestID, reviewerID, decision, newPassword, note string) (*PasswordRecoveryRequest, error) {
	var passwordHash string
	if strings.ToLower(strings.TrimSpace(decision)) == "reset" {
		if strings.TrimSpace(newPassword) == "" {
			return nil, fmt.Errorf("new password required")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		passwordHash = string(hash)
	}
	return reviewPasswordRecoveryRequest(c.DB, requestID, reviewerID, decision, passwordHash, note)
}

func (c *Core) TransferUserID(userID, newName string) (*User, error) {
	return transferUserID(c.DB, userID, newName)
}

func (c *Core) ListUserPersonalFiles(userID string, includePrivate bool) ([]UserPersonalFile, error) {
	return listUserPersonalFiles(c.DB, userID, includePrivate)
}

func (c *Core) GetUserPersonalFile(userID, name string, includePrivate bool) (*UserPersonalFile, error) {
	return getUserPersonalFile(c.DB, userID, name, includePrivate)
}

func (c *Core) SaveUserPersonalFile(userID, name, body string, public bool) (*UserPersonalFile, error) {
	return saveUserPersonalFile(c.DB, userID, name, body, public)
}

func (c *Core) DeleteUserPersonalFile(userID, name string) error {
	return deleteUserPersonalFile(c.DB, userID, name)
}

func (c *Core) ListUserSignatures(userID string) (*UserSignatureBundle, error) {
	return projections.ListUserSignatures(c.DB, userID)
}

func (c *Core) SaveUserSignature(userID, signatureID, label, body string, position int, active bool) (*UserSignature, error) {
	if strings.TrimSpace(signatureID) == "" {
		signatureID = newID("sig_")
	}
	return projections.UpsertUserSignature(c.DB, signatureID, userID, label, body, position, active)
}

func (c *Core) DeleteUserSignature(userID, signatureID string) error {
	return projections.DeleteUserSignature(c.DB, userID, signatureID)
}

func (c *Core) SetUserSignatureSettings(userID, selectedSignatureID string, randomEnabled bool) error {
	return projections.SetUserSignatureSettings(c.DB, userID, selectedSignatureID, randomEnabled)
}

func (c *Core) RecountUserSignatures(userID string) (*UserSignatureRecount, error) {
	return recountUserSignatures(c.DB, userID)
}

func (c *Core) ListUserLoginACL(userID, host string) (*UserLoginACLBundle, error) {
	return projections.ListUserLoginACL(c.DB, userID, host)
}

func (c *Core) SaveUserLoginACLRule(userID, ruleID, pattern, note string, position int, active bool) (*UserLoginACLRule, error) {
	if strings.TrimSpace(ruleID) == "" {
		ruleID = newID("acl_")
	}
	return projections.UpsertUserLoginACLRule(c.DB, ruleID, userID, pattern, note, position, active)
}

func (c *Core) DeleteUserLoginACLRule(userID, ruleID string) error {
	return projections.DeleteUserLoginACLRule(c.DB, userID, ruleID)
}

func (c *Core) SetUserLoginACLSettings(userID string, enabled bool) error {
	return projections.SetUserLoginACLSettings(c.DB, userID, enabled)
}

func (c *Core) ListModerationReviews(status string, limit, offset int) ([]ModerationReview, error) {
	return listModerationReviews(c.DB, status, limit, offset)
}

func (c *Core) ListContentFilters(scope string, includeInactive bool, limit, offset int) ([]ContentFilter, error) {
	return listContentFilters(c.DB, scope, includeInactive, limit, offset)
}

func (c *Core) ListUserSanctions(userID string, limit, offset int) ([]UserSanction, error) {
	return listUserSanctions(c.DB, userID, limit, offset)
}

// RebuildProjectionsFromEventLog truncates projection tables and replays all durable
// events from the given sequence onward to rebuild event-derived state.
func (c *Core) RebuildProjectionsFromEventLog(fromSeq int64) error {
	return rebuildProjectionsFromEventLog(c.DB, fromSeq)
}
