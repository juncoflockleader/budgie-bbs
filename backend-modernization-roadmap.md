# BudgieBBS Backend Modernization Roadmap

## Current Architecture

BudgieBBS already has the right spine for a modern forum/BBS hybrid: commands enter through HTTP, WebSocket, or the server-hosted SSH TUI; the core validates them through one command handler; durable events are appended to a monotonic log; projection tables serve reads; live subscribers receive scoped events from a bus. The current implementation is deliberately compact and development-friendly: one Go binary, SQLite WAL, an in-process pub/sub bus, and React plus Bubble Tea clients.

That spine should remain. The modernization work is about making the spine production-shaped without losing the protocol-first property that makes web and terminal clients peers.

## Selected Direction

- Keep Go and the existing command/event protocol.
- Preserve the local single-process SQLite mode for development and small boards.
- Add Postgres as the production event store and projection store.
- Add a NATS-backed bus for multi-node live wakeups while keeping replay authoritative in the database.
- Move post-commit side effects to an outbox worker model.
- Keep SSH as the first-class rendered BBS transport.
- Add NNTP as the first old-school network gateway.
- Build core Discourse-like forum features before retro extras: categories, profiles, notifications, trust, moderation review, search, and admin APIs.

## M18 Backend Stabilization

Fix the parts of the current MVP that would become expensive to migrate later.

- Normalize identity internally: posts and threads store `author_id`; APIs may still expose display usernames for client compatibility.
- Add real `created_at` and `updated_at` timestamps to thread/post projections.
- Fix edit-window and author-permission checks to use user IDs and timestamps.
- Replace post-commit goroutines with durable outbox jobs.
- Introduce migration metadata and storage interfaces for event store, projections, idempotency, and migrations.
- Harden command handling with actor-scoped idempotency, command hash conflict checks, payload limits, rate limits, and consistent validation errors.

## M19 Postgres Event Store

Add Postgres without forcing every developer to run it immediately.

- Add Postgres migrations for `events`, normalized `event_scopes`, projection tables, command idempotency, and outbox jobs.
- Implement the `EventStore` contract for Postgres.
- Add projection rebuild tooling that can replay durable events into fresh projection tables.
- Add SQLite-to-Postgres migration tooling that preserves event IDs and sequence numbers.
- Keep the SQLite implementation as the default local mode until production config chooses Postgres.

## M20 Multi-Node Runtime

Make horizontal runtime topology explicit.

- Split logical roles: stateless API/WS nodes, SSH nodes, one active command writer, and workers.
- Protect the active command writer with Postgres advisory locking or equivalent leader election.
- Add NATS-backed live event wakeups; every node replays from Postgres when exact ordering matters.
- Add health checks, readiness checks, metrics, structured logs, and graceful shutdown across all roles.
- Ensure any node can serve any read, write, or reconnect request without sticky sessions.

## M21 Modern Forum Core

Build the features that make Budgie usable as a serious forum.

- Categories with hierarchy, ordering, visibility, permissions, and default notification levels.
- User profiles with display name, bio, avatar marker, stats, trust level, and recent activity.
- Richer notifications for mentions, replies, watched threads, moderation actions, and account events.
- Trust levels computed by workers from activity, visits, reads, reactions, and staff grants.
- Moderation review queue for flags, staff decisions, notes, sanctions, and audit history.
- Admin APIs for users, categories, sanctions, audit export, and moderation operations.

## M22 NNTP Gateway

Add a BBS/newsgroup-compatible gateway without bypassing Budgie authority.

- Map categories/boards to configured newsgroups.
- Support read commands such as `CAPABILITIES`, `LIST`, `GROUP`, `OVER`, `ARTICLE`, `HEAD`, and `BODY`.
- Support authenticated `POST` by routing articles through `createThread` or `appendPost`.
- Generate stable `Message-ID` values from Budgie post IDs and the instance domain.
- Apply the same permissions, sanctions, rate limits, moderation, and idempotency rules as every other transport.

## M23 Search and Discovery

Move discovery from MVP search to production-quality projections.

- Replace SQLite FTS5 in production with Postgres `tsvector` and GIN indexes.
- Add filters by category, author, date range, content type, and moderation visibility.
- Add stable `new`, `hot`, and `best` projections as derived folds over the event log.
- Keep live delivery chronological; expose ranked views as read projections rather than reordering live streams under clients.

## M24 Production Hardening

Make the system safe to operate for a public community.

- Backups and restore drills for event log, projections, and uploaded/static assets if added later.
- Retention policy for ephemeral chat/presence and old outbox jobs.
- Audit export for moderation and admin actions.
- Abuse tooling: rate-limit dashboards, suspicious account reports, and sanction review.
- Deployment documentation for single-node, small multi-node, and production topologies.

## M25 Expanded BBS Layer

Deepen the terminal/sysop identity once the forum core is solid.

- ANSI capability detection and graceful terminal degradation.
- Optional baud pacing and welcome ANSI screens.
- Sysop node console with active sessions, status messages, and kick controls.
- Door-game support behind explicit sandboxing and per-user time limits.
- Later: Gopher or other read-only retro gateways if there is real demand.

## M26 Federation Research

Prototype federation only after single-instance production behavior is trustworthy.

- Start with read-only event sharing between Budgie instances for selected public categories.
- Preserve source instance, source event ID, and portable author references.
- Use relaxed eventual ordering across instances; local `seq` remains authoritative only within one instance.
- Explore signed instance-to-instance feeds before any writable federation.

## Test Plan

- Run full Go tests with writable cache path after each storage/backend slice:
  - `GOCACHE=/private/tmp/budgie-gocache /opt/homebrew/bin/go test ./...`
- Add storage contract tests shared by SQLite and Postgres implementations.
- Add replay/live-tail equivalence tests across HTTP, WS, SSH-internal, and NNTP command paths.
- Add outbox retry/idempotency tests.
- Add migration and projection-rebuild tests.
- Add security tests for roles, sanctions, JWT validation, and moderation visibility.
