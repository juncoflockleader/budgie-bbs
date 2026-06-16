# Security

This document describes Budgie BBS's security posture, the hardening an operator
must apply when deploying, and the known limitations we are transparent about.

## Reporting a vulnerability

Please report suspected vulnerabilities privately to the maintainers rather than
opening a public issue. Include the affected version/commit, a description, and a
reproduction if possible. We aim to acknowledge reports promptly and coordinate a
fix and disclosure.

## Operator hardening (required for a safe deployment)

The defaults are safe-by-default where possible, but a production deployment
**must**:

- **Set a strong `BUDGIE_JWT_SECRET`** (≥ 32 bytes, unique, random). The server
  refuses to start with the placeholder or a short secret, and generates a
  random *ephemeral* secret if none is set (sessions won't survive a restart or
  match across nodes) — so always set one explicitly in production.
- **Terminate TLS** in front of the HTTP/WebSocket and SSH transports. Tokens
  and credentials must not traverse plaintext links.
- **Multi-node:** set NATS authentication/TLS via `BUDGIE_NATS_CREDS` /
  `BUDGIE_NATS_TOKEN` / `BUDGIE_NATS_USER`+`BUDGIE_NATS_PASSWORD` /
  `BUDGIE_NATS_CA` / `BUDGIE_NATS_TLS`. The command log trusts the actor identity
  carried in records, so broker write access must be restricted to your nodes.
- **Email:** prefer `relay` mode with a trusted provider. `direct` (to-MX) mode
  makes outbound SMTP connections to recipient-controlled hosts; the mailer
  blocks private/loopback/link-local/metadata addresses, but relay mode is the
  recommended posture for untrusted-registration sites.
- **Staff 2FA:** enable the staff-2FA requirement once your admins/mods have
  enrolled, so privileged accounts are protected by a second factor.

## Controls enforced by the application

- **Authentication:** bcrypt password hashing; JWT (HS256) sessions with the
  signing method pinned (no `alg` confusion). Pre-verification 2FA challenge
  tokens cannot authenticate normal endpoints. SSH public-key and password auth
  apply the same account-state and login-host-ACL gates.
- **Two-factor:** TOTP (single-use per time-step, replay-protected), email codes,
  and single-use backup codes; all 2FA verification is rate-limited.
- **Brute-force protection:** in-memory per-IP and per-account rate limiting with
  escalating lockout on login, 2FA verification, change-password, and
  password-recovery (HTTP and SSH). It is process-local; for cluster-wide limits
  back it with a shared store.
- **Session revocation:** changing or resetting a password invalidates existing
  session tokens.
- **Authorization:** board read-access (member-read-mode/private boards) is
  enforced uniformly across the HTTP, NNTP, SSH/TUI, and live event-stream
  transports. Privileged commands are guarded consistently in both the legacy and
  native command paths.
- **Live event streams (SSE/WebSocket):** subscriptions are filtered to the
  scopes the authenticated actor may receive; you cannot stream another board's,
  user's, or moderation scope you aren't entitled to.
- **Transport/DoS:** strict security headers + a Content-Security-Policy;
  request-body size limits; SSH/NNTP read deadlines and size caps; bounded
  automod regex evaluation; the `?token=` query parameter is accepted only on the
  streaming routes.
- **Input handling:** parameterized SQL throughout; React auto-escaping with a
  markup renderer that blocks `javascript:`/`data:` links; outbound email header
  sanitization.

## Known limitations (transparency)

These are tracked, accepted-for-now items. None enables account takeover or
content/credential disclosure on a correctly-deployed instance; they are
defense-in-depth or low-severity, and are slated for follow-up.

- **Session token storage (frontend):** the web client stores the JWT in
  `localStorage` with a 30-day lifetime. This is only exploitable in the presence
  of a cross-site-scripting flaw — which the CSP, output escaping, and
  `nosniff`/`Content-Disposition` headers defend against — but moving the token to
  an `HttpOnly`+`Secure`+`SameSite` cookie (with CSRF protection) would reduce the
  blast radius. **Planned.**
- **Logout does not revoke the token server-side:** sessions are stateless JWTs,
  so "log out" clears the client but the token remains valid until expiry (a
  password change *does* revoke it). Matters mainly on shared devices. A token
  denylist or short-lived access tokens with refresh are the planned remedy.
- **Board membership cap (Postgres):** under two simultaneous membership
  approvals for a board that is exactly at `MaxMembers`, the cap can be exceeded
  by a small margin. No security boundary is crossed (the extra members are still
  legitimately approved); it is a quota-accuracy issue pending a partition-aligned
  fix.
- **Moderation metadata on automod events:** the automod-triggered audit event is
  delivered to board subscribers in addition to moderators, exposing rule/target
  metadata to board members. (The reporter-identity leak on manual/content-filter
  flags has been fixed.) A follow-up will scope it to moderators only.
- **Username enumeration:** account existence is observable by design —
  registration reports a name conflict, and login returns distinct messages for
  pending/deactivated/rejected accounts. Login response timing has been equalized;
  fully closing enumeration would require generic registration/login responses (a
  UX trade-off).

The maintainers keep a more detailed internal security review that is not
published to avoid providing an exploitation roadmap; the items above reflect its
current open set.
