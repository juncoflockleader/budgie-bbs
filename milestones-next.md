# BudgieBBS — Next Milestones (Post-MVP)

> Research basis: Discourse feature set + LBBS (github.com/InterLinked1/lbbs) review.
> This document extends the completed MVP (M1–M7) with the next wave of features.
> Keep scalability in mind at every step — this is not a single-server application.

---

## What we shipped in MVP (M1–M7)

- Append-only event log + CQRS command handler
- HTTP REST + WebSocket (real-time) + SSE transports
- SSH TUI in Bubbletea (full terminal client)
- Boards, threads (1-level replies), posts with markup
- FTS5 full-text search
- JWT + SSH pubkey auth; user roles (user / trusted / moderator / admin)
- Mute / ban sanctions with optional duration and board scope
- Thread locking / moving, post redact / restore / purge (GDPR)
- Live chat (lobby), presence updates
- React + TypeScript web SPA

---

## M8 — Notifications & Watched Threads

**Why**: The single biggest forum feature gap vs. Discourse. Users churn when
they miss replies. Without a persistent notification inbox there is no reason
to keep coming back.

### Features
- **Per-thread watch/mute preferences**: stored in a `thread_prefs` table
  (user_id, thread_id, level: "watch" | "normal" | "mute"). Default = normal.
- **Mention detection**: when a post body contains `@username`, emit a
  `EvtMentioned` event scoped to `account:<user_id>`.
- **Notification inbox**: `notifications` table (id, user_id, kind, ref_thread,
  ref_post, read, ts). Kinds: mention, reply_to_yours, watched_thread_reply.
- **HTTP endpoint**: `GET /api/v1/notifications` (paginated, filter by read).
  `POST /api/v1/notifications/{id}/read` to mark read.
  `POST /api/v1/notifications/read-all`.
- **Web**: bell icon in nav with unread count badge; notification panel dropdown.
- **TUI**: `N` key in board list opens notification list; new-notification dot
  in header bar.
- **Email digest** (optional, later): configuration for SMTP server; daily/weekly
  digest batching. Flag as `M8b` — don't block M8a on it.

### Scalability note
`EvtMentioned` and watch-fanout should be computed by a background projection
worker, not inline in the command handler. This keeps command latency flat even
when a post mentions many users or a popular thread has many watchers.

---

## M9 — Trust Levels (Activity-Earned Roles)

**Why**: Discourse proves that graduated trust dramatically reduces spam and
abuse while rewarding real contributors — without constant sysop intervention.
Flat role assignment (current) doesn't scale to a busy board.

### Trust level ladder

| Level | Name     | Earned by                                  | Unlocks                            |
|-------|----------|--------------------------------------------|------------------------------------|
| TL0   | Newcomer | (default on register)                      | Read, post with rate limits        |
| TL1   | Basic    | Read ≥10 posts, reply ≥1                   | Inline images, longer posts        |
| TL2   | Member   | ≥30 days, ≥100 posts read, ≥15 replies     | Create polls, DMs, wiki edits      |
| TL3   | Regular  | ≥100 days, ≥500 posts read, ≥50 replies    | Re-categorize threads, silence TL0 |
| TL4   | Leader   | Manual promotion only (= current "trusted") | Mod-lite: close own threads        |

### Implementation
- `user_activity` table tracks: posts_read, posts_created, days_visited, etc.
  Updated by a projection worker reading events (not in command path).
- Daily cron (or on-login check) recomputes TL and updates `users.trust_level`.
- Command handler checks TL for feature gates (poll creation, DM, etc.).
- New event: `EvtTrustLevelChanged`.

---

## M10 — Reactions / Likes

**Why**: The lowest-noise signal of appreciation. Reduces "me too!" reply spam.
Engagement metric for ranking later (see M13).

### Features
- Schema: `post_reactions (post_id, user_id, emoji TEXT, ts)`.
  Start with a single `:heart:` / thumbs-up; expand to a small fixed set later.
- `CmdReactPost {post, emoji}` / `CmdUnreactPost {post, emoji}`.
- Events: `EvtPostReacted`, `EvtPostUnreacted` scoped to `thread:<id>`.
- Projection: `posts.reaction_count INTEGER DEFAULT 0`.
- Web: reaction bar below each post; animated on click.
- TUI: `r` key cycles reaction on selected post (single heart for simplicity).
- Aggregate reaction counts exposed in `listPosts` response.

---

## M11 — Polls in Posts

**Why**: Highest-traffic Discourse feature. Community decisions, preference
gauges, game votes. The markup-based approach fits our existing content model.

### Markup syntax
```
[poll]
Option A
Option B
Option C
[/poll]
```

### Implementation
- `polls` table: `(id, post_id, question, expires_at)`.
- `poll_options (id, poll_id, text, vote_count)`.
- `poll_votes (poll_id, option_id, user_id, ts)` — one vote per user per poll.
- Parser runs at post-insert time; strip `[poll]` from body, create poll rows.
- `CmdVotePoll {poll_id, option_id}` command + `EvtPollVoted` event.
- Web: inline poll UI rendered in place of `[poll]` markup.
- TUI: separate `P` key opens poll view for current post.
- Votes are anonymous by default; show totals after voting.

---

## M12 — Door Games Framework (SSH TUI)

**Why**: The single biggest differentiator from web forums. Classic BBS door
games (Trade Wars, Legend of the Red Dragon, etc.) run over a well-defined
stdio/pty interface. Adding a door hook makes BudgieBBS unmistakably a BBS.

### Design
- Sysop configures doors in `doors.toml`:
  ```toml
  [[door]]
  name = "Trade Wars 2002"
  cmd  = "/usr/local/games/tw2002/tw2002"
  args = ["-d", "/var/data/tw2002"]
  ```
- TUI menu: `/doors` command or `d` key in board list shows available doors.
- Selecting a door creates a PTY, execs the configured binary, and pipes
  the SSH session's pty into it (`wish`/`pty` in Go).
- Door session runs as a child process; sysop can interrupt via node-spy (M14).
- ANSI passthrough — the SSH client gets raw ANSI from the door program.

### Safety
- Doors run as a separate unprivileged user (configurable).
- Optional daily time limit per door per user.

---

## M13 — ANSI Terminal Autodetection + Baud Emulation

**Why**: Makes the SSH experience feel like a real BBS. Zero-cost differentiator.

### Features
- On SSH connect, query terminal capabilities: check `TERM` env and `$COLORTERM`.
- Detect ANSI color support; degrade gracefully for `vt100`, `dumb`, etc.
- Opt-in "baud emulation" mode (e.g., 2400 baud) that paces output with a
  configurable delay between characters — pure nostalgia with no server cost.
- Welcome screen ANSI art that tests ANSI support and shows system stats.
- `tui/ansi.go`: helper for baud-paced writer wrapping `ssh.Session`.

---

## M14 — Sysop Console / Node Spy

**Why**: Real-time session visibility is a BBS staple. Admins need to see active
connections and have emergency controls.

### Features
- `nodes` table (or in-memory map): active SSH sessions with username, IP,
  current page, login time.
- TUI sysop panel (`S` from board list for admins): live node list (updates via
  bus events).
- `EvtNodeConnected`, `EvtNodeDisconnected` ephemeral events.
- Sysop can send a "message" to a node (injected into that session's status bar).
- Sysop can kick a node (graceful close of the SSH session).
- Web equivalent: admin panel at `/admin/nodes`.

---

## M15 — Inter-BBS Networking (FidoNet-lite / NNTP)

**Why**: BBS culture was always networked. A BudgieBBS that can share boards with
other BudgieBBS instances unlocks federated community growth.

### Phase A — NNTP gateway (read/post via newsreader)
- Map BudgieBBS boards to NNTP newsgroups under a configured domain.
- `net_nntp.go`: implement NNTP server (RFC 3977) — enough for `NEWNEWS`,
  `ARTICLE`, `POST`, `LIST`, `GROUP`.
- Wire NNTP posts into the normal command pipeline as `CmdAppendPost` from a
  synthetic `nntp_bot` actor.
- Lets users read and post from Thunderbird, tin, slrn, etc.

### Phase B — BudgieBBS federation
- Define a simple JSON-over-HTTPS federation protocol: one BBS pulls event
  streams from another for specific boards (like PubSub over HTTP).
- Federated posts are tagged `source: "bbs.example.com"`.
- Read-only initially; writable federation requires identity verification.

---

## M16 — Gopher Server

**Why**: Gopher is resurging in hobbyist circles. A Gopher frontend to boards and
files costs ~2 days to implement and appeals to the retro-net crowd.

- `net_gopher.go`: RFC 1436 Gopher server (port 70 by default).
- Board list → Gopher menu. Thread list → Gopher menu. Posts → Gopher text file.
- Read-only. No auth (Gopher has no auth concept).
- New `-gopher` flag in `budgied`.

---

## M17 — User Profiles & Public Stats

**Why**: Users expect a public identity page. Drives community pride and helps
mods evaluate users.

### Fields
- Display name, bio (markup), avatar (emoji or single-character ASCII art).
- Join date, last seen, post count, reaction_count_received, trust_level.
- List of recent posts (paginated).
- SSH pubkeys registered (titles only, not the key material).

### Endpoints
- `GET /api/v1/users/{name}` (public, no auth).
- `PATCH /api/v1/users/me` (edit own bio, display name).

### TUI
- `u` key in thread view on a post author's name shows their profile panel.

---

## Priority order

| # | Milestone | User impact | Sysop value | Effort |
|---|-----------|-------------|-------------|--------|
| 1 | M8 Notifications | ★★★★★ | ★★★ | Medium |
| 2 | M10 Reactions | ★★★★ | ★★★ | Small |
| 3 | M17 Profiles | ★★★★ | ★★ | Small |
| 4 | M9 Trust Levels | ★★★ | ★★★★★ | Medium |
| 5 | M11 Polls | ★★★ | ★★ | Medium |
| 6 | M12 Door Games | ★★★ | ★★★★ | Large |
| 7 | M13 ANSI/baud | ★★ | ★★★ | Small |
| 8 | M14 Node spy | ★ | ★★★★ | Small |
| 9 | M15 NNTP / federation | ★★ | ★★★★★ | Large |
| 10| M16 Gopher | ★ | ★★ | Small |
