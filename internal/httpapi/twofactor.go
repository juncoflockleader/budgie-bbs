package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/skip2/go-qrcode"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// mintChallengeToken issues a short-lived signed token that authorizes a single
// 2FA verification for the given user (10 minute TTL, distinct "typ" claim).
func (s *Server) mintChallengeToken(userID string) (string, error) {
	exp := time.Now().Add(10 * time.Minute)
	claims := jwt.MapClaims{"sub": userID, "exp": exp.Unix(), "typ": "2fa"}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(s.jwtSecret)
}

// parseChallengeToken validates a 2FA challenge token and returns its user id.
func (s *Server) parseChallengeToken(tokenStr string) (string, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return "", err
	}
	if typ, _ := claims["typ"].(string); typ != "2fa" {
		return "", errors.New("not a 2fa challenge token")
	}
	uid, _ := claims["sub"].(string)
	if uid == "" {
		return "", errors.New("invalid challenge token")
	}
	return uid, nil
}

type verify2FARequest struct {
	ChallengeToken string `json:"challengeToken"`
	Method         string `json:"method"`
	Code           string `json:"code"`
}

// handleVerify2FA completes a login challenge: verify the code, then issue the
// real session token.
func (s *Server) handleVerify2FA(w http.ResponseWriter, r *http.Request) {
	var req verify2FARequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChallengeToken == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "challengeToken and code required", false)
		return
	}
	ipKey := "2fa-ip:" + requestHost(r)
	if wait := maxRetryAfter(s.twoFactorLimiter, ipKey); wait > 0 {
		writeRateLimited(w, wait)
		return
	}
	uid, err := s.parseChallengeToken(req.ChallengeToken)
	if err != nil {
		s.twoFactorLimiter.Fail(ipKey)
		writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid or expired challenge", false)
		return
	}
	userKey := "2fa-user:" + uid
	if wait := maxRetryAfter(s.twoFactorLimiter, userKey); wait > 0 {
		writeRateLimited(w, wait)
		return
	}
	var verr error
	switch req.Method {
	case "email":
		verr = s.core.VerifyEmail2FACode(uid, req.Code)
	case "backup":
		verr = s.core.VerifyBackupCode(uid, req.Code)
	default:
		verr = s.core.VerifyTOTP(uid, req.Code)
	}
	if verr != nil {
		s.twoFactorLimiter.Fail(ipKey)
		s.twoFactorLimiter.Fail(userKey)
		writeError(w, http.StatusUnauthorized, "two_factor_failed", "invalid verification code", false)
		return
	}
	s.twoFactorLimiter.Reset(ipKey)
	s.twoFactorLimiter.Reset(userKey)
	u, err := s.core.UserByID(uid)
	if err != nil || u == nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "user not found", false)
		return
	}
	if err := s.core.RecordLogin(u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not record login", true)
		return
	}
	tok, exp, err := s.mintToken(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not issue token", true)
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{
		Token: tok, ExpiresAt: exp,
		User: authUser{ID: u.ID, Name: u.Name, Role: u.Role, RegistrationStatus: u.RegistrationStatus},
	})
}

type challengeOnlyRequest struct {
	ChallengeToken string `json:"challengeToken"`
}

// handleRequestEmail2FACode sends an email code mid-login. Always returns ok so
// the response does not reveal enrollment details.
func (s *Server) handleRequestEmail2FACode(w http.ResponseWriter, r *http.Request) {
	var req challengeOnlyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChallengeToken == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "challengeToken required", false)
		return
	}
	uid, err := s.parseChallengeToken(req.ChallengeToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid or expired challenge", false)
		return
	}
	_ = s.core.SendEmail2FACode(uid)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- self-service enrollment (authenticated) ---

func (s *Server) handleGet2FA(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	st, err := s.core.TwoFactorStatus(actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleInitTOTP(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	secret, uri, err := s.core.BeginTOTPEnrollment(actor.ID, actor.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	png, err := qrcode.Encode(uri, qrcode.Medium, 256)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not render qr", true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"secret":     secret,
		"otpauthUri": uri,
		"qr":         "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

type confirmTOTPRequest struct {
	Code string `json:"code"`
}

func (s *Server) handleConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req confirmTOTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "code required", false)
		return
	}
	if err := s.core.ConfirmTOTPEnrollment(actor.ID, req.Code); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "two_factor_failed", "invalid code", false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if err := s.core.DisableTOTP(actor.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleEnableEmail2FA(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if err := s.core.EnableEmail2FA(actor.ID); err != nil {
		if errors.Is(err, core.ErrTwoFactorNoEmail) {
			writeError(w, http.StatusUnprocessableEntity, "no_email", "no verified email on file", false)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDisableEmail2FA(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if err := s.core.DisableEmail2FA(actor.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGenerateBackupCodes issues a fresh set of single-use recovery codes,
// returned once in plaintext for the user to save.
func (s *Server) handleGenerateBackupCodes(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	codes, err := s.core.GenerateBackupCodes(actor.ID)
	if err != nil {
		if errors.Is(err, core.ErrTwoFactorNotEnrolled) {
			writeError(w, http.StatusUnprocessableEntity, "not_enrolled", "set up an authenticator or email 2FA first", false)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

// --- admin security settings + per-user status ---

func (s *Server) handleGetSecuritySettings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	settings, err := s.core.SecuritySettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

type securitySettingsRequest struct {
	Staff2FARequired bool `json:"staff2faRequired"`
}

func (s *Server) handleSetSecuritySettings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	var req securitySettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	settings, err := s.core.SetSecuritySettings(req.Staff2FARequired)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// handleGetUser2FAStatus lets staff see whether a target has 2FA enrolled (used
// when granting roles before enabling enforcement).
func (s *Server) handleGetUser2FAStatus(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsMod() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "moderator role required", false)
		return
	}
	target, err := s.core.UserByName(r.PathValue("name"))
	if err != nil || target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	st, err := s.core.TwoFactorStatus(target.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
