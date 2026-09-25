# Roadmap

This is the canonical compact roadmap. The historical MVP, next-milestones,
backend-modernization, and small-cluster milestone notes have been folded into
this file and the deployment docs.

## Current Baseline

BudgieBBS now has:

- A Go `budgied` server with SQLite single-node mode.
- HTTP REST, WebSocket, SSE, SSH TUI, and optional NNTP runtime roles.
- A React/TypeScript web SPA.
- Durable event log, command handler, projections, and replay paths.
- Postgres multi-node mode with role splitting and operational runbooks.
- Internet-scale command/event log gates for NATS and Kafka.
- Redis seams for command index batching and hot read-cache work.

## Deployment Roadmap

1. **Single-node default**
   - One `budgied` process.
   - SQLite under `/var/lib/budgie`.
   - Optional launchd/systemd service.
   - No NATS, Kafka, Postgres, or Redis required.
   - Canonical doc: [deployment-single-node.md](../deployment-single-node.md).

2. **Small multi-node production**
   - Shared Postgres event log and projections.
   - Explicit runtime roles for API/gateway, SSH, workers, and NNTP.
   - Cross-node wakeups and replay repair.
   - Canonical doc: [deployment-multi-node.md](../deployment-multi-node.md).

3. **Internet-scale path**
   - Split delivery from durability.
   - Use partitioned command/event logs.
   - Keep global views asynchronous.
   - Use Redis as recoverable batching/index/cache support, not authority.
   - Canonical docs:
     [milestones-internet-scale.md](../milestones-internet-scale.md) and
     [design-internet-scale-writes.md](../design-internet-scale-writes.md).

## Product Roadmap

Near-term product work should prioritize ordinary forum value before deeper
retro extras:

- Notifications and watched/muted thread preferences.
- Reactions and lightweight engagement signals.
- Public profiles and user stats.
- Trust/activity levels.
- Polls.
- Moderation review and stronger admin tooling.
- Door-game framework after core forum workflows are solid.
- ANSI capability detection, baud pacing, and sysop/node console polish.
- Federation, Gopher, and additional retro gateways only after single-instance
  operations are trustworthy.

## Backend Roadmap

- Keep storage contracts shared by SQLite and Postgres.
- Keep command idempotency actor-scoped and hash-checked.
- Keep post-commit work in durable worker/outbox paths.
- Keep projection rebuild tools current for every derived view.
- Keep search/ranking/hot feeds as derived projections rather than command-path
  work.
- Keep full restore drills and failure runbooks close to deployment docs.

## Definition Of Done For Future Roadmap Items

- The feature works in SQLite single-node mode unless explicitly production-only.
- Multi-node behavior is documented when it touches durability, live delivery,
  workers, or runtime roles.
- Replay from the durable log can repair missed live delivery.
- Operator docs and tests move with the implementation.
- New roadmap notes update this file or an existing canonical doc rather than
  creating another root-level memo.
