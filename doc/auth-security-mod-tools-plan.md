# Auth, Security, And Moderator Tools Plan

Date: 2026-06-14

## Goals

Add practical account hardening and moderation controls:

- Optional site-wide captcha on signup.
- Mandatory email verification for new accounts.
- Optional 2FA for site admins and moderators, default off.
- Enforceable anti-spam registration modes.
- Clear privacy baseline for additional registration data.
- Board mute for board moderators and site mute for admins.
- Rule-based automoderation for board moderators, including spam removal, bans, keyword rules, board rate limits, and manual-review routing.

## Current State

Existing pieces:

- Public signup currently accepts username and password only.
- Admins can toggle registration approval and review pending registrations.
- Password recovery requests collect submitted name, email, and notes for admin review.
- Private user profile storage already has fields for real name, email, registration email, address, phone, birthday, school, and contact note.
- Global/board user sanctions already exist through `sanctionUser` / `clearUserSanction`.
- Existing sanctions support `mute` and `ban`, with `global` or board scopes.
- Content filters exist, but are admin-managed and currently route matching posts into moderation review rather than taking configurable actions.
- Moderation review queues and generated moderation audit boards exist.

Not yet present:

- Captcha challenge verification during signup.
- Email verification token issuance and enforcement.
- 2FA enrollment/challenge flow.
- Additional signup fields sent through the registration API.
- Public privacy policy route.
- Board-mod-owned automod rules with action policies.
- Board-level posting rate limits.

## Progress

2026-06-15 (automod enhancements — multi-action rules + staff exemption):

- A rule may now carry several comma-separated actions (e.g. `redact,board_ban`), applied together in order when it matches; each action is authorized independently (union of requirements) and gets its own audit-log entry. Implemented across the proto validator (`ParseAutomodActions`), both execution paths, and the AdminPage form (action checkboxes instead of a single select). Single-action rules are unaffected.
- Staff are exempt from automod: `evaluateBoardAutomod` skips users who can moderate the board (site mod/admin, board moderator, or member with can_moderate_posts/threads), so moderators are never auto-actioned by their own board's rules.
- Tests: `TestBoardAutomodMultipleActions` (redact+ban → post redacted, author banned, both audited) and `TestBoardAutomodStaffExempt` (moderator exempt, ordinary user still actioned). Verified on the running binary.

2026-06-15 (Phase 9 — board rate limits + automod audit reporting):

- `rate_threshold` automod rules now enforce. Implemented in `evaluateBoardAutomod` by counting the author's recent posts on the board within the rule's window (derived from the durable posts projection — consistent across API nodes, no separate counter store, deterministic per board partition). Added a `posts(author_id, created_at)` index. This completes the match type deferred from Phase 8.
- Automod audit log: every fired rule emits `EvtBoardAutomodTriggered` into a new `automod_audit_log` table (board, rule id, match type, action, target user, post/thread, reason, ts; SQLite + Postgres migration 76), in both the legacy handler and the native command-log decider. Read via `GET /api/v1/boards/{board}/automod-activity` (board-mod/admin gated); the AdminPage Automod panel shows a "Recent automod activity" feed.
- Tests: `TestBoardAutomodRateThreshold` (3rd post within window is auto-actioned), audit assertions in `TestBoardAutomodExecution`. Verified on the running binary: a rate-limited author is board-muted and the activity feed records `board_mute (rate_threshold)`.

This is the final phase of the plan — all of Phases 1–9 (plus SSH self-registration) are now complete.

2026-06-15 (Phase 8 — automod execution):

- Board automod rules are now evaluated when a thread/post is created. Shared `evaluateBoardAutomod` (internal/core/automod_eval.go) matches the first enabled rule by priority. Match types evaluated now: keyword, regex, repeated_text (longest same-rune run), link_count, account_age (author account younger than N hours). `rate_threshold` is deferred to Phase 9 (needs durable counters) and is skipped during evaluation.
- Actions applied: manual_review (flags the post + opens a moderation review), redact (removes the just-created post), lock_thread, board_mute / board_ban / global_mute (sanctions the author via the same EvtUserSanctioned path as a manual sanction). One action per matched rule (first match wins).
- Hooked into BOTH execution paths to stay consistent: the legacy handler (`appendPost` + `createThread`, applying events + projection mutations in-tx) and the native command-log decider (`decideAppendPost` + `decideCreateThread`, emitting the same events for downstream apply). Mirrors the existing content-filter precedent.
- Tests: core `TestBoardAutomodExecution` (redact removes post; link_count→board_mute then subsequent post blocked with ErrMuted; keyword→manual_review opens a review), `TestNativeAutomodEmitsActionEvents` (native decider emits the action event).
- NOTE: automod is not exempted for staff/board-moderators in v1 (rules apply to all posters on the board). `rate_threshold` execution + audit reporting land in Phase 9.

2026-06-15 (Phase 7 — board automod rule storage + APIs):

- Board-owned automod rule model (separate from the admin-only site content filters). Table `board_automod_rules` (schema/migrations + Postgres migration 75) storing all plan match types (keyword/regex/repeated_text/link_count/account_age/rate_threshold) and actions (manual_review/redact/lock_thread/board_mute/board_ban/global_mute), with priority, duration, public reason, and private note.
- Commands `setBoardAutomodRule` / `deleteBoardAutomodRule` (handler + native command-log decider + partition specs by board) with action-based authz: global_mute → admin, lock_thread → thread moderation, others → post moderation; delete by any board moderator. Shared `proto.ValidateAutomodRule` keeps handler and decider validation identical. Projection + event-log replay (rebuild) wired.
- HTTP `GET /boards/{board}/automod-rules` (board-moderator/admin gated via `Core.UserCanModerateBoard`); rules created/deleted via the generic command endpoint.
- Web: AdminPage "Automod rules" panel — board picker, add rule (match type / pattern / threshold / window / action / duration / reason), list, delete.
- Tests: core `TestBoardAutomodRuleCRUDAndAuthz` (CRUD + authz + rebuild), httpapi `TestBoardAutomodRulesHTTP`. Browser-verified the admin panel end to end.
- NOTE: Phase 7 is storage + management only — rules are NOT yet evaluated against posts/threads. That is Phase 8 (automod execution).

2026-06-15 (Phase 5 — staff 2FA):

- Added `internal/totp` (RFC 6238, stdlib only; verified against the RFC test vectors) for authenticator-app codes, and a pure-Go QR (`skip2/go-qrcode`) for enrollment.
- Storage (schema.go + SQLite migrations + Postgres migration 74): `security_settings` (staff_2fa_required), `user_2fa_settings` (totp_secret/pending/enrolled, email_enrolled), `two_factor_email_codes` (hashed, TTL).
- Core (`internal/core/twofactor.go`): security settings get/set; TOTP enroll/confirm/disable; email-code enable/disable + send (outbox `email.2fa`) + verify; `TwoFactorRequiredForLogin` (staff + enforced + enrolled), `StaffShouldEnroll2FA` nudge.
- HTTP: `handleLogin` returns `202`-style `{status:"2fa_required", challengeToken, methods}` for enrolled staff under enforcement; `POST /auth/2fa/verify` (short-lived challenge JWT) issues the session token; `POST /auth/2fa/email` sends a code; self-service `/account/2fa*`; admin `/admin/security-settings`; `GET /users/{name}/2fa`. SSH password auth is refused for 2FA-required staff (their key is the second factor).
- Web: AuthPage 2FA challenge step (authenticator + email-code, method toggle); profile enrollment section (QR + secret + confirm, email toggle); AdminPage "Require staff 2FA" toggle + "Check 2FA" in role grant.
- Tests: `internal/totp` RFC vectors; core `TestTwoFactorTOTPEnrollAndVerify` + `TestTwoFactorEnforcement`; httpapi `TestStaffTwoFactorLoginChallenge`. Browser-verified the full enroll → enforce → challenge → verify loop.

2026-06-14 (Phase 2 finish — rich signup intake + privacy acceptance):

- Bundled the default privacy policy into the binary (`internal/policy`, go:embed + content-hash version) and served it at `GET /api/v1/auth/privacy-policy`; a guard test keeps it identical to `doc/default-privacy-policy.md`.
- Signup now collects optional private intake (real name, school/affiliation, reason for joining) into `user_private_profiles`, and records explicit privacy-policy acceptance (`policy_accepted_at` + `policy_version`; new SQLite columns + Postgres migration 73).
- `GET /api/v1/auth/policy` reports `privacyPolicy{required,version}`; when `-require-policy-acceptance` (BUDGIE_REQUIRE_POLICY_ACCEPTANCE) is set, `handleRegister` rejects signup without acceptance (`422 policy_acceptance_required`).
- Admin review: `AccountRegistration` + `ListAccountRegistrations`/`GetAccountRegistrationByID` now LEFT JOIN the private profile to surface email/realName/affiliation/note; rendered in AdminPage and UserProfilePage pending-registration rows.
- Web: AuthPage adds the optional intake fields and a required privacy-policy acceptance checkbox with an inline policy viewer. Tests: `internal/policy` guard + version tests, httpapi `TestSignupPrivacyPolicyAcceptance`.
- SSH TUI self-registration (follow-up): the TUI was previously login/guest-only with no signup. Added an opt-in guest "create account" wizard (`internal/tui/signup.go`) gated behind `-allow-ssh-registration` (default off, since captcha can't run over SSH). It collects username/password, optional intake, and scrollable privacy-policy acceptance, then mirrors the HTTP handler (RegisterUser + SaveRegistrationIntake + StartEmailVerification when enabled). Tests: `TestSSHSignupFlow`, `TestSSHSignupDisabledByDefault`; verified end-to-end over real SSH.

2026-06-14 (Phase 6 — mod/admin sanction UI):

- Extended board-scoped sanction authorization: a board's moderators (site mods/admins, board moderators, or members with `can_moderate_posts`) can now apply and clear board-scoped mutes/bans for that board; global sanctions still require the site moderator role. (`sanctionUser`/`clearUserSanction` in `internal/core/handler/commands_admin.go`, mirroring `actorCanModerateBoardPostsTx`.)
- Added `internal/core` test `TestBoardModeratorCanSanctionWithinBoard` covering the new authz matrix.
- Web: added `UserSanction` type + `listUserSanctions` client reader; AdminPage "Sanctions" panel (lookup, apply site-wide mute/ban with duration+reason, list active, lift per-scope); ThreadPage per-post board Mute/Ban/Lift for board moderators.

2026-06-14:

- Added explicit registration modes: `open`, `manual`, and `paused`.
- Kept `requireApproval` as a compatibility alias where `true` maps to `manual` and `false` maps to `open`.
- Enforced `paused` during signup before normal user account creation; first-user admin bootstrap remains allowed.
- Updated admin registration controls to use the three-state mode selector.
- Added a bundled default privacy policy at `doc/default-privacy-policy.md`.

## Registration And Auth Plan

### Registration Controls

Add site registration mode:

- `open`: users can register normally.
- `manual`: registration creates a pending account for admin review.
- `paused`: signup is rejected before account creation.

Keep the legacy `requireApproval` API field as a compatibility alias:

- `requireApproval=false` maps to `open`.
- `requireApproval=true` maps to `manual`.

Add signup intake fields:

- email
- real name
- school or affiliation
- contact note / reason for joining
- captcha response token
- policy acceptance timestamp or boolean

Store private registration fields in private profile storage, not public profile state.

### Captcha

Add site-wide captcha config:

- disabled by default
- provider key/config stored server-side
- public site key exposed through a public auth policy endpoint
- signup accepts a captcha response token
- backend verifies token before account creation when enabled

Provider integration should live behind a small verifier interface so tests can use a deterministic local verifier.

### Email Verification

Email verification is mandatory for all new accounts once implemented:

- signup stores pending email verification state
- server creates a single-use verification token with expiry
- verified email is required before login or before registration approval finalizes
- first admin bootstrap path must remain possible without email delivery

This needs a mail delivery abstraction before enforcement is enabled in production.

### 2FA

2FA is default off and only required when admin enables it:

- site setting: `staff2FARequired`
- applies to admin and moderator roles
- supported methods: email code and TOTP authenticator app
- login returns a challenge instead of a JWT when required
- admin/mod role grants should surface whether the target has 2FA enrolled before enforcement is enabled

## Anti-Spam Registration Mode

Initial implementation should land the explicit registration mode first:

- add `mode` to registration settings
- enforce `paused` in signup
- use `manual` as the canonical manual-verification state
- display mode in admin registration settings
- retain old `requireApproval` behavior for compatibility

This is the safest first slice because it is independent of external captcha and email providers.

## Privacy Baseline

Add `doc/default-privacy-policy.md` with a default policy covering:

- what signup data is collected
- why it is collected
- who can view it
- retention expectations
- security and disclosure notes
- contact/update process

The app should later expose this policy in signup UI and require explicit acceptance.

## Mute/Ban Status

Existing backend support:

- board-scoped `mute` and `ban`
- global `mute` and `ban`
- generated public audit records for board posting denials when the board is public

Required follow-up:

- confirm board moderator authorization for board-scoped sanctions matches the product requirement; current command requires moderator role for sanctions and admin for mod targets.
- expose board mute workflows clearly in board moderation UI.
- expose site mute workflows clearly in admin UI.

## Automod Plan

Create a board-owned rule model:

- rule id
- board scope
- enabled
- priority
- match type: keyword, regex, repeated text, link count, account age, rate threshold
- action: send to manual review, redact/remove post, lock thread, board mute, global/site mute, board ban
- duration for sanctions
- public/audit reason
- private moderator note
- created/updated actor and timestamp

Authorization:

- board moderators with `canModeratePosts` can manage post moderation rules for their boards.
- board moderators with `canModerateThreads` can manage thread actions.
- admins can manage all rules and global rules.

Execution:

- evaluate rules in create-thread and append-post command paths.
- record matched rule ids in moderation review/audit state.
- apply blocking/removal actions inside the command transaction.
- rate-limit counters should use a bounded time window and be durable enough to enforce across API nodes.

Initial useful actions:

- manual review only
- redact/remove matched post after creation
- board mute user
- board ban user

## Delivery Phases

1. Registration mode and privacy baseline.
2. Rich signup fields and admin review display.
3. Captcha verifier interface and disabled-by-default config.
4. Email verification token storage and mailer abstraction.
5. Staff 2FA enrollment and login challenge flow.
6. Mod/admin sanction UI.
7. Board automod rule storage and read/write APIs.
8. Automod execution in post creation paths.
9. Board rate limit rules and audit reporting.

## Validation

Required checks per phase:

- Go unit tests for registration mode behavior.
- HTTP API tests for paused/manual/open registration.
- Web build after admin/signup UI updates.
- Core command tests for sanctions and automod actions.
- Privacy policy link visible from signup once UI work lands.
