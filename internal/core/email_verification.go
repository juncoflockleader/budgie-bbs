package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/mailer"
)

var (
	// ErrEmailNotVerified is returned at login for an account whose email has
	// not been confirmed.
	ErrEmailNotVerified = errors.New("email not verified")
	// ErrVerificationTokenInvalid is returned for a missing/expired token.
	ErrVerificationTokenInvalid = errors.New("verification token invalid or expired")
	// ErrEmailRequired is returned when signup needs an email but none was given.
	ErrEmailRequired = errors.New("email is required")
)

const (
	outboxEmailSend = "email.send"
	emailTokenTTL   = 24 * time.Hour
)

// Process-wide mailer used by the outbox worker. Set via SetMailer; the API node
// composes and enqueues, the worker node delivers, so both call SetMailer.
var (
	outboxMailer mailer.Mailer
	mailFrom     string
)

// SetMailer configures outbound email. enforce gates login on a verified email
// for new accounts; baseURL is the public site URL used to build verification
// links. A nil mailer disables sending and enforcement.
func (c *Core) SetMailer(m mailer.Mailer, from string, enforce bool, baseURL string) {
	outboxMailer = m
	mailFrom = strings.TrimSpace(from)
	c.emailVerifyEnabled = enforce && m != nil
	c.emailVerifyBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

// EmailVerificationEnabled reports whether new accounts must confirm their email.
func (c *Core) EmailVerificationEnabled() bool {
	return c != nil && c.emailVerifyEnabled
}

// SetMailDevInbox records the web-inbox URL of a local SMTP catcher (mailpit)
// so the signup UI can link to captured verification mail during local testing.
// Empty disables the hint; never set this for a real provider.
func (c *Core) SetMailDevInbox(url string) {
	c.emailDevInboxURL = strings.TrimRight(strings.TrimSpace(url), "/")
}

// MailDevInboxURL returns the local SMTP-catcher inbox URL, or "" if none.
func (c *Core) MailDevInboxURL() string {
	if c == nil {
		return ""
	}
	return c.emailDevInboxURL
}

// StartEmailVerification marks a user unverified, records the email, mints a
// single-use token, and enqueues the verification email. Idempotent enough to
// double as "resend": old tokens for the user are cleared first.
func (c *Core) StartEmailVerification(userID, email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return ErrEmailRequired
	}
	now := nowMS()
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	if _, err := qExec(tx, `UPDATE users SET email_verified=0 WHERE id=?`, userID); err != nil {
		return err
	}
	if _, err := qExec(tx,
		`INSERT INTO user_private_profiles (user_id, registration_email, updated_at)
		 VALUES (?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET registration_email=excluded.registration_email, updated_at=excluded.updated_at`,
		userID, email, now,
	); err != nil {
		return err
	}
	// One outstanding token per user.
	if _, err := qExec(tx, `DELETE FROM email_verification_tokens WHERE user_id=?`, userID); err != nil {
		return err
	}
	token := newID("everi_")
	exp := now + emailTokenTTL.Milliseconds()
	if _, err := qExec(tx,
		`INSERT INTO email_verification_tokens (token, user_id, email, created_at, expires_at) VALUES (?,?,?,?,?)`,
		token, userID, email, now, exp,
	); err != nil {
		return err
	}
	subject, body := c.verificationEmail(token)
	if err := enqueueOutboxJob(tx, outboxEmailSend, emailSendJob{From: mailFrom, To: email, Subject: subject, Body: body, TS: now}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Core) verificationEmail(token string) (subject, body string) {
	link := "/api/v1/auth/verify-email?token=" + token
	if c.emailVerifyBaseURL != "" {
		link = c.emailVerifyBaseURL + link
	}
	subject = "Confirm your email"
	body = "Welcome! Please confirm your email address by opening this link:\n\n" +
		link + "\n\nThis link expires in 24 hours. If you did not sign up, ignore this message.\n"
	return subject, body
}

// VerifyEmailToken consumes a verification token and marks the account verified.
// Returns the verified user.
func (c *Core) VerifyEmailToken(token string) (*User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrVerificationTokenInvalid
	}
	var userID string
	var exp int64
	err := qQueryRow(c.DB,
		`SELECT user_id, expires_at FROM email_verification_tokens WHERE token=?`, token,
	).Scan(&userID, &exp)
	if err == sql.ErrNoRows {
		return nil, ErrVerificationTokenInvalid
	}
	if err != nil {
		return nil, err
	}
	// Single-use: atomically claim the token. Only the request that actually
	// deletes the row proceeds; a concurrent replay gets 0 rows and fails.
	res, err := qExec(c.DB, `DELETE FROM email_verification_tokens WHERE token=?`, token)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrVerificationTokenInvalid
	}
	if exp < nowMS() {
		return nil, ErrVerificationTokenInvalid
	}
	if _, err := qExec(c.DB,
		`UPDATE users SET email_verified=1, email_verified_at=? WHERE id=?`, nowMS(), userID,
	); err != nil {
		return nil, err
	}
	return getUserByID(c.DB, userID)
}

// ResendEmailVerification re-issues a verification email for an unverified
// account, looked up by name. Returns the email it was sent to (or "" / error).
func (c *Core) ResendEmailVerification(name string) error {
	u, err := getUserByName(c.DB, name)
	if err != nil || u == nil {
		return ErrInvalidCredentials
	}
	var verified int
	var email string
	_ = qQueryRow(c.DB, `SELECT email_verified FROM users WHERE id=?`, u.ID).Scan(&verified)
	_ = qQueryRow(c.DB, `SELECT COALESCE(registration_email,'') FROM user_private_profiles WHERE user_id=?`, u.ID).Scan(&email)
	if verified != 0 {
		return nil // already verified; nothing to do (don't leak status)
	}
	if email == "" {
		return ErrEmailRequired
	}
	return c.StartEmailVerification(u.ID, email)
}

// emailVerified reports whether a user's email is confirmed.
func (c *Core) emailVerified(userID string) bool {
	var v int
	if err := qQueryRow(c.DB, `SELECT email_verified FROM users WHERE id=?`, userID).Scan(&v); err != nil {
		return true // fail open on read error; login's other gates still apply
	}
	return v != 0
}

type emailSendJob struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	TS      int64  `json:"ts"`
}

// processEmailSendJob delivers a queued verification email via the process mailer.
func processEmailSendJob(payload emailSendJob) error {
	if outboxMailer == nil {
		return fmt.Errorf("email send: no mailer configured on this node")
	}
	from := payload.From
	if from == "" {
		from = mailFrom
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := outboxMailer.Send(ctx, mailer.Message{From: from, To: payload.To, Subject: payload.Subject, Body: payload.Body}); err != nil {
		slog.Warn("email send failed", "to", payload.To, "err", err)
		return err
	}
	slog.Info("verification email sent", "to", payload.To)
	return nil
}
