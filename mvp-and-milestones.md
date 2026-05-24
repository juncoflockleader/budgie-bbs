# BudgieBBS — MVP & Milestones

*Derived from the protocol definition, technical decisions, and general directions. This document translates the architecture into a sequenced build plan. The goal is the smallest thing that proves the thesis — then layers that make it a serious forum.*

---

## What "done" looks like at MVP

> A single `chat.line` event appears live in both a terminal and a browser with no F5, anywhere. A post written in one client is immediately visible in the other.

That moment is the proof: one event log, two first-class clients, one push model. Everything after MVP layers on without touching the spine.

The server at MVP needs to do exactly four things:
1. Accept commands, validate them, and append events to a durable log with monotonic `seq`.
2. Fan new events out to all subscribers whose cursor is behind head.
3. Replay events after a given `seq` on reconnect (catch-up = live tailing, different starting point).
4. Serve the server-hosted TUI over SSH and a thin web client over WebSocket.

---

## Milestone 0 — Spec & Schema ✓ (complete)

**Deliverable:** A typed, agreed-upon wire contract. Nothing is built against assumptions; everything is built against this document.

- Protocol Definition (`protocol-definition.md`) — envelope format, transport ladder, command/event catalog, cursor semantics, error model, REST surface, handshake lifecycle.
- High-Level Technical Decisions — event vocabulary, single-writer concurrency model, medium-neutral content format, moderation-on-immutable-log, open questions enumerated.
- Architecture: Client/Server Inversion — the shared client core principle, proxy-thin vs. client-thin distinction, the litmus test.

**Open questions to resolve before Milestone 2 begins:**
- Threading depth: flat-with-quotes or shallow 1-level nesting? *(Recommended: shallow, capped.)*
- Projection store: SQLite (recommended for v1) or Postgres?
- Auth credentials v1: SSH pubkey + password; passkeys as fast-follow?

---

## Milestone 1 — Core Server

**Goal:** A working append-only log and command/event bus, exercised entirely in-process via unit tests. No transports yet.

**Scope:**

- `events` table in SQLite: columns `seq INTEGER PRIMARY KEY`, `id TEXT`, `kind TEXT`, `payload JSON`, `ts INTEGER`. Append is a single write; `seq` is the SQLite rowid or a dedicated sequence.
- **Command handler** (single writer): receives a command struct, validates (basic: required fields present, author exists, thread not locked), assigns `seq`, appends, updates projection tables, publishes to pub/sub bus.
- **In-process pub/sub**: a map of `{scope → []subscriber}`. Each subscriber is a goroutine-safe channel holding a cursor. Fan-out broadcasts each new event to all subscribers whose subscribed scopes match.
- **Projection tables** (SQLite, derived, rebuildable):
  - `boards (id, name, description)`
  - `threads (id, board, author, title, locked, post_count, last_seq, created_ts)`
  - `posts (id, thread, author, body, content_type, reply_to, version, redacted, created_seq, updated_seq)`
  - `users (id, name, role)`
  - `cursors (user_id, seq)` — shared unread state
- **Commands implemented:** `createThread`, `appendPost`, `editPost`, `redactPost`.
- **Events emitted:** `thread.new`, `post.appended`, `post.edited`, `post.redacted`.

**Tests to assert (the spine — never regress these):**
- A command rejected for permissions never appends an event.
- After any append, replaying `events WHERE seq > N` yields exactly the same sequence as live tailing from `N`.
- Projection tables rebuilt from full log replay match the live projection tables.

**Not in scope:** any transport, any auth, any HTTP. Pure Go packages, exercised by tests.

---

## Milestone 2 — HTTP Transport (Tier 3 & 4)

**Goal:** End-to-end round-trip exercisable with `curl`. This validates the entire stack before building any client UI.

**Scope:**

- **Auth endpoints** (out-of-band of event protocol):
  - `POST /api/v1/auth/register` — create account with password.
  - `POST /api/v1/auth/login` → JWT bearer token.
  - SSH public-key registration stub (bind a pubkey to an account).
- **Event read surface** (§9 of Protocol Definition):
  - `GET /api/v1/events?after={seq}&scope={s}&limit={n}` — poll tier (returns immediately).
  - `GET /api/v1/events?after={seq}&wait=30` — long-poll tier (holds request until new events or timeout).
  - Response: `{ "events": [...], "head": N }` + `X-Log-Head` header.
- **Projection reads:**
  - `GET /api/v1/boards`
  - `GET /api/v1/boards/{id}/threads`
  - `GET /api/v1/threads/{id}/posts`
- **Command write surface:**
  - `POST /api/v1/commands` — uniform envelope.
  - RESTful aliases: `POST /api/v1/boards/{id}/threads`, `POST /api/v1/threads/{id}/posts`, `PATCH /api/v1/posts/{id}`, `DELETE /api/v1/posts/{id}`.
  - All return the `ack` envelope; `cid` deduplication within a 10-minute window.
- **Error model:** map `error.code` to HTTP status codes as defined in Protocol Definition §8.

**Validation scenario (run by hand):**
```bash
# 1. Create account + get token
curl -X POST .../auth/register -d '{"user":"alice","password":"..."}'
TOKEN=$(curl -X POST .../auth/login -d '{"user":"alice","password":"..."}' | jq -r .token)

# 2. Create a board (seeded), create a thread
curl -X POST .../boards/general/threads -H "Authorization: Bearer $TOKEN" \
  -d '{"cid":"c1","title":"Hello","body":"First post"}'

# 3. Long-poll for events
curl ".../events?after=0&scope=board:general&wait=10"

# 4. Append a reply in a second terminal; watch it appear in (3)'s response.
```

**Tests to assert:**
- Transport equivalence: posting via `POST /api/v1/commands` and via RESTful alias with the same `cid` is idempotent — second call returns the original `ack`, no duplicate event.
- Cursor monotonicity: events returned by poll and long-poll for the same `after` value are identical.

---

## Milestone 3 — WebSocket Transport

**Goal:** Full-duplex real-time connection. The protocol gains liveness. This is the web client's native transport.

**Scope:**

- WebSocket upgrade at `/api/v1/ws`.
- **Connection lifecycle** (Protocol Definition §4):
  - Server sends `welcome` control on connect: `head`, `capabilities`, `heartbeatSec`.
  - Client sends `resume` control: `after` cursor + initial `subscriptions`.
  - Server replays `[cursor+1 .. head]` for subscribed scopes, then attaches live tail.
  - Server sends `ping` every `heartbeatSec`; client replies `pong`; missed → server closes, client reconnects from cursor.
- **`backfill-required` control:** if `after < oldestRetainedSeq`, server returns this control with current `head`; client loads projections then tails from `head`.
- **Subscription management:** `subscribe` / `unsubscribe` commands over the live connection; return `ack`.
- **Commands over WS:** same command envelope as HTTP; same `ack` response; same `cid` deduplication.

**Tests to assert:**
- A command sent over WS and the same command sent over HTTP (same `cid`) produce one event, not two.
- After a disconnect and reconnect with the last cursor, replayed events match what long-poll would have returned for the same range.

---

## Milestone 4 — SSH & Server-Hosted TUI

**Goal:** `ssh budgie.local` and you're in a real-time forum. This is the "I dialed in and it works" moment.

**Scope:**

- **SSH server** using `wish` (Charm): listens on port 2222; authenticates via public key (key registered to account via `/api/v1/auth/pubkey`), falling back to password.
- **Server-hosted TUI** using `bubbletea`: runs as a real client of the in-process event bus — holds a cursor, sends commands, receives events. It is not a session engine; it is the client.
- **TUI screens (MVP):**
  - Board list — shows boards with unread counts (`head - cursor` filtered to subscribed boards).
  - Thread list — sorted by `last_seq`; unread indicator.
  - Thread reader — posts rendered as constrained Markdown → ANSI (bold, emphasis, blockquote, code, links as footnotes, lists). Live-appends new posts as events arrive — no F5.
  - Compose — inline form to `appendPost`. Sends command, waits for `ack`.
  - Live chat room — scrolling `chat.line` feed at bottom or in a split pane.
- **ANSI rendering rules** (from Decision 3):
  - `markup` posts: constrained Markdown → ANSI via per-element mapping.
  - `ansi-art` posts: raw CP437+ANSI payload rendered in a fixed-width framed viewport.
  - UTF-8 throughout; CP437 conversion only at the terminal boundary if client negotiates it.
- **Presence:** `user.joined` / `user.left` / `presence.update` displayed in a sidebar or status bar.

**Milestone 4 = MVP complete.** The thesis is proved: the same event, written from either client, appears in both with no polling.

---

## Milestone 5 — Web Client (Browser)

**Goal:** A real web UI — not a terminal emulator in a browser, but a first-class, mobile-usable forum interface.

**Scope:**

- **Stack:** TypeScript + a lightweight framework (React or Preact). Single-page app connecting over WebSocket.
- **Shared client core:** Cursor management, subscription state, command construction, event folding — extracted into a transport-agnostic module that the browser and (eventually) any native client import. This is the code expression of the inversion principle: write the client once, render twice.
- **Browser screens:**
  - Board list, thread list, thread reader, compose.
  - Live chat panel.
  - Presence / who's online.
- **Responsive layout:** mobile-first. The majority of general-purpose users arrive on a phone, not SyncTERM.
- **Transport degradation:** if WebSocket is unavailable, fall back to SSE (`GET /api/v1/events/stream`) for push, `POST /api/v1/commands` for writes. The cursor mechanism is identical; `Last-Event-ID` in SSE headers carries it automatically.
- **Auth:** login/register form; store bearer token; SSH key registration UI.

**Conformance tests:** run a shared suite against both the Go TUI and the JS browser client verifying identical behavior for the same event sequence — this is what keeps the two implementations honest across languages (per Architecture doc §4).

---

## Milestone 6 — Moderation & Roles

**Goal:** A serious forum needs moderation tooling before any real public use.

**Scope:**

- **Role system** — `user`, `trusted`, `moderator`, `admin`. Roles are projection state updated by `grantRole` / `revokeRole` commands. Enforcement lives in the command handler, never in a client.
- **Commands and events:**
  - `redactPost` / `restorePost` → `post.redacted` / `post.restored`
  - `lockThread` → `thread.locked`
  - `moveThread` → `thread.moved`
  - `sanctionUser` (mute / ban, scoped to board or global, optional duration) → `user.sanctioned`
  - `grantRole` / `revokeRole` → `role.granted` / `role.revoked`
- **Moderator UI** (both TUI and web):
  - Post menu: redact, restore, move thread.
  - Thread menu: lock/unlock, move.
  - User panel: mute/ban with reason and duration.
  - Audit log: replay mod-events to produce a transparency log of all mod actions.
- **GDPR / hard-delete escape hatch:** a privileged admin-only operation that overwrites the body payload of a specific event in place, preserving the event's existence and `seq`. Logged as its own meta-event. Restricted to admins. Design this in now — retrofitting is expensive.
- **Moderator projection view:** `GET /api/v1/threads/{id}/posts?include=redacted` (never cached) — shows tombstones with original content visible to mods.

---

## Milestone 7 — Search

**Goal:** Content is useless if it can't be found.

**Scope:**

- **Full-text search** over post bodies: SQLite FTS5 to start (zero additional infrastructure; FTS5 is a projection like any other, rebuilt from the log).
- Search surface: `GET /api/v1/search?q={query}&board={id}&limit=&before=`.
- Results ranked by relevance; filter by board, author, date range.
- TUI: `/` to open search; results navigable with arrow keys.
- Web: search bar in header; results page.
- Postgres migration path: if FTS5 saturates, drop in `tsvector` + GIN index as a projection swap with no protocol changes.

---

## Milestone 8 — Multi-Board & Sub-Forums

**Goal:** Board structure that scales to a general-purpose community.

**Scope:**

- Board hierarchy: parent boards with child sub-forums (one level; resist Reddit-style infinite nesting).
- Board creation, description, per-board permissions (restrict posting to `trusted`+, restrict thread creation to mods, etc.).
- Per-board role grants (a mod of `tech` is not a mod of `music`).
- Board-level subscriptions: a user follows boards, not the global log.
- Unread counts per board, not just global.

---

## Milestone 9 — ANSI Art & Retro Scene Features

**Goal:** Honor the retro/scene audience without compromising the general-purpose foundation.

**Scope:**

- `contentType: "ansi-art"` fully implemented end-to-end: CP437 + ANSI escape payload stored, rendered faithfully in TUI, rendered via ANSI-to-canvas renderer in a fixed-width framed viewport on web.
- CP437 ↔ UTF-8 conversion at the terminal boundary, with correct wide-character support (CJK, Hangul, fullwidth forms) — study ENiGMA½'s implementation.
- Art upload flow in both clients.
- Art gallery view: a board or thread type that presents posts as art thumbnails.
- SyncTERM / standard telnet client compatibility layer (stretch goal).

---

## Milestone 10 — Ranking & Discovery

**Goal:** Content surfaces by importance, not just time.

*Design space is mapped in `ranking-and-compaction-raw-ideas.md`. Decisions deferred until the core is live and the tradeoffs are felt with real data.*

**Scope (starting point):**

- **Multiple folds over one log:** `new` (chronological, default), `hot` (activity-weighted decay), `best` (all-time vote count). No fold is privileged; all are projections.
- **Votes:** `votePost` command → `post.voted` durable event. Vote projection: per-post signed count. Compaction of cold vote events into aggregate counts (tradeoff: loses per-user vote detail; keep in a side index if needed).
- **Compaction cadence:** windowed recompute (every N seconds) for `hot`; on-read for `best` on cold threads.
- **Sort option** on thread and post lists: `?sort=new|hot|best`.
- Defer: personalized ranking, the `rank.changed` event stream, federation vote merging (CRDT PN-counters) — these are Milestone 10+ territory.

---

## Milestone 11 — Federation (Future)

*Not in scope for v1. Architecture is designed to leave the door open; this milestone closes it.*

**Prerequisites (already baked into the spec):**
- Every durable event is self-contained: stable `id`, portable author reference, not a local row ID.
- `seq` is authoritative within an instance; cross-instance ordering will need logical or hybrid logical clocks.
- No DB-row IDs embedded in event shapes.

**Scope (sketch, not committed):**
- Instance-to-instance event subscription: a remote instance subscribes to a board's event stream, applies events to its own log, resolves conflicts via relaxed eventual consistency.
- Cross-instance identity: federated accounts linked by verifiable identifiers.
- Vote merging: PN-counter CRDTs for vote tallies across instances.
- `thread.merged` domain event for structural federation (cross-posted thread consolidation).

---

## Summary Table

| Milestone | What it proves / delivers | Key risk |
|---|---|---|
| 0 — Spec ✓ | Durable wire contract; nothing built on assumptions | Open questions block M2 if not resolved |
| 1 — Core | Command/event/log/pub-sub; all authority in one path | Single-writer must not become a bottleneck at fan-out |
| 2 — HTTP | Full round-trip testable with `curl`; cursor semantics validated | Idempotency + dedup easy to get wrong |
| 3 — WebSocket | Liveness; push without polling | Reconnect/catch-up must be identical to poll |
| 4 — SSH TUI ← **MVP** | Thesis proved: two live clients, one event stream | TUI must be a client, not a session engine |
| 5 — Web Client | First-class browser UI; mobile-usable | Two client cores drifting without conformance tests |
| 6 — Moderation | Safe for real users | GDPR escape hatch must be designed in, not bolted on |
| 7 — Search | Content findable | FTS5 may saturate; Postgres migration path is cheap if projections are clean |
| 8 — Multi-Board | General-purpose community structure | Per-board permissions complicate command handler checks |
| 9 — ANSI Art | Retro/scene audience; visual identity | Wide-character terminal rendering is genuinely fiddly |
| 10 — Ranking | Content surfaces by importance | Decay folds that read wall-clock time aren't pure; decide how strict to be |
| 11 — Federation | Multi-instance network | Cross-instance ordering; don't start here |

---

## The failure mode to avoid

> Build a gorgeous ANSI client, demo it, everyone says "wow," and it never crosses into general-purpose because the moderation, search, and mobile web work never gets done.

Sequence accordingly. The terminal client is the delight; it is not what makes this a serious forum. Milestones 6–8 (moderation, search, multi-board) are unglamorous and load-bearing. The architecture gives a clean foundation for all of it, but none of it is free.
