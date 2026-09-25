# Milestone: Scaling to Multiple Servers

## Goal

Run BudgieBBS on more than one server while preserving the BBS experience:
new posts, likes, poll votes, notifications, and chat should reach users
connected to any node without requiring sticky sessions.

The target is not internet-scale sharding. The target is a production-ready
small cluster: multiple HTTP/WebSocket nodes, multiple SSH TUI nodes, shared
durable storage, and reliable cross-node live wakeups.

## Current State

Budgie already has a good spine for this:

- Commands enter through HTTP, WebSocket, or SSH TUI.
- The core appends durable events and updates projections.
- Clients can replay events by cursor.
- WebSocket, SSE, long-poll, and TUI all consume scoped events.

The current runtime is still mostly single-node:

- `cmd/budgied` opens runtime storage through `core.New(*dbPath)`, which uses
  SQLite.
- `core.New` and `core.NewPostgres` both create `NewMemBus()`.
- `MemBus` is in-process only.
- `NATSBus` exists, but it only publishes local events outward; there is no
  remote subscriber loop that brings events from other nodes back into local
  subscribers.
- Slow local subscribers can drop live events when their 512-event channel is
  full; durable replay exists, but live gap detection is not complete.

## Target Topology

```
             +-------------------+
             | Load balancer     |
             +---------+---------+
                       |
       +---------------+----------------+
       |                                |
+------+-------+                 +------+-------+
| API/WS node  |                 | SSH TUI node |
| budgied      |                 | budgied      |
+------+-------+                 +------+-------+
       |                                |
       +---------------+----------------+
                       |
              +--------+--------+
              | Event wakeups   |
              | NATS/Redis/PG   |
              +--------+--------+
                       |
              +--------+--------+
              | Postgres        |
              | events + reads  |
              +-----------------+
```

All nodes should be able to serve reads. Writes must still be globally
serialized so event sequence and projections remain authoritative.

## Non-Goals

- No multi-region active-active.
- No sharding by board or tenant.
- No federation between independent Budgie instances.
- No replacement of the event-log model.
- No requirement to remove SQLite single-node mode.

## Design Principles

- Database replay is authoritative. Broker messages are wakeups, not the source
  of truth for durable forum state.
- SQLite remains the default development and hobby single-node runtime.
- Postgres is the production multi-node runtime.
- Live delivery can be at-least-once; clients and nodes must tolerate duplicate
  events by event ID or sequence.
- Missing live events must be corrected by replay from the durable event log.
- Sticky sessions must not be required.

## Workstream 1: Runtime Storage Selection

### Tasks

- Add a runtime storage selector:
  - `-storage sqlite|postgres`, default `sqlite`.
  - `-postgres-dsn`, also read from `BUDGIE_POSTGRES_DSN`.
- Use `core.NewPostgres` in normal server mode when storage is `postgres`.
- Keep migration and rebuild behavior unchanged.
- Add startup logging that clearly reports storage mode and DSN with password
  redacted.
- Add tests for flag/env selection.

### Acceptance Criteria

- `budgied -storage sqlite -db budgie.db` works as today.
- `budgied -storage postgres -postgres-dsn ...` starts HTTP, WS, SSH, and NNTP
  against Postgres.
- Full Go tests pass in SQLite mode.
- A Postgres integration test or manual smoke script can create a user, create a
  thread, append a post, list posts, and replay events.

## Workstream 2: Global Write Serialization

### Problem

Today each process owns a single command-handler goroutine. In a cluster, that
becomes one writer per process. That can break the intended global single-writer
model unless writes are serialized across nodes.

### Preferred First Implementation

Use a Postgres advisory transaction lock around mutating command execution in
Postgres mode.

This keeps all nodes capable of accepting writes while ensuring only one command
mutates the event log and projections at a time.

### Tasks

- Add a Postgres-only global command lock helper.
- Wrap mutating command execution with the lock in Postgres mode.
- Keep read-only calls lock-free.
- Add timeout and clear error reporting when the writer lock cannot be acquired.
- Add tests or an integration stress fixture that fires concurrent post appends
  from multiple `Core` instances sharing one Postgres database.

### Acceptance Criteria

- Concurrent writes from multiple nodes produce strictly increasing event
  sequences.
- Projection post counts and thread last sequence remain correct.
- Command latency remains acceptable under moderate concurrent posting.

## Workstream 3: Cross-Node Event Wakeups

### Problem

When node A commits an event, users connected to node B do not receive the live
event because `MemBus` is local to node A.

### Preferred First Implementation

Introduce a durable-event wakeup bridge.

For durable events, broker payloads should carry enough metadata to wake other
nodes:

```json
{
  "seq": 123,
  "event": "post.appended",
  "scopes": ["thread:thr_1", "board:general"]
}
```

Receiving nodes should fetch the authoritative event from Postgres by sequence,
then publish it into their local `MemBus`.

### Broker Options

- Postgres `LISTEN/NOTIFY`: simplest operationally if Postgres is already
  required. Good enough for small clusters, but payload size and queue behavior
  need care.
- NATS: already sketched by `NATSBus`; better for fanout and future workers.
- Redis Pub/Sub or Streams: acceptable, but adds another dependency without as
  much existing code.

Recommendation for this milestone: use NATS if we are comfortable adding the
dependency; otherwise start with Postgres `LISTEN/NOTIFY` and keep the bridge
interface broker-neutral.

### Tasks

- Split local publish from remote publish to avoid loops.
- Add a `WakeupBus` or `ClusterBus` interface for cross-node notifications.
- Add remote subscriber loop in `budgied`.
- On remote wakeup, fetch event by `seq` from Postgres and publish local-only.
- Deduplicate events by `seq` on each node.
- Add cluster tests with two `Core` instances and two local subscribers.

### Acceptance Criteria

- Client connected to node A receives a post created through node B.
- Client connected to node B receives a like created through node A.
- Remote wakeups do not duplicate events on the origin node.
- If a node misses a wakeup, reconnect/replay still catches up.

## Workstream 4: Live Gap Detection and Replay

### Problem

`MemBus` drops events for slow consumers instead of blocking. This is the right
backpressure choice, but the connection layer must notice gaps and replay from
the durable event log.

### Tasks

- Track last delivered durable `seq` per WebSocket connection.
- Before writing a durable event, detect `evt.Seq > lastSeq + 1`.
- On a gap, replay from `lastSeq` for the connection's scopes before sending
  the new event.
- Apply the same rule to SSE and long-poll where applicable.
- Add equivalent protection for SSH TUI subscriptions or force a refresh when a
  gap is detected.
- Add tests for dropped local bus events followed by replay catch-up.

### Acceptance Criteria

- A slow WS consumer does not permanently miss durable post/like events.
- A reconnecting client resumes from its cursor on any node.
- Duplicate events are tolerated and do not corrupt client state.

## Workstream 5: Ephemeral Events

### Scope

Forum state events are durable and replayable. Presence is ephemeral. Chat is
currently stored in `chat_lines` and broadcast as `chat.line`, so it sits between
the two categories.

### Tasks

- Decide event policy:
  - Posts, threads, edits, redactions, likes, polls, notifications: durable log.
  - Presence: broker-only with optional local state.
  - Chat: persisted bounded history plus broker wakeup.
- Ensure chat sent on node A appears on node B.
- Ensure presence updates do not inflate the durable event log.
- Add bounded retention policy for chat history.

### Acceptance Criteria

- Live chat works across nodes.
- Presence is best-effort and recovers on reconnect.
- Durable replay is not polluted by high-frequency presence churn.

## Workstream 6: Deployment Roles and Health

### Runtime Roles

Start with one binary that can enable or disable roles:

- `api`: HTTP REST, WebSocket, SSE, web static serving.
- `ssh`: SSH TUI server.
- `worker`: outbox jobs, notification fanout, trust/activity jobs.
- `nntp`: NNTP gateway.

### Tasks

- Add role flags such as `-roles api,ssh,worker,nntp`.
- Add `/healthz` for liveness.
- Add `/readyz` for database and broker readiness.
- Add structured startup summary: node ID, roles, storage mode, broker mode.
- Add graceful shutdown checks for HTTP, WS, SSH, and worker loops.

### Acceptance Criteria

- It is possible to run API-only and SSH-only nodes.
- Load balancer can use readiness to avoid routing to half-started nodes.
- A worker-only node can process outbox jobs without opening public transports.

## Workstream 7: Observability and Performance

### Metrics

- Open WS connections.
- Open SSH sessions.
- Local subscribers.
- Events published local and remote.
- Remote wakeup lag.
- Replay count and replay batch size.
- Dropped local subscriber sends.
- Command latency.
- Writer-lock wait time.
- Outbox pending/running/dead counts.

### Load Targets

Initial targets for this milestone:

- 3 app nodes.
- 1 Postgres primary.
- 1 broker.
- 1,000 concurrent WebSocket clients.
- 200 concurrent SSH clients.
- 20 posts/likes per second sustained for 5 minutes.
- Cross-node p95 live delivery under 500 ms on a local network.
- No durable event loss after node restart.

## Workstream 8: Documentation and Operations

### Tasks

- Add `deployment-single-node.md`.
- Add `deployment-multi-node.md`.
- Add example systemd units or container commands.
- Add backup/restore instructions for Postgres.
- Add broker restart behavior notes.
- Add a cluster smoke-test script.

### Acceptance Criteria

- A new operator can start a 2-node cluster from docs.
- The smoke test proves cross-node post and like delivery.
- Failure modes and expected recovery behavior are documented.

## Suggested Slice Order

1. Add Postgres runtime mode to `budgied`.
2. Add global write serialization for Postgres mode.
3. Add broker-neutral cluster wakeup interface.
4. Implement one broker backend.
5. Add cross-node durable event fanout.
6. Add live gap detection and replay.
7. Add chat/presence cross-node behavior.
8. Add roles, health checks, and cluster docs.
9. Add load tests and metrics.

## Final Definition of Done

- Two or more Budgie server nodes can run against the same Postgres database.
- A user connected to any node receives durable events created through any other
  node.
- Writes remain globally ordered.
- Reconnect and replay work across nodes.
- Chat works across nodes.
- Sticky sessions are not required.
- SQLite single-node mode still works for local development.
- Full Go tests pass.
- Cluster smoke test passes.
