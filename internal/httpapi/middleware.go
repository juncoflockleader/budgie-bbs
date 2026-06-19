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

// guestPrincipal is the sentinel actor for unauthenticated (guest) browsing.
// Its empty ID and "guest" role mean board-read checks treat it as a guest and
// IsAdmin/IsMod are false; per-user data (favorites, unread) comes back empty.
func guestPrincipal() *core.User { return &core.User{Role: "guest"} }

// optionalAuth is middleware for the public browsing surface (boards, threads,
// posts, categories). A request with NO credentials is served as a guest. A
// request that DOES present a token must pass full validation — a stale,
// revoked, deactivated, or otherwise invalid token is rejected with 401 rather
// than silently downgraded to guest, so clients clear dead sessions instead of
// browsing under a defunct identity. Use only on safe (GET) read routes.
func (s *Server) optionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok, _ := resolveToken(r); tok == "" {
			ctx := context.WithValue(r.Context(), ctxUser, guestPrincipal())
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		s.requireAuth(next).ServeHTTP(w, r)
	})
}

// requireAuth is middleware that validates the Bearer JWT and attaches the user
// to the request context. Returns 401 if the token is missing or invalid.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, viaCookie := resolveToken(r)
		if tok == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "missing token", false)
			return
		}
		// CSRF: a cookie is attached automatically by the browser, so
		// cookie-authenticated mutations must be same-origin. Header (Bearer)
		// auth is not CSRF-able and is exempt.
		if viaCookie && isStateChangingMethod(r.Method) && !requestIsSameOrigin(r) {
			writeError(w, http.StatusForbidden, "forbidden", "cross-origin request blocked", false)
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
		// Session revocation: reject tokens minted before the user's last
		// password change or an explicit "sign out everywhere". Tokens predating
		// this feature have no iat and are left alone (they expire on their own).
		if !tokenIssuedAfterRevocation(claims, user) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "session expired; please sign in again", false)
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

// tokenIssuedAfterRevocation reports whether a token is still valid with respect
// to the user's revocation cutoff — the later of their last password change and
// an explicit "sign out everywhere". A token with an `iat` earlier than that
// cutoff is revoked; a token without `iat` (pre-feature) or a user who has
// neither changed their password nor revoked sessions is accepted.
func tokenIssuedAfterRevocation(claims jwt.MapClaims, user *core.User) bool {
	cutoff := user.PasswordChangedAt
	if user.SessionsValidAfter > cutoff {
		cutoff = user.SessionsValidAfter
	}
	if cutoff <= 0 {
		return true
	}
	iat, ok := claims["iat"].(float64)
	if !ok {
		return true // legacy token without iat; leave it to expire naturally
	}
	return int64(iat) >= cutoff
}

// isStreamPath reports whether the path is an SSE/event-stream endpoint that
// must accept the token as a query parameter.
func isStreamPath(path string) bool {
	return strings.HasSuffix(path, "/events") || strings.HasSuffix(path, "/events/stream")
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
