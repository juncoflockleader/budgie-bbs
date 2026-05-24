# General Technical Directions

*A modern BBS / forum system — landscape, opportunity, and chosen direction*

---

## 1. What we're building and why

The goal is a forum system that recovers what term-based BBSes had and the web lost: **the server pushes to you instead of you asking it.** On a classic BBS, an open connection meant new messages, chat lines, and "user X just logged on" arrived the instant they happened. The web's request–response model broke that, and the F5 refresh is the scar tissue. We want that immediacy back — without giving up a modern, web-native experience for the people who'll never open a terminal.

Three decisions frame everything that follows:

- **Core experience:** both a terminal client and a web client, sharing one backend protocol.
- **Build approach:** a fresh protocol and server from scratch (study the existing projects, inherit none of their core abstraction).
- **Audience:** a serious, general-purpose forum alternative — not only a retro/scene board.

---

## 2. The current landscape

The space splits into three camps. The distinction matters because it tells us what's already solved and what's still open.

### Camp 1 — Preservation / nostalgia
People rebuilding actual 90s software (e.g. Impulse BBS compiled from Borland Pascal) and running classic boards over telnet/SSH, reached with clients like SyncTERM, MuffinTerm, or Cool Retro Term. This is lovingly maintained, but it's archaeology — not where new architecture happens.

### Camp 2 — Modern reimplementations
The interesting camp. Two projects stand out as genuinely active and architecturally serious:

- **LBBS** (InterLinked1) — Written in C, ~80K SLOC, few dependencies, GPLv2, designed to be modular. Far more than a message board: terminal access via Telnet/RLogin/SSH/UNIX socket, mail modules usable as a standalone private mail server, a built-in IRC client+server with native Slack/Discord relays, and even its own WebSocket library (libwss) and realtime webmail client (wssmail). A one-person passion project, but the engineering is real.
- **ENiGMA½** (NuSkooler, Node.js) — The most complete and usable modern BBS. HJSON config, themes, JavaScript mods, SQLite storage, PBKDF2 + 2FA/OTP. Telnet, SSH, and both secure and non-secure WebSocket access built in. Crucially, correct CP437 **and** UTF-8 rendering with wide-character support (CJK, Hangul, fullwidth forms). This is what most live boards actually run on today.

**Why we don't extend either:** both treat WebSocket as just another way to reach the same terminal session. Their core abstraction is *a terminal session walking a server-side menu tree*. That is precisely the thing we want **not** to inherit. They're invaluable to study (ENiGMA½ for CP437/UTF-8 terminal rendering and theming; LBBS for C-level networking and its module system) — but not as a base.

### Camp 3 — Web-terminal plumbing
This is the source of the underwhelming "WebSocket BBS" efforts. The typical pattern (pyxtermjs and friends): spawn a pty running bash on the backend, pipe its output into xterm.js in the browser, send browser input back over a websocket. That's a terminal **transport**, not a BBS. xterm.js itself is mature and fast (GPU-accelerated WebGL renderer, an addon that attaches to a server process over a websocket) — but bolting it to a shell gives you a remote terminal, not a forum. Nobody in this camp built the application layer.

---

## 3. The gap — and where we go

**Nobody owns the middle.** Camp 2 is servers with bolted-on transports. Camp 3 is renderers with no application underneath. The open territory is:

> A board that is **natively a real-time application** — a clean, documented message protocol over which the server is the single source of truth, so that multiple *first-class* clients (terminal and web) can render the same event stream however suits their medium.

The retro feeling we're chasing turns out to be, in 2026 terms, just good architecture. The industry has come all the way back around to server-push: a large majority of new web apps now use WebSockets or similar real-time protocols, and WebSockets cut latency materially versus polling. What was exotic on a 1990s BBS is mainstream infrastructure now. We're not fighting the grain of the modern web — we're using it to rebuild something the web briefly abandoned.

---

## 4. Architectural direction (high level)

The detailed decisions live in the companion document, *High-Level Technical Decisions*. In brief:

- **Protocol carries semantic domain events, not render instructions.** The server says `post.appended {thread, author, body, seq}`, never "draw at row 12, col 4." Each client renders events for its own medium. Both clients are smart renderers; neither is a dumb pipe.
- **The board is an append-only event log.** Every connected client holds a cursor (a sequence number). This single mechanism unifies live updates (push the tail), reconnection/catch-up ("send me everything after seq N"), and history (read the log).
- **Commands in, events out.** Clients send commands (`createThread`, `appendPost`, …) that can be rejected; the server validates, applies, and emits events that are facts. All authority — permissions, rate limiting, moderation — lives in one command-handler path, enforced identically regardless of which transport the command arrived on.
- **The terminal client is a server-hosted TUI.** The user SSHes in; the server spawns a TUI that subscribes to the same event bus and paints ANSI to the pty. This preserves the two magical properties of terminal access — zero install and any-terminal-works — and gives passwordless SSH-key auth for free.
- **One identity, two front doors.** The account is the source of truth; an SSH public key and a web credential are both just credentials attached to it. Unread state is "my cursor vs. the log head," shared across both.

---

## 5. Transport choices

WebSocket is the default for the high-frequency, bidirectional surface (chat, live threads, presence). But it isn't automatically right for everything:

- **WebSocket** — bidirectional, lowest latency. For chat, live thread updates, presence.
- **SSE (Server-Sent Events)** — HTTP-based, auto-reconnect with last-event-ID, simpler, but one-way. A reasonable fit for purely push surfaces (ambient notifications) if we want to keep those off the WS path.
- **SSH** — the terminal front door; carries the same semantic events to the server-hosted TUI.

The append-only-log + cursor model maps cleanly onto all three, because "catch me up from cursor N" is the same idea as SSE's `last-event-ID`.

---

## 6. Stack recommendation

The workload is: many persistent connections (SSH + WS), pub/sub fan-out, presence, a durable ordered log. Three credible options:

- **Go + Charm ecosystem** *(recommended default)* — `wish` is purpose-built for serving a TUI over SSH (exactly our terminal-client model); `bubbletea` renders the TUI; goroutines/channels map directly onto "one connection per goroutine, fan events over channels." Mature WS and SSH libraries. Write one core, two thin renderers, language stays out of the way.
- **Elixir / Phoenix** — Arguably stronger on paper: OTP is built for millions of persistent stateful connections; Phoenix Channels + Presence give pub/sub and who's-online almost for free; the actor model fits "each connection is a process holding a cursor." Cost: rarer skill set, less turnkey SSH-TUI story.
- **Rust** (`russh` + `ratatui`; cf. the `ssh_ui` project = russh serving a TUI) — Maximum performance, more ceremony. Only if there's a specific reason.

**Recommendation:** Go + Charm unless Elixir is already a known quantity, in which case Elixir.

---

## 7. The unglamorous truths

Two cautions, because the fun part can starve the necessary part.

- **The content model must be medium-neutral.** A post can't be both "this exact ANSI art screen" and "this responsive HTML." Pick a semantic body format (a sane Markdown subset) both clients render their own way; treat ANSI art as a distinct content *type*, not the universal post format. Decide this early — retrofitting a content model is brutal.
- **The terminal is the delight; it is not what makes this a serious forum.** Seriousness comes from the boring work: moderation tooling, search, roles/permissions, multiple boards, and a genuinely good *mobile web* experience (most of the target audience arrives on a phone, not SyncTERM). The architecture gives a clean foundation for all of it, but none of it is free. The failure mode is: build a gorgeous ANSI client, demo it, everyone says "wow," and it never crosses into general-purpose because the unglamorous work never gets done. Sequence accordingly.

On threading: resist Reddit-style infinite nesting — it's a known UX failure at depth and hard to render in a terminal pane. Classic flat-with-quotes or shallow (1–2 level) threading reads better and renders cleanly in **both** media. Let the dual-client constraint discipline the feature set: if it can't render cleanly in a terminal, it probably shouldn't exist.

---

## 8. First milestone

A thread to pull, rather than a blank repo:

1. **Define the spec** — ~8 events, ~5 commands, as typed schemas. This is the durable artifact; write it first.
2. **Build the core** — append-only log (SQLite to start: one `events` table, monotonic `seq`, JSON payload), the command handler (validate → append), an in-process pub/sub that fans new events to subscribers with cursor support.
3. **SSH front door + dumb TUI** — list threads, read a thread, post a reply, one live chat line. Server-hosted ANSI. This is the "I dialed in and it works" moment and it proves the core.
4. **Web client over WebSocket** — same core, same protocol. A renderer, not a redesign. Seeing one `chat.line` appear live in both a terminal and a browser with no F5 anywhere is the moment the thesis is real.

Everything heavier — moderation UI, search, sub-forums, federation — layers on after that without touching the spine.
