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
POST /api/v1/auth/refresh      { "token": "..." }                    -> { "token": "...", "expires": ... }
```

Roadmap (does not change the protocol): passkeys / OAuth as additional credential types that mint the same token. SSH keys may also be registered to an account so the same identity spans SSH and web.

---

## 6. Subscriptions & scoping

A client rarely wants every global event. It subscribes to **scopes**; the event stream is filtered to subscribed scopes (plus events addressed to the account, e.g. a sanction against you, which always deliver).

Scope grammar: `type:id`.

| Scope | Delivers |
|---|---|
| `board:<id>` | `thread.new`, `thread.moved`, `thread.locked` in that board |
| `thread:<id>` | `post.appended`, `post.edited`, `post.redacted`, `post.restored` in that thread |
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
GET /api/v1/boards/{id}/threads?sort=recent&limit=&before=
GET /api/v1/threads/{id}                 -> thread + posts (current, redactions applied)
GET /api/v1/threads/{id}/posts?after=&limit=
GET /api/v1/users/{id}/profile
GET /api/v1/chat/{room}/recent?limit=    -> bounded ephemeral history (ring buffer)
```
These read projection tables directly (see Decision 2) and are CDN-cacheable. Moderator views (`?include=redacted`) require role and are never cached.

### Writes — uniform command endpoint

```
POST /api/v1/commands
Body: { "cid": "...", "command": "appendPost", "payload": { } }
Returns: the ack envelope.
```

### Writes — RESTful aliases (sugar over the same commands)

| Method + path | Dispatches command |
|---|---|
| `POST /api/v1/boards/{id}/threads` | `createThread` |
| `POST /api/v1/threads/{id}/posts` | `appendPost` |
| `PATCH /api/v1/posts/{id}` | `editPost` |
| `DELETE /api/v1/posts/{id}` | `redactPost` |
| `POST /api/v1/posts/{id}/restore` | `restorePost` |
| `POST /api/v1/threads/{id}/lock` | `lockThread` |
| `POST /api/v1/chat/{room}/lines` | `sendChatLine` |
| `POST /api/v1/users/{id}/sanctions` | `sanctionUser` |

Every alias accepts the same `cid` (as header `X-Command-Id`) for idempotency and returns the same `ack` body.

---

## 10. Command catalog

Payloads are sketches; required fields marked `*`. All commands accept a `cid`. All return an `ack`.

### Content & threads

```
createThread   { board*, title*, body*, contentType }      -> thread.new (+ first post.appended)
appendPost     { thread*, body*, replyTo, contentType }     -> post.appended
editPost       { post*, body* }                             -> post.edited
redactPost     { post*, reason }                            -> post.redacted
restorePost    { post* }                                    -> post.restored
```
- `contentType`: `"markup"` (default — the medium-neutral subset, Decision 3) or `"ansi-art"` (raw CP437+ANSI payload, fixed-geometry).
- `replyTo`: optional parent post id for shallow threading. Depth is capped server-side (open question #1 in Decisions doc); over-deep replies are flattened, not rejected.

### Moderation & structure

```
lockThread     { thread*, locked* }                         -> thread.locked
moveThread     { thread*, toBoard* }                         -> thread.moved
sanctionUser   { user*, kind*, scope, durationSec, reason }  -> user.sanctioned
grantRole      { user*, role* }                              -> role.granted
revokeRole     { user*, role* }                              -> role.revoked
```
- `kind` (sanction): `"mute"` | `"ban"`. `scope`: board id or `"global"`.
- All require role; the command handler checks the role projection before accepting.

### Ephemeral & session

```
sendChatLine   { room*, text* }                             -> chat.line   (ephemeral)
setPresence    { status* }                                  -> presence.update (ephemeral)
subscribe      { scopes* }                                  -> ack only
unsubscribe    { scopes* }                                  -> ack only
```
- `status`: `"active"` | `"idle"` | `"typing"` | location hints like `"reading:thread:88"`.

---

## 11. Event catalog

### Durable (carry `seq`, persist permanently, replayable, federatable)

```
thread.new      { id, board, author, title, ts }
post.appended   { id, thread, author, body, contentType, replyTo, ts }
post.edited     { id, thread, newBody, version, ts }
post.redacted   { id, thread, by, reason, ts }        // body NOT included
post.restored   { id, thread, by, ts }
thread.locked   { thread, locked, by, ts }
thread.moved    { thread, fromBoard, toBoard, by, ts }
user.sanctioned { user, kind, scope, durationSec, by, reason, ts }
role.granted    { user, role, by, ts }
role.revoked    { user, role, by, ts }
```

### Ephemeral (carry `eseq` or nothing, best-effort, prunable, never federated)

```
chat.line       { id, room, user, text, ts }
presence.update { user, status, ts }
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
