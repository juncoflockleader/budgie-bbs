package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type contextKey int

const ctxUser contextKey = 1

func userFromCtx(ctx context.Context) *core.User {
	u, _ := ctx.Value(ctxUser).(*core.User)
	return u
}

// requireAuth is middleware that validates the Bearer JWT and attaches the user
// to the request context. Returns 401 if the token is missing or invalid.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if tok == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "missing token", false)
			return
		}
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(tok, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return s.jwtSecret, nil
		})
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid token", false)
			return
		}
		// Reject non-session tokens (e.g. the pre-verification 2FA challenge
		// token, which carries typ:"2fa"). Full session tokens have no typ or
		// typ:"session"; without this check a challenge token would authenticate
		// every protected endpoint and bypass the second factor entirely.
		if typ, _ := claims["typ"].(string); typ != "" && typ != "session" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid token type", false)
			return
		}
		uid, _ := claims["sub"].(string)
		if uid == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid token claims", false)
			return
		}
		user, err := s.core.UserByID(uid)
		if err != nil || user == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "user not found", false)
			return
		}
		if user.DeactivatedAt > 0 {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "account deactivated", false)
			return
		}
		switch user.RegistrationStatus {
		case "", "approved":
		case "pending":
			writeError(w, http.StatusUnauthorized, "unauthenticated", "account pending approval", false)
			return
		case "rejected":
			writeError(w, http.StatusUnauthorized, "unauthenticated", "account registration rejected", false)
			return
		default:
			writeError(w, http.StatusUnauthorized, "unauthenticated", "account not approved", false)
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) string {
	// Support both Authorization header and ?token= query param (for WS upgrades).
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a standard error JSON body.
func writeError(w http.ResponseWriter, status int, code, msg string, retryable bool) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   msg,
			"retryable": retryable,
		},
	})
}

// errorCode maps an internal error code string to an HTTP status.
func errorCode(code string) int {
	switch code {
	case "unauthenticated":
		return http.StatusUnauthorized
	case "forbidden":
		return http.StatusForbidden
	case "rate_limited":
		return http.StatusTooManyRequests
	case "validation_failed":
		return http.StatusUnprocessableEntity
	case "not_found":
		return http.StatusNotFound
	case "thread_locked", "edit_window_expired", "conflict", "muted", "banned", proto.ErrBlobStagingRequired:
		return http.StatusConflict
	case proto.ErrCommandLogUnavailable:
		return http.StatusServiceUnavailable
	case proto.ErrProjectionStale:
		return statusTooEarly
	case proto.ErrWriteRegionUnavailable:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}
