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

2026-06-14 (Phase 2 finish — rich signup intake + privacy acceptance):

- Bundled the default privacy policy into the binary (`internal/policy`, go:embed + content-hash version) and served it at `GET /api/v1/auth/privacy-policy`; a guard test keeps it identical to `doc/default-privacy-policy.md`.
- Signup now collects optional private intake (real name, school/affiliation, reason for joining) into `user_private_profiles`, and records explicit privacy-policy acceptance (`policy_accepted_at` + `policy_version`; new SQLite columns + Postgres migration 73).
- `GET /api/v1/auth/policy` reports `privacyPolicy{required,version}`; when `-require-policy-acceptance` (BUDGIE_REQUIRE_POLICY_ACCEPTANCE) is set, `handleRegister` rejects signup without acceptance (`422 policy_acceptance_required`).
- Admin review: `AccountRegistration` + `ListAccountRegistrations`/`GetAccountRegistrationByID` now LEFT JOIN the private profile to surface email/realName/affiliation/note; rendered in AdminPage and UserProfilePage pending-registration rows.
- Web: AuthPage adds the optional intake fields and a required privacy-policy acceptance checkbox with an inline policy viewer. Tests: `internal/policy` guard + version tests, httpapi `TestSignupPrivacyPolicyAcceptance`.
- Still open for a later pass: TUI registration path does not yet collect intake/acceptance (enforcement currently lives in the HTTP signup handler).

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
