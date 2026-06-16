package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	mailpkg "net/mail"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

type registerRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Email    string `json:"email"`
	// Optional private intake fields (stored in the private profile, never public).
	RealName    string `json:"realName"`
	Affiliation string `json:"affiliation"`
	Note        string `json:"note"`
	// Privacy policy acceptance (required when the site enables it).
	AcceptPolicy  bool   `json:"acceptPolicy"`
	PolicyVersion string `json:"policyVersion"`
	// Captcha (when enabled): native challenges send challenge id + answer;
	// provider challenges send a single token.
	CaptchaChallengeID string `json:"captchaChallengeId"`
	CaptchaAnswer      string `json:"captchaAnswer"`
	CaptchaToken       string `json:"captchaToken"`
}

type loginRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type passwordRecoveryRequest struct {
	Name          string `json:"name"`
	SubmittedName string `json:"submittedName"`
	Email         string `json:"email"`
	Note          string `json:"note"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

type deactivateAccountRequest struct {
	Password string `json:"password"`
	Reason   string `json:"reason"`
}

type authUser struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Role               string `json:"role"`
	RegistrationStatus string `json:"registrationStatus,omitempty"`
}

type loginResponse struct {
	Token         string   `json:"token"`
	ExpiresAt     int64    `json:"expiresAt"`
	User          authUser `json:"user"`
	MustEnroll2FA bool     `json:"mustEnroll2fa,omitempty"`
}

type pubkeyRequest struct {
	Pubkey string `json:"pubkey"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "name and password required", false)
		return
	}
	if err := s.core.VerifyCaptcha(r.Context(), core.CaptchaSubmission{
		ChallengeID: req.CaptchaChallengeID,
		Answer:      req.CaptchaAnswer,
		Token:       req.CaptchaToken,
		RemoteIP:    requestHost(r),
	}); err != nil {
		if errors.Is(err, core.ErrCaptchaRequired) {
			writeError(w, http.StatusBadRequest, "captcha_required", "captcha is required", false)
			return
		}
		if errors.Is(err, core.ErrCaptchaFailed) {
			writeError(w, http.StatusBadRequest, "captcha_failed", "captcha verification failed", false)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "captcha check failed", true)
		return
	}
	if s.core.EmailVerificationEnabled() && !validEmailAddress(req.Email) {
		writeError(w, http.StatusUnprocessableEntity, "email_required", "a valid email is required", false)
		return
	}
	if s.core.PrivacyPolicyRequired() && !req.AcceptPolicy {
		writeError(w, http.StatusUnprocessableEntity, "policy_acceptance_required", "you must accept the privacy policy", false)
		return
	}
	if len(req.RealName) > 200 || len(req.Affiliation) > 200 || len(req.Note) > 1000 {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "registration fields are too long", false)
		return
	}
	u, err := s.core.RegisterUser(req.Name, req.Password)
	if err != nil {
		writeError(w, http.StatusConflict, "conflict", err.Error(), false)
		return
	}
	if err := s.core.SaveRegistrationIntake(u.ID, core.RegistrationIntake{
		RealName:       req.RealName,
		Affiliation:    req.Affiliation,
		Note:           req.Note,
		PolicyAccepted: req.AcceptPolicy,
		PolicyVersion:  req.PolicyVersion,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not save registration details", true)
		return
	}
	if s.core.EmailVerificationEnabled() {
		if err := s.core.StartEmailVerification(u.ID, req.Email); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "could not start email verification", true)
			return
		}
	}
	if u.RegistrationStatus == "pending" {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status": "pending",
			"user":   authUser{ID: u.ID, Name: u.Name, Role: u.Role, RegistrationStatus: u.RegistrationStatus},
		})
		return
	}
	if s.core.EmailVerificationEnabled() {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status": "verification_required",
			"user":   authUser{ID: u.ID, Name: u.Name, Role: u.Role, RegistrationStatus: u.RegistrationStatus},
		})
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
	writeJSON(w, http.StatusCreated, loginResponse{
		Token: tok, ExpiresAt: exp,
		User: authUser{ID: u.ID, Name: u.Name, Role: u.Role, RegistrationStatus: u.RegistrationStatus},
	})
}

// handleAuthPolicy exposes unauthenticated signup policy (currently captcha
// config) so the client can render the right challenge before registering.
func (s *Server) handleAuthPolicy(w http.ResponseWriter, r *http.Request) {
	emailVerification := map[string]any{"required": s.core.EmailVerificationEnabled()}
	if inbox := s.core.MailDevInboxURL(); inbox != "" {
		// Local SMTP catcher (mailpit) — let the signup UI link to captured mail.
		emailVerification["devInboxUrl"] = inbox
	}
	_, policyVersion := s.core.PrivacyPolicy()
	writeJSON(w, http.StatusOK, map[string]any{
		"captcha":           s.core.CaptchaPolicy(),
		"emailVerification": emailVerification,
		"privacyPolicy": map[string]any{
			"required": s.core.PrivacyPolicyRequired(),
			"version":  policyVersion,
		},
	})
}

// handlePrivacyPolicy serves the bundled privacy policy markdown so the signup
// UI can display it before the user accepts. Public, unauthenticated.
func (s *Server) handlePrivacyPolicy(w http.ResponseWriter, r *http.Request) {
	markdown, version := s.core.PrivacyPolicy()
	writeJSON(w, http.StatusOK, map[string]any{
		"markdown": markdown,
		"version":  version,
	})
}

// handleCaptchaChallenge issues a fresh native captcha challenge (id + SVG).
func (s *Server) handleCaptchaChallenge(w http.ResponseWriter, r *http.Request) {
	ch, err := s.core.IssueCaptchaChallenge()
	if err != nil {
		writeError(w, http.StatusBadRequest, "captcha_unavailable", "native captcha is not enabled", false)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

// handleVerifyEmail confirms an account email from the single-use link in the
// verification email. It renders a small HTML page since it is opened in a
// browser.
func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	_, err := s.core.VerifyEmailToken(r.URL.Query().Get("token"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, verifyEmailPage("Verification failed", "This link is invalid or has expired. Request a new one from the sign-in page."))
		return
	}
	fmt.Fprint(w, verifyEmailPage("Email verified", "Your email is confirmed — you can now sign in."))
}

type resendVerificationRequest struct {
	Name string `json:"name"`
}

// handleResendVerification re-issues a verification email. It always returns ok
// so it does not reveal whether an account exists or its verification state.
func (s *Server) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	var req resendVerificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "name required", false)
		return
	}
	_ = s.core.ResendEmailVerification(req.Name)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func verifyEmailPage(title, body string) string {
	return "<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">" +
		"<title>" + html.EscapeString(title) + "</title></head>" +
		"<body style=\"font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;text-align:center\">" +
		"<h1 style=\"font-weight:500\">" + html.EscapeString(title) + "</h1>" +
		"<p>" + html.EscapeString(body) + "</p></body></html>"
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "name and password required", false)
		return
	}
	u, err := s.core.AuthenticateUserFromHost(req.Name, req.Password, requestHost(r))
	if err != nil {
		if errors.Is(err, core.ErrAccountDeactivated) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "account deactivated", false)
			return
		}
		if errors.Is(err, core.ErrAccountPending) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "account pending approval", false)
			return
		}
		if errors.Is(err, core.ErrEmailNotVerified) {
			writeError(w, http.StatusUnauthorized, "email_not_verified", "email not verified", false)
			return
		}
		if errors.Is(err, core.ErrAccountRejected) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "account registration rejected", false)
			return
		}
		if errors.Is(err, core.ErrLoginIPDenied) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "login host not allowed", false)
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid credentials", false)
		return
	}
	// Staff 2FA: when enrolled and enforced, return a challenge (no token yet);
	// the client completes it via POST /auth/2fa/verify.
	required, err := s.core.TwoFactorRequiredForLogin(u.ID, u.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "two-factor check failed", true)
		return
	}
	if required {
		challenge, cerr := s.mintChallengeToken(u.ID)
		if cerr != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "could not issue challenge", true)
			return
		}
		st, _ := s.core.TwoFactorStatus(u.ID)
		methods := []string{}
		if st.TOTPEnrolled {
			methods = append(methods, "totp")
		}
		if st.EmailEnrolled {
			methods = append(methods, "email")
		}
		if st.BackupCodesRemaining > 0 {
			methods = append(methods, "backup")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":         "2fa_required",
			"challengeToken": challenge,
			"methods":        methods,
		})
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
	mustEnroll, _ := s.core.StaffShouldEnroll2FA(u.ID, u.Role)
	writeJSON(w, http.StatusOK, loginResponse{
		Token: tok, ExpiresAt: exp,
		User:          authUser{ID: u.ID, Name: u.Name, Role: u.Role, RegistrationStatus: u.RegistrationStatus},
		MustEnroll2FA: mustEnroll,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.core.RecordLogout(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not record logout", true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRequestPasswordRecovery(w http.ResponseWriter, r *http.Request) {
	var req passwordRecoveryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "name required", false)
		return
	}
	if _, err := s.core.RequestPasswordRecovery(req.Name, req.SubmittedName, req.Email, req.Note); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not queue recovery request", true)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "currentPassword and newPassword required", false)
		return
	}
	if err := s.core.ChangePassword(actor.ID, req.CurrentPassword, req.NewPassword); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid credentials", false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeactivateAccount(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req deactivateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "password required", false)
		return
	}
	err := s.core.DeactivateAccount(actor.ID, req.Password, req.Reason)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if errors.Is(err, core.ErrAccountAlreadyClosed) || errors.Is(err, core.ErrAccountDeactivated) {
		writeError(w, http.StatusConflict, "conflict", "account already deactivated", false)
		return
	}
	if errors.Is(err, core.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid credentials", false)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
}

func (s *Server) handleAddPubkey(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req pubkeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pubkey == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "pubkey required", false)
		return
	}
	if err := s.core.AddPubkey(actor.ID, req.Pubkey); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// validEmailAddress accepts only a bare, well-formed address with no control
// characters — rejecting the header-injection / SSRF inputs the mailer also
// guards against, but at the door so they are never stored.
func validEmailAddress(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "\r\n\x00") {
		return false
	}
	parsed, err := mailpkg.ParseAddress(s)
	return err == nil && parsed.Address == s
}

func (s *Server) mintToken(userID string) (string, int64, error) {
	exp := time.Now().Add(30 * 24 * time.Hour)
	claims := jwt.MapClaims{
		"sub": userID,
		"exp": exp.Unix(),
		"typ": "session",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(s.jwtSecret)
	return signed, exp.UnixMilli(), err
}
