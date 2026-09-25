# High-Level Technical Decisions

*Companion to* General Technical Directions. *Where that document says "the board is an append-only event log," this one decides what the events are, how the core processes them concurrently, what a post actually contains, and how moderation works on top of immutable data.*

These are decisions with reasoning and tradeoffs, not final API specs. They're written to be argued with.

---

## Decision 1 — The event and command vocabulary

### The command / event split (CQRS-lite)

Clients **never write to the log directly.** They send **commands**; the server emits **events**. This asymmetry is the spine of the whole authority model, so it's worth stating precisely:

- A **command** is a request that can be rejected. `appendPost` may fail on permissions, rate limits, spam heuristics, a locked thread, a banned user. Commands are imperative and addressed to the server.
- An **event** is a fact that already happened. `post.appended` is not a request — it's the server announcing an immutable entry now exists in the log. Events are past-tense and broadcast to clients.

Why not a symmetric "messages" model where clients write what they read? Because the moment a client can append to the log directly "to save a round trip," every authority check (permissions, rate limiting, moderation, validation) either gets duplicated per-client or gets bypassed. The asymmetry guarantees **one enforcement path, two front doors**: an `appendPost` command is validated identically whether it arrived over SSH or WebSocket.

### Starting vocabulary

Deliberately small. Resist the urge to add events until a client genuinely needs to render the distinction.

**Events** (past-tense facts, appended to the log, broadcast):

| Event | Payload (sketch) | Notes |
|---|---|---|
| `thread.new` | `{id, board, author, title, seq, ts}` | A new thread exists. |
| `post.appended` | `{id, thread, author, body, replyTo?, seq, ts}` | The workhorse. `replyTo` enables shallow threading. |
| `post.edited` | `{id, thread, newBody, seq, ts}` | New version; old version stays in the log. |
| `post.redacted` | `{id, thread, by, reason?, seq, ts}` | Moderation/deletion. Body is **not** repeated. |
| `user.joined` | `{user, seq, ts}` | Presence: logged on. |
| `user.left` | `{user, seq, ts}` | Presence: logged off. |
| `chat.line` | `{user, text, room, seq, ts}` | Ephemeral live chat (see Decision 2 on persistence). |
| `presence.update` | `{user, status, seq, ts}` | Typing / idle / location-in-board. |

**Commands** (imperative, rejectable):

| Command | Becomes (on success) | Can be rejected for |
|---|---|---|
| `createThread` | `thread.new` | permissions, board locked, rate limit |
| `appendPost` | `post.appended` | permissions, thread locked, rate limit, validation |
| `editPost` | `post.edited` | not author / not mod, edit window expired |
| `redactPost` | `post.redacted` | not mod (or not author within grace window) |
| `sendChatLine` | `chat.line` | muted, rate limit, room permissions |

### Design rules for the vocabulary

- **Events are self-contained and addressable.** Every event has a stable `id` and a monotonic `seq`. Author identity is carried as a portable reference, not a local row ID. This is what keeps the door open for federation later (an event can be shipped to another instance and still make sense). Bake in DB-row assumptions here and federation becomes a rewrite.
- **`seq` is global and monotonic, `ts` is advisory.** Ordering is by `seq` (assigned by the core at append time), never by wall-clock timestamp. `ts` is for display only. This sidesteps clock-skew ordering bugs entirely.
- **Redaction events don't carry the redacted body.** `post.redacted` references the post and says it's gone; it never repeats the content. The original `post.appended` stays in the log for the audit trail, but nothing re-broadcasts the removed text.

---

## Decision 2 — The concurrency and cursor model inside the core

This is the heart of the server. The shape: **one writer, many readers, every reader holds a cursor.**

### The log is a single-writer structure

All commands funnel through one serialization point — the command handler — which is the only thing that assigns `seq` and appends. This is not a bottleneck to fear: appends are cheap, and a single writer makes `seq` trivially monotonic with no locking gymnastics or distributed-ordering problem. In Go this is naturally a single goroutine reading commands off a channel; in Elixir, a single process (e.g. a GenServer) owning the log. Concurrency lives in the *readers* and in *validation*, not in the append itself.

```
        commands (many)                         events (many)
   ┌─────────┐  ┌─────────┐                ┌─────────┐  ┌─────────┐
   │ SSH sess│  │ WS conn │  ...           │ SSH sess│  │ WS conn │
   └────┬────┘  └────┬────┘                └────▲────┘  └────▲────┘
        │            │                          │            │
        └─────┬──────┘                          └─────┬──────┘
              ▼                                       │
      ┌───────────────┐   validate + assign seq   ┌───┴────────┐
      │ Command handler├──────────► append ───────► Pub/sub bus │
      │  (single writer)│                          │ (fan-out)  │
      └───────┬─────────┘                          └────▲───────┘
              ▼                                         │
        ┌──────────┐    tail (new events)               │
        │ Event log │────────────────────────────────────┘
        │ append-only│
        └──────────┘
```

### Each connection is a subscriber with a cursor

A connection (SSH session or WS conn) is, internally, a subscriber holding a single integer: the `seq` of the last event it has seen. From that one number, everything follows:

- **Live tailing** — when the bus broadcasts event `seq=N`, subscribers at `seq=N-1` receive it and advance. This is the no-F5 push.
- **Catch-up on reconnect** — a client reconnects and sends its last cursor; the core replays `log[cursor+1 .. head]` then attaches it to the live tail. There is no separate "sync" subsystem — catch-up and live are the same path with a different starting point.
- **Unread state** — "unread" is just `head - cursor` (filtered to the boards/threads the user follows). Shared identity means the same cursor governs unread whether you're on SSH or web.
- **Backpressure** — a slow consumer (a laggy mobile connection) simply has a cursor further behind head. The core never blocks on it; it serves events as fast as the connection drains. If a consumer falls catastrophically behind, you can drop and force a fresh catch-up rather than buffering unboundedly.

### Two classes of event, two persistence policies

Not everything belongs in the durable log forever. Split by nature:

- **Durable events** — `thread.new`, `post.appended`, `post.edited`, `post.redacted`. These *are* the forum. They persist permanently, get `seq` from the main log, and are the federatable, replayable record.
- **Ephemeral events** — `chat.line`, `presence.update`, `user.joined/left`. High-frequency, low-durability. These can flow through the same pub/sub bus for live delivery but be persisted shallowly (a bounded ring buffer — "last 500 chat lines per room") or not at all. Presence is pure runtime state, reconstructed on connect, never replayed from history.

A clean way to keep this honest: durable events draw from the **monotonic global `seq`**; ephemeral events can carry their own lightweight sequence or none. Don't let presence churn inflate the durable log's sequence space.

### Projections (current state) are derived, never primary

The board's queryable state — thread lists, post counts, "is this thread locked," a user's unread count — is a **projection** built by folding the event log. Two practical notes:

- You don't replay from `seq=0` on every read. Maintain materialized projection tables (ordinary SQLite/Postgres tables) that the command handler updates as it appends, plus the ability to rebuild them from the log if they're ever corrupted or schema-changed. The log is the source of truth; the tables are a cache you can always regenerate.
- This is what makes schema evolution safe: change how a projection is computed, replay the log, get the new tables. The durable events never change shape under you.

---

## Decision 3 — The medium-neutral content format

This is the constraint the dual-client requirement forces, and it's easy to underestimate.

### The core problem

A single post must render acceptably as **ANSI in an 80-column terminal pane** *and* as **responsive HTML on a phone**. Those are not the same artifact. If we let authors write raw HTML, the terminal can't render it. If we let them write raw ANSI art, the web either shows a monospace block (ugly and non-responsive) or has to emulate a terminal (the xterm.js trap we rejected). Neither medium can be the canonical format.

### The decision: a restricted semantic markup, rendered per-medium

The post body is stored as a **constrained Markdown subset** — a semantic description of *intent*, not appearance. Each client renders that intent natively:

| Markup intent | Web renders as | Terminal renders as |
|---|---|---|
| Heading | `<h_>` with CSS | bold + underline ANSI, or a `═══` rule |
| Bold / emphasis | `<strong>` / `<em>` | ANSI bold / dim or color |
| Quote (`>`), reply context | blockquote with bar | indented + left `│` gutter, dim color |
| Code / preformatted | `<pre>` with highlighting | raw monospace block (terminal is already monospace) |
| Link | `<a>` | underlined text + footnote-style URL list |
| List | `<ul>`/`<ol>` | `•` / `1.` prefixed lines |

**Deliberately excluded from the subset:** arbitrary HTML, raw colors/fonts, absolute positioning, images-inline-as-data. Anything that presumes a rendering surface breaks medium-neutrality.

### ANSI art is a content *type*, not the post format

The retro crowd will want real ANSI/ASCII art, and that's legitimate — but it's handled as a **distinct content type** (an "art post" or an attachment), not as the body format of every post. An art post:

- carries raw CP437 + ANSI escape data as its payload;
- renders faithfully in the terminal (it's home turf);
- on web, renders via a dedicated ANSI-to-canvas/HTML renderer in a fixed-width framed viewport (explicitly *not* responsive — art has a fixed geometry, and that's fine because it's framed as "art," not "text you read on a phone").

This cleanly separates "text content that must reflow everywhere" from "fixed-geometry art that's shown as-is."

### Encoding note

Store text as **UTF-8 throughout**; convert to CP437 only at the terminal boundary when a client negotiates it. ENiGMA½'s handling of CP437 ↔ UTF-8 with wide-character support (CJK, Hangul, fullwidth forms) is the reference to study here — getting wide characters right in a fixed-column terminal layout is genuinely fiddly and worth not reinventing blind.

---

## Decision 4 — Moderation on top of an immutable log

The instinct that immutability fights moderation is backwards. Append-only is **better** than mutable rows here, and this decision explains why and how.

### Deletion is an event, not a `DELETE`

You never remove a row. To "delete" a post, the command handler appends `post.redacted {id, by, reason, seq, ts}`. Then:

- The **projection** that normal users query hides any post that has a redaction event. To a regular user, it's gone — replaced by a tombstone ("[removed by moderator]") or omitted, your choice per-board.
- The **log** retains the original `post.appended`. Moderators (and the audit view) can see what was removed, by whom, when, and why.

You get **deletion + full audit trail + reversibility** from one mechanism. Un-redacting is just another event (`post.restored`) — impossible to express cleanly if you'd actually `DELETE`d the row.

### Edits work identically — and give version history for free

`editPost` appends `post.edited` with the new body; the old `post.appended` stays. The projection shows the latest version; the log holds every version. "Show edit history" and "this post was edited" indicators are free — they're just multiple events for one post `id`. No separate revisions table, no soft-delete columns.

### What moderation actions look like as commands

All moderation is the same command/event pattern, which means it inherits the single enforcement path and the audit trail automatically:

| Mod action | Command → Event | Audit captured |
|---|---|---|
| Remove a post | `redactPost` → `post.redacted` | who, when, reason |
| Restore a post | `restorePost` → `post.restored` | who, when |
| Lock a thread | `lockThread` → `thread.locked` | who, when |
| Ban / mute a user | `sanctionUser` → `user.sanctioned` | who, scope, duration, reason |
| Move a post/thread | `moveThread` → `thread.moved` | who, from, to |

Because these are events on the same log, the moderation timeline of the entire board is queryable by replaying mod-events — a built-in transparency log, not a bolted-on feature.

### Permissions and roles

Roles (user, trusted, moderator, admin) and per-board permissions are themselves projection state, updated by `grantRole` / `revokeRole` commands. The command handler checks the current role projection before accepting any privileged command. Keep the check in **one place** (the handler) — never in a client. A terminal client showing a "delete" option to a non-mod is a UI nicety; the *authority* is the server rejecting the `redactPost` command regardless of what UI offered it.

### The GDPR / hard-delete caveat

Append-only-forever collides with "right to erasure" and genuinely illegal content (where retention itself is the liability). Plan for a **genuine hard-delete escape hatch** from day one, even if rarely used: a privileged operation that overwrites the body payload of specific durable events in place (tombstoning the content while preserving the event's existence and `seq`). This is the one sanctioned exception to immutability, it's logged as its own meta-event, and it's restricted to admins. Designing it in early is far cheaper than retrofitting erasure into a system that assumed the log was write-once.

---

## Open questions to resolve next

These are the forks that change downstream work and are worth deciding before heavy building:

1. **Threading depth** — flat-with-quotes vs. shallow (1–2 level) nesting. Affects the `replyTo` semantics and both renderers. (Recommendation: shallow, capped, for terminal-renderability.)
2. **Federation timing** — design-for-now / build-later is the assumed stance. Confirm, because it sets how strict we are about portable identity and self-contained events from the start.
3. **Ephemeral persistence depth** — how much chat history survives a restart (ring buffer size, or durable chat as a separate opt-in log).
4. **Projection store** — start single-node SQLite (recommended for v1) vs. Postgres from the outset if multi-node is a near-term goal.
5. **Auth credentials** — SSH pubkey + web password to start; passkeys/OAuth as fast-follow? Decides the account/credential schema.
