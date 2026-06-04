# Protocol Definition

*The authoritative wire specification. This is the durable artifact: clients and the server are implementations of this document. Hand this to Claude Code first — everything else (renderers, storage, moderation UI) is built against it.*

**Status:** v1 draft. Stable enough to build against; payload fields may gain optional members.
**Wire format:** JSON for v1 (debuggable, REST-native). A binary format (msgpack/CBOR) may be negotiated later via the handshake without changing semantics.

---

## 1. Design principles

1. **Transport-agnostic core.** The protocol is an abstract exchange of *commands* (client → server) and *events* (server → client). Transports carry that exchange; they do not define it. The same command means the same thing over WebSocket, HTTP, or the in-process SSH path.
2. **Commands in, events out.** Clients send commands that may be **rejected**. The server validates, applies, assigns a sequence number, and emits events that are **facts**. All authority — permissions, rate limits, moderation — lives in the one command-handling path. (See *High-Level Technical Decisions*, Decision 1.)
3. **One log, one cursor.** Every durable event has a global monotonic `seq`. A client's entire position is a single integer. Live delivery, catch-up, and history are the same mechanism at different starting points.
4. **Graceful degradation.** Liveness is a transport choice, not a data-model choice. A client picks the richest transport it can sustain; the semantics are identical down to a cron job running `curl`.

---

## 2. The transport ladder

This is the centerpiece and the answer to the fallback requirement. All four tiers exchange the **same** commands and events with the **same** cursor semantics. A client degrades down the ladder without the server caring.

| Tier | Transport | Liveness | Direction | Works through |
|---|---|---|---|---|
| 1 | **WebSocket** | Instant push | Full duplex | Anything that allows WS upgrade |
| 2 | **SSE** (`text/event-stream`) | Instant push | Server→client only (commands via POST) | Any HTTP/1.1+; survives most proxies |
| 3 | **HTTP long-poll** | Near-instant | Request/response, held | Literally any HTTP client |
| 4 | **HTTP poll** | Interval (stale-tolerant) | Request/response | Anything, including `curl`/cron, fully CDN-cacheable |

Plus the **rendered transport**:

| — | **SSH** | Instant push | Full duplex (in-process) | Any SSH client |

SSH is special: the user does **not** speak this protocol over the wire. They SSH in, the server spawns a TUI that is itself an in-process client of the command/event bus, and it paints ANSI. The protocol below is what that server-hosted client uses internally — identical commands and events, no serialization to the user's terminal beyond rendered ANSI. Everything in §3–§10 applies to it as in-process calls.

### Why this falls out for free

Because position is a single `seq` cursor, every read tier is a variation of one operation — *"give me events after `N`"*:

- WS: pushes them as they're appended (`after` = your live position).
- SSE: same, over an HTTP event stream; `Last-Event-ID` header **is** the cursor on reconnect.
- Long-poll: `GET /events?after=N&wait=30` — server holds the request until new events exist or 30s elapses, then returns the batch. Client immediately re-requests with the new cursor.
- Poll: `GET /events?after=N` — returns immediately with whatever's there (possibly empty). Stale-tolerant and cacheable.

No separate sync subsystem, no divergent code paths. The fallback is the same query with a different waiting policy.

---

## 3. Message envelope

Every message is a JSON object with a `kind` discriminator.

### Client → server

```json
{
  "kind": "command",
  "cid": "c_01J8X...",          // client-generated correlation id (idempotency key)
  "command": "appendPost",
  "payload": { },
  "ts": 1716500000000           // client clock, advisory only
}
```

### Server → client

Three kinds:

**`event`** — a fact (durable events carry `seq`; ephemeral events carry `eseq`, a separate lightweight counter):
```json
{
  "kind": "event",
  "event": "post.appended",
  "seq": 41021,                  // durable: global monotonic. omitted for ephemeral
  "eseq": null,
  "payload": { },
  "ts": 1716500000123
}
```

**`ack`** — the response to a command, correlated by `cid`:
```json
{
  "kind": "ack",
  "cid": "c_01J8X...",
  "ok": true,
  "result": { "id": "p_55", "seq": 41021 },   // ids/seq of what was created
  "error": null
}
```
On rejection: `"ok": false`, `"result": null`, and a populated `error` (see §8).

**`control`** — connection-level messages (handshake, heartbeat, cursor advisories):
```json
{ "kind": "control", "control": "welcome", "payload": { } }
```

---

## 4. Connection lifecycle & handshake

### WebSocket / SSE

On connect, the server sends a `welcome` control message:

```json
{
  "kind": "control",
  "control": "welcome",
  "payload": {
    "protocol": "1",
    "server": "name/0.1.0",
    "head": 41020,                 // current durable log head
    "capabilities": ["ws", "sse", "longpoll", "poll", "ansi-art", "edit", "redact"],
    "wireFormats": ["json"],       // future: "msgpack"
    "heartbeatSec": 30
  }
}
```

The client then sends a `resume` control with its last-known cursor and its desired subscriptions (§6):

```json
{
  "kind": "control",
  "control": "resume",
  "payload": { "after": 40990, "subscriptions": ["board:tech", "thread:88", "chat:lobby", "presence:global"] }
}
```

The server replays `40991..head` for subscribed scopes, then attaches the live tail. A fresh client sends `"after": 0` (or omits it) to backfill from the projection rather than the full log — see §7.

**Heartbeat:** server sends `{"kind":"control","control":"ping"}` every `heartbeatSec`; client replies `pong`. Missed heartbeats → client reconnects and resumes from its cursor.

### HTTP (long-poll / poll)

No persistent handshake. Each request carries auth (§5) and the cursor as a query param. The equivalent of `welcome.head` is returned in a response header (`X-Log-Head`) on every read.

---

## 5. Authentication

The **account** is the source of truth. Credentials attach to it. (See *High-Level Technical Decisions*, Decision 4 on roles.)

| Transport | How identity is established |
|---|---|
| SSH | SSH public-key auth at the SSH layer proves the account. No app-level token needed; the server-hosted client runs as that account. |
| WebSocket / SSE | Bearer token presented at connect (query param `?token=` or `Authorization` header on the upgrade). Connection is authenticated once for its lifetime. |
| HTTP | `Authorization: Bearer <token>` on every request (stateless). |

Tokens are obtained out-of-band of the event protocol:

```
POST /api/v1/auth/login        { "user": "...", "password": "..." }  -> { "token": "...", "expires": ... }
POST /api/v1/auth/password-recovery { name, submittedName?, email?, note? } -> accepted
POST /api/v1/auth/refresh      { "token": "..." }                    -> { "token": "...", "expires": ... }
```

Roadmap (does not change the protocol): passkeys / OAuth as additional credential types that mint the same token. SSH keys may also be registered to an account so the same identity spans SSH and web.

---

## 6. Subscriptions & scoping

A client rarely wants every global event. It subscribes to **scopes**; the event stream is filtered to subscribed scopes (plus events addressed to the account, e.g. a sanction against you, which always deliver).

Scope grammar: `type:id`.

| Scope | Delivers |
|---|---|
| `board:<id>` | `thread.new`, `thread.title_set`, `thread.moved`, `thread.locked` in that board |
| `thread:<id>` | `thread.title_set`, `post.appended`, `post.edited`, `post.flags_set`, `post.redacted`, `post.restored` in that thread |
| `chat:<room>` | `chat.line` in that room (ephemeral) |
| `presence:global` or `presence:<board>` | `user.joined`, `user.left`, `presence.update` |
| `account:self` | events targeting this account (always implicitly subscribed) |

- **WS/SSE:** manage via `subscribe` / `unsubscribe` commands (below); initial set sent in `resume`.
- **HTTP:** scope is a query param: `GET /api/v1/events?after=N&scope=thread:88&scope=chat:lobby`.

Subscription commands:
```
subscribe    { "scopes": ["thread:91"] }
unsubscribe  { "scopes": ["thread:88"] }
```
These return an `ack` and do not produce durable events.

---

## 7. Cursor & resume semantics

- **`seq`** is the global monotonic durable cursor. It only ever increases and is assigned by the single-writer command handler at append time. **Ordering is by `seq`, never by `ts`.**
- **`eseq`** is a separate, lighter counter for ephemeral events (chat/presence). Ephemeral events are best-effort and may be pruned (see Decision 2); a client that misses some `eseq` values does not error — ephemeral gaps are expected and ignored.
- **Backfill vs. replay.** Resuming from a recent cursor replays exact events from the log. A brand-new client (or one resuming from a cursor older than the log's retention) should **not** replay the entire log — it requests current state from projection endpoints (§9 reads) and sets its cursor to the returned `X-Log-Head`, then tails forward. The protocol distinguishes:
  - `after >= oldestRetainedSeq` → exact event replay.
  - `after < oldestRetainedSeq` (or `0`) → server responds with `control: "backfill-required"` carrying the current `head`; client loads projections then tails from `head`.
- **Idempotent retries.** Because HTTP retries are routine, the server deduplicates commands by `cid` within a window (default 10 min). Re-sending a command with a `cid` it already applied returns the original `ack`, not a second effect. Clients SHOULD reuse the same `cid` when retrying a command they're unsure landed.

---

## 8. Error model

Command rejections come back in the `ack`'s `error`; protocol/transport problems come back as a `control` error or HTTP status.

```json
{
  "kind": "ack",
  "cid": "c_01J8X...",
  "ok": false,
  "result": null,
  "error": { "code": "thread_locked", "message": "Thread is locked.", "retryable": false }
}
```

Stable error codes (extensible):

| Code | Meaning | Retryable |
|---|---|---|
| `unauthenticated` | Missing/invalid token | after re-auth |
| `forbidden` | Authenticated but lacks permission/role | no |
| `rate_limited` | Too many commands; includes `retryAfterMs` | yes |
| `validation_failed` | Payload invalid; includes `fields` | no |
| `not_found` | Target thread/post/user doesn't exist | no |
| `thread_locked` | Thread is locked to new posts | no |
| `edit_window_expired` | Past the allowed edit window | no |
| `conflict` | State changed under the command (e.g. already redacted) | sometimes |
| `muted` / `banned` | User is sanctioned | after expiry |

HTTP transport maps these to status codes (`401`, `403`, `429`, `422`, `404`, `409`) with the same JSON `error` body.

---

## 9. HTTP/REST surface

Two halves: **reads** (events + projections) and **writes** (commands). Writes are offered both as a uniform command endpoint and as idiomatic RESTful aliases that internally dispatch the same command.

### Reads — events (the cursor surface)

```
GET /api/v1/events?after={seq}&scope={s}&scope={s}&limit={n}&wait={sec}
```
- `wait=0` (or omit) → poll: returns immediately, possibly `{ "events": [], "head": N }`.
- `wait=30` → long-poll: holds up to 30s for new events.
- Response: `{ "events": [ <event envelopes> ], "head": 41021 }`, plus `X-Log-Head: 41021`.
- Cacheable when `wait=0` and `after` is at/near a cache boundary.

SSE variant (push over HTTP):
```
GET /api/v1/events/stream?after={seq}&scope={s}          Accept: text/event-stream
```
Each SSE `id:` field carries the `seq`, so the browser's automatic `Last-Event-ID` on reconnect resumes the cursor with zero client logic.

### Reads — projections (current state, no event replay)

For clients that just want state (a fresh web load, a poller building a view):

```
GET /api/v1/boards
GET /api/v1/categories
GET /api/v1/stats/community
GET /api/v1/stats/community/history?limit=&offset=
GET /api/v1/rankings/boards?limit=&offset=
GET /api/v1/rankings/threads?board=&limit=&offset=
GET /api/v1/rankings/replies?limit=&offset=
GET /api/v1/rankings/users?limit=&offset=
GET /api/v1/rankings/blessings?limit=&offset=
GET /api/v1/rankings/archive?kind=&limit=&offset=
GET /api/v1/boards/favorites
GET /api/v1/boards/favorites/tree
GET /api/v1/boards/favorites/export
GET /api/v1/boards/summary?q=&sort=&new=&newDays=
GET /api/v1/boards/unread?q=&sort=&new=&newDays=
GET /api/v1/boards/{id}
GET /api/v1/boards/{id}/online?limit=&offset=
GET /api/v1/boards/{id}/members
GET /api/v1/boards/{id}/member-applications?status=
GET /api/v1/boards/{id}/digest?kind=&path=&limit=&offset=
GET /api/v1/boards/{id}/digest/tree?kind=
GET /api/v1/digest?kind=&path=&limit=&offset=
GET /api/v1/digest/search?q=&board=&kind=&path=&limit=&offset=
GET /api/v1/digest/{id}/download
GET /api/v1/announcements?path=&limit=&offset=
GET /api/v1/boards/{id}/threads?sort=recent&limit=&before=&unread=1
GET /api/v1/mail?mailbox=inbox&unread=1&limit=&offset=
GET /api/v1/mail/groups
GET /api/v1/mail/usage
GET /api/v1/mail/attachments/{id}
GET /api/v1/mail/{id}
GET /api/v1/relay/deliveries?status=pending&limit=&offset=
GET /api/v1/messages?limit=&offset=
GET /api/v1/messages/settings
GET /api/v1/messages/{user}?limit=&offset=
GET /api/v1/admin/registration-settings
GET /api/v1/admin/registrations?status=pending&limit=&offset=
GET /api/v1/admin/password-recovery?status=pending&limit=&offset=
GET /api/v1/social/{friends|fans|ignores|online-friends}
GET /api/v1/presence/online?limit=&offset=
GET /api/v1/threads/unread?favorites=1&folder=&limit=&offset=
GET /api/v1/authors/{name}/posts?limit=&offset=
GET /api/v1/posts/{id}/reply-tree?limit=&offset=
GET /api/v1/threads/{id}                 -> thread + posts (current, redactions applied)
GET /api/v1/threads/{id}/posts?after=&limit=
GET /api/v1/attachments/{id}
GET /api/v1/users/{id}/profile
GET /api/v1/users/{name}/posts?limit=&offset= -> public-board recent posts
GET /api/v1/users/me/private-profile
GET /api/v1/users/{name}/private-profile     -> admin-only private contact
GET /api/v1/users/{name}/files
GET /api/v1/users/{name}/files/{file}
GET /api/v1/users/me/files                   -> own public/private files
GET /api/v1/users/me/files/{file}
GET /api/v1/users/me/signatures
GET /api/v1/users/me/login-acl
GET /api/v1/admin/content-filters?scope=&includeInactive=
GET /api/v1/chat/{room}/recent?limit=    -> bounded ephemeral history (ring buffer)
```
These read projection tables directly (see Decision 2) and are CDN-cacheable. Moderator views (`?include=redacted`) require role and are never cached.
Ranking reads are derived projection views for KBS-style public lists: community
counters including cumulative online/stay time, active boards, hot threads,
latest replies, top posters, blessing rituals, and active archive paths.
Hot-thread `score` is recency-decayed: visible posts and reactions form the
activity base, then thread `updatedAt` applies a 48-hour half-life so stale
activity gradually falls behind fresh conversation.
Board/thread/reply/archive rankings hide member-read boards unless the viewer
can read them; direct thread ranking queries scoped to an inaccessible board are
rejected. Public generated system boards such as `newcomers`, `BBSLists`,
`Registry`, `reject_registry`, `syssecurity`, `Goodbye`, `Blessing`,
`GiveupNotice`, `bbsnet`, `notepad`, `Filter`, `Recommend`, `denypost`,
`undenypost`, `vote`, `0announce`, and `0moderation` remain directly readable
boards but are excluded from community counters and ranking surfaces so
generated logs do not masquerade as organic activity. Restricted generated
boards such as `sysmail` use normal member-read policy and are also excluded
from community counters and rankings.

### Writes — uniform command endpoint

```
POST /api/v1/commands
Body: { "cid": "...", "command": "appendPost", "payload": { } }
Returns: the ack envelope.
```

### Writes — RESTful aliases (sugar over the same commands)

| Method + path | Dispatches command |
|---|---|
| `POST /api/v1/boards` | `createBoard` |
| `POST /api/v1/boards/{id}/threads` | `createThread` |
| `POST /api/v1/boards/{id}/mail-in` | `postBoardMail` |
| `POST /api/v1/threads/{id}/posts` | `appendPost` |
| `POST /api/v1/threads/{id}/mail-in` | `postBoardMail` |
| `POST /api/v1/posts/{id}/attachments` | `attachPost` |
| `PATCH /api/v1/posts/{id}` | `editPost` |
| `POST /api/v1/posts/{id}/repost` | `repostPost` |
| `DELETE /api/v1/posts/{id}` | `redactPost` |
| `POST /api/v1/posts/{id}/restore` | `restorePost` |
| `PATCH /api/v1/threads/{id}/title` | `setThreadTitle` |
| `POST /api/v1/threads/{id}/lock` | `lockThread` |
| `PATCH /api/v1/categories/{id}` | update category metadata/visibility |
| `PATCH /api/v1/boards/{id}/settings` | `setBoardSettings` |
| `PATCH /api/v1/boards/{id}/member-requirements` | `setBoardMemberRequirements` |
| `PUT /api/v1/boards/{id}/moderators/{user}` | `setBoardModerator` |
| `DELETE /api/v1/boards/{id}/moderators/{user}` | `setBoardModerator` |
| `PUT /api/v1/boards/{id}/members/{user}` | `setBoardMember` |
| `DELETE /api/v1/boards/{id}/members/{user}` | `setBoardMember` |
| `POST /api/v1/boards/{id}/member-applications` | `applyBoardMembership` |
| `POST /api/v1/board-member-applications/{id}/review` | `reviewBoardMembership` |
| `POST /api/v1/boards/{id}/members/leave` | `leaveBoardMembership` |
| `PUT /api/v1/boards/{id}/favorite` | `setBoardFavorite` |
| `PATCH /api/v1/boards/{id}/favorite` | `moveBoardFavorite` |
| `DELETE /api/v1/boards/{id}/favorite` | `setBoardFavorite` |
| `POST /api/v1/boards/favorites/import` | `importFavoriteTree` |
| `POST /api/v1/boards/favorites/read` | `markFavoriteFolderRead` |
| `POST /api/v1/boards/favorites/read/restore` | `restoreFavoriteFolderRead` |
| `POST /api/v1/boards/favorites/folders` | `createFavoriteFolder` |
| `PATCH /api/v1/boards/favorites/folders/{id}` | `updateFavoriteFolder` |
| `DELETE /api/v1/boards/favorites/folders/{id}` | `deleteFavoriteFolder` |
| `POST /api/v1/boards/favorites/folders/{id}/read` | `markFavoriteFolderRead` |
| `POST /api/v1/boards/favorites/folders/{id}/read/restore` | `restoreFavoriteFolderRead` |
| `POST /api/v1/boards/{id}/read` | `markBoardRead` |
| `POST /api/v1/boards/{id}/read/restore` | `restoreBoardRead` |
| `POST /api/v1/threads/{id}/read` | `markThreadRead` |
| `POST /api/v1/threads/{id}/read/restore` | `restoreThreadRead` |
| `POST /api/v1/posts/{id}/read` | `markPostRead` |
| `POST /api/v1/posts/{id}/flag` | `flagPost` |
| `POST /api/v1/posts/{id}/digest` | `curatePost` |
| `POST /api/v1/threads/{id}/digest` | `curateThread` |
| `POST /api/v1/boards/{id}/digest/directories` | `createDigestDirectory` |
| `POST /api/v1/boards/{id}/digest/paths/move` | `moveDigestPath` |
| `POST /api/v1/boards/{id}/digest/paths/copy` | `copyDigestPath` |
| `DELETE /api/v1/boards/{id}/digest/paths?kind=&path=` | `deleteDigestPath` |
| `POST /api/v1/digest/{id}/mail` | `sendDigestEntryMail` |
| `PATCH /api/v1/digest/{id}` | `updateDigestEntry` |
| `PUT /api/v1/digest/{id}/body` | `setDigestEntryBody` |
| `DELETE /api/v1/digest/{id}/body` | `setDigestEntryBody` |
| `DELETE /api/v1/digest/{id}` | `removeDigestEntry` |
| `POST /api/v1/posts/{id}/mail` | `mailPostAuthor` |
| `POST /api/v1/mail` | `sendMail` |
| `POST /api/v1/mail/groups` | `setMailGroup` |
| `PUT /api/v1/mail/groups/{id}` | `setMailGroup` |
| `PATCH /api/v1/mail/groups/{id}` | `setMailGroup` |
| `DELETE /api/v1/mail/groups/{id}` | `deleteMailGroup` |
| `POST /api/v1/mail/{id}/attachments` | `attachMail` |
| `PATCH /api/v1/mail/{id}` | `updateMail` |
| `DELETE /api/v1/mail/{id}` | `deleteMail` |
| `PATCH /api/v1/admin/registration-settings` | enable/disable account approval queue |
| `POST /api/v1/admin/registrations/{name}/review` | approve/reject pending account |
| `POST /api/v1/admin/password-recovery/{id}/review` | reset/reject password recovery request |
| `POST /api/v1/admin/users/{name}/transfer-id` | transfer login ID/name to a new name |
| `DELETE /api/v1/admin/users/{name}` | hard-delete account/private state and tombstone public authorship |
| `PATCH /api/v1/users/me` | update public profile fields |
| `PATCH /api/v1/users/me/private-profile` | update own private contact profile |
| `PUT /api/v1/users/me/files/{file}` | create/update own personal file |
| `PATCH /api/v1/users/me/files/{file}` | create/update own personal file |
| `DELETE /api/v1/users/me/files/{file}` | delete own personal file |
| `PATCH /api/v1/users/me/password` | self-service password change |
| `POST /api/v1/users/me/deactivate` | self-service account deactivation |
| `POST /api/v1/users/me/signatures` | create saved signature |
| `PATCH /api/v1/users/me/signatures/{id}` | update saved signature |
| `DELETE /api/v1/users/me/signatures/{id}` | delete saved signature |
| `PATCH /api/v1/users/me/signatures/settings` | select fixed/random signature mode |
| `POST /api/v1/users/me/signatures/recount` | recount and repair saved signature metadata |
| `POST /api/v1/users/me/login-acl/rules` | create login host ACL rule |
| `PATCH /api/v1/users/me/login-acl/rules/{id}` | update login host ACL rule |
| `DELETE /api/v1/users/me/login-acl/rules/{id}` | delete login host ACL rule |
| `PATCH /api/v1/users/me/login-acl/settings` | enable/disable login host allow-list |
| `POST /api/v1/messages` | `sendDirectMessage` |
| `PATCH /api/v1/messages/settings` | `setDirectMessageSettings` |
| `POST /api/v1/messages/{id}/read` | `markDirectMessageRead` |
| `DELETE /api/v1/messages/{id}` | `deleteDirectMessage` |
| `PATCH /api/v1/posts/{id}/flags` | `setPostFlag` |
| `PUT /api/v1/users/{user}/friend` | `setUserRelationship` |
| `DELETE /api/v1/users/{user}/friend` | `setUserRelationship` |
| `PUT /api/v1/users/{user}/ignore` | `setUserRelationship` |
| `DELETE /api/v1/users/{user}/ignore` | `setUserRelationship` |
| `PUT /api/v1/users/{user}/login-watch` | `setLoginWatch` |
| `DELETE /api/v1/users/{user}/login-watch` | `setLoginWatch` |
| `POST /api/v1/users/{user}/bless` | `blessUser` |
| `POST /api/v1/presence/guest` | public anonymous guest presence ping |
| `POST /api/v1/chat/{room}/lines` | `sendChatLine` |
| `POST /api/v1/mod/reviewables/{id}/resolve` | `resolveReview` |
| `POST /api/v1/admin/content-filters` | `setContentFilter` |
| `PATCH /api/v1/admin/content-filters/{filter}` | `setContentFilter` |
| `POST /api/v1/stats/community/snapshot` | `publishStatsSnapshot` |
| `POST /api/v1/admin/notices` | `publishSystemNotice` |
| `POST /api/v1/polls/{poll}/vote` | `votePoll` |
| `POST /api/v1/polls/{poll}/publish-result` | `publishPollResult` |
| `POST /api/v1/users/{id}/sanctions` | `sanctionUser` |
| `DELETE /api/v1/users/{id}/sanctions?kind=&scope=` | `clearUserSanction` |

Every alias accepts the same `cid` (as header `X-Command-Id`) for idempotency and returns the same `ack` body.

---

## 10. Command catalog

Payloads are sketches; required fields marked `*`. All commands accept a `cid`. All return an `ack`.

### Content & threads

```
createThread   { board*, title*, body*, contentType, anonymous, attachments[] } -> thread.new (+ first post.appended)
appendPost     { thread*, body*, replyTo?, quotePost?, contentType,
                 anonymous, attachments[] }                    -> post.appended
repostPost     { post*, board*, title? }                    -> thread.new (+ source-linked post.appended)
postBoardMail  { board, thread, subject, body*, contentType, attachments[] } -> thread.new or post.appended
attachPost     { post*, filename*, contentType, sizeBytes } -> post.attachment_added
editPost       { post*, body* }                             -> post.edited
setPostFlag    { post*, marked?, recommended?, noReply?, tex?, mailBack? } -> post.flags_set
redactPost     { post*, reason }                            -> post.redacted
restorePost    { post* }                                    -> post.restored
setThreadTitle { thread*, title* }                          -> thread.title_set
flagPost       { post*, reason }                             -> post.flagged
```
- `contentType`: `"markup"` (default — the medium-neutral subset, Decision 3) or `"ansi-art"` (raw CP437+ANSI payload, fixed-geometry).
- `anonymous`: optional on `createThread` and `appendPost`; accepted only when
  the board allows anonymous posting or the actor can moderate the board.
- `quotePost`: optional on `appendPost` and requires `replyTo`. It prepends
  editable quoted article text from the reply target, rejects redacted or
  cross-thread quote sources, and preserves the normal direct reply link.
- `attachments`: optional file metadata, max 8 per post:
  `{ filename*, contentType, sizeBytes, url }`. The handler generates
  attachment ids in the durable `post.appended` payload. Normal users may attach
  only when the board has `attachmentsAllowed`; board moderators can attach for
  repair/import workflows.
- `postBoardMail` is the authenticated inbound mail bridge surface. Board
  mail-in must be enabled with `mailInAllowed` unless the actor can moderate the
  board. With `thread` omitted it creates a new thread titled by `subject`;
  with `thread` set it appends a reply to that thread. The underlying
  create/reply handlers still enforce read-only, no-reply, member-only,
  attachment, sanction, and trust rules.
- Relay-enabled boards create pending `relay_deliveries` rows for every new
  post. `GET /api/v1/relay/deliveries` is admin-only and returns relay payloads
  for an external SMTP/NNTP/email bridge to drain.
- Binary uploads use `POST /api/v1/posts/{id}/attachments` with multipart
  field `file`. The command writes replayable metadata while the HTTP layer
  stores bytes in `attachment_blobs`; `GET /api/v1/attachments/{id}` streams the
  stored file after the same board-read authorization check used for posts.
- `post.appended` may carry `signature`, a snapshot of the visible author's
  current saved signature at posting time. Users can keep up to eight saved
  signatures, choose a fixed current signature, or rotate randomly among active
  saved signatures. Later signature edits do not rewrite old posts. Anonymous
  posts omit signatures.
- `replyTo`: optional parent post id for shallow threading. Depth is capped server-side (open question #1 in Decisions doc); over-deep replies are flattened, not rejected.
- `setPostFlag` stores KBS-style article metadata. `marked` and `recommended`
  require board curation permission; `noReply` requires board thread-moderation
  permission. `tex` and `mailBack` may be set by the post author or a board
  thread moderator. A `noReply` thread starter blocks ordinary replies to the
  thread, and a `noReply` parent article blocks ordinary direct replies to that
  article; board thread moderators may bypass those article-local reply stops.
  Replies to a non-anonymous article with `mailBack` enabled create a private
  `mail.sent` copy to the article author when delivery is allowed by mail
  quotas and ignore relationships.
- `repostPost` creates a new thread on the destination board from an existing
  readable source article. The actor must be able to read the source board and
  post to the destination board. The first destination post carries source
  lineage in `sourcePost`, `sourceThread`, `sourceBoard`, `sourceAuthor`,
  `sourceAuthorId`, and `sourceTitle`; attachments are not cloned by this
  command.
- `setThreadTitle` changes the thread/topic title. The thread starter may use
  it during the normal edit window; board thread moderators may use it later.
  It does not append a post or advance unread markers.

### Moderation & structure

```
createBoard    { id*, name*, description, parentId?, position? } -> board.created
lockThread     { thread*, locked* }                         -> thread.locked
moveThread     { thread*, toBoard* }                         -> thread.moved
sanctionUser   { user*, kind*, scope, durationSec, reason }  -> user.sanctioned
clearUserSanction { user*, kind?, scope?, reason }            -> user.sanction_cleared
setContentFilter { id?, pattern*, scope?, active? }           -> content_filter.set
grantRole      { user*, role* }                              -> role.granted
revokeRole     { user*, role* }                              -> role.revoked
resolveReview  { review*, resolution* }                      -> review.resolved
publishStatsSnapshot { date? }                               -> thread.new (+ post.appended)
publishSystemNotice { board?, title*, body*, source? }        -> thread.new (+ post.appended)
blessUser       { user*, message? }                           -> user.blessed (+ thread.new/post.appended on Blessing)
votePoll        { poll*, option* }                             -> poll.voted
publishPollResult { poll* }                                    -> thread.new (+ post.appended on vote)
setBoardSettings { board*, anonymousAllowed?, readOnly?, noReply?,
                   attachmentsAllowed?, mailInAllowed?, relayEnabled?,
                   memberReadMode?, memberPostMode? }        -> ack only
setBoardMemberRequirements { board*, minLoginCount?, minPostCount?,
                   minTrustLevel?, minScore?, minBoardPostCount?,
                   minBoardOriginalPostCount?, minBoardDigestCount?,
                   minBoardMarkCount?, maxMembers?,
                   approvalMode? }                              -> ack only
setBoardModerator { board*, user*, moderator*, position? }   -> ack only
setBoardMember { board*, user*, member*, title?,
                 position?,
                 canManageMembers?, canCurate?,
                 canModeratePosts?, canModerateThreads?,
                 canAnnounce?, canManagePolls?,
                 canSetBoardSettings? }                      -> ack only
applyBoardMembership { board*, note }                         -> ack id=application
reviewBoardMembership { application*, status*, title, note }   -> ack only
leaveBoardMembership { board* }                               -> ack only
curatePost     { post*, kind, title, path, note }             -> ack id=entry
curateThread   { thread*, kind, title, path, note }           -> ack id=entry
removeDigestEntry { entry* }                                  -> ack only
updateDigestEntry { entry*, title?, path?, note? }             -> ack only
setDigestEntryBody { entry*, body, reset? }                    -> ack only
createDigestDirectory { board*, kind?, path* }                 -> ack id=directory
moveDigestPath { board*, kind?, fromPath*, toPath* }            -> ack only
copyDigestPath { board*, kind?, fromPath*, toPath* }            -> ack only
deleteDigestPath { board*, kind?, path* }                       -> ack only
sendDigestEntryMail { entry*, to[], toGroups[], toFriends,
                      toAll, subject, note, saveSent }        -> mail.sent
```
- `kind` (sanction): `"mute"` | `"ban"`. `scope`: board id or `"global"`.
- All require role; the command handler checks the role projection before accepting.
- `createBoard` creates the board projection and a matching category row.
  `parentId` points at an existing category, empty means a root directory item,
  and omitted `position` appends among siblings.
- `PATCH /api/v1/categories/{id}` lets admins edit category name/description,
  parent, sibling position, and directory `visibility`. Visibility is a
  directory-level ACL: `public` is visible to all authenticated users, `staff`
  is visible to moderators/admins, and `hidden` is visible only to admins.
- `GET /api/v1/boards/summary` and `/boards/unread` return per-board favorite
  state, read-marker cursors, unread counts, article/thread counts, current
  online-user count, category creation time, and a derived `newBoard` flag for
  KBS-style board discovery. `q` searches board id/name/description; `new=1`
  filters to boards created within `newDays` days, defaulting to 30; `sort`
  accepts `name`, `new`, `online`, `posts`, `threads`, `activity`, and
  `unread`.
- Board settings expose KBS-style policy flags. `readOnly`, `noReply`, and
  `anonymousAllowed` are enforced in the posting path. `memberReadMode` gates
  board/thread/post reads, `memberPostMode` gates new threads and replies, and
  `attachmentsAllowed` gates post attachment metadata. `mailInAllowed` gates
  the authenticated inbound mail bridge. `relayEnabled` queues pending
  `relay_deliveries` records for external bridge delivery.
- Unscoped `GET /api/v1/search` applies the same member-read board filter as
  thread and post reads; scoped searches still reject unreadable boards.
- Board moderators can manage board settings and moderate threads/posts in
  their board. Only admins can add or remove board moderators; board
  moderators and global moderators/admins can add or remove board members and
  grant delegated board-member permissions. `grantRole`, `revokeRole`,
  public-board `setBoardSettings`, and public-board `setBoardModerator` changes
  also lazily create the `syssecurity` system board and sanitized generated
  security/admin notice threads. Member-read board security changes are not
  mirrored into the public audit board.
- Public-board `flagPost` and `resolveReview` also lazily create the
  `0moderation` system board and deterministic sanitized audit threads/posts.
  The generated body includes review id, status, board, thread, post, and actor
  metadata, but excludes report reason, moderator resolution text, and article
  body. Member-read board reviews are not mirrored into a public system board.
- Admin-managed content filters are replayable projection rows. Matching a new
  public-board thread or reply creates an open `content_filter` moderation
  review and a sanitized KBS-style `Filter` system-board record. The generated
  body includes review id, filter id/scope, board, thread, post, and public
  author metadata, but excludes the matched pattern and article body.
  Member-read board matches stay only in the moderator review queue.
- Board members can carry a short board-local title and explicit position for
  KBS-style ordered member rolls.
  `canManageMembers` delegates member roster and application review,
  `canCurate` delegates article mark/recommend flags plus
  digest/archive/recommended/pinned curation,
  `canAnnounce` delegates announcement entries, `canModeratePosts` delegates
  redact/restore, `canModerateThreads` delegates lock/move and article no-reply
  flags, `canManagePolls` delegates poll result publishing, and
  `canSetBoardSettings` delegates board policy and member-requirement edits.
  Delegated member managers cannot grant or revoke those delegation flags,
  manage board moderators, manage members who already hold delegated board
  permissions, blacklist applications, or review their own application.
- Board membership requirements expose KBS-style admission knobs. `maxMembers`,
  `minLoginCount`, `minPostCount`, `minTrustLevel`, `minScore`,
  `minBoardPostCount`, `minBoardOriginalPostCount`, `minBoardDigestCount`, and
  `minBoardMarkCount` are enforced on application and approval. Score and board
  marks are reaction counts received on authored posts. `approvalMode` is
  `"manual"` or `"auto"`; auto mode immediately approves an eligible
  application.
- Board membership applications are durable projection rows. Users can apply
  with a note, board moderators can approve/reject/blacklist pending
  applications, delegated member managers can approve/reject pending
  applications, approval creates the member row, blacklist removes any member
  row and blocks later self-application, and members can leave themselves.
  Public-board approvals also lazily create the
  `Registry` system board and a deterministic sanitized generated thread/post;
  public-board rejections and blacklists do the same in `reject_registry`.
  Generated bodies include application id, status, board, applicant, and
  reviewer metadata, but omit application notes, review notes, and member
  titles. Member-read board applications remain only in the private manager
  queue.
- Digest curation stores durable board-local archive entries. `kind` is one of
  `"digest"`, `"archive"`, `"recommended"`, `"pinned"`, or `"announcement"`;
  `path` is a lightweight archive menu path. Re-curating the same target with
  the same `kind` and `path` updates its title/note instead of duplicating it.
  Board-local digest reads expose entries directly, `digest/tree` derives a
  navigable path tree from those paths, and site-wide digest/announcement reads
  include only boards the viewer may read. `digest/search` searches curated
  entry title, path, note, author, target thread title, and target post body
  across readable boards, with optional board/kind/path filters.
  `GET /api/v1/digest/{id}/download` returns a plain-text export of a curated
  post or thread, and `sendDigestEntryMail` sends that export through durable
  private mail. `updateDigestEntry` renames entries, changes notes, and moves
  them between archive paths; `setDigestEntryBody` stores or resets an edited
  archive article body. `createDigestDirectory` creates explicit empty archive
  submenus that appear in `digest/tree` with `explicit: true`. Path-level
  `moveDigestPath`, `copyDigestPath`, and `deleteDigestPath` operate on all
  entries and explicit directories under a lightweight archive path subtree;
  omitted `kind` defaults to `"archive"` for those path operations. Search,
  download, and mail export prefer the edited archive body when present.
  Read/export paths enforce the owning board's read policy, while edit/remove
  paths require the same curator or announcement permission used for curation.
  Public-board `announcement` curation also lazily creates the `0announce`
  system board and a deterministic generated thread/post for the announcement;
  public-board `recommended` curation does the same in the KBS-style
  `Recommend` system board. Member-read announcements and recommendations
  remain digest-only so private content is not mirrored into public system
  boards.
- `publishStatsSnapshot` is admin-only. It creates the `BBSLists` system board
  if needed and writes a deterministic daily generated thread/post containing
  community counters, cumulative online/stay time, max-online history, recent
  daily stat-history rows, plus public-safe board, thread, latest-reply, user,
  blessing, and archive rankings. It also writes a deterministic daily
  `BBSLists` login-count history thread/post modeled on KBS `countlogins`
  autoposts.
  `date` is optional `YYYY-MM-DD` and defaults to the current UTC day; publishing
  the same date again returns the existing generated thread instead of
  duplicating it.
- Presence changes and stats snapshots upsert `community_stat_history` rows.
  `GET /api/v1/stats/community` exposes current authenticated `onlineUsers`,
  anonymous web `onlineGuests`, historical `maxOnlineUsers`, `maxOnlineAt`,
  `maxOnlineGuests`, `maxOnlineGuestsAt`, and cumulative
  `totalLogins` and `totalOnlineSeconds`; `GET
  /api/v1/stats/community/history` returns daily stat-log rows ordered newest
  first with derived `deltaUsers`, `deltaBoards`, `deltaThreads`, `deltaPosts`,
  `deltaReactions`, `deltaMail`, `deltaDirectMessages`, `deltaLogins`,
  `deltaOnlineSeconds`, and `deltaGuests` fields compared to the next older
  fetched row.
- Presence updates accrue per-user `totalOnlineSeconds` while the previous
  session status was visibly online. A single update contributes at most five
  minutes so stale sessions do not create inflated stay-time totals.
- `POST /api/v1/presence/guest` is public and records anonymous web guest
  sessions by opaque `sessionId`, `status`, optional `location`, and request
  host. The web SPA pings it while unauthenticated, and guest sessions age out
  of current counters after the same five-minute freshness window used for
  authenticated online users.
- The `budgied` server also runs an automatic stat publisher by default
  (`-auto-stats=true`). On startup and hourly thereafter it ensures the current
  UTC day has the same deterministic `BBSLists` snapshot, authored by the
  system account label. Operators can disable this with `-auto-stats=false`.
- `publishSystemNotice` is admin-only. It publishes a public operator notice to
  a lazily-created KBS-style notice board: `notepad` by default, or
  `GiveupNotice` / `bbsnet` when the payload selects those boards. These boards
  remain directly readable like normal boards but are excluded from community
  counters and organic ranking lists.
- `publishPollResult` can be run by the poll/thread author, board moderators,
  delegated board members with `canManagePolls`, or site moderators/admins. It
  lazily creates the KBS-style `vote` system board and a deterministic public
  poll-result thread/post. Polls from member-read boards are not mirrored into
  `vote`; their live results remain on the source thread only.
- Board-scoped `sanctionUser` posting mutes/bans on public boards lazily create
  sanitized KBS-style `denypost` records, and `clearUserSanction` creates
  matching `undenypost` restoration records. Global sanctions and member-read
  board sanctions remain private account/moderation state and are not mirrored
  into public system boards.

### Private communication

```
mailPostAuthor { post*, subject?, body*, saveSent? }         -> mail.sent
sendMail       { to[], toGroups[], toFriends, toAll, subject, body*, replyTo, saveSent, attachments[] } -> mail.sent
setMailGroup   { group?, name*, members[] }                    -> ack only
deleteMailGroup { group* }                                     -> ack only
attachMail     { id?, mail*, filename*, contentType?, sizeBytes? } -> mail.attachment_added
updateMail     { mail*, mailbox?, read?, kept? }               -> ack only
deleteMail     { mail* }                                       -> ack only
sendDirectMessage { to*, body* }                               -> direct_message.sent
setDirectMessageSettings { policy* }                           -> ack only
markDirectMessageRead { message* }                             -> ack only
deleteDirectMessage { message* }                               -> ack only
```
- Private mail is durable and article-like. A single `mail_messages` body is
  exposed as per-user `mail_copies`, allowing inbox, sent, trash, kept/custom
  mailboxes, read state, and reply threading without duplicating message body
  storage.
- `mailPostAuthor` is the KBS-style article-reader action for mailing the
  source article author through internal private mail. It requires read access
  to the source board, rejects redacted and anonymous posts, adds board/thread/
  post context plus a short article excerpt to the body, and then follows the
  same quota, ignore, sent-copy, and `mail.sent` path as `sendMail`.
- `to` accepts usernames or user ids. Entries prefixed with `group:` resolve
  against the actor's mail groups. `toGroups` accepts group ids or names, and
  the built-in dynamic group `friends` expands to the actor's current friend
  list. `toFriends` expands to the same friend list. Recipients are
  deduplicated. `toAll` is admin-only sysop mail-all; it addresses every other
  user, bypasses personal ignore rows, and mirrors the broadcast into a
  restricted `sysmail` generated board/thread/post for operator review.
  `saveSent` defaults to true. `replyTo` must be a mail item visible to the
  actor.
- Mail groups are user-owned mailing lists. `members` accepts usernames or user
  ids and is stored with stable ordering.
- `attachments` on `sendMail` stores URL/metadata attachments. Binary mail file
  uploads use `attachMail` via `POST /api/v1/mail/{id}/attachments` and are
  sender-only; any user with a visible mail copy can download stored files from
  `GET /api/v1/mail/attachments/{id}`.
- Mail quota usage is exposed by `GET /api/v1/mail/usage`. Quota is calculated
  from non-trash mail copies, including message subject/body and attachment
  sizes. Sending mail, adding binary mail attachments, and restoring mail out of
  trash are rejected when they would exceed the affected user's quota.
- `mailbox` may be a built-in mailbox (`inbox`, `sent`, `keep`, `trash`) or a
  lightweight custom mailbox slug.
- Direct messages are short conversation messages between two users. They are
  account-scoped, support unread counts, read marking, per-user deletion, and
  conversation reads distinct from private mail and notification feeds.
- `setDirectMessageSettings.policy` is `"all"`, `"friends"`, or `"none"`.
  Friends-only delivery requires the recipient to have the sender on their own
  friend list.

### Social graph

```
setUserRelationship { user*, kind*, active*, note }           -> ack only
setLoginWatch       { user*, active* }                        -> ack only
blessUser           { user*, message? }                       -> user.blessed
```
- `kind` is `"friend"` or `"ignore"`. Friends are user-owned following rows;
  fans are the reverse lookup of friend rows. Mutual friendship is derived when
  both users have friend rows for each other.
- Friend rows can carry a short private note. Ignore rows are also user-owned.
- Ignore rows block private mail, short direct messages, and public blessings
  from the ignored user to the actor who set the ignore.
- `setLoginWatch` is a one-shot wait-for-login request for an existing friend.
  When that friend next publishes online presence, or immediately if they are
  already online, the watcher receives a `login` notification and the watch is
  cleared. Ignore rows suppress delivery.
- `blessUser` records a public KBS-style blessing ritual, updates
  `GET /api/v1/rankings/blessings`, and lazily creates a generated `Blessing`
  board thread/post. Self-blessings are rejected.
- `GET /api/v1/social/online-friends` returns friend rows whose latest persisted
  presence is recent and not hidden (`"offline"`, `"invisible"`, or `"cloak"`).
- `GET /api/v1/presence/online` returns recent online users with `status`,
  `sessionId`, `mode`, `boardId`, `boardName`, `threadId`, `locationLabel`,
  `fromHost`, `idleSeconds`, and relationship flags. A user may appear more
  than once when they have multiple visible sessions. `GET
  /api/v1/boards/{id}/online` scopes that list to one board and requires the
  same read permission as the board itself. Global reads mask board/thread
  details for member-read boards the viewer cannot enter.
- The web chat room uses the same online-friends read as its in-room friend
  shortcut list, with direct-message jumps for reachable friends.

### Personal preferences

```
setBoardFavorite { board*, favorite*, folderId?, position? }  -> ack only
createFavoriteFolder { name*, parentId?, position? }          -> ack id=folder
updateFavoriteFolder { folder*, name?, parentId?, position? } -> ack only
deleteFavoriteFolder { folder* }                              -> ack only
moveBoardFavorite { board*, folderId?, position? }            -> ack only
importFavoriteTree { folders[], boards[], replace? }          -> ack only
markBoardRead    { board* }                                  -> ack only
restoreBoardRead { board* }                                  -> ack only
markFavoriteFolderRead { folder? }                           -> ack only
restoreFavoriteFolderRead { folder? }                        -> ack only
markThreadRead   { thread* }                                 -> ack only
restoreThreadRead { thread* }                                -> ack only
markPostRead     { post* }                                   -> ack only
```
- Stores a user's favorite-board collection. Empty `folderId` / `parentId`
  means the root favorite list.
- Favorite folders can be nested, renamed, deleted, and manually ordered.
  Deleting a folder moves its boards and child folders up one level.
- Favorite boards can be moved between folders and manually ordered.
- `GET /api/v1/boards/favorites/export` returns the portable favorite tree.
  `POST /api/v1/boards/favorites/import` accepts the same JSON shape and
  defaults to replacing the caller's tree; `"replace": false` merges instead.
- Per-board read markers support unread-board lists and restoration after an
  accidental mark-read action.
- Favorite-folder read marker commands mark/restore every favorite board in the
  selected folder and descendant folders; an empty folder targets all favorites.
- Per-thread read markers support first-unread navigation inside boards. Board
  markers are the baseline; thread markers can clear or restore a single thread.
- Marking a post read advances the owning thread's marker through that article,
  enabling article-level "mark to here" reading.
- `GET /api/v1/boards/{id}/threads?unread=1` returns only thread summaries
  with unread posts, enabling cross-thread unread traversal inside a board.
- `GET /api/v1/threads/unread` returns a site-wide unread thread queue across
  boards the viewer can read. `favorites=1` restricts it to favorite boards;
  `folder` restricts it to boards in that favorite folder and its descendants.
- `GET /api/v1/authors/{name}/posts` returns readable posts by one author
  across boards, including board/thread context for same-author reading jumps.
  Member-read boards are included only when the viewer can read them. The public
  `GET /api/v1/users/{name}/posts` view is restricted to public-board posts.
- Public `GET /api/v1/users/{name}` profile reads include `title`, `signature`,
  `plan`, and `homepage`. Authenticated `PATCH /api/v1/users/me` updates
  `displayName`, `title`, `bio`, `avatar`, `signature`, `plan`, and `homepage`.
  Authenticated `PATCH /api/v1/users/me/password` changes the caller's password
  after checking `currentPassword`.
- `POST /api/v1/auth/password-recovery` accepts account, real-name, email, and
  note evidence without revealing whether the account exists. Admins review
  pending recovery requests with `GET /api/v1/admin/password-recovery` and
  `POST /api/v1/admin/password-recovery/{id}/review`; a `reset` decision sets
  the new password, while `rejected` records the review without changing login
  credentials.
- `GET/PATCH /api/v1/users/me/private-profile` reads and updates the caller's
  private KBS-style real/contact profile fields. Public profile reads never
  include these fields. Admins may inspect another account's private contact
  record with `GET /api/v1/users/{name}/private-profile`.
- `GET /api/v1/users/{name}/files` and `GET /api/v1/users/{name}/files/{file}`
  expose only public named personal files. Authenticated
  `GET/PUT/PATCH/DELETE /api/v1/users/me/files...` lets the caller manage up to
  16 public or private named text files.
- `GET /api/v1/users/me/signatures` returns the caller's saved signature bank,
  fixed/random selection settings, and max count. Saved-signature writes create,
  update, delete, and select the current signature. The legacy profile
  `signature` field remains the public/current signature for compatibility.
  `POST /api/v1/users/me/signatures/recount` returns total/active signature
  counts, clears stale selected-signature settings, and refreshes the public
  current-signature preview.
- `GET /api/v1/users/me/login-acl` returns the caller's login-host allow-list,
  whether it is enabled, the current request host, and whether that host would
  be allowed. Rules accept exact IPs, CIDR ranges, and simple wildcard patterns
  such as `192.168.*`. When enabled, password login fails unless the request
  host matches an active rule. Existing authenticated sessions are not
  retroactively revoked by this ACL.
- `POST /api/v1/auth/register` creates an immediate account by default and
  lazily creates a sanitized `newcomers` system-board article with
  deterministic thread/post IDs. When admin registration approval is enabled,
  registration returns `202` with `status: "pending"`, reserves the name, and
  does not issue a token. Pending/rejected accounts cannot authenticate.
  Admins manage the queue with `GET/PATCH /api/v1/admin/registration-settings`,
  `GET /api/v1/admin/registrations`, and
  `POST /api/v1/admin/registrations/{name}/review`; approval creates the normal
  `newcomers` record, rejection keeps the account locked out.
- `POST /api/v1/admin/users/{name}/transfer-id` renames an account login ID
  while preserving the stable internal user ID. Authored thread/post display
  names and search author fields tied to that user ID are updated to the new
  name.
- `DELETE /api/v1/admin/users/{name}` hard-deletes an account row and its
  private/user-owned state. Public threads/posts remain readable under a
  `[deleted]` author tombstone, old tokens stop working, and self-deletion plus
  last-admin deletion are rejected.
- Authenticated `POST /api/v1/users/me/deactivate` checks the caller's
  password, marks the account deactivated, rejects future login and old-token
  requests, and lazily creates a sanitized `Goodbye` system-board article.
  Private deactivation notes are stored on the user row but omitted from the
  public generated article.
- `GET /api/v1/posts/{id}/reply-tree` returns the root post and its descendant
  replies with `replyDepth`, enabling focused reply-tree traversal inside a
  thread. It is guarded by the owning thread's board-read policy.

### Ephemeral & session

```
sendChatLine   { room*, text* }                             -> chat.line   (ephemeral)
setPresence    { status*, sessionId?, mode?, board?, thread?,
                 location?, fromHost? }                     -> presence.update (ephemeral)
subscribe      { scopes* }                                  -> ack only
unsubscribe    { scopes* }                                  -> ack only
```
- `status`: `"active"` | `"idle"` | `"typing"` | `"offline"` |
  `"invisible"` | `"cloak"` | location hints like `"reading:thread:88"` or
  `"reading:general"`. Legacy location hints are parsed into
  `mode`/`board`/`thread` when possible. The latest presence is persisted for
  online-user reads while `presence.update` remains an ephemeral live event.
  `sessionId` defaults to `"default"` and lets multiple active terminals publish
  independent presence rows for the same user.
  `"invisible"` persists a hidden presence state, clears visible location
  details, is excluded from online lists, and does not satisfy login watches
  until the user publishes visible presence again. `"cloak"` is accepted only
  from moderators/admins, is hidden from ordinary online lists and public online
  counts, remains visible to moderators/admins in global and board-scoped online
  reads, and also does not satisfy login watches until visible presence is
  published again.

---

## 11. Event catalog

### Durable (carry `seq`, persist permanently, replayable, federatable)

```
board.created   { id, name, description, parentId, position, by, ts }
thread.new      { id, board, author, title, ts }
post.appended   { id, thread, author, body, signature, contentType, replyTo, tex, mailBack, sourcePost, sourceThread, sourceBoard, sourceAuthor, sourceAuthorId, sourceTitle, ts }
post.edited     { id, thread, newBody, version, ts }
post.flags_set  { id, thread, marked, recommended, noReply, tex, mailBack, by, ts }
post.redacted   { id, thread, by, reason, ts }        // body NOT included
post.restored   { id, thread, by, ts }
thread.title_set { thread, title, by, ts }
thread.locked   { thread, locked, by, ts }
thread.moved    { thread, fromBoard, toBoard, by, ts }
user.sanctioned { user, kind, scope, durationSec, by, reason, ts }
user.sanction_cleared { user, kind, scope, by, reason, ts }
content_filter.set { id, pattern, scope, active, by, ts }
role.granted    { user, role, by, ts }
role.revoked    { user, role, by, ts }
mail.sent       { id, fromUserId, from, toUserIds, to, subject, body, parentId, saveSent, attachments, ts }
mail.attachment_added { id, mail, filename, contentType, sizeBytes, authorId, author, ts }
direct_message.sent { id, conversationId, fromUserId, from, toUserId, to, body, ts }
```

### Ephemeral (carry `eseq` or nothing, best-effort, prunable, never federated)

```
chat.line       { id, room, user, text, ts }
presence.update { user, userId, sessionId, status, mode, board, thread, location, fromHost, ts }
user.joined     { user, ts }
user.left       { user, ts }
```

**Invariant:** a `post.redacted` or hard-delete (Decision 4) never re-broadcasts removed content. Redaction hides via projection; the original `post.appended` stays in the log for audit but is not re-emitted.

---

## 12. Versioning & capability negotiation

- **HTTP path** is versioned in the URL: `/api/v1/`.
- **Protocol version** is announced in `welcome.protocol` and asserted by clients; mismatch on a major version is a hard error.
- **Capabilities** (`welcome.capabilities`) let a client discover optional features (e.g. `ansi-art`, `sse`) rather than assuming them. New optional events/commands are additive; clients ignore event types they don't recognize (forward-compatible).
- **Wire format** negotiable via `welcome.wireFormats`; JSON is mandatory baseline.

---

## 13. Notes for implementation (Claude Code hand-off)

Build order against this spec mirrors the first milestone in *General Technical Directions*:

1. **Envelope + command/event types** (§3, §10, §11) as the typed schema. This is the foundation everything imports.
2. **Single-writer core**: command handler (validate → assign `seq` → append → update projections) and the in-process pub/sub with cursor-based subscribers (Decision 2). SQLite `events` table + projection tables.
3. **HTTP read/write surface first** (§9) — it's the simplest transport to test end-to-end and exercises the whole core with nothing but `curl`. Poll tier validates cursors; long-poll validates the held-request path.
4. **WebSocket** (§3–§4) as the second transport against the now-proven core — a renderer/transport, not new logic.
5. **SSH server-hosted TUI** as an in-process client of the same bus.
6. SSE can come whenever a browser wants push without WS; it reuses the events query with `Last-Event-ID`.

Two properties to assert in tests early, because they're the spine:
- **Transport equivalence:** the same `cid`/command yields the same `ack` and the same resulting event regardless of transport.
- **Cursor monotonicity & replay fidelity:** replaying `after=N` over HTTP and tailing from `N` over WS deliver an identical event sequence.

Everything heavier — moderation UI, search, sub-forums, federation — layers on without touching this spec. Keep the durable event shapes stable; evolve projections freely.
