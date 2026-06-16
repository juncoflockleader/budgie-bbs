package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

// sessionCookieName holds the session JWT for browser clients. It is HttpOnly so
// page JavaScript cannot read it (limits XSS token theft).
const sessionCookieName = "budgie_session"

// requestIsHTTPS reports whether the request arrived over TLS (directly or via a
// trusted proxy). The session cookie is marked Secure only then, so local HTTP
// development still works through the Authorization-header path.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// setSessionCookie issues the session token as an HttpOnly cookie. expUnixMillis
// is the token's expiry (matches the JWT exp).
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expUnixMillis int64) {
	exp := time.UnixMilli(expUnixMillis)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(time.Until(exp).Seconds()),
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie expires the session cookie (logout).
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// resolveToken returns the session token and whether it came from the cookie.
// Precedence: Authorization header (programmatic clients) > session cookie
// (browsers) > ?token= (only on stream routes, which EventSource can't add a
// header to). Cookie-sourced tokens require a CSRF check on mutations.
func resolveToken(r *http.Request) (token string, viaCookie bool) {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer "), false
	}
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	if isStreamPath(r.URL.Path) {
		return r.URL.Query().Get("token"), false
	}
	return "", false
}

// isStateChangingMethod reports whether a method mutates state (and thus needs
// CSRF protection when authenticated by cookie).
func isStateChangingMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

// requestIsSameOrigin is a stateless CSRF guard for cookie-authenticated
// mutations. It trusts the browser-set Sec-Fetch-Site when present, otherwise
// compares the Origin host to the request host. A request with neither header
// (e.g. a non-browser client) is allowed — those don't carry the session cookie
// in a CSRF scenario, and SameSite=Lax is the backstop.
func requestIsSameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "cross-site", "same-site":
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
