package core

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/juncoflockleader/budgie-bbs/internal/core/accountmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/accountstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// RegisterUser creates a normal account. Names ending in the reserved AI suffix
// are rejected; the bot accounts themselves are
// created via registerUserInternal, which skips this check.
func (c *Core) RegisterUser(name, password string) (*projections.User, error) {
	if accountmodel.IsReservedAIBotName(name) {
		return nil, fmt.Errorf("user name may not end in %q (reserved for AI bots)", accountmodel.ReservedAIBotNameSuffix)
	}
	return c.registerUserInternal(name, password)
}

func (c *Core) registerUserInternal(name, password string) (*projections.User, error) {
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

	// Serialize registrations on Postgres so the "first user becomes admin"
	// bootstrap cannot race: under READ COMMITTED two concurrent registrations
	// against an empty DB would both see COUNT(users)=0 and both become admin.
	// SQLite is already serialized by its single writer connection.
	if err := acquireUserBootstrapGate(tx); err != nil {
		return nil, fmt.Errorf("registration gate: %w", err)
	}

	userCount, requireApproval, err := accountstore.RegistrationStateTx(tx)
	if err != nil {
		return nil, err
	}
	role := "user"
	status := "approved"
	if userCount == 0 {
		role = "admin"
	} else if requireApproval {
		status = "pending"
	}

	if err := accountstore.CreateRegisteredUserTx(tx, id, name, role, string(hash), status, ts); err != nil {
		return nil, err
	}
	user := &projections.User{ID: id, Name: name, Role: role, Created: ts, RegistrationStatus: status}
	events := []*proto.Event{}
	if status == "approved" {
		events, err = c.appendAccountLifecycleRecordTx(tx, accountmodel.NewcomerLifecycleRecord(accountLifecycleUser(user)), ts)
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

func accountLifecycleUser(user *projections.User) accountmodel.LifecycleUser {
	if user == nil {
		return accountmodel.LifecycleUser{}
	}
	return accountmodel.LifecycleUser{
		ID:   user.ID,
		Name: user.Name,
		Role: user.Role,
	}
}

func (c *Core) appendAccountLifecycleRecordTx(tx *sql.Tx, record accountmodel.LifecycleRecord, ts int64) ([]*proto.Event, error) {
	exists, err := projections.ThreadExists(tx, record.ThreadID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, nil
	}

	out := []*proto.Event{}
	exists, err = projections.BoardExists(tx, record.BoardID)
	if err != nil {
		return nil, err
	}
	if !exists {
		position, err := projections.NextCategoryPosition(tx, "")
		if err != nil {
			return nil, err
		}
		boardScopes := []string{"board:" + record.BoardID}
		boardSeq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          record.BoardID,
			Name:        record.BoardName,
			Description: record.BoardDescription,
			Position:    position,
			By:          record.AuthorID,
			TS:          ts,
		})
		if err != nil {
			return nil, err
		}
		if err := projections.InsertBoard(tx, record.BoardID, record.BoardName, record.BoardDescription, "", position); err != nil {
			return nil, err
		}
		out = append(out, &proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: boardScopes,
			Payload: &proto.BoardCreatedPayload{ID: record.BoardID, Name: record.BoardName, Description: record.BoardDescription, By: record.AuthorName, TS: ts}, TS: ts})
	}

	scopes := []string{"board:" + record.BoardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: record.ThreadID, Board: record.BoardID, Author: record.AuthorName, AuthorID: record.AuthorID, Title: record.Title, TS: ts,
	})
	if err != nil {
		return nil, err
	}
	threadScopes := append(scopes, "thread:"+record.ThreadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: record.PostID, Thread: record.ThreadID, Author: record.AuthorName, AuthorID: record.AuthorID, Body: record.Body, RawBody: record.Body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return nil, err
	}
	if err := projections.InsertThread(tx, &projections.Thread{
		ID: record.ThreadID, Board: record.BoardID, Author: record.AuthorName, AuthorID: record.AuthorID, Title: record.Title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := projections.InsertPost(tx, &projections.Post{
		ID: record.PostID, Thread: record.ThreadID, Author: record.AuthorName, AuthorID: record.AuthorID,
		Body: record.Body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return nil, err
	}
	if err := projections.BumpThread(tx, record.ThreadID, pseq); err != nil {
		return nil, err
	}
	if err := commandFtsInsertPost(tx, record.PostID, record.ThreadID, record.BoardID, record.AuthorName, record.Body); err != nil {
		return nil, err
	}
	if record.MarkBoardReadForAllUsers {
		if err := projections.MarkBoardReadForAllUsersTx(tx, record.BoardID, pseq, ts); err != nil {
			return nil, err
		}
	}
	out = append(out,
		&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
			Payload: &proto.ThreadNewPayload{ID: record.ThreadID, Board: record.BoardID, Author: record.AuthorName, AuthorID: record.AuthorID, Title: record.Title, TS: ts}, TS: ts},
		&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
			Payload: &proto.PostAppendedPayload{ID: record.PostID, Thread: record.ThreadID, Author: record.AuthorName, AuthorID: record.AuthorID, Body: record.Body, RawBody: record.Body, ContentType: "markup", TS: ts}, TS: ts},
	)
	return out, nil
}

// dummyBcryptHash is compared against on the no-such-user login path so
// authentication spends roughly the same time whether or not the account
// exists, mitigating username enumeration via response timing. (bcrypt is the
// dominant cost of a login.)
var dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("constant-time-placeholder"), bcrypt.DefaultCost)

// AuthenticateUser verifies credentials and returns the user on success.
func (c *Core) AuthenticateUser(name, password string) (*projections.User, error) {
	u, err := projections.GetUserByName(c.DB, name)
	if err != nil || u == nil {
		// Run a dummy comparison so a missing account isn't distinguishable from
		// a wrong password by timing.
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
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
	// Accounts that began email verification (email_verified=0) must confirm
	// before logging in. Existing/bootstrap accounts default to verified.
	if !c.emailVerified(u.ID) {
		return nil, ErrEmailNotVerified
	}
	return u, nil
}

func (c *Core) AuthenticateUserFromHost(name, password, host string) (*projections.User, error) {
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

// AuthorizeUserSession applies the non-credential login gates to an
// already-identified user: account deactivation, registration status, email
// verification, and the login-host ACL. The SSH public-key path uses this so it
// cannot bypass the gates the password and HTTP login paths enforce (a
// deactivated, banned, pending/rejected, unverified, or host-denied account
// must not be admitted just because it holds a registered key).
func (c *Core) AuthorizeUserSession(u *projections.User, host string) error {
	if u == nil {
		return ErrInvalidCredentials
	}
	if u.DeactivatedAt > 0 {
		return ErrAccountDeactivated
	}
	switch u.RegistrationStatus {
	case "", "approved":
	case "pending":
		return ErrAccountPending
	case "rejected":
		return ErrAccountRejected
	default:
		return ErrAccountPending
	}
	if !c.emailVerified(u.ID) {
		return ErrEmailNotVerified
	}
	allowed, err := c.UserLoginAllowed(u.ID, host)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrLoginIPDenied
	}
	return nil
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
	u, err := projections.GetUserByID(c.DB, userID)
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
	// Record the change time (unix seconds) so existing session tokens, which
	// carry an `iat`, are invalidated (session revocation on password change).
	if err := accountstore.UpdatePassword(c.DB, userID, string(hash), nowMS()/1000); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// RevokeUserSessions invalidates all of a user's existing session tokens
// ("sign out everywhere") by advancing sessions_valid_after to now (unix
// seconds). Session tokens carry an `iat`; requireAuth rejects any minted before
// this cutoff. Stateless JWTs give per-user (every-device) granularity, enforced
// cluster-wide via the shared column.
func (c *Core) RevokeUserSessions(userID string) error {
	if err := accountstore.RevokeSessions(c.DB, userID, nowMS()/1000); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return nil
}

func (c *Core) DeactivateAccount(userID, password, reason string) error {
	if strings.TrimSpace(password) == "" {
		return ErrDeactivationIncomplete
	}
	u, err := projections.GetUserByID(c.DB, userID)
	if err != nil || u == nil {
		return ErrInvalidCredentials
	}
	if u.DeactivatedAt > 0 {
		return ErrAccountAlreadyClosed
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return ErrInvalidCredentials
	}
	reason = accountmodel.NormalizeAccountClosureReason(reason)
	ts := nowMS()
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	if err := accountstore.DeactivateTx(tx, userID, reason, ts); err != nil {
		return err
	}
	events, err := c.appendAccountLifecycleRecordTx(tx, accountmodel.GoodbyeLifecycleRecord(accountLifecycleUser(u)), ts)
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
	if targetUserID == accountmodel.DeletedUserID {
		return fmt.Errorf("%w: cannot delete account tombstone", ErrAccountDeleteForbidden)
	}
	reason = accountmodel.NormalizeAccountClosureReason(reason)

	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	actor, err := accountstore.UserByIDTx(tx, actorID)
	if err != nil {
		return err
	}
	if actor == nil || !actor.IsAdmin() {
		return fmt.Errorf("%w: admin role required", ErrAccountDeleteForbidden)
	}
	target, err := accountstore.UserByIDTx(tx, targetUserID)
	if err != nil {
		return err
	}
	if target == nil {
		return sql.ErrNoRows
	}
	if target.Role == "admin" {
		adminCount, err := accountstore.OtherAdminCountTx(tx, targetUserID)
		if err != nil {
			return err
		}
		if adminCount == 0 {
			return ErrLastAdminDeletion
		}
	}

	counterIdentity, reactionAuthors, err := c.snapshotUserCounterIdentityTx(tx, targetUserID)
	if err != nil {
		return err
	}
	ts := nowMS()
	if err := accountstore.EnsureDeletedUserTx(tx, actorID, ts); err != nil {
		return err
	}
	if isSQLCounterStore(c.counterStore) {
		if err := cleanupUserCounterIdentityTx(tx, targetUserID, counterIdentity, reactionAuthors); err != nil {
			return err
		}
	}
	if err := accountstore.PurgeUserTx(tx, actorID, target, ts); err != nil {
		return err
	}
	if err := accountstore.DeleteUserTx(tx, targetUserID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !isSQLCounterStore(c.counterStore) {
		if err := c.cleanupDeletedUserCounterIdentity(targetUserID, counterIdentity, reactionAuthors); err != nil {
			slog.Error("deleted user counter identity cleanup failed", "target", targetUserID, "error", err)
			return err
		}
	}
	slog.Info("user hard-deleted", "actor", actorID, "target", targetUserID, "name", target.Name, "reason", reason)
	return nil
}

func (c *Core) RecordLogin(userID string) error {
	return projections.RecordLogin(c.DB, userID)
}

func (c *Core) RecordLogout() error {
	return recordLogout(c.DB)
}

// AddPubkey registers an SSH public key for the given user.
func (c *Core) AddPubkey(userID, pubkey string) error {
	return accountstore.AddPubkey(c.DB, userID, pubkey)
}

// UserByPubkey looks up a user by their SSH public key fingerprint.
func (c *Core) UserByPubkey(pubkey string) (*projections.User, error) {
	return projections.GetUserByPubkey(c.DB, pubkey)
}

// UserByID returns the user for the given ID.
func (c *Core) UserByID(id string) (*projections.User, error) {
	return projections.GetUserByID(c.DB, id)
}

// UserByName returns the user for the given username.
func (c *Core) UserByName(name string) (*projections.User, error) {
	return projections.GetUserByName(c.DB, name)
}
