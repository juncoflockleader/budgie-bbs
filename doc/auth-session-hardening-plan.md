# Plan: Auth Session Hardening — HttpOnly Cookie + Logout Revocation

Status: **in progress** — Part B (logout revocation) is **done**; Part A is at
**phase A-1** (server issues + accepts the cookie with CSRF, backward
compatible); phases A-2 (flip the SPA) and A-3 (cleanup) remain. Covers the two
`SECURITY.md`
"known limitations" follow-ups:

1. The web client stores the session JWT in `localStorage` (readable by any JS —
   XSS-exfiltratable — with a 30-day lifetime). Move it to an `HttpOnly` cookie.
2. Logout clears the client but does not revoke the token server-side (a captured
   token stays valid until expiry). Add server-side revocation.

Both are low risk but expected for a public-facing service. This document is the
implementation plan; it is intentionally phased so nothing breaks on the way.

## Current state (for reference)

- **Token mint** (`internal/httpapi/auth.go` `mintToken`): HS256 JWT with
  `{sub, exp(30d), iat, typ:"session"}`. Returned in the JSON body of
  `/auth/login`, `/auth/register` (auto-login), and `/auth/2fa/verify`.
- **Token validation** (`internal/httpapi/middleware.go` `requireAuth`, and
  `internal/wsapi/conn.go` `validateToken`): read from `Authorization: Bearer`
  or the `?token=` query param (the latter restricted to the SSE/WS stream
  routes); checks signature/method, `typ`, `sub`, deactivation, registration
  status, and `iat >= PasswordChangedAt`.
- **Existing revocation (M2):** a per-user `password_changed_at` epoch; tokens
  minted before it are rejected (`tokenIssuedAfterPasswordChange`). This is the
  pattern Part B generalizes.
- **Frontend:** `web/src/hooks/useAuth.ts` persists `{token,user}` in
  `localStorage["budgie_token"]`; `web/src/api/client.ts` sends
  `Authorization: Bearer`; `web/src/hooks/useStream.ts` passes `?token=` to the
  WebSocket (`/api/v1/ws?token=`) and SSE (`events/stream?token=`).
- **Logout** (`handleLogout`): only records a presence/stat row; no revocation,
  no cookie (none exists yet).

## Goals / non-goals

**Goals**
- Session token is not readable by page JavaScript (HttpOnly cookie) → an XSS can
  no longer exfiltrate a long-lived session.
- Server-side "sign out everywhere" that invalidates outstanding tokens
  cluster-wide.
- Keep programmatic clients working via `Authorization: Bearer` (dual-mode).
- No flag-day breakage: existing sessions and the current SPA keep working during
  rollout.

**Non-goals (call out as future options)**
- Refresh-token rotation / short-lived access tokens.
- Per-device server-side revocation via a `jti` denylist (needs a shared store);
  see "Per-device vs global" below.
- Any OAuth/SSO change.

---

## Part A — HttpOnly cookie session

### A1. Cookie design
- Name: `budgie_session`; value: the JWT.
- Attributes: `HttpOnly`, `Secure` (when the request is HTTPS — see dev note),
  `SameSite=Lax`, `Path=/`, `Max-Age` = token TTL (30d, matching `exp`).
- Set on every response that currently returns a token (login, register
  auto-login, 2FA verify). Cleared on logout (`Max-Age=0`).
- Dev/HTTP note: `Secure` cookies are not sent over plain HTTP, so set `Secure`
  based on the effective request scheme (honor `X-Forwarded-Proto`) or a config
  flag; local HTTP dev falls back to the `Authorization` header path.

### A2. Server validation (dual-read)
- Extend the token source in `requireAuth` and `wsapi.validateToken` to:
  1. `Authorization: Bearer` (programmatic clients), then
  2. `budgie_session` cookie (browsers), then
  3. existing `?token=` (already restricted to stream routes; keep for now,
     remove for browsers once the cookie covers WS/SSE).
- Same-origin browsers send the cookie automatically on `fetch`, `WebSocket`,
  and `EventSource` upgrades, so `?token=` becomes unnecessary for the web app.

### A3. CSRF (required once cookies authenticate mutations)
Cookie auth means the browser attaches credentials to cross-site requests, so
state-changing endpoints need CSRF protection. Layered options:
- **`SameSite=Lax`** already blocks cookie attachment on cross-site
  subrequests/POSTs in modern browsers (a strong baseline).
- **Origin / `Sec-Fetch-Site` check** on mutating methods (POST/PATCH/PUT/DELETE):
  reject when `Origin`/`Sec-Fetch-Site` is cross-site. Stateless, cheap.
  **Recommended primary**, with `SameSite=Lax` as backstop.
- **Double-submit token** (stronger, more work): server also sets a non-HttpOnly
  `budgie_csrf` cookie; the SPA echoes it in an `X-CSRF-Token` header on
  mutations; server verifies header == cookie. Use if we want defense beyond
  SameSite+Origin.

Decision needed: SameSite `Lax` vs `Strict`; Origin-check vs double-submit.
Recommendation: `SameSite=Lax` + Origin-check first; add double-submit later if
desired.

### A4. Frontend changes
- `useAuth.ts`: stop persisting the JWT. Keep only non-sensitive user info in
  memory (and optionally localStorage for UI), never the token. Auth state on
  load comes from a lightweight `GET /api/v1/auth/me` (cookie-authenticated)
  rather than a stored token.
- `client.ts`: `fetch(..., { credentials: 'include' })`; drop the `Authorization`
  header for the web app; add `X-CSRF-Token` on mutations if double-submit is
  chosen.
- `useStream.ts`: drop `?token=`; rely on the cookie for same-origin WS/SSE
  upgrades.
- New endpoint: `GET /api/v1/auth/me` returning the current user (for bootstrap),
  since the SPA can no longer read the token/user from localStorage.

### A5. Rollout (no breakage)
- **Phase A-1 (server, backward compatible):** set the cookie on login/register/
  2FA *and* keep returning the token in JSON; accept token from cookie OR header.
  Add `GET /auth/me`. Deploy server. Existing SPA (header-based) keeps working;
  new logins also get a cookie.
- **Phase A-2 (frontend):** switch the SPA to cookie mode (`credentials:'include'`,
  remove localStorage token, remove `?token=`, add CSRF). Deploy web.
- **Phase A-3 (cleanup):** stop returning the token in JSON for browser logins
  (keep a documented path for programmatic clients if needed); consider removing
  `?token=` entirely. Tighten as desired.

---

## Part B — Logout / global session revocation

### B1. Design (generalize the M2 epoch)
- Add a per-user `sessions_valid_after` epoch (or reuse/rename around
  `password_changed_at`). `requireAuth` rejects tokens with
  `iat < max(password_changed_at, sessions_valid_after)`.
- **"Sign out everywhere"**: bump `sessions_valid_after = now` → every existing
  token for that user is immediately invalid, on every node (the epoch lives in
  the DB, so revocation is cluster-wide for free).
- **Normal per-device logout**: clear the `budgie_session` cookie (the browser
  drops it). This logs out *that device* without global revocation — the common
  case — and needs no server state.
- Storage: add a column via SQLite `ensureColumn` + a Postgres migration (next
  version), mirroring how `password_changed_at` (migration v80) was added.

### B2. Per-device vs global
Stateless JWTs carry no per-token identity, so server-side revocation is
per-*user* (everywhere) only. Per-*device* server-side revocation would require a
`jti` claim + a shared denylist (Redis/Postgres) keyed by `jti` with a TTL of the
token lifetime. That is heavier and is listed as a future option; cookie-clear
already covers per-device logout for the normal case.

### B3. Endpoints
- `POST /api/v1/auth/logout` (existing): clear the cookie; keep `RecordLogout`
  (stats). Device-local.
- `POST /api/v1/auth/logout-all` (new) or `logout?all=1`: bump
  `sessions_valid_after` (global revoke) + clear the cookie. Authenticated.

---

## Security considerations
- `Secure` cookie requires TLS termination (already an operator requirement in
  `SECURITY.md`). Gate `Secure` on the effective scheme so dev-over-HTTP still
  works via the header path.
- Removing `?token=` for browsers eliminates token leakage into logs / history /
  `Referer`.
- Revocation epoch in the DB → cluster-wide by construction (no extra infra).
- CSRF must land *with* cookie auth (Part A3), not after.

## Testing
- **Server:** cookie attributes on login (HttpOnly/Secure/SameSite/Max-Age);
  dual-read (header-only, cookie-only, both); logout clears the cookie;
  logout-all bumps the epoch and invalidates a previously-valid token; CSRF
  rejection of a cross-site mutation; `/auth/me` returns the current user.
- **Frontend:** after login no token is in `localStorage`; requests carry the
  cookie (`credentials:'include'`); WS/SSE connect without `?token=`; CSRF header
  present on mutations (if double-submit chosen).
- **Regression:** all existing `Authorization: Bearer` tests stay green
  (dual-read preserves them).

## Effort / risk
- **Part B (revocation):** small, low-risk — reuses the M2 epoch pattern; mostly
  a column + a claim comparison + one endpoint.
- **Part A (cookie):** medium — touches the server, the SPA, and CSRF; phased
  (A-1 server is backward compatible, A-2 flips the SPA, A-3 cleans up) so there
  is no flag day.

## Decisions to confirm before implementation
1. `SameSite=Lax` vs `Strict`.
2. CSRF: Origin/`Sec-Fetch-Site` check (recommended first) vs double-submit token.
3. Keep returning the JWT in JSON for programmatic clients, or document a separate
   token-issuance path for them?
4. Revocation scope: ship global "sign out everywhere" only (recommended), or also
   invest in per-device `jti` denylist now?
5. Cookie name (`budgie_session`) and dev-mode `Secure` handling.
