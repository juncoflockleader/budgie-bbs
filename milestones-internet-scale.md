# Milestones: Path to Internet Scale

## Status

Execution roadmap. This document turns
[`design-internet-scale-writes.md`](design-internet-scale-writes.md) into
milestones that can be worked in order.

It starts from the current production-scale cluster plan in
[`deployment-multi-node.md`](deployment-multi-node.md). That baseline is a
shared Postgres event log, one global advisory write lock, Postgres
`LISTEN/NOTIFY` wakeups, cursor replay, and local per-node subscribers.

The internet-scale target is not a rewrite. It is the same event-log/CQRS
system, with three constraints relaxed or split apart:

- Delivery is separated from durability.
- Global write order becomes per-aggregate write order.
- Global views become asynchronous derived projections.

## Goal

Scale BudgieBBS from a small multi-node cluster to an internet-scale service
that can handle:

- Very high concurrent writes across independent boards and hot threads.
- Millions of connected clients over WebSocket, SSE, and SSH/TUI gateways.
- Low-latency live delivery without making live delivery the source of truth.
- Correct local ordering inside boards/threads, with eventual global views.

## Ground Rules

- SQLite single-node mode remains supported for development and small installs.
- The current Postgres multi-node mode remains the supported simple production
  topology until the partitioned log is proven.
- The durable event log remains authoritative at every step.
- Broker delivery is best-effort; reconnect/replay must always repair misses.
- New scale machinery is adopted behind interfaces and measured gates, not by
  breaking the existing command, event, and projection model.
- Each milestone must leave the system deployable.

## Current Slice Status - June 14, 2026

The internet-scale epic is functionally ready for deployment polish. The earlier
native NATS/Postgres remote gate failures were reclassified as benchmark
topology failures after isolating each component on the staging host. A
high-jitter client path made synchronous broker and database round trips look
like an application bottleneck.

On the staging host itself, the primitive dependencies have large headroom:

- Redis loopback: roughly `120k-170k ops/s`, sub-millisecond p50.
- Postgres loopback: trivial selects at `43.7k TPS` with one client and
  `166.6k TPS` with eight clients; single-row inserts at `9.8k TPS` with one
  client and `52.4k TPS` with eight clients.
- NATS JetStream loopback: synchronous publish at `18.6k msg/s`, asynchronous
  publish at `199k msg/s`, and batched publish at `131k msg/s`.

The actual app-path load generator, copied to the staging host and run against
`127.0.0.1` NATS/Postgres/Redis, passed the promoted 2,400-command shape with
substantial margin:

- Redis command index enabled, batch size 25:
  `4,013 submit cmd/s`, `1,577 drain cmd/s`, zero lag after drain.
- Redis index disabled, batch size 25:
  `20,690 submit cmd/s`, `1,928 drain cmd/s`, zero lag after drain.
- Redis index enabled, batch size 250:
  `3,967 submit cmd/s`, `1,671 drain cmd/s`, zero lag after drain.

`140007c3` added a broker command/event batch transaction boundary, and
`d7f858a7` snapshotted command drain offsets per writer round. Together with
the Redis command partition index and read-cache seams, the remaining work is no
longer core scale mechanics. It is deployment hardening: keep the gate scripts
as the promotion entrypoints, document that throughput evidence must be
collected from a host colocated with staging services or an equivalently
low-jitter path, archive bundle manifests, and keep cleanup/runbook guidance
current.

Redis remains part of the plan as a recoverable batching, indexing, and
hot-serving layer. The immediate use is a Redis-backed command partition
tail/commit index so writer drain assignment and lag checks can avoid repeated
broker-wide scans while NATS/Kafka remain the durable command/event logs. The
next use is a read-through hot cache for stable-watermark feed, board, and
thread slices. The later IS6 use is a coalescing buffer for derived-view work
inside a bounded freshness window. Redis loss can make reads cold, force broker
rescans, or make derived views stale; it must not lose authoritative commands,
events, or replay positions.

## IS0 - Baseline, Budgets, And Tripwires

**Purpose:** Know exactly where the current multi-node design bends before
replacing it.

### Workstreams

- Finish and keep green the existing multiple-server milestone.
- Record baseline metrics for:
  - `budgie_writer_lock_wait_ms`
  - command latency by command kind
  - cross-node wakeup lag
  - replay count and replay batch size
  - local subscriber drops
  - open WebSocket and SSH sessions
- Add load fixtures for boards, hot threads, reactions, chat, reconnects, and
  slow consumers.
- Define traffic budgets for the simple Postgres cluster:
  - writes per second before lock wait is saturated
  - live delivery p50/p95/p99
  - max acceptable wakeup DB reads per event
  - replay recovery time after a missed wakeup
- Write operator guidance for when to stay on Postgres mode and when to start
  enabling the internet-scale path.

### Acceptance Criteria

- A 3-node Postgres cluster passes the existing cluster smoke test.
- A repeatable load test reports the current global-lock ceiling.
- Dashboards expose the tripwires that justify later milestones.
- No internet-scale dependency is required for ordinary small deployments.

### Implementation Progress

- Added `cmd/budgie-loadgen`, a repeatable Postgres write-load fixture that
  provisions an isolated schema, runs equal-sized same-board and spread-board
  `createThread` workloads through the real command path, and emits JSON with
  throughput, latency percentiles, failures, and spread-partition speedup.
  Optional thresholds (`-min-spread-speedup`,
  `-min-spread-writes-per-sec`) make the fixture usable as a CI or promotion
  gate without requiring any internet-scale dependency for ordinary
  deployments.
- Added a shared JSON scale-budget evaluator and an example operator artifact
  (`ops/internet-scale-budgets.example.json`). `cmd/budgie-loadgen` and
  `cmd/budgie-gateway-loadgen` both accept `-budget-file`, so deployments can
  copy the example, tune it to observed Postgres and gateway capacity, and use
  the same budget as a staged promotion gate.

## IS1 - Split Delivery From Durability

**Purpose:** Move live delivery off Postgres wakeup reads while keeping Postgres
as the authoritative event log and keeping the global write lock.

This corresponds to Appendix A in
[`design-internet-scale-writes.md`](design-internet-scale-writes.md).

### Workstreams

- Wire `NATSBus` into `budgied` behind a `-nats <url>` flag.
- Add the remote subscriber loop that receives full event bodies from NATS and
  publishes them local-only.
- Include origin `node_id`, scopes, event kind, sequence, timestamp, and payload
  in the NATS envelope.
- Gate off redundant Postgres `LISTEN/NOTIFY` wakeups when NATS owns cross-node
  delivery.
- Keep Postgres cursor replay as the repair path.
- Add metrics for remote NATS wakeups, publish failures, subscriber lag, and
  delivery drops.
- Add tests for self-skip, local-only publish, duplicate tolerance, missed
  wakeup replay, and origin-node delivery.

### Acceptance Criteria

- A client connected to node A receives a durable event created through node B
  without node A fetching that event from Postgres for live delivery.
- Dropping a NATS message does not lose durable state; replay catches up.
- Duplicate NATS messages do not corrupt clients or projections.
- Cross-node p95 live delivery improves materially from the Postgres wakeup path.
- The system still runs with NATS disabled.

### Implementation Progress

- `budgied -nats <url>` / `BUDGIE_NATS_URL` wires `NATSBus` into Postgres mode.
- Remote NATS events carry full event bodies and scopes, self-skip by origin
  node, and publish local-only on receipt.
- Postgres `LISTEN/NOTIFY` wakeups are skipped when NATS owns cross-node
  delivery.
- Unit tests cover envelope publishing, typed remote reconstruction, self-skip,
  local-only publish, and subscription shutdown.

## IS2 - Partition Semantics In The Existing Model

**Purpose:** Make partition identity explicit before changing the storage engine
or writer model.

### Workstreams

- Define aggregate keys:
  - board-level partitions for normal board/thread ordering
  - thread-level or sub-thread partitions for future hot-thread escape hatches
  - user partitions for private notifications and account-scoped events where
    useful
- Add partition metadata to durable events:
  - `partition_key`
  - `partition_kind`
  - `partition_offset`
- Add command routing classification so every mutating command declares its
  ordering scope before execution.
- Introduce vector-cursor types in the protocol layer while preserving scalar
  `seq` compatibility.
- Add replay APIs that can operate by partition and offset, even if backed by
  the existing Postgres log initially.
- Mark cross-partition views as eventually consistent in code and docs.

### Acceptance Criteria

- Every durable event can be mapped to one or more explicit delivery scopes and
  one authoritative write-ordering partition.
- Existing clients continue using scalar cursors.
- New clients can accept a vector cursor envelope in a feature-flagged path.
- Replay by partition returns the same durable events as replay by global `seq`
  for the covered scopes.
- Tests fail if a new mutating command omits its partition classification.

### Implementation Progress

- Durable events now carry `partition_kind`, `partition_key`, and
  `partition_offset` metadata in SQLite and Postgres schemas.
- `partition_offset` is a true per-partition offset assigned from
  `event_partition_offsets`; global `seq` remains as the compatibility cursor.
- Replay hydrates partition metadata, and `Core.ReplayPartition` can replay one
  partition by offset.
- SQLite-to-Postgres migration preserves partition metadata.
- Command partition classification is registered for every routed command, with
  a test that fails when a new route lacks a partition spec.
- The wire protocol now advertises `partition-cursors`, emits `cursor` and
  `headCursor` envelopes, and accepts cursor envelopes on WS resume plus HTTP
  poll, long-poll, and SSE replay paths while preserving scalar `after`
  compatibility.
- HTTP poll/long-poll now return head cursors with current per-partition
  offsets and can replay from partition-only cursor envelopes by unioning known
  event partitions, replaying each partition after its cursor offset, and
  compatibility-sorting the returned batch by `seq`. This gives IS2/IS4 a
  concrete partition-native replay path before scalar `seq` is removed from the
  write hot path.
- The web stream hook merges event partition cursors instead of replacing the
  vector with the latest event, then carries the accumulated partition-only
  cursor through WS resume and SSE fallback while keeping scalar `after` as the
  compatibility fallback.
- WS and SSE live delivery now track a durable cursor instead of only the last
  scalar `seq`. Both transports replay from cursor envelopes during backlog
  catch-up, dedupe by partition offset when available, and repair partition gaps
  even when scalar `seq` has already advanced on another partition.
- Cross-partition global HTTP projection reads now include eventual-consistency
  metadata and `X-Projection-*` headers for head, applied position, and lag.
- IS2 is complete for the current Postgres-backed compatibility substrate;
  IS3 can build parallel writer execution on top of the durable partition
  offset substrate.

## IS3 - Per-Partition Writers On Postgres

**Purpose:** Remove the single global writer bottleneck without changing the
operational substrate yet.

Postgres remains the event store, but the global advisory lock is replaced by
per-partition serialization. This gives most of the correctness transition while
keeping rollback simple.

### Workstreams

- Replace the single global write lock with per-partition lock keys in
  Postgres mode.
- Add per-partition offsets assigned inside the partition lock.
- Keep a global compatibility `seq` while clients and read models migrate.
- Update projections to rely on partition order where local order matters.
- Add conflict tests for:
  - concurrent replies in one thread or board
  - concurrent writes across different boards
  - moderation racing with a target post
  - read-your-writes after a routed write
- Add command-id dedup per partition.
- Add metrics for hot partitions, lock wait by partition, and partition skew.

### Acceptance Criteria

- Writes to different boards can commit in parallel.
- Writes within the same board/thread remain strictly ordered.
- Global feeds, rankings, search, and stats do not require synchronous global
  ordering for correctness.
- Existing scalar clients still function through the compatibility `seq`.
- Load tests show near-linear improvement as write load spreads across boards.

### Implementation Progress

- Added the `event_partition_offsets` table in SQLite and Postgres schemas,
  with backfill from existing event rows.
- `appendEvent` now assigns `partition_offset` from that table inside the same
  transaction, so different partitions each have independent offset sequences
  while global `seq` remains available for compatibility replay.
- The command handler now supports partition-routed worker lanes; commands in
  the same classified partition share a lane, while different partitions can be
  dispatched independently once the storage lock permits it.
- Postgres command execution now acquires a deterministic per-partition advisory
  lock. Scalar `seq` clients are protected by a transaction-scoped append gate
  that serializes durable event `seq` assignment through commit/rollback without
  holding a full-command global lock.
- Command-id dedup now uses `processed_commands_v2`, keyed by command
  partition, actor, and `cid`; tests cover same-`cid` reuse across independent
  partitions and conflict detection inside one partition.
- Metrics now expose per-partition durable offsets, partition count, offset
  skew, and hottest-partition lock wait count/sum/max, plus scalar append gate
  wait time.
- Handler-level tests prove different partition lanes can enter command
  execution concurrently while same-partition commands stay serialized.
- An opt-in Postgres integration test (`BUDGIE_TEST_POSTGRES_DSN`) proves
  advisory locks are scoped by partition: different partitions can lock
  concurrently while the same partition blocks.
- An opt-in end-to-end Postgres write test holds one board's partition lock,
  proves another board can still commit and become visible to reads, and proves
  the held board returns retryable `lock_unavailable` until released.
- Added an opt-in Postgres partition write load test
  (`BUDGIE_TEST_POSTGRES_LOAD=1`) that logs the same JSON throughput report as
  `cmd/budgie-loadgen` and can enforce
  `BUDGIE_TEST_POSTGRES_MIN_SPREAD_SPEEDUP`. This gives IS3 a repeatable gate
  for validating that write throughput improves as load spreads across board
  partitions.
- Added shared scale-budget evaluation for Postgres write-load reports; the
  remaining IS3 work is to tune the example budget values against a real
  Postgres database and promote the tuned file into CI/operator promotion.

## IS4 - Partitioned Command And Event Logs

**Purpose:** Move the authoritative write hot path from Postgres locks to a
partitioned append log.

Primary target: Redpanda. Kafka-compatible alternatives remain acceptable if
they preserve the same interfaces.

### Workstreams

- Add log interfaces for:
  - command production
  - writer consumption by partition
  - event append
  - partition offset replay
- Stand up Redpanda in local and CI integration environments.
- Implement the writer/decider tier as a consumer group over the command log.
- Use idempotent or transactional producer semantics for:
  - consume command
  - decide events
  - append events
  - commit command offset
- Keep Postgres as a projection store fed from the event log.
- Add a Redis-backed command partition index for gate and writer experiments:
  track produced tails and committed offsets by logical partition, use it for
  snapshot assignment and lag metrics, and fall back to broker listing/replay if
  Redis is empty or unavailable.
- Build a shadow mode:
  - commands/events still commit through Postgres
  - events are also published to Redpanda
  - replay parity is checked continuously
- Promote the partitioned event log to source of truth only after parity holds.

### Acceptance Criteria

- Rebuilding Postgres projections from the partitioned event log yields the same
  read models as the existing Postgres event log.
- Writer ownership follows broker partition assignment; no separate distributed
  lock is required for normal writes.
- A writer crash and rebalance does not lose or duplicate committed commands.
- Per-partition replay is authoritative.
- The Postgres global compatibility `seq` is no longer on the write hot path.
- Redis command partition index loss does not lose command/event data; writers
  can rebuild observable tail/commit state from the durable command log and
  committed offsets.

### Implementation Progress

- Added the IS4 log boundary in `internal/core/storage.go`: `CommandLog` for
  partitioned command production, partition fetch, and partition offset commits;
  `EventStore` for event append, scalar replay, and partition replay; and
  partition-scoped receipt storage.
- Added `SQLEventStore` as a shadow/parity adapter over the existing SQL events
  table. It appends through the current durable path and replays through the
  same scalar and partition APIs, preserving deployability while Redpanda is
  introduced later.
- Added `MemoryCommandLog` as a deterministic reference implementation for
  writer-tier tests and fixtures. Tests prove command offsets are independent by
  partition and SQL event appends replay with partition metadata.
- Added `ShadowEventStore` and `MemoryEventStore` for fail-open event-log shadow
  mode. Primary appends remain authoritative while mirror append failures and
  logical mismatches are recorded and counted with
  `budgie_event_log_shadow_*` metrics.
- Added `CheckEventReplayParity`, which compares primary and shadow
  `ReplayPartition` windows by partition offset and reports missing, extra, or
  mismatched logical events. This is the reusable core of continuous replay
  parity before a partitioned log can be promoted.
- Added `EventReplayParityRunner`, an opt-in periodic checker that discovers hot
  event partitions, replays bounded windows against primary and shadow logs, and
  advances per-partition checkpoints only after a clean comparison. SQL and
  in-memory event stores both expose partition discovery for this runner.
- Wired shadow mode into `budgied` behind `-event-log-shadow memory`. The node
  mirrors committed durable events from the post-commit bus into the shadow log,
  seeds tail checkpoints from SQL heads by default, and runs periodic replay
  parity checks without changing SQL as the source of truth.
- Added a broker-shaped `BrokerEventStore` and event-record codec with explicit
  logical partition offsets. This keeps replay parity independent of any
  broker's physical stream offsets and gives IS4 a stable adapter boundary for
  Redpanda/NATS implementations.
- Added a NATS JetStream shadow backend behind `-event-log-shadow nats`. It
  writes one subject per logical event partition, stores the Budgie
  `PartitionOffset` inside each record, uses JetStream subject-sequence CAS for
  concurrent appends, and replays partitions through direct subject reads for
  continuous SQL-vs-broker parity checking.
- Broker shadow records now preserve the SQL compatibility `seq` during shadow
  mode, and `BrokerEventStore` supports scalar compatibility replay by merging
  partition replays in sequence order. This lets projection rebuilds use the
  broker log as an input while SQL remains authoritative.
- Added `Core.RebuildProjectionsFromEventStore` and `budgied
  -rebuild-projections -rebuild-source nats`, a read-only NATS JetStream rebuild
  path that refuses to create a missing stream. Unit tests rebuild SQL
  projections from the broker-shaped event store, covering the first concrete
  piece of the IS4 projection promotion gate.
- Added `CheckEventLogPromotionReadiness`, a fail-closed SQL-vs-candidate event
  log preflight for broker-source projection rebuilds and derived-view
  backfills. It unions SQL and broker partitions, replays each partition in
  bounded windows, compares logical payloads, partition offsets, and SQL
  compatibility `seq`, and checks for broker-only tails before a NATS event log
  can be used as a rebuild source. The memory shadow store now preserves
  `CompatibilitySeq`, so local shadow fixtures exercise the same rebuild
  contract as broker shadow logs.
- Added `budgied -check-event-log-promotion-readiness`, a read-only one-shot
  SQL-vs-NATS event-log promotion gate. It opens the configured
  `-event-log-shadow-nats-stream` without creating it, prints the parity report
  as JSON, and exits nonzero on replay mismatches, broker-only tails, missing
  stream coverage, or partition-limit truncation before operators attempt
  NATS-backed projection rebuilds or source-of-truth promotion.
- Added a broker-shaped `BrokerCommandLog` and command-record codec with stable
  partition subjects, logical per-partition command offsets, partition replay,
  durable committed offsets, and hot-partition listing. This gives gateways and
  future writer consumers the command-log side of the same broker boundary the
  event log now uses.
- Added a NATS JetStream command-log backend with command subjects and
  commit-marker subjects in one direct-readable stream. Appends use
  subject-sequence CAS to assign one logical command offset per partition; commit
  markers advance monotonically per partition.
- Added fail-open command-log shadowing behind `Core.WithCommandLogShadow` and
  `budgied -command-log-shadow memory|nats`. Submitted commands are mirrored to
  the command log before the current SQL handler path executes, allowing the
  broker command stream to be populated and inspected without making it
  authoritative yet.
- Added `CommandLogWorker`, the first writer-consumer skeleton for broker-owned
  command partitions. It discovers command partitions, fetches records after the
  committed offset, executes them through a `CommandLogExecutor`, commits
  successful and terminally failed commands, and stops before retryable failures
  so later workers can resume from the last safe offset. `Core` now exposes
  `ExecuteCommandLogRecord` to bridge these records through the current
  SQL-backed handler without mirroring them back into the command-log shadow.
- Wired the worker skeleton into `budgied` as an explicit experimental `writer`
  role behind `-command-log-worker memory|nats`. Dedicated writer nodes can now
  drain the broker command stream through the SQL-backed executor while API/SSH
  nodes keep using shadow mode; startup rejects the unsafe same-process shape
  where local shadowed commands would also be consumed by the writer. Broker
  consumer-group partition ownership and idempotent event-log append/command
  commit remain the next promotion gates.
- Added `CommandPartitionClaimer` and a SQL-backed
  `command_partition_leases` table. Experimental writer nodes now take a
  short-lived lease before draining a command partition, refresh ownership
  before command execution and before offset commit, skip partitions owned by
  another live writer, and can take over after lease expiry. If ownership is
  lost mid-drain, the worker stops without committing additional offsets. This
  provides a concrete multi-writer experiment guardrail while the final
  broker-native consumer-group assignment is still pending.
- Command-log appends now synthesize a stable internal receipt id from the
  command partition and offset when a record has no client `cid`. Shadow-mode
  API execution uses the same generated id for the immediate SQL command, so a
  dedicated writer draining the shadow stream later routes through the existing
  partition-scoped idempotency table instead of appending a duplicate event.
  Tests cover both direct empty-`cid` command-log replay and shadow-stream replay
  through the real SQL-backed handler without advancing the event head twice.
- Added command-log partition offset listing and lag metrics for memory, broker,
  and NATS command logs. Shadow and writer nodes now expose sampled
  `budgie_command_partition_*` gauges for produced tails, writer commits,
  per-partition lag, total lag, max lag, and lag skew so IS4 promotion can watch
  whether broker-owned writers are keeping up before the command log becomes
  authoritative.
- The command-log execution bridge now recomputes the canonical command
  partition before routing a broker-owned record through the SQL-backed handler.
  Mispartitioned records are treated as terminal poison commands and committed
  past by the worker without appending events, protecting per-partition ordering
  while gateways and broker writer ownership are still being hardened.
- Command-log writers now retry command-offset commits after a successful or
  terminal SQL-backed command execution, refresh the partition lease before each
  attempt, and return/log partial partition results when every commit attempt
  fails. Tests prove the current idempotency bridge covers the crash window
  where SQL commits but the command offset does not: a later drain replays the
  same command-log record, returns the cached command result, commits the offset,
  and does not append a duplicate event.
- Successful command-log writer drains now persist an `applied` command-log
  receipt after SQL materialization and before committing the broker command
  offset. If that marker write fails, the worker leaves the offset uncommitted
  and replay uses the existing idempotency cache to recover. Command status now
  includes the committed command-log offset even for `applied` responses, and
  receipt gauges export `applied` rows alongside `retrying` and `failed`, making
  the post-materialization/pre-offset-commit bridge window auditable while the
  final broker transaction path is still pending.
- Broker event-log appends are now idempotent by event id. The memory broker log
  returns the original event for duplicate appends and rejects id reuse with
  different content; the NATS JetStream event log sets an explicit duplicate
  window and resolves duplicate publish acknowledgements back to the already
  stored record. This hardens the future consume-command / append-event /
  commit-command-offset retry path so a retried event append does not advance a
  logical partition offset twice.
- Command-log production is now idempotent for actor-scoped client receipts.
  Memory and broker-shaped command logs return the original command record when
  the same partition/actor/`cid` is produced again with the same command payload,
  reject conflicting receipt reuse, and leave the command tail unchanged on
  retries. The NATS command log uses the same receipt as `Nats-Msg-Id`, sets a
  duplicate window on the stream, and resolves duplicate publish acknowledgements
  back to the stored command record. This moves gateway retries closer to the
  authoritative command-log behavior instead of relying on downstream SQL
  idempotency to clean up duplicate command records.
- Command-log workers now heartbeat their partition lease while a command is
  executing, with the refresh cadence configurable by
  `-command-log-worker-claim-refresh-interval` and defaulting from the claim
  TTL. If a heartbeat observes that another writer owns the partition, the
  worker waits for the in-flight SQL-backed execution to finish but does not
  commit the command offset; replay then relies on the existing command
  idempotency bridge. Tests cover both long-running execution that stays owned
  and execution where the heartbeat loses ownership before commit.
- Command-log outcome finalization now refreshes ownership after command
  execution and before writing `applied`, `failed`, or `retrying` receipts. A
  worker that loses assignment or claim after SQL execution but before
  finalization leaves both the receipt and command offset untouched, so the new
  owner replays through the idempotency bridge and owns the visible outcome.
- Added a `CommandLogFinalizer` boundary inside the writer drain loop. The
  default finalizer preserves today's SQL receipt behavior while the worker
  still owns guarded offset commits; tests also exercise a transaction-style
  finalizer that appends to the broker-shaped event log and commits the command
  offset itself. This gives the future Redpanda/Kafka writer a concrete hook for
  replacing SQL materialization with append-events-plus-offset-commit semantics.
- Added a first-class `CommandEventTransactionStore` plus
  `CommandEventTransactionFinalizer`. Successful command decisions can now be
  represented as a broker transaction that appends decided events and advances
  the consumed command offset through one boundary; the memory broker
  transaction client validates the same shape for local promotion fixtures and
  leaves the command offset untouched when event decisions are invalid.
- Added a NATS JetStream `CommandEventTransactionStore` adapter for staging the
  same boundary against broker-backed logs. JetStream does not provide the final
  cross-log atomic transaction, so this adapter appends decided events first and
  relies on stable event ids plus idempotent broker appends to replay safely if
  the command-offset commit fails. Focused tests prove event append happens
  before commit, replay resolves the already-appended event, and invalid batches
  fail before any event or commit is written.
- Added the first native command-log decision executor for basic
  `createThread`. It validates the command against current SQL board/user state,
  emits deterministic `thread.new` and `post.appended` `EventAppend` records,
  and lets `CommandEventTransactionFinalizer` append those events plus commit
  the consumed command offset without writing SQL projections. Basic
  `appendPost` decisions are now covered too: plain replies and safe directed
  replies can produce deterministic broker `post.appended` events after the
  target thread is visible in SQL projections, including mention-bearing posts,
  poll-bearing posts from trusted users, quoted replies, deterministic random
  signature snapshots, inline post attachment metadata, and metadata-only
  standalone `attachPost` commands on boards that allow attachments. Standard
  `editPost` commands now produce deterministic broker `post.edited` events and
  rely on event-store projection for body and search-index catch-up. Standard
  `setPostFlag` commands produce deterministic broker `post.flags_set` events
  with native curator, author, and board-thread moderation checks. Standard
  single-post `redactPost` and `restorePost` commands produce deterministic
  broker `post.redacted` and `post.restored` events with native author-window
  and board-post moderation checks. Standard `redactPostRange` and
  `restorePostRange` commands produce deterministic range `post.redacted` and
  `post.restored` event batches with native board-post moderation checks and
  moderator recycle-bin projection semantics. Standard `clearBoardJunk`
  commands produce deterministic `post.deletion_cleared` batches for selected
  or whole-board junk-bin clears. Standard `purgePost` commands produce
  deterministic `post.purged` events and broker projection clears post bodies,
  resident/latest feeds, and local FTS rows. Standard
  `setThreadTitle`, `lockThread`, and `moveThread` commands produce
  deterministic broker `thread.title_set`, `thread.locked`, and
  `thread.moved` events with native board-thread moderation checks. Native
  `createThread`/`appendPost` admission now mirrors
  SQL board policy for read-only boards, member-scoped boards, and moderator
  bypasses for locked or no-reply threads/posts.
  Directed replies validate the parent post, preserve SQL's one-level
  reply-tree flattening, produce deterministic `mail.sent` events for
  article mail-back replies, and rely on the broker projection `post.committed`
  outbox job for reply, mention, and watched-thread notification recovery. The
  broker projection also creates deterministic pending relay deliveries from
  native `post.appended` events on relay-enabled boards. Native anonymous
  `createThread`/`appendPost` decisions now mirror SQL board policy, expose
  public `Anonymous` post identity, and carry hidden commit actor metadata for
  projection-side activity and notification recovery. Standard
  `createBoard` commands now produce deterministic `board.created` events with
  native admin, slug, parent-category, position, duplicate-board, and
  board-partition projection handling; already-projected matching boards are
  accepted as idempotent retries so append-events/command-offset recovery can
  replay safely after projection catch-up. Standard `setBoardSettings`
  commands now produce deterministic final-state `board.settings_set` events
  with native board-partition, delegated board-settings permission, projection
  replay, and generated syssecurity-board audit handling. Standard
  `setBoardMemberRequirements` commands now produce deterministic final-state
  `board.member_requirements_set` events with native threshold validation,
  approval-mode normalization, delegated board-settings permission, and
  board-partition projection handling. Standard `setBoardModerator` commands
  now produce deterministic `board.moderator_set` events with native admin,
  target-user, position, board-partition, moderator-tenure projection, and
  public-board syssecurity audit handling. Standard `setBoardMember` commands
  now produce deterministic final-state `board.member_set` events with native
  board member-manager permission, delegated-permission safeguards, target-user,
  title, position, and board-partition projection handling. Standard
  `applyBoardMembership` commands now produce deterministic
  `board.member_application_submitted` events, with native board application
  admission checks, counter-store-backed score/mark thresholds, auto-approval
  `board.member_application_reviewed` events, member projection, and sanitized
  Registry-board projection handling. Standard `reviewBoardMembership` commands
  now produce deterministic `board.member_application_reviewed` events with
  native member-manager permission checks, self-review and blacklist safeguards,
  approval admission rechecks, member projection, and sanitized Registry or
  reject-registry projection handling. Standard `leaveBoardMembership` commands
  now produce deterministic final-state `board.member_set` removal events with
  native board validation and retry-safe member projection handling. Standard
  `setRecommendedBoard`
  commands now produce deterministic `board.recommended_set` events with native
  admin, public-board eligibility, note-length, position-defaulting,
  recommendation removal, and board-partition projection handling. Standard
  `setContentFilter` commands now produce deterministic `content_filter.set`
  events with native admin, filter-id, pattern, and board-scope checks. Native
  content-filter matches produce deterministic `post.flagged` review events
  plus sanitized generated Filter-board log events for public boards.
  User-submitted
  `flagPost` and moderator `resolveReview` commands now produce deterministic
  `post.flagged`/`review.resolved` events plus sanitized generated
  0Moderation-board audit log events for public boards. Standard
  `publishPollResult` commands now produce deterministic generated vote-board
  `board.created`, `thread.new`, and `post.appended` records with native poll
  author and delegated board poll-manager checks. Standard `grantRole` and
  `revokeRole` commands now produce deterministic `role.granted`/`role.revoked`
  events plus generated syssecurity-board audit records with native admin and
  target-user checks. Standard `publishStatsSnapshot` commands now produce a
  deterministic `community_stats.snapshot_recorded` event plus generated
  BBSLists board, thread, and post records for the daily stats/ranking family
  with native admin, date, stat-history, and retry-safe existing-thread checks.
  Standard `publishSystemNotice` commands now produce deterministic generated
  notepad/GiveupNotice/bbsnet board, thread, and post records with native admin,
  board-name, title, body, and source checks.
  Standard `sanctionUser` commands now produce deterministic
  `user.sanctioned` events with native moderator, target-user, role, board
  scope, duration, and reason checks, plus generated public denypost-board audit
  records for public board-scoped sanctions. Standard `clearUserSanction`
  commands now produce deterministic idempotent `user.sanction_cleared` events
  with native moderator, target-user, role, board-scope, and reason checks,
  plus generated public undenypost-board audit records for public board-scoped
  clears; generated sanction audit boards are emitted as replay-safe board
  upserts so append-events/commit-command-offset retries remain deterministic
  after projection catch-up.
  Standard `blessUser` commands now produce deterministic `user.blessed` events
  and generated Blessing-board public records with native target-reference,
  self-blessing, ignore-list, and message-length checks.
  Standard `sendDirectMessage` commands now produce deterministic
  `direct_message.sent` events with native recipient-reference, ignore-list,
  friends-only policy, and self-message handling.
  Standard `setDirectMessageSettings` commands now produce deterministic
  `direct_message.settings_set` events with legacy-compatible policy
  normalization and user-partition projection.
  Standard `markDirectMessageRead` commands now produce deterministic
  `direct_message.read` events with recipient-only visibility, no-op handling
  for already-read messages, and sender-partition replay ordering.
  Standard `deleteDirectMessage` commands now produce deterministic
  `direct_message.deleted` events with sender/recipient copy flags, no-op
  handling for already-deleted copies, and sender-partition replay ordering.
  Standard `setUserRelationship` commands now produce deterministic
  `user.relationship_set` events with native user-reference, relationship-kind,
  self-relationship, note-length, upsert, and no-op removal handling.
  Standard `setLoginWatch` commands now produce deterministic
  `user.relationship_set` login-watch events and `notification.created`
  immediate-login events with native friend, target-user, self-watch,
  online-presence, and user-partition projection handling.
  Standard `setBoardFavorite` commands now produce deterministic
  `board.favorite_set` events with native board existence, folder validation,
  position, and board-partition projection handling.
  Standard `createFavoriteFolder` commands now produce deterministic
  `favorite_folder.created` events with native parent-folder validation,
  stable folder IDs, resolved position, and user-partition projection handling.
  Standard `updateFavoriteFolder` commands now produce deterministic
  `favorite_folder.updated` final-state events with native ownership,
  parent-folder, cycle, name, position, and user-partition projection handling.
  Standard `deleteFavoriteFolder` commands now produce deterministic
  `favorite_folder.deleted` events with native ownership, resolved parent, and
  replay-safe child-folder and board-favorite move-up projection handling.
  Standard `moveBoardFavorite` commands now reuse deterministic
  `board.favorite_set` events with native board existence, folder validation,
  and board-partition projection handling. Standard `importFavoriteTree`
  commands now produce deterministic `favorite_tree.imported` final-state
  events with native import validation, stable imported folder IDs,
  duplicate-board collapse, and user-partition projection handling.
  Standard `setBoardZap` commands now produce deterministic `board.zap_set`
  events with native board existence, zap-policy, and board-partition
  projection handling.
  Standard `sendMail` commands now produce deterministic `mail.sent` events
  with native recipient, group, friends-list, mail-all, ignore-list, quota,
  reply-target, sent-copy, attachment-metadata, and generated sysmail checks.
  Standard `forwardMail` commands now produce deterministic forwarded
  `mail.sent` events with native source-copy ownership, subject defaulting,
  forwarded metadata/body formatting, and the same recipient-policy checks as
  native mail sends.
  Standard `mailPostAuthor` commands now produce deterministic contextual
  `mail.sent` events with native source-post visibility, redaction,
  anonymous-author, subject-defaulting, article excerpt, and recipient-policy
  checks.
  Standard `postBoardMail` commands now produce deterministic `thread.new` or
  `post.appended` events with native mail-in gating, board/thread partition
  validation, posting-policy checks, content-type handling, and board
  projection replay for authenticated inbound mail.
  Standard `postMailToBoard` commands now produce deterministic `thread.new`
  or `post.appended` events from actor-visible private mail copies, with
  board/thread partition validation, private-mail transcript formatting, and
  ordinary destination-board posting policy checks.
  Standard `sendDigestEntryMail` commands now produce deterministic archived
  `mail.sent` events with native digest export, member-read visibility,
  subject defaulting, note/body formatting, and recipient-policy checks.
  Standard `curatePost` and `curateThread` commands now produce deterministic
  `digest.entry_upserted` events with native target validation, digest-kind
  normalization, stable entry-id reuse, curator/announcement permission checks,
  and public `Recommend`/`0Announce` mirror thread generation.
  Standard `updateDigestEntry` and `setDigestEntryBody` commands now produce
  deterministic `digest.entry_updated` and `digest.entry_body_set` events with
  native entry lookup, metadata conflict detection, body reset handling,
  curator/announcement permission checks, and board-partition projection
  handling. Standard `removeDigestEntry` commands now produce deterministic
  `digest.entry_removed` events with native curation permission checks and a
  projection tombstone that preserves board/kind metadata for replay-safe
  append-events/command-offset retry.
  Standard `createDigestDirectory` commands now produce deterministic
  `digest.directory_set` events with native board, digest-kind, path,
  curator/announcement permission, stable directory-id, and board-partition
  projection handling.
  Standard `copyDigestPath` commands now produce deterministic
  `digest.path_copied` events with native archive-kind defaulting, subtree
  source ordering, stable copied entry/directory ids, conflict detection, and
  retry-safe handling when a copied destination is nested under the source path.
  Standard `moveDigestPath` and `deleteDigestPath` commands now produce
  deterministic `digest.path_moved` and `digest.path_deleted` events with native
  archive-kind defaulting, subtree conflict validation, and event-id keyed
  mutation-result projection metadata so retries preserve the original moved or
  deleted subtree count after source rows have moved or disappeared.
  Standard `setMailGroup` commands now produce deterministic `mail.group_set`
  events with native group identity, name-conflict, member-resolution,
  self-member, size-limit, and account-partition projection checks. Standard
  `deleteMailGroup` commands now produce deterministic `mail.group_deleted`
  events with native group ownership checks and event-id keyed deletion
  tombstones so retries preserve the original deleted group id after projection.
  Standard `updateMail` commands now produce deterministic `mail.copy_updated`
  events with native mailbox normalization, per-user copy visibility,
  restore-quota, and sender-partition replay ordering.
  Standard `deleteMail` commands now produce deterministic `mail.copy_updated`
  trash-move events with native per-user copy visibility and sender-partition
  replay ordering.
  Standard `deleteMailRange` commands now produce deterministic
  `mail.copy_updated` trash-move events with native range normalization,
  all-or-nothing copy visibility checks, and sender-partition replay ordering.
  Native `attachMail` now produces deterministic `mail.attachment_added`
  events with sender-only, attachment-count, quota, metadata, scoped delivery,
  and replay-safe staged mail-blob promotion checks.
  Standard `repostPost` commands now produce deterministic `thread.new` and
  `post.appended` repost threads with native source-board privacy, destination
  board policy, sanction, signature, lineage, relay, and content-filter checks.
  Native `attachPost` validates staged blob availability and lets broker
  projection promote bytes into final attachment storage after inserting
  metadata. Poll-bearing post
  edits remain command-level unsupported in both SQL-backed and native
  executors, so they are treated as validation parity rather than a remaining
  native promotion gap.
- Wired the native command-decision path into `budgied` behind
  `-command-log-worker-executor native`. Dedicated NATS writer nodes can now use
  `CommandLogNativeDecisionExecutor` plus the NATS command/event transaction
  finalizer to append broker events and commit consumed command offsets without
  running the SQL command materializer; SQL-backed execution remains the default
  and unsupported native side effects still fail closed. The transaction
  finalizer can also record retrying/failed command-log receipts, so native
  terminal outcomes remain visible through the existing command status surface.
- Added `MaterializeEventStorePartition`, the first incremental broker-event to
  SQL projection worker boundary. It replays one logical event partition from an
  `EventStore`, applies the existing projection event semantics inside one SQL
  transaction, and advances a source/partition watermark in the same
  transaction. Broker-projected `post.appended` events now enqueue the existing
  `post.committed` outbox job in that same transaction, so post-created trust,
  mention/reply/watch notification, and related post-commit side effects can
  catch up after event projection commits. Native basic `createThread` tests now
  prove the broker-written `thread.new` and `post.appended` events become
  visible in SQL projections without rerunning the SQL command handler, reruns
  are checkpointed no-ops, and partition offset gaps fail closed without
  projection writes.
- Added `EventStoreProjectionWorker`, a repeatable worker loop around the
  partition materializer. It discovers event partitions from the broker-backed
  `EventStore`, drains each partition in bounded batches, keeps looping while a
  batch is saturated, and then ticks on an interval. Focused coverage proves a
  broker partition with `thread.new` plus `post.appended` drains across multiple
  batches into SQL projections and subsequent runs are watermark no-ops.
- Wired that worker into `budgied` behind the explicit
  `-event-store-projection nats` switch. Worker-role nodes now tail a read-only
  JetStream event-log source, apply broker events into SQL projections, and
  checkpoint source/partition offsets through `derived_view_watermarks`.
- Added an explicit command-partition assignment boundary that models broker
  consumer-group ownership separately from the temporary SQL lease table.
  Workers now check assignment before draining, before command execution, during
  execution heartbeats, and before command-offset commits. A deterministic
  `hash-assignment` writer mode lets dedicated writer nodes simulate assignment
  and rebalance without SQL leases; tests prove assigned partitions drain,
  unassigned partitions are skipped, and an in-flight command is not offset-
  committed after assignment is revoked.
- Added a NATS JetStream key-value backed command-partition assigner and
  `budgied -command-log-worker-ownership nats-kv`. Dedicated writer nodes now
  can share a broker-backed assignment group record with CAS-protected
  generation bumps. The worker's existing ownership refresh path observes that
  generation before execution and offset commits, giving IS4 a concrete
  broker-backed rebalance proof point while native broker consumer-group
  assignment remains the final promotion target.
- NATS-KV assignment records now fail closed when persisted or directly
  submitted override entries are malformed or reference owners outside the
  current member set, preventing corrupted broker ownership state from silently
  dropping an intended partition override and falling back to hash assignment.
- Added a broker assignment snapshot boundary for native consumer-group
  adapters. `SnapshotCommandPartitionAssigner` can be fed from future
  Redpanda/Kafka rebalance callbacks, lists only the partitions owned by the
  local writer, and fail-closes partitions absent from the current generation.
  Command-log workers now use that owned-partition list directly when
  available, so native assignment can avoid a global command-partition scan
  while still rechecking ownership before execution and command-offset commits.
- Snapshot command-partition assignment now ignores repeated or stale explicit
  generations instead of rolling ownership backward, so an old broker rebalance
  callback cannot resurrect a previous writer after a newer consumer-group
  generation has been applied.
- Command-log assignment ownership is now visible in metrics. Writer nodes can
  scrape sampled `budgie_command_partition_assigned{kind,key,owner_id}`,
  `budgie_command_partition_assignment_generation{kind,key,owner_id}`,
  `budgie_command_partition_assigned_count`, and
  `budgie_command_log_assignment_losses_total`, making rebalance churn and
  owner/generation drift observable before broker-owned command writers are
  promoted.
- Added an end-to-end NATS-KV assignment rebalance fixture: writer A executes a
  broker command through the real SQL-backed `Core`, observes assignment
  generation loss before committing the command offset, and writer B then
  replays the same command-log record, commits the offset, and proves SQL
  idempotency prevents a duplicate durable event. This covers the core
  crash/rebalance window for the current command-log promotion path without
  needing a live NATS server.
- Added `cmd/budgie-commandlog-loadgen`, a synthetic IS4 writer-drain load
  fixture. It creates board partitions, produces `createThread` commands into a
  broker-shaped command log, drains them through hash-assigned writer workers,
  materializes through the current SQL-backed executor, and emits JSON for
  production throughput, drain throughput, rounds, failures, assignment/commit
  trouble, and max partition lag before/after drain. The shared
  `ops/internet-scale-budgets.example.json` now includes a `commandLogDrain`
  section so this command-log promotion gate can fail staged runs before
  authoritative submitters depend on the writer tier. The same run now emits a
  command-log promotion-readiness report and fails unless every sampled
  partition has zero uncommitted tail lag and every committed command has either
  a processed-command result or a terminal failure receipt, making the current
  non-transactional materialization/offset bridge explicitly promotion-checked.
- `cmd/budgie-commandlog-loadgen -authoritative-submit` now exercises the
  public authoritative enqueue contract as part of the same gate. Setup boards
  are created locally, then measured `createThread` writes go through
  `Core.ExecCmd` with `WithAuthoritativeCommandLog`; submit throughput counts
  only pending command-log acknowledgements, and the existing writer drain plus
  promotion-readiness report proves those queued receipts later materialize.
- `cmd/budgie-commandlog-loadgen -command-log-backend nats` can now run the
  same direct or authoritative-submit writer-drain gate against a real NATS
  JetStream command log instead of only the in-memory broker fixture. This gives
  staging a durable-stream promotion test for command production, writer drain,
  committed offsets, and materialization readiness before public nodes depend on
  `-command-log-authoritative nats`.
- `cmd/budgie-commandlog-loadgen -assignment-mode snapshot-assignment` now
  exercises the native consumer-group ownership shape in the same promotion
  gate. The fixture snapshots produced command partitions, each synthetic writer
  lists only its owned partitions, and tests prove the drain does not require
  the command log to expose a global partition scanner to workers.
- `cmd/budgie-commandlog-loadgen -command-log-worker-executor native` now runs
  the same promotion gate through the broker-native command/event transaction
  finalizer. The gate drains commands into broker events, projects those events
  back into SQL, validates projected thread visibility, and includes an
  `eventProjection` section so staged runs can prove both command offsets and
  event-store projections are caught up before native writer nodes are promoted.
- `cmd/budgie-commandlog-loadgen -replies-per-thread` can now widen that gate
  beyond `createThread`: the runner drains/projects created threads first,
  discovers their projected thread/root-post ids, then enqueues `appendPost`
  commands on the thread partitions. `-directed-replies` makes those replies
  target the root post, proving the safe native directed-reply path and forcing
  promotion readiness to cover both board and thread command partitions.
- The local synthetic native command-log promotion gates now pass with the
  shared scale budget file. A native smoke with directed replies drained 60
  commands and projected 90 events with zero lag. The documented native
  `createThread` gate drained 800 commands and projected 1,600 events with
  promotion readiness true. The documented native directed-reply gate drained
  2,400 commands across 808 board/thread command partitions, projected 3,200
  events, and reported zero command lag or missing materialization.
- The local authoritative native writer shape now passes through the same gate:
  `-authoritative-submit` plus `-assignment-mode snapshot-assignment` drained
  pending-submitted commands through the native command/event transaction path
  with event projection caught up. The larger directed-reply run drained 2,400
  commands across 808 board/thread partitions, projected 3,200 events, and
  reported promotion readiness true with zero lag, assignment loss, or missing
  materialization.
- Added executable coverage for that combined local promotion shape: an
  authoritative native snapshot-assignment fixture now submits pending
  `createThread` and directed `appendPost` commands, drains them through the
  native command/event transaction finalizer, projects broker events back into
  SQL, and verifies command-log promotion readiness across board and thread
  partitions.
- `cmd/budgie-commandlog-loadgen` now has an explicit `-require-postgres`
  staging guard, and native NATS command/event stream gates require a Postgres
  DSN automatically, so durable-stream promotion runs cannot silently fall back
  to a temporary SQLite materialization database. Native NATS load runs also
  fail fast when command and event stream names are accidentally identical.
  The emitted JSON report now includes runtime metadata for command-log backend,
  event-log backend, materialization store, stream names, and durable-staging
  status so saved promotion artifacts are self-describing. The shared example
  scale budget now requires durable staging for command-log drain gates, so
  memory/SQLite fixture reports fail promotion-budget evaluation by default.
- The broker operations runbook now includes the concrete durable native NATS
  command/event staging gate operators must run before promoting NATS-backed
  authoritative command submission or native writer execution. It requires
  `BUDGIE_NATS_URL`, `BUDGIE_POSTGRES_DSN`, distinct command/event streams,
  `-require-postgres`, authoritative submit, snapshot assignment, directed
  replies, and the shared `commandLogDrain` budget, then names the runtime,
  promotion-readiness, event-projection, and materialization-audit JSON fields
  that must prove the run used durable staging and reached zero lag/loss.
- Added `cmd/budgie-commandlog-report-check`, a strict archived-artifact
  verifier for saved `budgie-commandlog-loadgen` JSON reports. Promotion
  reviewers can now re-run the same `commandLogDrain` budget evaluation against
  captured durable staging evidence without rerunning the load fixture, and the
  broker operations runbook includes that re-check in the approval path.
  `requireDurableStaging` now validates the report's runtime shape too, so an
  archived artifact must show a NATS command log, Postgres materialization, a
  recorded command stream, and for native runs a distinct NATS event stream
  instead of passing on the boolean alone.
- Added `scripts/commandlog-native-nats-gate.sh`, an executable wrapper for the
  durable native NATS/Postgres staging gate. It validates the required
  `BUDGIE_NATS_URL` and `BUDGIE_POSTGRES_DSN` inputs, enforces distinct command
  and event streams, writes the archived load report, and immediately re-checks
  it with `cmd/budgie-commandlog-report-check` so promotion evidence is produced
  and verified by one repeatable operator command.
- Command-log load reports now include an `evidence` block with the producing
  tool, budget file, git revision, and git-modified state. The shared
  `commandLogDrain` budget requires that provenance, so archived durable
  staging artifacts must come from `budgie-commandlog-loadgen` on a clean,
  identifiable revision before promotion review can pass.
- The shared `commandLogDrain` budget now also pins
  `requiredReportBudgetFile`, and archived promotion reports include the
  budget file's SHA-256. The report checker compares that hash against the
  checked budget file, so reports cannot be approved if they were generated
  against a different local or relaxed budget path or contents.
- The promoted `commandLogDrain` budget now pins the native staging gate shape:
  fail-closed Postgres requirement, native executor, authoritative submission,
  snapshot assignment, directed replies, and at least two replies per thread.
  It also requires a disposable generated Postgres schema that is not kept, and
  staging command/event streams with `BUDGIE_COMMAND_LOG_LOAD_` and
  `BUDGIE_EVENT_LOG_LOAD_` prefixes, plus at least the documented staging load
  size: eight boards, one hundred commands per board, eight writers, batch size
  twenty-five, and 2400 total commands. Saved reports that used production-like
  streams, a persistent schema, easier command-log fixture shape, or tiny load
  fail archived promotion review even if their
  broker/runtime metadata is otherwise durable.
- Command-log load reports now include redacted NATS and Postgres endpoint
  fields. The promoted budget requires them, so archived evidence can be tied
  back to staging infrastructure without leaking credentials or DSN query
  material.
- The durable native NATS/Postgres staging wrapper now fails before touching
  staging if operators weaken the documented load shape below the promoted
  budget, and it refuses to overwrite an existing archived report unless
  `BUDGIE_COMMANDLOG_GATE_ALLOW_OVERWRITE=1` is explicit. This keeps the
  remaining external staging blocker from consuming a real run on evidence the
  budget would reject or accidentally replacing the report reviewers need.
- The same wrapper now preflights the promoted command/event stream prefixes
  before launching the load, so an operator cannot spend a durable staging run
  on production-like or otherwise budget-rejected JetStream names.
- The wrapper also rejects arbitrary load-generator passthrough flags; operators
  tune the promoted run through documented environment variables, preventing a
  late CLI flag from overriding the native executor, NATS backends, stream
  names, disposable schema, budget, or minimum load shape after preflight.
- The wrapper now pins the promoted budget file itself to
  `ops/internet-scale-budgets.example.json`, so a durable staging run cannot
  pass by having both the load generator and report checker agree on a relaxed
  budget.
- The wrapper now invokes `go run` directly for both the load generator and
  report checker instead of honoring a separate `GO_BIN` override, reducing the
  chance of producing promotion evidence with a substituted toolchain wrapper.
- The wrapper now generates unique default command/event JetStream names under
  the promoted load prefixes, avoiding accidental reuse of stale fixed staging
  streams while still allowing explicit prefixed stream names.
- The broker operations and staging handoff docs now call out the remaining
  JetStream rerun constraint: because the NATS adapters claim
  `budgie.commandlog.>`, `budgie.commandcommit.>`, and `budgie.eventlog.>`,
  one account/domain can host only one active load stream pair at a time. The
  wrapper preserves successful streams for inspection and points operators to a
  fresh isolated account/domain or explicit `nats stream rm` cleanup before a
  rerun.
- The durable native NATS/Postgres staging wrapper now uses the `nats` CLI when
  available to preflight those broad subjects before launching the load. If
  prior `BUDGIE_COMMAND_LOG_LOAD_*` or `BUDGIE_EVENT_LOG_LOAD_*` streams are
  still present, the wrapper fails before touching Postgres or writing a report
  temp file and prints the cleanup commands; accounts without list permission
  continue to the load generator's JetStream validation.
- Added `scripts/commandlog-native-nats-cleanup.sh`, a dry-run-first helper
  that lists streams owning the gate subjects, refuses to delete non-load
  streams, and deletes only `BUDGIE_COMMAND_LOG_LOAD_*` /
  `BUDGIE_EVENT_LOG_LOAD_*` streams when an operator passes `--execute`.
- The gate and cleanup scripts now resolve the `nats` CLI through an explicit
  `NATS_BIN` override, normal `PATH`, or common Homebrew locations. This keeps
  Codex and staging service shells able to use a locally installed CLI for
  preflight and load-stream cleanup even when Homebrew is not on the default
  path.
- The durable staging wrapper now also resolves the Go tool from normal `PATH`
  or common local install locations before touching NATS/Postgres, keeping
  service-shell runs compatible with Homebrew Go without reintroducing a
  `GO_BIN` tool override into promotion evidence generation.
- The wrapper now also requires archived report paths to stay under the ignored
  `artifacts/internet-scale/` tree, preventing promotion evidence runs from
  creating unignored files or awkward local overwrite targets.
- The promoted budget file now has executable coverage that pins the required
  command-log drain gate fields, including durable staging, native
  NATS/Postgres shape, provenance, minimum load, event projection throughput,
  and zero-lag/loss/failure thresholds.
- Command-log load reports now record the effective NATS JetStream replica
  counts for the command and native event streams. The promoted budget requires
  both counts to be present and at least one replica, so archived staging
  evidence proves the stream replication shape used by the gate.
- The durable native NATS/Postgres staging wrapper now exposes
  `BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS` and
  `BUDGIE_COMMANDLOG_GATE_EVENT_REPLICAS`, so remote/shared staging can request
  the intended JetStream replication factor while the wrapper still rejects
  non-positive replica overrides before starting the load.
- The wrapper now also preflights git cleanliness before launching the durable
  load, matching the promoted report-evidence budget's
  `evidence.gitModified == false` requirement before any staging broker or
  Postgres state is touched.
- The wrapper now writes the load output to a temporary report, verifies that
  temporary artifact against the shared budget, and only then moves it to the
  archived report path. Failed load or checker runs therefore cannot leave a
  partial or budget-rejected report in the promotion evidence location.
- A disposable localhost NATS/Postgres durable gate passed at
  `3492456b299a06b39cab031d3dfbb0530a50a970`: 2,400 commands submitted and
  drained through NATS, 3,200 broker events projected into Postgres, zero lag,
  zero failed commands, zero missing materialization, command stream replicas
  `1`, event stream replicas `1`, and clean provenance in the archived report.
  This removes the local durable-path blocker; remote/shared staging signoff
  still needs scoped credentials and an isolated account/domain or cleaned load
  streams.
- A follow-up local disposable gate also passed at
  `efad5cf4a3731c883e5da7a48114d7ae1ae2b7ce` after exercising the staging
  script NATS/Go resolver paths: 2,400 commands submitted and drained, 3,200
  broker events projected, zero max partition lag, zero failed/retrying/commit
  failures, zero missing materialization, command/event stream replicas `1`,
  clean git provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-efad5cf4.json`.
- After tightening the promoted budget to require exact native broker-event
  counts, the local disposable gate passed again at
  `195b6b55e0a6d0053ec0045e22660aba8bf4f04e`: 2,400 commands submitted and
  drained, `eventProjection.expectedEvents == 3200`,
  `eventProjection.appliedEvents == 3200`, zero max partition lag, zero
  failed/retrying/commit failures, zero missing materialization, command/event
  stream replicas `1`, clean git provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-195b6b55.json`.
- After tightening duplicate event-id handling in the memory and NATS
  command/event transaction adapters, the local disposable gate passed again at
  `be720dedb57c53a55549b09bb8a320f0617aba5b`: 2,400 commands submitted and
  drained, `eventProjection.expectedEvents == 3200`,
  `eventProjection.appliedEvents == 3200`, zero max partition lag, zero missing
  materialization, promotion readiness true, command/event stream replicas `1`,
  clean git provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-be720ded.json`.
- After hardening assignment snapshot and override failure modes, the local
  disposable gate passed again at
  `46e1a622f7ac213b11c2c7574a899b0699dcdb75` using the installed `nats` CLI
  for stream preflight: 2,400 commands submitted and drained, 3,200 expected
  broker events projected into Postgres, zero total/max lag, zero assignment or
  claim losses, zero missing materialization, command/event stream replicas
  `1`, clean git provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-46e1a622.json`.
- After hardening native finalizer ownership and failure-evidence paths, the
  local disposable gate passed again at
  `6a76abdb96662cdea54b92de9423c11377d050f9`: 2,400 commands submitted and
  drained, `eventProjection.expectedEvents == 3200`,
  `eventProjection.appliedEvents == 3200`, zero total/max lag, zero
  terminal/retryable/commit failures, zero assignment or claim losses, zero
  missing materialization, command/event stream replicas `1`, clean git
  provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-6a76abdb.json`.
- The shared `commandLogDrain` scale budget now covers native event projection
  throughput/duration and command ownership claim losses, so staged native
  writer gates fail if commands drain but broker event projection is missing,
  slow, or incompletely covered.
- Native command-log load reports now include `eventProjection.expectedEvents`,
  derived from the staged command shape, and the promoted budget requires both
  the recorded expected count and actual projected event count to match that
  derived value. Archived reports therefore cannot pass by projecting only one
  broker event per command when the native `createThread` path should emit both
  thread and root-post events.
- Added a stricter remote/shared staging budget,
  `ops/internet-scale-remote-staging-budgets.example.json`, and a reusable
  `requireNonLocalRuntimeEndpoints` budget guard. Local disposable evidence can
  still pass the default promoted budget, while shared staging signoff can now
  reject archived reports whose redacted NATS or Postgres endpoints are
  localhost/loopback.
- The durable staging wrapper now has an explicit remote signoff mode:
  `BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1` selects the remote budget for both
  report generation and verification and rejects loopback NATS/Postgres inputs
  before the load starts. Remote reports therefore carry matching
  `evidence.budgetFile`/`budgetSha256` values for the stricter re-check instead
  of being generated against the local-capable budget.
- Native command-log load gates now fail closed when the broker event projection
  partition sample is truncated. The projection stage probes event partitions
  with a limit overflow check, reports `partitionLimitExceeded`, and the shared
  budget treats that as a hard violation, preventing staged native writer runs
  from passing after projecting only part of the broker event stream.
- The runtime `EventStoreProjectionWorker` now uses the same broker event
  partition overflow check, returning and logging a projection-pass failure when
  its partition limit does not cover every broker event partition instead of
  silently materializing a prefix.
- The NATS command/event transaction adapter now has explicit replay coverage
  for ambiguous multi-event append failures: if a generated command stores part
  of a broker event batch but fails before committing the command offset, retry
  resolves the already-stored event IDs, finishes the batch, and then commits
  the consumed command offset without duplicate events.
- The broker command/event transaction store now validates successful client
  results before marking a command applied: returned event messages must match
  the decided event batch one-for-one, duplicate event IDs inside one decision
  are rejected before any broker call, and missing or mismatched broker events
  fail closed instead of recording a misleading applied receipt.
- The broker command/event transaction client contract now returns an explicit
  committed command offset, and the shared transaction store rejects successful
  broker responses that did not advance at least to the consumed command. This
  pins the future Redpanda/Kafka adapter contract to prove both event appends
  and command-offset advancement before a native writer records success.
- Transaction results now also name the committed command partition. The shared
  store rejects successful broker responses that advance a different partition,
  closing another adapter-contract gap before broker consumer-group assignment
  replaces local staging ownership.
- Transaction results now also require scalar replay evidence for every
  returned broker event. The shared store rejects successful broker responses
  whose decoded event has no positive `Seq`, while still accepting an explicit
  `CompatibilitySeq` when a future Redpanda/Kafka adapter cannot use a single
  stream sequence as the scalar replay cursor.
- After adding scalar replay-evidence validation, the local disposable
  NATS/Postgres gate passed again at
  `ba041a38c6616aa24a59ddf423747359db26cbe1` using the installed `nats` CLI
  for stream preflight: 2,400 commands submitted and drained, 3,200 expected
  broker events projected into Postgres, zero max partition lag, zero
  terminal/retryable/commit failures, zero assignment or claim losses, zero
  missing materialization, command/event stream replicas `1`, clean git
  provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-ba041a38.json`.
- Broker-native transaction validation now requires returned event scalar replay
  cursors to increase in the decided event order. Future Redpanda/Kafka
  adapters can still use explicit `CompatibilitySeq` values, but they must
  provide a deterministic ordered scalar cursor instead of returning an
  out-of-order replay batch that only happens to contain every event.
- After ordered scalar replay validation, the local disposable NATS/Postgres
  gate passed again at `479131229cd9ab4fa136d37e47dbd265e70d94f1`: 2,400
  commands submitted and drained, 3,200 expected broker events projected into
  Postgres, zero max partition lag, zero terminal/retryable/commit failures,
  zero assignment or claim losses, zero missing materialization, command/event
  stream replicas `1`, clean git provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-47913122.json`.
- Broker-native transaction validation now also requires returned logical event
  partition offsets to increase for repeated events on the same partition. A
  future broker adapter therefore cannot prove scalar replay order while hiding
  stale or reordered per-partition event evidence inside the same transaction.
- After returned partition-offset validation, the local disposable
  NATS/Postgres gate passed again at
  `0c5910a53377e09a1d3c44a152c957bbaa5c5280`: 2,400 commands submitted and
  drained, 3,200 expected broker events projected into Postgres, zero max
  partition lag, zero terminal/retryable/commit failures, zero assignment or
  claim losses, zero missing materialization, command/event stream replicas
  `1`, clean git provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-0c5910a5.json`.
- Added the first compiled Redpanda/Kafka-facing command/event transaction
  boundary in `internal/kafkaconn`. The dependency-free adapter maps logical
  command/event partitions to deterministic Kafka keys, begins a transactional
  append/offset-commit unit, appends decided broker events, commits the consumed
  command offset for the writer consumer group, and aborts pre-commit failures.
  Unit tests cover key round-trips, shared store validation, append and
  offset-commit aborts, ambiguous transaction commit failure handling, and
  invalid-batch rejection before a transaction begins.
- The command-log load gate now recognizes `kafka` and `redpanda` as the
  Redpanda/Kafka backend target and fails closed with an explicit pending
  live-adapter error. Staging operators therefore cannot accidentally run a
  Kafka-named gate through memory/NATS semantics or lose the target behind a
  generic unsupported-backend failure.
- `budgied` now applies the same fail-closed recognition to command-log shadow,
  authoritative command-log submit, command-log writer, and event-log shadow
  backend flags. `redpanda` normalizes to the pending `kafka` target, but
  startup exits before any worker role can accidentally run with substitute
  memory/NATS semantics.
- After the Kafka/Redpanda backend recognition hooks, the local disposable
  NATS/Postgres gate passed again at
  `3e49ce50f8ea894e27d4f16696c31ae1977957e7`: 2,400 commands submitted and
  drained, 3,200 expected broker events projected into Postgres, zero max
  partition lag, zero terminal/retryable/commit failures, zero assignment or
  claim losses, zero missing materialization, command/event stream replicas
  `1`, clean git provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-3e49ce50.json`.
- The Kafka/Redpanda transaction boundary now requires append calls to return
  explicit topic/key evidence plus a decodable broker event message before the
  consumed command offset is committed. The boundary rejects wrong-topic,
  wrong-key, wrong-partition, missing logical-offset, or missing scalar-sequence
  evidence and aborts the transaction before the offset commit point.
- Added a Kafka/Redpanda command-partition assignment translator that maps
  owned Kafka topic partitions back to Budgie logical command partitions using
  the same deterministic logical partition key and Kafka-compatible key hash.
  A future consumer-group rebalance callback can now feed the existing
  `SnapshotCommandPartitionAssigner` without scanning unowned partitions or
  inventing a separate writer-lease path.
- Added a dependency-free Kafka/Redpanda rebalance adapter that applies
  consumer-group assignment callbacks directly to
  `SnapshotCommandPartitionAssigner`. Assignment callbacks now have a compiled
  path to publish owned logical partitions for a broker generation, and revoke
  callbacks publish an empty fail-closed snapshot so a stale writer stops
  draining before the future live broker client is wired in.
- Added shared Kafka/Redpanda runtime-config validation for the server and
  command-log load gate. Kafka-named backends now require an explicit broker
  list and validate the command topic, event topic, and writer consumer group;
  native command/event runs reject shared command/event topics before reaching
  the intentional live-adapter-pending stop.
- Introduced the first live Kafka/Redpanda client dependency and writer-client
  factory using franz-go. The factory builds a transactional command-writer
  client with explicit seed brokers, command topic consumption, writer consumer
  group, disabled auto-commit, read-committed fetches, Kafka-compatible keyed
  partitioning, and rebalance callbacks that update or fail-close the snapshot
  command-partition assigner.
- Added a Kafka command-record codec and source-position layer that stores
  command records without pretending to know a preassigned Budgie offset, then
  hydrates command messages from the produced/fetched Kafka topic partition and
  physical offset. This makes the upcoming live command-log adapter carry the
  physical commit offset explicitly instead of hiding it behind the logical
  partition-oriented `CommandLog` interface.
- Promoted command source positions into the core command-log record shape.
  `CommandLogRecord` can now carry optional physical broker evidence, and core
  validation checks that source backend/topic, physical commit offset, logical
  partition, and logical offset match the record before a Kafka-backed writer
  uses that evidence for offset commits.
- Threaded command source positions through the command/event transaction
  boundary. `CommandEventTransactionFinalizer` now forwards a record's broker
  source evidence into `CommandEventTransaction`, the broker transaction store
  validates it before calling an adapter, and the Kafka transaction seam commits
  the physical command topic partition/offset while returning Budgie's logical
  partition/offset as writer progress evidence.
- After the source-position transaction boundary, the local disposable
  NATS/Postgres gate passed again at
  `400c7ca3116c112381278d31b865d5602f6f3fce`: 2,400 commands submitted,
  drained, and applied, zero terminal/retryable/commit failures, zero
  assignment or claim losses, zero max promotion lag, zero missing
  materialization, command/event stream replicas `1`, clean git evidence, and
  budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-400c7ca3.json`.
- Added the first Kafka/Redpanda command-log adapter boundary. It implements
  `core.CommandLog` directly so fetched command records retain Kafka
  source-position evidence, maps Budgie logical partitions to deterministic
  Kafka topic partitions for fetch/commit requests, delegates partition
  discovery to a candidate lister, and fails closed when physical partition
  configuration is missing. The worker now permits sparse command offsets only
  when a record carries valid source-position evidence, preserving contiguous
  offset checks for memory/NATS logs while allowing Kafka physical offsets to
  skip over other logical keys in the same topic partition.
- Added the first franz-go-backed command-log client wrapper for that adapter.
  It produces command records through `ProduceSync`, polls command-topic records
  through the writer consumer group, commits group offsets with
  `CommitOffsetsSync`, and reads committed offsets from franz-go group state.
  The wrapper buffers by physical topic partition and only returns records that
  are safe at the physical offset prefix, so one logical partition cannot commit
  past an earlier unprocessed logical key that shares the same Kafka partition.
- Wired the Kafka/Redpanda command-log adapter into
  `cmd/budgie-commandlog-loadgen` for SQL-executor and authoritative command-log
  runs. The load gate now builds a live franz-go command-log client when
  `-command-log-backend kafka|redpanda` is selected, requires
  `-kafka-command-partitions` for deterministic logical-to-physical mapping,
  and still fails closed for native Kafka command/event transactions until the
  event-log transaction adapter exists.
- Added a loadgen-local command-log partition index for Kafka/Redpanda runs.
  The synthetic gate now derives logical partition candidates and lag from
  produced/fetched/committed records inside the loadgen process, instead of
  expecting broker metadata to discover Budgie logical keys. This unblocks
  SQL-executor and authoritative Kafka command-log load gates while leaving the
  cross-process server-side candidate source as a separate production wiring
  task.
- Added the durable server-side command-log partition index needed for
  Kafka/Redpanda command-log writers. `core.IndexedCommandLog` records logical
  partition candidates before produce, records exact tails after produce/fetch,
  and records committed progress after commits into
  `command_log_partition_offsets`. `budgied` can now open Kafka/Redpanda
  command-log shadow, authoritative submit, and SQL-executor writer backends
  with `-kafka-command-partitions`, while native Kafka command/event execution
  still fails closed until the event transaction adapter is wired.
- After the server-side command-log partition index landed, the local
  disposable NATS/Postgres gate passed again at
  `74bbfc061028b940de8de48255e7e4a07d544b6b` using the installed Homebrew
  `nats` CLI through `NATS_BIN`: the prior disposable load streams were already
  captured in an archived report, then cleaned up with the dry-run-first helper;
  the fresh run submitted, drained, and applied 2,400 commands, projected 3,200
  expected broker events into Postgres, reported zero max partition lag, zero
  terminal/retryable/commit failures, zero assignment or claim losses, zero
  missing materialization, command/event stream replicas `1`, clean git
  provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-74bbfc06-nats-cli.json`.
- Added the first franz-go-backed Kafka/Redpanda command/event transaction
  runtime. It wraps a `GroupTransactSession`, delegates logical event-position
  allocation to an injected durable allocator, produces encoded broker event
  records to the event topic, rewrites the transactional offset commit request
  to the single consumed command topic partition/offset, and only advances the
  local franz group offsets after `End(TryCommit)` reports success. Native
  Kafka command/event execution remains fail-closed until durable event-position
  allocation and server/loadgen wiring are connected.
- Added a SQL-backed Kafka/Redpanda event-position allocator. It reserves
  broker event scalar compatibility cursors from a new
  `event_scalar_offsets` row seeded from `MAX(events.seq)`, reserves logical
  partition offsets from `event_partition_offsets`, and returns durable
  `{partition, partitionOffset, CompatibilitySeq}` allocations before the
  franz transaction appends event records. SQLite schema helpers and Postgres
  migration v70 create/seed the scalar reservation table.
- Added the first Kafka/Redpanda event-log replay adapter. It decodes encoded
  broker event records from the Kafka event topic, maps Budgie logical event
  partitions to deterministic Kafka physical partitions, buffers interleaved
  logical keys that share one physical partition, and exposes a broker-shaped
  `EventStore` for projection replay while direct event appends fail closed
  behind the command/event transaction boundary. The SQL event-position
  allocator now also exposes durable event partition listing and scalar head
  evidence from the same reservation tables, giving the Kafka event projector a
  concrete partition catalog; server/loadgen wiring remains the next slice.
- After the Kafka event replay adapter slice, the local disposable
  NATS/Postgres gate passed at `d14bcfd3ba786370be3f8e99213229f0c38f23b8`
  using `NATS_BIN=/opt/homebrew/bin/nats`: 2,400 commands submitted and
  drained, 3,200 expected broker events projected into Postgres, zero max
  partition lag, zero terminal/retryable/commit failures, zero assignment or
  claim losses, zero missing materialization, command/event stream replicas
  `1`, clean git provenance, promotion readiness true, and budget-accepted
  report
  `artifacts/internet-scale/commandlog-native-nats-report-d14bcfd3-kafka-event-replay.json`.
- Wired the Kafka/Redpanda native command/event path into the server and load
  gate. Native Kafka loadgen runs now build one franz transactional command
  writer session, reuse that client for command fetch/commit evidence, bind the
  SQL-backed event-position allocator once the materialization DB exists, and
  replay Kafka event records through a broker-shaped event store. `budgied`
  now supports `-command-log-worker kafka -command-log-worker-executor native`
  and `-event-store-projection kafka`, with explicit command/event partition
  counts and fail-closed validation for shared or missing Kafka runtime
  settings.
- After wiring Kafka/Redpanda native server/loadgen paths, the local disposable
  NATS/Postgres gate passed at
  `5218d2a8f6ac2ca1d73b56e63cbeb298b9ccfc0c` using
  `NATS_BIN=/opt/homebrew/bin/nats`: 2,400 commands submitted, drained, and
  applied, 3,200 expected broker events projected into Postgres, zero max
  partition lag, zero terminal/retryable/commit failures, zero assignment or
  claim losses, zero missing materialization, command/event stream replicas
  `1`, clean git provenance, promotion readiness true, and budget-accepted
  report
  `artifacts/internet-scale/commandlog-native-nats-report-5218d2a8-kafka-native-wiring.json`.
- Wired Kafka/Redpanda event-log shadow mode. `budgied -event-log-shadow
  kafka|redpanda` now opens an append-capable franz event-log shadow client,
  requires explicit event-topic partition count, mirrors SQL-primary event id,
  timestamp, scalar compatibility sequence, and logical partition offset into
  Kafka records, and reuses the Kafka event replay adapter for parity windows.
  The native Kafka event-log replay adapter remains direct-append fail-closed;
  only already-durable SQL shadow events can use this non-transactional mirror
  path.
- Extended `budgied -check-event-log-promotion-readiness` beyond the original
  NATS-only source. The one-shot still defaults to the historical NATS shadow
  candidate, but `-event-log-shadow kafka|redpanda` now opens the Kafka event
  replay store with SQL-backed event-position partition/head evidence and runs
  the same SQL-vs-candidate promotion report. Kafka/Redpanda event shadows and
  native event logs therefore have an explicit promotion-readiness target before
  the broker event log can become authoritative.
- After the Kafka/Redpanda event-log promotion readiness slice, the local
  disposable NATS/Postgres gate passed on
  `eb60e4e76f43dab14c2a6dcba29d0a28d0f3cc4c` with the installed NATS CLI
  preflight active. The run used generated disposable streams
  `BUDGIE_COMMAND_LOG_LOAD_20260613122534_95636` and
  `BUDGIE_EVENT_LOG_LOAD_20260613122534_95636`, submitted, drained, and
  applied 2,400 native commands, projected 3,200 expected broker events into
  Postgres, reported zero max lag, zero terminal/retryable/commit failures,
  zero assignment or claim losses, zero missing materialization, command/event
  stream replicas `1`, clean git provenance, promotion readiness true, and a
  budget-accepted local report
  `artifacts/internet-scale/commandlog-native-nats-report-eb60e4e7-kafka-promotion-readiness.json`.
- The promoted command-log drain budget and loadgen report contract are now
  broker-aware. Existing NATS budgets explicitly pin NATS command/event
  backends, while the new Kafka/Redpanda budget pins Kafka command/event
  backends, redacted broker evidence, generated command/event topic prefixes,
  and minimum command/event topic partition counts. Kafka native reports can
  therefore be evaluated by the same zero-lag, zero-loss, exact-event-count
  promotion gate once a Redpanda/Kafka staging broker is available.
- Added `scripts/commandlog-native-kafka-gate.sh`, a Redpanda/Kafka sibling to
  the promoted NATS gate wrapper. It pins the Kafka budget file, generates
  disposable `budgie.commands.load.*` and `budgie.events.load.*` topics plus a
  consumer group, requires at least 32 command/event topic partitions, enforces
  the same native authoritative directed-reply load shape, archives only
  report-check-passing artifacts under `artifacts/internet-scale/`, and adds a
  remote Kafka budget that rejects loopback broker/Postgres evidence.
- Added explicit Kafka load-topic setup/preflight to the load generator. Before
  opening franz runtime clients, Kafka/Redpanda load runs create the generated
  command/event topics with the requested partition counts and
  `-kafka-topic-replicas`, accept already-existing topics only after a metadata
  check with auto-create disabled, and fail early if an existing topic is below
  the promoted partition floor. This prevents broker auto-create defaults from
  producing misleading internet-scale gate evidence.
- After the Kafka load-topic preflight slice, the local disposable
  NATS/Postgres gate passed on
  `8201d2bc28ac9d128628d25d65e060fda5ef144c` with the installed NATS CLI
  preflight active. The run first cleaned up the prior disposable load streams,
  then used generated streams
  `BUDGIE_COMMAND_LOG_LOAD_20260613130309_20072` and
  `BUDGIE_EVENT_LOG_LOAD_20260613130309_20072`, submitted, drained, and applied
  2,400 native commands, projected 3,200 expected broker events into Postgres,
  reported zero max lag, zero terminal/retryable/commit failures, zero
  assignment or claim losses, zero missing materialization, command/event
  stream replicas `1`, clean git provenance, promotion readiness true, and
  budget-accepted local report
  `artifacts/internet-scale/commandlog-native-nats-report-8201d2bc-kafka-topic-preflight.json`.
- Added a dry-run-first Kafka/Redpanda load-topic cleanup path. The new
  `cmd/budgie-kafka-load-topic-cleanup` command lists broker topics through the
  franz-go admin path, selects only `budgie.commands.load.*` and
  `budgie.events.load.*`, and deletes those topics only when requested. The
  `scripts/commandlog-native-kafka-cleanup.sh` wrapper gives staging operators
  the same inspect-then-`--execute` workflow as the NATS load-stream cleanup,
  without requiring `rpk` or deleting production-like topics. The wrapper now
  exposes `BUDGIE_COMMANDLOG_KAFKA_CLEANUP_TIMEOUT`, so slower shared staging
  brokers can use the same bounded metadata window for cleanup dry runs and
  execute passes.
- Added Kafka/Redpanda TLS and SASL runtime configuration for remote staging.
  `budgied`, `cmd/budgie-commandlog-loadgen`, the Kafka topic preflight path,
  and the Kafka load-topic cleanup command all honor `BUDGIE_KAFKA_TLS`,
  optional CA/server-name overrides, and SASL `plain`, `scram-sha-256`, or
  `scram-sha-512` credentials. Load reports expose only non-secret evidence
  (`runtime.kafkaTls` and `runtime.kafkaSaslMechanism`), giving shared staging
  a real credential path without leaking secrets into archived artifacts.
- After the Kafka/Redpanda TLS/SASL slice, the local disposable NATS/Postgres
  gate passed again on `21701f8fbc91289c019bf60278de4cf7a96b5d9f` with the
  installed `nats` CLI preflight active. The run first deleted the prior
  disposable load streams with the dry-run-first cleanup helper, then used
  generated streams `BUDGIE_COMMAND_LOG_LOAD_20260613132734_34807` and
  `BUDGIE_EVENT_LOG_LOAD_20260613132734_34807`, submitted, drained, and
  applied 2,400 native commands, projected 3,200 expected broker events into
  Postgres, reported zero max lag, zero terminal/retryable/commit failures,
  zero assignment or claim losses, zero missing materialization, zero missing
  command-log records, command/event stream replicas `1`, clean git
  provenance, promotion readiness true, and budget-accepted local report
  `artifacts/internet-scale/commandlog-native-nats-report-21701f8f-tls-sasl.json`.
- After the broker-aware budget/report contract landed, the local disposable
  NATS/Postgres gate passed again on
  `77c91d93f7a6239aab085b818e0682ae74b61bbb` and recorded the updated promoted
  budget hash `6d271e260f1af9518ddf39740afa22f155a770c4fda65bee935d25ba4cf025e9`.
  The run used generated disposable streams
  `BUDGIE_COMMAND_LOG_LOAD_20260613123832_4289` and
  `BUDGIE_EVENT_LOG_LOAD_20260613123832_4289`, submitted, drained, and applied
  2,400 native commands, projected 3,200 expected broker events into Postgres,
  reported zero max lag, zero terminal/retryable/commit failures, zero
  assignment or claim losses, zero missing materialization, command/event stream
  replicas `1`, clean git provenance, promotion readiness true, and
  budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-77c91d93-broker-aware-budget.json`.
- The local durable NATS/Postgres staging gate caught and verified a NATS
  retry hardening fix in the same slice: JetStream event appends now preserve
  caller-requested offsets across CAS retries instead of treating a previously
  assigned retry offset as explicit caller input. After the fix, the local
  disposable gate passed at `017f794825fabf2c6dbfd0d4f4e63c1707657424` with
  `NATS_BIN=/opt/homebrew/bin/nats`: 2,400 commands submitted, drained, and
  applied, 3,200 expected broker events projected into Postgres, zero max
  partition lag, zero terminal/retryable/commit failures, zero assignment or
  claim losses, zero missing materialization, clean git provenance, promotion
  readiness true, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-017f7948-kafka-event-shadow.json`.
- The shared broker command/event transaction validator now accepts
  adapter-assigned `CompatibilitySeq` values when the command decision did not
  request a scalar cursor, while still rejecting drift when the decision did
  request one. This gives the future Kafka/Redpanda adapter a legal place to
  attach durable scalar replay evidence before the command offset commits.
- After the adapter-assigned transaction sequence contract, the local
  disposable NATS/Postgres gate passed again at
  `b9ec9ed61ec5d7bf8a93b3bdf5c2cdf909027c89`: 2,400 commands submitted and
  drained, 3,200 expected broker events projected into Postgres, zero max
  partition lag, zero terminal/retryable/commit failures, zero assignment or
  claim losses, zero missing materialization, command/event stream replicas
  `1`, clean git provenance, and budget-accepted report
  `artifacts/internet-scale/commandlog-native-nats-report-b9ec9ed6.json`.
- The Kafka/Redpanda transaction boundary now has an explicit logical
  event-position allocation seam. Transaction implementations must allocate
  `{partition, partitionOffset, CompatibilitySeq}` before event append, append
  only the positioned records, and return broker evidence that matches those
  allocated offsets and scalar replay cursors before the command offset can be
  committed.
- Broker-native terminal command failures now record their visible failure
  receipt only after the command/event transaction has advanced the consumed
  command offset. A failed broker commit therefore cannot leave a command marked
  terminal while the command log still requires replay.
- The higher `CommandEventTransactionStore` result now carries the committed
  command partition as well as the offset, and the finalizer rejects missing or
  wrong-partition commit evidence before writing applied or terminal receipts.
  This keeps custom Redpanda/Kafka stores under the same proof contract as the
  broker client adapter.
- `budgied` now fails native NATS writer startup when the command and event
  streams resolve to the same JetStream stream, and when a same-process native
  writer/projector tails a different event stream than the writer appends. Split
  writer and projector nodes remain supported, but compact trials now fail fast
  on stream miswiring before any command offsets are consumed.
- Native writer finalizers now return committed progress even when a visible
  applied or terminal receipt write fails after the broker transaction commits,
  and the worker reports the advanced offset before surfacing that recorder
  error. Staging artifacts therefore show the true broker commit position while
  still failing the run on missing receipt evidence.
- Command-log workers now keep refreshing assignment/claim ownership while a
  command finalizer is running, not only while the command executor is running.
  If broker-style assignment is revoked during finalization, the worker cancels
  the finalizer context and stops without recording applied/failed/retrying
  receipt progress or advancing an uncommitted offset, tightening the native
  consumer-group rebalance contract before Redpanda/Kafka promotion.
- Command-log drain load aggregation now preserves partial worker results before
  surfacing a worker error, so staged reports retain committed offsets, applied
  and terminal counters, retryable/loss samples, and commit-failure evidence
  even when a native writer fails after making broker progress.
- Native command/event transaction-store failures now populate worker
  `CommitFailures` and `CommitFailure` evidence, so load reports and
  `commandLogDrain` budgets count failed event-append/command-offset
  finalization through the same zero-commit-failure gate as direct command-log
  offset commit failures.
- Native retryable command failures now preserve worker retryable-failure
  evidence even when writing the retrying receipt fails. The drain still
  returns the recorder error and leaves the command offset uncommitted, but
  staged load reports can count the retryable command outcome instead of losing
  it behind a generic finalizer error.
- Command-log drain reports now preserve terminal command failure details in
  worker results and sample errors, matching the existing retryable,
  assignment, claim, and commit-failure evidence. Failed staging artifacts can
  now show which partition hit a terminal native decision instead of exposing
  only a terminal-failure count.
- Worker finalizer errors now also carry partition-scoped sample evidence in
  command-log drain reports. Native event-decision and receipt-write failures
  that happen before a command outcome is visible can be tied back to the exact
  logical command partition instead of appearing only as a top-level drain
  error.
- Broker-native transaction decisions now require deterministic event
  timestamps and validate returned broker messages against those timestamps.
  A retry that reuses an event id with the same payload but a different durable
  timestamp now fails closed instead of silently accepting event timestamp drift
  under the command-offset commit window.
- The NATS command/event transaction adapter now also rejects timestamp-less
  broker event records before appending events or committing the consumed
  command offset, so direct adapter use cannot reintroduce wall-clock event time
  under the non-atomic replay window.
- The memory broker reference clients now enforce the same deterministic command
  receipt and transaction event timestamp requirements on direct client calls,
  keeping local promotion fixtures aligned with the durable broker adapters.
- Broker event-log idempotency now treats timestamps as part of event identity.
  Broker appends with explicit event IDs must also provide explicit timestamps,
  and memory/NATS duplicate resolution rejects the same event ID if its durable
  timestamp drifts even when payload and partition data otherwise match.
- Command-log receipt idempotency now treats enqueue time as part of command
  identity. Explicit client receipts must provide deterministic enqueue times,
  and memory/broker/NATS duplicate resolution rejects the same actor-scoped
  `cid` if its enqueue timestamp drifts under gateway retry.
- Authoritative and shadow command-log submitters now derive that enqueue time
  deterministically for explicit client receipts, so retrying the same
  `X-Command-Id` through a gateway reuses the original command-log record
  instead of generating a fresh timestamp that would look like conflicting
  receipt content.
- Added `budgied -check-command-log-promotion-readiness`, a read-only one-shot
  command-log promotion gate that reuses the configured `-command-log-worker`
  backend and exits before starting writer/server loops. For NATS command logs
  it validates the JetStream stream without creating it, prints JSON for lag and
  materialization evidence, and exits nonzero if writers are behind or committed
  offsets outran SQL materialization evidence. The narrower
  `-audit-command-log-materialization` diagnostic remains available for checking
  committed-offset materialization without enforcing tail catch-up.
- Command-log materialization audits and promotion-readiness checks now fail
  closed when the configured partition limit truncates coverage, and the shared
  `commandLogDrain` scale budget enforces promotion readiness, zero lagging
  partitions, zero materialization holes, and full partition coverage. Staged
  command-log load gates therefore cannot accidentally pass on a partial stream
  sample.
- Added guarded authoritative command-log submission behind
  `Core.WithAuthoritativeCommandLog` and
  `budgied -command-log-authoritative memory|nats`. Public nodes can now append
  commands to the broker-owned command log without executing the local SQL
  handler, returning a pending acknowledgement with command id, partition, and
  offset metadata. A real SQL-backed fixture proves the write is invisible in
  projections until a command-log writer drains it, and that retries with the
  same `cid` reuse the original command-log record instead of advancing the
  partition tail.
- Added authenticated command receipt reads for authoritative command-log
  submissions. `GET /api/v1/commands/{commandId}` takes the ack's partition kind,
  partition key, and command offset, then reports `pending`, `retrying` with the
  latest retryable writer error, `applied` with the stored materialized ack
  result, `failed` with the terminal command error, or `committed` when the
  writer checkpoint has passed the offset but no result/error receipt is
  available. Status reads now validate that the requested partition and offset
  contain the same command id before consulting the SQL result cache, so a stale
  or wrong offset cannot masquerade as an applied command. Gateway handlers
  expose the same read-only status surface, closing the loop for regional/live-
  only nodes that queue commands without local SQL execution. Retryable and
  terminal failures are recorded in
  `command_log_receipts`, separate from the success idempotency cache; terminal
  receipts are written before the worker commits the command offset, while
  retryable receipts preserve observability without advancing the offset.
  Successful materializations write an `applied` receipt before command offset
  commit; `committedOffset` on the status response shows whether the broker
  checkpoint has caught up. A later successful materialization clears the prior
  retrying receipt in the same transaction that records the command result,
  keeping retrying receipt metrics scoped to outstanding writer trouble. Receipt
  counts and oldest-age gauges are exported for applied, retrying, and failed
  rows, with an alert for retrying receipts that stop clearing. Browser thread
  creation and reply submission now use the
  receipt endpoint to resolve pending authoritative acks before navigating or
  clearing compose drafts, so regional/live-only clients no longer assume a
  queued write has already materialized. Browser thread moderation, title, and
  lock actions now share the same resolved-command helper so pending
  authoritative receipts are not treated as complete before the writer
  materializes the command result. REST alias mutation helpers now unwrap HTTP
  ack envelopes and resolve pending authoritative receipts before returning
  success, keeping queued broker-log writes from reaching UI code as completed
  mutations. REST aliases with legacy post-command readbacks, including favorite
  imports, now emit the pending ack before reading local state so authoritative
  API nodes cannot return stale materialized payloads. The favorite-tree import
  helper resolves that ack and rereads the tree with the materialized sequence
  before returning its readback-shaped result. Multipart binary attachment
  uploads on authoritative command-log submitters now stage bytes in shared SQL
  blob staging under the future attachment id, enqueue replayable metadata with
  `stagedBlobId`, and let the writer promote the bytes into the final blob table
  in the same transaction that records the attachment event. Missing staged
  bytes produce retryable `blob_staging_required` and leave the command offset
  uncommitted, preserving the write-region/blob-staging boundary for regional
  nodes that do not share staging with writers. Worker-role nodes now prune
  expired staged blob rows in bounded batches under the existing background
  leader election, and scrape-time gauges expose staged blob rows, bytes,
  expired rows, expired bytes, and oldest-row age by post/mail attachment kind.
- Restored NATS broker event partition-offset listing after the shared
  projection drain switched from short-read detection to source-tail
  watermarks. At `6c6dc67746c1dd43f04a29cfa2586f80de2db704`, both local
  disposable durable gates passed from a clean tree: the NATS/Postgres gate
  submitted and drained 2,400 commands, projected 3,200/3,200 expected events,
  reported zero lag/failures/missing materialization, and passed
  `ops/internet-scale-budgets.example.json`; the Kafka/Postgres gate submitted
  and drained 2,400 commands across generated 32-partition command/event topics,
  projected 3,200/3,200 expected events, reported zero lag/failures/missing
  materialization, and passed
  `ops/internet-scale-kafka-budgets.example.json`. This leaves remote/shared
  staging signoff as an environment-evidence step rather than a local durable
  path blocker.
- Extended projection rebuild and derived-view backfill source selection to
  Kafka/Redpanda. `budgied -rebuild-projections -rebuild-source kafka` and
  `budgied -backfill-derived-views ... -rebuild-source kafka` now open the
  same Kafka event replay store used by event projection and promotion
  readiness, require Kafka runtime config plus event topic partition count, and
  run event-log promotion readiness before applying broker-sourced projection
  repair.
- Added the first Redis command-partition index adapter and load-gate plan:
  Redis hashes record monotonic command tail and committed offsets by logical
  partition, the load generator can wrap NATS/Kafka command logs with an
  opt-in Redis index, and the NATS staging gate can pass
  `BUDGIE_COMMANDLOG_GATE_COMMAND_INDEX=redis` with `BUDGIE_REDIS_URL`. This
  targets the remaining tiny-partition drain overhead without making Redis the
  durable command log.
- Provisioned a dedicated remote/LAN Redis 8.8.0 instance for staging evidence:
  the instance uses a staging-scoped password, listens only on loopback plus
  the staging LAN interface, and keeps disposable runtime files outside the
  repo. Local AUTH+PING over the staging LAN succeeded, and the gitignored
  `artifacts/internet-scale/staging.env` records the Redis URL. A Redis-indexed
  remote NATS/Postgres gate using
  `BUDGIE_COMMANDLOG_GATE_COMMAND_INDEX=redis`, batch size 250, and report path
  `artifacts/internet-scale/commandlog-native-nats-report-99d0458d-remote-lan-redis-index-20260614.json`
  failed the remote budgets before archive: submit throughput was
  36.3537 cmd/s, drain throughput was 16.2248 cmd/s, and drain duration was
  147,922 ms. Cleanup removed the generated
  `BUDGIE_COMMAND_LOG_LOAD_20260614041954_35467` and
  `BUDGIE_EVENT_LOG_LOAD_20260614041954_35467` streams plus the two Redis
  index keys for that run.
- Added an opt-in per-writer command partition concurrency knob for the native
  drain experiments. `CommandLogWorker` still defaults to sequential partition
  draining, but `PartitionConcurrency > 1` drains distinct assigned partitions
  concurrently while each individual partition preserves offset order. The
  load generator exposes this as `-partition-concurrency`, `budgied` exposes it
  as `-command-log-worker-partition-concurrency`, and the native NATS/Kafka
  gates expose `BUDGIE_COMMANDLOG_GATE_PARTITION_CONCURRENCY`.
- Added the broker-native multi-command transaction boundary needed for the
  next tiny-partition drain optimization: `CommandEventTransactionBatchStore`
  can commit several logical command partitions through one backend batch when
  the adapter supports it, and falls back to the existing per-command
  transaction path otherwise. The in-memory reference broker and the NATS
  command/event transaction adapter now implement the batch client, flattening
  mixed-partition event appends while returning per-command commit evidence.

## IS5 - Move High-Volume Unordered Traffic Off The Log

**Purpose:** Keep the ordered event log for things that need order.

Reactions, votes, presence, and typing signals can exceed post volume by orders
of magnitude. They must not re-create a hot ordered bottleneck.

### Workstreams

- Move reactions and vote counts to sharded counters or CRDT-backed storage.
- Emit periodic checkpoint or milestone events for durable count recovery.
- Keep per-user reaction identity where product semantics require undo or abuse
  review.
- Move presence and typing to broker-only delivery.
- Reconfirm chat policy:
  - ephemeral live delivery
  - bounded persisted history if needed
  - no durable event-log pollution for high-frequency churn
- Update clients to treat counts as convergent values, not ordered events.

### Progress

- Moved `reactPost`, `unreactPost`, and `votePoll` off the durable ordered event
  append path. These commands now update the SQL side-store for reaction
  identity/counts and poll-vote identity/counts, return a successful ack without
  a durable `seq`, and publish non-durable live events for connected clients.
  Postgres LISTEN/NOTIFY wakeups reconstruct those live events from the
  side-store on sibling nodes, matching the existing chat-line pattern.
- Projection rebuilds now snapshot unordered reaction and poll-vote side-store
  rows before clearing projections, replay durable post/poll structure, restore
  valid unordered rows onto rebuilt posts/polls, and recompute
  `reactions_recv`. This gives the current SQL side-store a concrete recovery
  bridge while sharded counters/checkpoint events remain the later internet-scale
  storage target.
- Added an explicit handler-level `CounterStore` boundary for unordered reaction
  and poll-vote writes. The production adapter is still SQL-backed, but
  `reactPost`, `unreactPost`, and `votePoll` now depend on one replaceable
  counter-store contract for reaction identity, reaction counts, poll vote
  identity, and received-reaction activity counters. Focused handler coverage
  pins that routing so a sharded or CRDT-backed store can replace SQL without
  rewriting the command handlers.
- Refined the `CounterStore` boundary so command handlers no longer pass
  `*sql.DB` or `*sql.Tx` through the counter contract. Stores now expose a
  backend-neutral mutation lifecycle (`BeginMutation`, `Commit`, `Rollback`),
  with SQL transactions hidden inside the SQL adapter. This makes the next
  Scylla/Redis/CRDT counter adapter a store implementation problem rather than
  another command-handler rewrite.
- Added the first non-SQL `CounterStore` implementation,
  `MemoryCounterStore`, with identity-sharded in-memory maps for reaction
  identity/counts, poll vote identity/counts, and received-reaction activity.
  `core.WithCounterStore` can now inject that backend, and core reaction, post,
  poll, and poll-list reads overlay counts from the injected store. Coverage
  proves reaction and poll commands can run without writing SQL counter rows,
  and `Core.CheckpointCounters` snapshots injected-store aggregate counts into
  durable `counter.checkpointed` events.
- Added the first durable coarse counter checkpoint event,
  `counter.checkpointed`, plus a `counter_checkpoints` projection table.
  `Core.CheckpointCounters` records complete post-reaction and poll-option vote
  count snapshots without returning per-click reaction/vote churn to the
  ordered log. Replay materializes those checkpoints back into
  `counter_checkpoints`; coverage proves checkpointed aggregate counts recover
  after unordered side-store rows and the checkpoint projection are deleted.
  Per-user reaction/vote identity still remains a counter-store responsibility.
- Updated the web thread client to treat live reaction events as convergence
  hints instead of authoritative count writes. Reaction live events now preserve
  the current user's local reacted state and schedule a debounced post
  projection refresh for counts, matching the existing poll-vote refresh
  behavior.
- Added worker-leader scheduling for unordered counter checkpoints via
  `budgied -counter-checkpoint-interval`. The scheduler is disabled at `0`,
  runs only under the existing singleton worker leadership in Postgres, and
  focused coverage proves it emits durable `counter.checkpointed` events.
- Added SQL-backed counter shard aggregate tables for unordered reactions and
  poll-option votes behind the existing `CounterStore` boundary. Reaction/vote
  commands maintain shard totals while preserving per-user identity rows for
  undo and moderation; post/poll reads and durable counter checkpoints now
  prefer shard sums with identity-table fallback, and projection rebuild restores
  shard aggregates alongside unordered identity rows.
- Thread ranking, user ranking, community stat, and reaction wakeup reads now
  consume SQL-backed reaction shard aggregates first, with identity-row counts
  retained as the migration fallback. This removes the remaining ranking/stat
  dependency on joining the high-volume reaction identity table for steady-state
  counts.
- Added a durable distributed `CounterStore` candidate backed by NATS
  JetStream KV. `budgied -counter-store nats-kv` stores per-user reaction
  identity, post reaction shard counts, per-user poll votes, poll-option vote
  shard counts, and received-reaction counters outside SQL while preserving the
  same mutation/rollback contract used by command handlers. Focused KV tests
  cover reaction lifecycle, poll-vote replacement, rollback, and a core
  integration path where reaction/vote reads and counter checkpoints are backed
  by the KV store while SQL counter tables remain empty.
- Board membership admission checks for `minScore` and `minBoardMarkCount` now
  read authored-post reaction counts through the active `CounterStore` instead
  of joining SQL reaction identity rows. Focused memory-store coverage proves
  those product rules still auto-approve eligible applications when reaction
  and poll-vote traffic is backed by a non-SQL store with empty SQL counter
  tables.
- Hard account deletion now snapshots and purges the deleted user's unordered
  counter identity through the active `CounterStore`: reaction identity,
  poll-vote identity, aggregate reaction/vote shards, and received-reaction
  counters converge in both SQL and non-SQL stores. Memory and NATS KV coverage
  pins the cleanup path so account/private-state deletion does not leave
  high-volume side-store identity behind.
- High-volume unordered commands now bypass both shadow and authoritative
  command-log submission. `sendChatLine`, `setPresence`, `reactPost`,
  `unreactPost`, `votePoll`, read-marker mutations, and `setThreadPref` execute
  immediately through their chat, presence, counter-store, read-marker, or
  thread-preference side-store paths, preventing the IS4 command log from
  becoming the new ordered bottleneck for IS5 traffic. Coverage proves presence,
  read-marker, and thread-preference updates still materialize locally under
  authoritative command-log mode while no command-log record is produced.
- `typing` presence updates are now broker-only live hints. They still emit
  ephemeral `presence.update` events for subscribed gateways, but they no longer
  overwrite `user_presence_sessions`, appear in online rosters, satisfy login
  watches, or accrue online-time/stat-history writes. This leaves ordinary
  active/idle/offline presence available for online-user reads while removing the
  highest-frequency typing churn from SQL.
- Identical online authenticated and guest presence pings are now coalesced
  within a short write window. Status changes, location changes, hidden/offline
  transitions, and post-window refreshes still persist immediately, while rapid
  keepalive-style pings avoid repeated roster/stat-history writes and the next
  persisted ping catches up online-time accrual.
- Added a backend-neutral `PresenceStore` boundary for authenticated
  session-roster writes, guest session writes, online-roster reads, and live
  presence stats. SQL remains the default store, while `MemoryPresenceStore`
  proves `setPresence` can update and serve online rosters without writing
  `user_presence_sessions` or `user_presence`; focused coverage also keeps
  login-watch delivery and typing-as-live-only behavior intact under the
  injected store.
- Added the first durable distributed `PresenceStore` candidate backed by NATS
  JetStream KV. `budgied -presence-store nats-kv` stores one user/session
  roster record per authenticated presence session and one anonymous guest
  record per guest session in a TTL-backed KV bucket, filters online rosters
  and chat-room rosters from that bucket, decorates authenticated users from SQL
  profile/relationship tables at read time, supplies chat-room online counts,
  and overlays live community stats with online
  user/guest counts plus guest login/logout totals from KV. Coverage proves
  command presence, guest presence, online reads, login watches, coalescing,
  hidden presence, typing-as-live-only behavior, chat rosters, and community
  stats while SQL roster/guest-counter tables remain empty.
- Added a backend-neutral `ChatStore` boundary for high-volume unordered chat
  history. SQL remains the default bounded-history store, while
  `MemoryChatStore` proves `sendChatLine`, chat room listing, and recent-line
  reads can run without writing `chat_lines` or per-room SQL rows. This keeps
  the existing live `chat.line` behavior and command-log bypass intact while
  giving broker/KV chat history adapters a narrow contract to replace.
- Added the first durable distributed `ChatStore` candidate backed by NATS
  JetStream KV. `budgied -chat-store nats-kv` stores bounded recent chat
  transcript records and room metadata outside SQL while retaining the same
  200-line-per-room policy as the SQL store. Coverage proves `sendChatLine`,
  chat room listing, recent-line reads, and trim behavior work through the KV
  store while SQL `chat_lines` and non-default per-room rows remain empty.
- Chat online rosters and chat-room online counts now read through the active
  `PresenceStore`, not directly from `user_presence_sessions`. This keeps chat
  room lists and `/chat/{room}/online` correct when authenticated presence is
  backed by memory or NATS KV rather than SQL.

### Acceptance Criteria

- Viral reaction traffic does not increase ordered-log write pressure.
- Counts converge after retries, restarts, and shard movement.
- Presence never writes to the durable event log.
- Durable replay can recover count state from checkpoints plus counter storage.
- Abuse/moderation workflows still have enough identity data to inspect misuse.

## IS6 - Async Global Views, Search, And Ranking

**Purpose:** Remove global reads from the write hot path.

### Workstreams

- Build stream processors for:
  - global latest
  - resident feed
  - rankings
  - unread summaries
  - stats windows
- Feed search from the event log into OpenSearch, Meilisearch, or Typesense.
- Make derived views explicitly lag-aware in APIs and metrics.
- Add backfill jobs that can rebuild any derived view from the event log.
- Add a Redis speed layer for hot derived views:
  - consume durable event-log positions into Redis Streams, sorted sets, or
    hashes keyed by view and aggregate
  - coalesce repeated board/thread/user/feed updates within bounded freshness
    windows before flushing idempotent batches to Postgres or search indexes
  - serve recent hot read slices from Redis with Postgres/event-log fallback
  - store durable watermarks outside Redis so replay/backfill repairs loss
- Add consistency tests that verify local board/thread correctness does not
  depend on global-feed freshness.

### Acceptance Criteria

- Posting does not synchronously update global feeds, rankings, search, or stats.
- Derived views expose freshness/lag.
- Search and global feed rebuild from the event log after index deletion.
- Redis restart or data loss only affects cache warmth and bounded derived-view
  freshness; authoritative events and replay positions recover from the durable
  log.
- Hot read paths served from Redis expose fallback and freshness behavior rather
  than silently returning correctness-critical stale data.
- Local thread/board reads remain correct when derived global views are delayed.

### Progress

- Added `derived_view_watermarks`, a named applied-position table for async
  global/read-side views. Missing watermarks resolve to the current durable head
  so existing synchronous SQLite/Postgres projections remain deployable, while
  future stream processors can expose real lag by advancing their own view rows.
- Community stats/history, the resident-board aggregate feed, board/unread
  summary endpoints, ranking endpoints, post search, and digest search now
  return view-scoped projection metadata with `view`, `headSeq`, `appliedSeq`,
  and `lagEvents`, mirrored in `X-Projection-*` headers.
- HTTP coverage now pins stale `search.posts` and `search.digest` watermarks on
  the actual post-search and digest-search endpoints, closing a gap where some
  search responses still returned data without freshness metadata.
- Metrics now expose `budgie_derived_view_applied_seq{view=...}` and
  `budgie_derived_view_lag_events{view=...}` for known IS6 derived views,
  giving operators a promotion gate before global views move fully async.
- Added a named derived-view backfill path:
  `Core.BackfillDerivedViewsFromEventLog`,
  `Core.BackfillDerivedViewsFromEventStore`, and `budgied
  -backfill-derived-views <view|group[,view|group]|all>`. The command reuses
  the SQL or NATS event-log rebuild source, then advances only the selected view
  watermarks to the rebuilt head, giving search/ranking/stat views a concrete
  repair workflow. Operational groups include `search`, `rankings`,
  `summaries`, `community`, and `feeds`.
- Added `Core.SyncDerivedViewsToHead` and an opt-in worker-mode
  `budgied -derived-view-watermarks <view[,view]|all>` loop. This gives current
  synchronous projections a steady-state owner for named view freshness while
  preserving explicit lag semantics for future async processors.
- Split `search.posts` from the write transaction behind
  `core.WithAsyncPostSearch` / `budgied -async-post-search`, and added a
  durable event-log `PostSearchProcessor` / `budgied -post-search-processor`
  that replays post append/edit/redact/restore/purge and thread-move events into
  `posts_fts` while advancing the `search.posts` watermark.
- Added a backend-neutral `PostSearchIndex` contract plus a Meilisearch adapter
  behind `budgied -post-search-index meilisearch`. API nodes hydrate search
  candidate IDs from SQL before applying redaction, counter overlays, and
  member-read board policy, while worker nodes feed the external index from the
  durable event log and wait for Meilisearch mutation tasks before advancing the
  `search.posts` watermark. Backfill now clears and repopulates the configured
  external post-search index from projections rebuilt from the event log.
- Split `community_stat_history` snapshots from presence/logout/stat-publish
  call sites behind `core.WithAsyncCommunityStatHistory` /
  `budgied -async-community-stat-history`. Async mode enqueues coalesced daily
  outbox jobs; the worker materializes history and advances the
  `community_stat_history` watermark, while startup blocks compatibility
  watermark sync from claiming that async-owned view.
- Added a durable digest curation event contract for entry upsert/update/body
  changes, removal, directory creation, and path copy/move/delete operations.
  `budgied -digest-search-processor` now scans those events to own the
  `search.digest` watermark, and rebuild coverage proves deleted digest rows
  and subtree path mutations can be repaired from the event log.
- Split resident-board aggregate feed freshness from `search.posts` by naming it
  `feeds.resident`, then added a worker-owned `resident_feed_posts` materialized
  index and `budgied -resident-feed-processor`. The API falls back to the
  compatibility SQL join until the materialized index has rows, and backfill
  coverage proves the feed index can be rebuilt from the durable event log after
  deletion.
- Added a worker-owned global latest feed, `feeds.latest`, with a
  `latest_feed_posts` materialized candidate index and
  `budgied -latest-feed-processor`. `GET /api/v1/feed/latest` is lag-aware,
  falls back to the compatibility SQL projection until the index has rows, then
  serves latest posts from materialized candidates while applying current board
  visibility, generated-board, stats-excluded, and zap policy at read time.
  Coverage proves local thread/post reads remain correct while the global feed
  is intentionally stale, and that backfill repairs the feed after index
  deletion.
- Added a worker-owned `rankings.boards` processor and `board_ranking_stats`
  materialized read model. Board ranking reads fall back to the legacy SQL
  aggregation until stats rows exist, then serve ranking counts from the
  materialized table while applying current board visibility and stats-excluded
  policy at read time. Backfill coverage proves the table can be repaired from
  the durable event log after deletion.
- Added a worker-owned `rankings.threads` processor and `thread_ranking_stats`
  materialized read model. Thread ranking reads fall back to the legacy SQL
  aggregation until stats rows exist, then serve hot-thread counts from the
  materialized table while applying current board visibility and stats-excluded
  policy at read time. The processor also refreshes on no-event ticks so
  unordered reaction side-store updates converge without returning reactions to
  the durable ordered log; both the fallback query and the materialized rebuild
  prefer reaction shard aggregates with identity-row fallback.
- Added a worker-owned `rankings.replies` processor and `reply_ranking_posts`
  materialized read model. Reply ranking reads fall back to the legacy SQL
  query until candidate rows exist, then serve latest-reply candidates from the
  materialized table while joining current post body and board visibility policy
  at read time. Backfill coverage proves the table can be repaired from the
  durable event log after deletion.
- Added a worker-owned `rankings.users` processor and `user_ranking_stats`
  materialized read model. User ranking reads fall back to the legacy SQL query
  until stats rows exist, then serve post/reaction/login/online/trust ranking
  data from the materialized table. The processor refreshes on no-event ticks so
  unordered reaction, login, online-time, and trust side-store updates converge
  without returning those writes to the durable ordered log, and received
  reaction counts are read from shard aggregates before falling back to identity
  rows.
- Added a worker-owned `rankings.blessings` processor and
  `blessing_ranking_stats` materialized read model. Blessing ranking reads fall
  back to the legacy SQL query until stats rows exist, then serve blessing
  counts and last-blessed timestamps from the materialized table. Backfill
  coverage proves the table can be repaired from the durable event log after
  deletion.
- Added a worker-owned `rankings.archives` processor and
  `archive_ranking_stats` materialized read model. Archive ranking reads fall
  back to the legacy SQL aggregation until stats rows exist, then serve
  board/kind/path entry counts and edited counts from the materialized table
  while applying current board visibility and stats-excluded policy at read
  time. Backfill coverage proves the table can be repaired from the durable
  event log after deletion.
- Added a worker-owned `summaries.boards` processor and `board_summary_stats`
  materialized read model. Board summary reads fall back to the legacy SQL
  aggregation until stats rows exist, then serve global board activity counts
  from the materialized table while applying current favorites, zaps, read
  markers, policy flags, and online presence at read time. Backfill coverage
  proves the table can be repaired from the durable event log after deletion.
- Added a worker-owned `summaries.unread_threads` processor and
  `unread_thread_summary_stats` materialized candidate table. Cross-board
  unread-thread reads fall back to the legacy SQL query until candidate rows
  exist, then serve global thread candidates from the materialized table while
  applying current board visibility, favorite folders, zaps, read markers,
  unread post counts, and first-unread navigation at read time. Backfill
  coverage proves the table can be repaired from the durable event log after
  deletion.
- Added a worker-owned `community_stats` processor and
  `community_stats_snapshot` materialized row. Community stat reads fall back to
  the legacy SQL aggregation until the snapshot exists, then serve convergent
  totals from the worker-owned row. The processor refreshes on no-event ticks so
  login, online-time, and presence side-store updates converge without returning
  those writes to the durable ordered log.
- The deployment runbook now treats all known IS6 views as processor-owned once
  promoted: a fully promoted worker runs post search, digest search, latest and
  resident feeds, community stats/history, summaries, and rankings without
  `-derived-view-watermarks`; compatibility watermark sync remains a temporary
  bridge for partial rollouts only.
- Added `budgied -derived-view-processors <group[,group]|all>` so operators can
  promote search, feeds, summaries, rankings, community, or all IS6 processors
  as grouped worker-owned sets. The `search` group owns post and digest search,
  and the `community` group owns community stats plus stat-history outbox
  materialization, reducing promotion error from long per-view flag lists.
- Regression coverage now proves selected backfills advance only selected
  watermarks and that local thread/post reads remain correct while global/search
  derived-view watermarks are intentionally stale.
- Added the first hot read-cache contract for `feeds.latest` at stable
  watermarks plus Redis and in-memory adapters. `budgied -read-cache redis`
  wires the Redis adapter into API/gateway nodes with `BUDGIE_REDIS_URL`,
  `-read-cache-prefix`, and `-read-cache-ttl`; cache entries are stored under
  hashed feed keys with TTLs. Cache misses and Redis loss fall back to the
  materialized SQL read path rather than changing correctness.

## IS7 - Gateway Tier And Connection Scale

**Purpose:** Scale live connections independently from writers and projections.

### Workstreams

- Split stateless gateway nodes from writer and projection roles.
- Gateways own WebSocket/SSE/SSH connections and subscription maps.
- Gateways subscribe upstream to the union of local client scopes.
- Implement per-connection bounded queues with drop-and-replay recovery.
- Add gateway-level backpressure metrics:
  - queue depth
  - drops by scope
  - reconnects
  - replay repairs
  - send latency
- Add load tests for large fanout scopes and many idle subscribers.
- Ensure SSH/TUI sessions can run on gateway nodes without local write ownership.

### Acceptance Criteria

- Adding gateway nodes increases concurrent connection capacity without adding
  writer capacity.
- A hot event is delivered once per gateway, then fanned out locally.
- Slow clients cannot block a gateway or writer.
- Replay repairs any dropped gateway delivery.
- The system can demonstrate the first million-socket target in a staged or
  synthetic environment.

### Progress

- Added gateway live-delivery observability for connection queue depth/capacity,
  scope-labeled queue drops, WS/SSE cursor resumes, WS/SSE replay repairs, and
  WS/SSE send latency. The local bus now exposes low-cardinality queue snapshots
  for per-connection bounded buffers, and NATS-backed nodes report the same
  local fanout pressure.
- SSE live delivery now matches WebSocket gap recovery: when a durable event
  arrives after a dropped sequence, the stream replays missing scoped events
  from the durable log before delivering the current event. Focused coverage
  proves the repaired SSE response preserves order and does not re-deliver the
  already acknowledged event.
- Added an explicit `gateway` runtime role for live HTTP transports. Gateway
  nodes serve WebSocket plus poll/long-poll/SSE event replay and ops probes
  without exposing the REST API, auth endpoints, SPA, worker processors, or
  writer consumers. WebSocket command frames are rejected on live-only gateways
  unless `-command-log-authoritative` is enabled, in which case commands become
  broker-log receipts for dedicated writer nodes.
- NATS live delivery now subscribes upstream to the union of local gateway
  scopes instead of a single cluster-wide broadcast subject. Local subscription
  refcounts create/remove scope-specific NATS subscriptions dynamically, events
  publish to deterministic encoded scope subjects, and duplicate multi-scope
  remote deliveries are deduped before local fanout.
- Added a synthetic gateway fanout load fixture,
  `TestGatewayFanoutManyIdleSubscribers`, that exercises a hot scope beside many
  idle subscriber scopes. The default runs quickly in CI, while staged runs can
  raise `BUDGIE_GATEWAY_LOAD_HOT_SUBSCRIBERS`,
  `BUDGIE_GATEWAY_LOAD_IDLE_SUBSCRIBERS`, and
  `BUDGIE_GATEWAY_LOAD_BUFFER_SIZE` to probe larger connection targets without
  changing code.
- Added `cmd/budgie-gateway-loadgen`, a JSON-emitting wrapper around the same
  synthetic fanout model. It reports subscriber counts, publish duration,
  queued deliveries, queue capacity/depth, sampled idle-scope leakage, and
  estimated slow-client drops. Thresholds such as `-max-publish-ms` and
  `-min-drops` make the first staged million-socket target measurable without a
  browser farm.
- The gateway loadgen also supports the shared `-budget-file` scale gate, so
  staged fanout runs can enforce publish latency, queue depth, idle-scope
  isolation, queued deliveries, expected slow-client drops, per-node subscriber
  capacity, hot-scope subscriber capacity, and the projected gateway node count
  for a configured target such as 1,000,000 connections from the same
  operator-tuned JSON artifact used by the write-load fixture.
- Added `scripts/gateway-fanout-gate.sh`, a promoted gateway fanout wrapper that
  pins the staged million-connection budget shape: at least 100,000 synthetic
  subscribers, 10,000 hot-scope subscribers, queue capacity for at least 10,000
  hot deliveries, and a 1,000,000 connection target. It writes reports under
  `artifacts/internet-scale/` only after `cmd/budgie-gateway-loadgen` passes the
  promoted budget artifact.
- Gateway fanout reports now include `evidence.tool`, `evidence.budgetFile`,
  budget SHA-256, git revision, and dirty-tree state. The promoted and remote
  staging budgets pin the expected gateway fanout budget file, and
  `BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING=1` selects the remote staging
  budget so archived synthetic fanout evidence cannot be confused with local
  validation evidence.
- Added `scripts/internet-scale-staging-gate.sh`, a clean-tree evidence bundle
  wrapper that runs gateway fanout plus detected NATS/Kafka durable gates with a
  shared report suffix. `BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING=1` cascades
  the remote budget mode to the child gates, and explicit gateway-only bundles
  require `BUDGIE_INTERNET_SCALE_GATE_TARGETS=gateway` so local fanout evidence
  is not mistaken for durable staging signoff.
- Added `cmd/budgie-gateway-report-check`, a standalone verifier for archived
  gateway fanout reports. `scripts/gateway-fanout-gate.sh` now checks the temp
  report with that verifier before archiving, so remote handoffs can re-verify
  gateway evidence against the selected local or remote staging budget just like
  command-log drain reports.
- Added `scripts/internet-scale-report-check.sh`, a read-only archived bundle
  checker that revalidates gateway, NATS, and Kafka reports by shared suffix or
  explicit report path. It selects the local or remote staging budget set for
  each target, giving handoff reviewers a single command to verify returned
  internet-scale evidence without rerunning load.
- SSH/TUI command paths now recognize authoritative command-log pending
  acknowledgements as queued writes. Thread creation no longer treats a command
  receipt as a created thread id, and immediate TUI mutation feedback uses a
  queued status while materialized results arrive through replay or refreshed
  projections.

## IS8 - Hot-Partition Handling

**Purpose:** Keep viral boards and threads from becoming the new global lock.

### Workstreams

- Add hot-partition detection based on:
  - partition lag
  - command rate
  - write latency
  - fanout load
  - gateway queue drops
- Add operational tools to split a hot thread into reply sub-partitions.
- Define the user-visible ordering rule for sub-partitioned replies:
  - timestamp plus deterministic tiebreak
  - local moderation ordering preserved for the target object
- Keep reactions on counters, not reply partitions.
- Add partition reassignment and rebalance runbooks.
- Add tests for split, replay, moderation, and merge/read presentation.

### Acceptance Criteria

- A viral thread can be moved to sub-partitioned replies without losing posts.
- Readers see stable deterministic ordering after the split.
- Moderation actions remain causally correct for their target posts.
- Hot-partition lag falls after split or reassignment.
- Operators have a documented rollback path.

### Progress

- Added the first hot-partition candidate signal family:
  `budgie_hot_partition_candidate{kind,key,signal}`. It currently covers
  command-log backlog (`command_lag`), per-partition command volume
  (`command_count`, suitable for `rate()` alerts), and writer lock-wait latency
  (`writer_lock_wait_ms_max`). Local gateway fanout load now reports
  `budgie_gateway_scope_subscribers{scope}` and feeds the same candidate family
  as `gateway_subscribers`; scoped gateway queue drops feed it as
  `gateway_drops`. Operators can now identify write-ordering partitions whose
  backlog, rate, latency, fanout load, or gateway drops may require
  writer-capacity, reassignment, or future split action.
- Added the first hot-thread split mechanic behind
  `budgied -hot-thread-splits thread_id=shards`. Split-aware gateways route new
  `appendPost` commands for configured hot threads to deterministic reply
  subpartitions, command-log writers validate against the same split map, and
  posts still materialize into the original thread in `created_seq` order.
  Full API nodes also expose a persisted admin read/write surface at `GET`,
  `PUT`, and `DELETE /api/v1/admin/hot-thread-splits`; running nodes poll the
  shared map while `-hot-thread-splits` remains an explicit local override. This
  gives operators a staged reply-write split. Split changes and rollback now
  guard against authoritative command-log lag and report blocking partitions;
  writers also accept already-enqueued base/old reply subpartitions for the same
  target thread so polling skew during config changes does not strand commands.
  Authoritative command-log fixtures now cover split replay into stable
  `created_seq` read presentation and redact/restore moderation commands on
  posts created through split reply subpartitions.
- Hot-thread split state now has scrape-time convergence evidence via
  `budgie_hot_thread_split_shards{thread_id=...}`. Operators can compare the
  active shard count across gateways and writers before changing or rolling back
  a split, and the split/reassignment runbook now includes that metric in its
  preflight, validation, and exit criteria.
- Added explicit command-partition reassignment overrides for writer assignment
  modes. Operators can now pin `kind/key` command partitions to a named writer
  with `-command-log-worker-assignment-overrides`; the hash simulator and
  NATS-KV assignment group both honor the override, NATS-KV persists it with a
  generation bump, and startup rejects owners outside the configured writer
  group. This gives hot partitions a concrete rebalance path while native
  broker consumer-group assignment remains the later promotion target.
- Added an executable hot-partition reassignment fixture:
  `TestHotPartitionReassignmentLetsNewOwnerDrainLag`. It produces backlog on a
  hot reply partition, verifies `budgie_command_partition_lag` and
  `budgie_hot_partition_candidate{signal="command_lag"}`, applies a
  broker-style reassignment snapshot to a replacement writer, drains the
  partition, and proves the lag and hot-candidate signal clear.
- Added `TestHotThreadSplitDistributesReplyBacklogAndLagFalls`, which compares
  an unsplit hot thread backlog against the same reply volume routed through
  four deterministic reply subpartitions. The fixture proves split routing
  lowers maximum per-partition lag, keeps the total lag visible to operators,
  leaves no base-thread lag sample for new replies, and clears each
  hot-candidate lag signal after the split shards commit.

## IS9 - Operational Hardening And Regional Scale

**Purpose:** Make the internet-scale architecture operable.

This milestone is not multi-region active-active forum writes. The first
regional goal is regional gateways, regional read replicas/projections, and a
single authoritative write region unless a later design explicitly changes that.

### Workstreams

- Add production runbooks for:
  - Redpanda/Kafka operations
  - NATS operations
  - counter-store repair
  - projection rebuilds
  - search index rebuilds
  - partition split/reassignment
- Add chaos tests for:
  - writer crash during command processing
  - broker partition outage
  - NATS outage
  - projection lag
  - gateway restart storms
  - counter shard failure
- Add regional gateway support with write-region routing.
- Add read routing that respects read-your-writes for the author.
- Add capacity dashboards and SLO alerts.
- Add cost dashboards so scale knobs are visible to operators.

### Acceptance Criteria

- A single-region failure drill can be run from docs.
- Projection and search rebuilds are routine operations, not one-off scripts.
- Regional gateways can serve local clients while routing writes to the
  authoritative region.
- Read-your-writes holds for authors after region routing.
- Operators can see the cost and latency impact of hot scopes and partitions.

### Progress

- Added `ops/runbooks/broker-operations.md`, a broker operations runbook for the
  current NATS/JetStream implementation and the future Redpanda/Kafka promotion
  gate. It covers live NATS delivery, JetStream event-log shadow parity,
  command-log shadow/authoritative streams, NATS KV writer assignment,
  outage/recovery signals, and the adapter properties Redpanda/Kafka must prove
  before broker logs become authoritative.
- Added `ops/runbooks/partition-split-reassignment.md`, a hot-partition
  operations runbook that covers signal interpretation, split-vs-reassign
  choice, hot-thread split admin API usage, `blockingPartitions` drain checks,
  emergency `force` policy, command-log worker assignment overrides, NATS-KV
  rebalance validation, rollback, and exit criteria.
- Added routine derived-view repair groups for `-backfill-derived-views` and
  `-derived-view-watermarks`: `search`, `rankings`, `summaries`, `community`,
  and `feeds`. Operators no longer need to memorize every internal view name to
  repair search, rankings, summaries, community stats/history, or feed
  projections. Added `ops/runbooks/projection-search-rebuild.md` with SQL and
  NATS-shadow rebuild paths, validation, and rollback/escalation steps.
- Added the same derived-view groups to `budgied -derived-view-processors`, so
  promotion can enable processor-owned IS6 worker groups without spelling every
  individual processor flag by hand.
- Added `ops/runbooks/counter-store-repair.md`, a counter repair runbook for
  today's SQL side-store implementation. It documents the current reaction,
  poll-vote, activity, community, and derived-view counter surfaces; recomputes
  `user_activity.reactions_recv`; routes lost unordered rows to Postgres
  backup/PITR instead of ordered-log replay; runs projection and derived-view
  repair; and covers NATS KV aggregate shard repair from surviving identity
  keys.
- Added an executable current counter-store loss fixture:
  `TestCounterSideStoreLossIsNotRecreatedByOrderedReplay`. It deletes
  `post_reactions` and `poll_votes`, poisons `user_activity.reactions_recv`,
  runs projection rebuild, and proves ordered replay restores durable poll/post
  structure while refusing to fabricate lost unordered reaction or poll-vote
  identity. This pins the current recovery contract behind the counter repair
  runbook.
- Added `budgied -repair-counter-store-aggregates` for NATS KV counter-store
  drills. The one-shot repair scans surviving `reaction/*` and `poll_vote/*`
  identity keys, rebuilds `reaction_count/*` and
  `poll_option_vote_count/*` aggregate shards, removes stale aggregate-only
  shards, and exits. `TestJetStreamCounterStoreRebuildsAggregateShardsFromIdentity`
  proves a simulated aggregate-shard loss converges without replaying reaction
  or vote churn through the ordered log, while still refusing to recreate lost
  identity.
- Added an executable counter-shard failure chaos fixture:
  `TestCounterShardFailureChaosRepairRecoversCoreReadsAndCheckpoints`. It
  deletes NATS KV aggregate shards under a Core using the distributed
  `CounterStore`, proves product-visible reaction and poll counts drop while
  per-user identity remains intact, runs the aggregate-shard repair, and proves
  Core reads plus durable counter checkpoints recover from the surviving
  identity keys.
- Added `ops/runbooks/single-region-failure-drill.md`, a concrete staging-first
  drill for the IS9 single-write-region operating model. It includes required
  inputs, evidence capture, preflight, regional write-route outage, projection
  lag/read-your-writes, NATS/live-broker outage, command writer crash or
  rebalance, rollback steps, expected metric/alert signals, and exit criteria.
- Added `scripts/single-region-failure-drill-preflight.sh`, an executable
  preflight for that drill. It validates the required environment, verifies the
  IS9 alert rules, fails on currently firing critical Budgie alerts when
  `BUDGIE_PROMETHEUS_URL` is set, checks write and regional `/readyz`, captures
  baseline metrics for both endpoints, and delegates the documented cluster
  smoke step so operators fail before any manual outage injection if staging is
  not ready.
- Added `ops/prometheus/budgie-internet-scale-alerts.yml`, a Prometheus rules
  artifact for IS9 SLO/capacity gates: remote delivery p95, gateway queue
  saturation and drop/repair mismatch, derived-view lag, command-log writer
  lag, assignment loss, regional write-route failures, command latency, writer
  lock wait, dead outbox jobs, event-log shadow parity, and hot partition
  candidates. The deployment runbook now points operators to those alerts and
  names cost dashboard panels for gateway capacity, writer capacity, broker
  repair/egress, and regional write routing.
- Added `ops/grafana/budgie-internet-scale-dashboard.json`, a concrete Grafana
  dashboard for IS9 capacity and cost visibility. It covers live-delivery SLOs,
  gateway fanout and socket cost, writer partition lag/skew, hot partition
  candidates, writer assignment and command-log receipt health, command latency
  and lock wait, derived view freshness, broker egress/replay repair, regional
  write routing, event-log shadow promotion health, and worker repair backlog.
  A dashboard verifier test pins the required panel titles and metric
  expressions.
- Added regional HTTP write-region routing via `budgied -write-region-url` /
  `BUDGIE_WRITE_REGION_URL`. Regional API/gateway nodes can keep safe reads and
  live replay local while reverse-proxying mutating `/api/v1/*` requests to the
  authoritative write region with auth, body, query, and `X-Command-Id`
  preserved. Full API nodes disable local WebSocket command frames while this
  proxy is enabled unless they are explicitly submitting to the authoritative
  command log. Proxy failures return retryable `502 write_region_unavailable`.
- HTTP reads now accept `X-Budgie-Min-Seq` or `?minSeq=` as a minimum durable
  event sequence for read-your-writes. Stale canonical durable reads
  (board/thread/post, mail, direct-message, and notification surfaces) and stale
  derived views return retryable `425 projection_stale` responses with
  freshness metadata, `Retry-After`, and `X-Budgie-Read-Your-Writes: stale`;
  fresh reads continue normally with `X-Budgie-Read-Your-Writes: satisfied`.
  This gives regional gateways a concrete read-routing and retry contract before
  full regional topology work begins. The web API client now remembers durable
  result sequences from command-envelope acks, resolved command-log status
  reads, resolved HTTP ack envelopes from REST aliases, and bare REST
  `AckResult` responses, then sends `X-Budgie-Min-Seq` on those canonical
  durable reads and lag-aware projection reads, closing the client half of that
  regional read-your-writes contract for thread navigation, replies, private
  messages, notifications, summaries, rankings, search, resident feeds, unread
  threads, digest search, and community stats. Those reads also normalize nested API
  error bodies and retry bounded
  `projection_stale` responses using the advertised retry delay before
  surfacing stale-read failure to the UI.
- Added a dependency-free Node test harness for the web API client's regional
  read-your-writes contract. `npm --prefix web run test:api-client` transpiles
  the real client module, stubs `fetch`, and proves durable ack sequences are
  sent as `X-Budgie-Min-Seq`, `projection_stale` reads retry with the same
  minimum sequence, and resolved pending command status advances the minimum
  sequence for subsequent canonical reads.
- Added an executable projection-lag chaos fixture:
  `TestProjectionLagChaosBackfillRecoversReadYourWrites`. It pins the
  stale-read contract for lagging derived views, proves canonical thread/post
  reads remain available while a global ranking view is stale, runs the
  event-log backfill repair path, and proves the same minimum-sequence read
  returns `X-Budgie-Read-Your-Writes: satisfied` after repair.
- Added an executable NATS outage chaos fixture:
  `TestNATSOutageKeepsDurableReplayAuthoritative`. Two SQLite-backed cores share
  one durable event log while the writer node uses a failing NATS adapter. The
  test proves failed live fanout increments remote publish failures, the sibling
  node does not receive a live event, and replay from the durable log still
  returns the committed event. SQLite `Core.New` now honors `WithBus` /
  `WithBusFactory` so the same bus contract can be exercised in small-mode
  fixtures without requiring Postgres.
- Added an executable broker-partition outage fixture:
  `TestEventReplayParityRunnerSurvivesBrokerPartitionOutage`. It simulates one
  unavailable event-log shadow partition while another partition remains
  healthy, proves the parity runner records a replay error without advancing the
  failed partition checkpoint, lets the healthy partition advance independently,
  and then advances the failed partition only after the broker partition is
  repaired.
- Added an executable gateway restart-storm fixture:
  `TestSSEGatewayRestartStormReplaysFromLastEventID`. It repeatedly reconnects
  an SSE stream with `Last-Event-ID`, proves every restart replays missed durable
  events from the event log in order, proves already-acknowledged events are not
  re-delivered, and pins the `budgie_gateway_reconnects_total` signal that IS7
  and IS9 operators use during gateway churn.
- Added executable WebSocket delivery replay fixtures. They prove the WS gateway
  skips duplicate durable events already covered by the client's cursor and
  repairs a partition-offset gap before delivering the current live event, even
  after the scalar compatibility cursor advanced on another partition. WS
  `resume` controls sent after the initial handshake now use the same cursor
  replay path when changing subscriptions, while scope-only updates keep the
  current delivered cursor.
- HTTP poll and long-poll responses now expose a delivered cursor alongside the
  compatibility head cursor. Scoped responses advance only through returned
  event partitions, so partition-aware clients can keep a scope-preserving
  resume token without marking unrelated hot boards or threads as seen.
- After the delivered-cursor slice, the local disposable NATS/Postgres gate
  passed on `0be610e6` with report
  `artifacts/internet-scale/commandlog-native-nats-report-0be610e6-local-nats.json`:
  2,400 native commands submitted and drained, 3,200 expected broker events
  projected into Postgres, zero max partition lag after drain, and zero
  terminal, retryable, or commit failures. The generated NATS load streams were
  then removed with the dry-run-first cleanup helper.
- Kafka/Redpanda command/event transaction coverage now explicitly proves the
  zero-event path used by terminal command failures: the adapter and franz
  runtime commit the consumed command offset without allocating event positions
  or appending event records, and still return logical committed progress.
- After the offset-only Kafka transaction coverage slice, the local disposable
  Kafka/Postgres gate passed on `7f367cf9` with report
  `artifacts/internet-scale/commandlog-native-kafka-report-7f367cf9-local-kafka.json`:
  2,400 native commands drained, 3,200 expected broker events projected into
  Postgres, 32 command and event topic partitions, zero max partition lag after
  drain, zero terminal, retryable, or commit failures, and promotion readiness
  true. The generated Kafka load topics were then removed with the
  dry-run-first cleanup helper.
- The full local disposable internet-scale evidence bundle passed on
  `8426d752` with suffix `8426d752-local-bundle`, then
  `scripts/internet-scale-report-check.sh` re-verified the archived gateway,
  NATS, and Kafka reports against the promoted local budgets. The gateway report
  covered 100,000 subscribers, 10,000 hot-scope deliveries, zero estimated
  drops, and a 1,000,000 projected connection target. The native NATS and Kafka
  reports each drained 2,400 commands, projected 3,200 expected broker events
  into Postgres, and reported zero max partition lag, terminal failures,
  retryable failures, and commit failures; Kafka promotion readiness was true
  with 32 command/event topic partitions. Dry-run-first cleanup removed the
  generated NATS streams and Kafka topics, and follow-up dry runs found no
  disposable broker load artifacts.
- Added an executable command-writer crash fixture:
  `TestCommandLogWorkerCrashBeforeMaterializationReplaysOnReplacement`. It
  cancels a writer while a broker-owned command is in flight, proves no SQL
  event or command offset is committed by the interrupted worker, then lets a
  replacement writer replay the same command-log record and materialize it
  exactly once.
- The local disposable internet-scale evidence bundle passed on `7dd8ad93`
  using `scripts/internet-scale-staging-gate.sh` against localhost
  NATS/Kafka/Postgres. It archived gateway fanout evidence for 100,000
  synthetic subscribers, 10,000 hot-scope deliveries, and a 1,000,000
  connection target, plus native NATS and Kafka command-log reports with 2,400
  commands drained, 3,200 broker events projected, zero lag/failures/losses,
  and clean git provenance. The generated NATS load streams and Kafka load
  topics were then removed with the dry-run-first cleanup helpers.
- Re-ran the local disposable internet-scale evidence bundle on `d54fae55`
  after adding the archived bundle checker. The gateway, native NATS, and
  native Kafka reports for suffix `d54fae55-local-bundle` all passed the
  promoted local budgets, `scripts/internet-scale-report-check.sh` re-verified
  the bundle by suffix, and dry-run-first cleanup confirmed the generated NATS
  load streams and Kafka load topics were removed.
- Command-log load reports now record the scalar compatibility allocator used
  by the staged writer path, and command-log budgets can require that evidence.
  The promoted local and remote NATS budgets pin `broker-stream-sequence`; the
  Kafka/Redpanda budgets initially pinned `sql-event-scalar-offsets`, making
  the transitional SQL scalar cursor allocator explicit instead of hidden inside
  a green native broker drain. The full local disposable internet-scale bundle
  passed on `bda3aa5d` with suffix `bda3aa5d-scalar-allocator`: the gateway
  report covered 100,000 subscribers, 10,000 hot-scope deliveries, zero
  estimated drops, and a 1,000,000 projected connection target; the NATS report
  `artifacts/internet-scale/commandlog-native-nats-report-bda3aa5d-scalar-allocator.json`
  recorded `scalarCompatibilityAllocator=broker-stream-sequence`; and the Kafka
  report
  `artifacts/internet-scale/commandlog-native-kafka-report-bda3aa5d-scalar-allocator.json`
  recorded `scalarCompatibilityAllocator=sql-event-scalar-offsets`. The bundle
  checker re-verified gateway, NATS, and Kafka reports by suffix, and
  dry-run-first cleanup removed the generated NATS streams and Kafka topics.
- Kafka/Redpanda partition-only command/event staging was promoted after the
  live replay path stopped synthesizing scalar event `seq` values from Kafka
  physical offsets when `-kafka-scalar-allocator
  sql-event-partition-offsets` is active. The materializer now indexes
  partition-only broker events into SQL compatibility rows during projection
  without advancing the legacy `event_scalar_offsets` allocator. The official
  Kafka gate and both Kafka budget files now require
  `sql-event-partition-offsets`; the clean-tree gate on `25a2666e` archived
  `artifacts/internet-scale/commandlog-native-kafka-report-25a2666e-local-partition-only.json`,
  which drained 2,400 commands, projected 3,200/3,200 broker events, recorded
  `runtime.scalarCompatibilityAllocator == "sql-event-partition-offsets"`,
  and reported zero lag, commit failures, claim losses, or missing
  materialization.
- Added a native command-surface guardrail: every proto command constant must
  either have a `CommandLogNativeDecisionExecutor` decision case or explicitly
  bypass the durable command log. The remaining non-native commands are the
  intentional side-store/live-local set: chat, presence, reactions, votes,
  read markers, thread preferences, and client subscription controls. Coverage
  also proves `subscribe`/`unsubscribe` fail locally instead of entering the
  authoritative broker writer path.
- The full local disposable internet-scale evidence bundle passed again on
  `a66b4e21` with suffix `a66b4e21-local-bundle-20260613195351`, then
  `scripts/internet-scale-report-check.sh` re-verified the archived gateway,
  NATS, and partition-only Kafka reports by suffix. The gateway report covered
  100,000 subscribers, 10,000 hot-scope deliveries, zero estimated drops, and a
  1,000,000 projected connection target; the native NATS report drained 2,400
  commands and projected 3,200/3,200 broker events with
  `scalarCompatibilityAllocator=broker-stream-sequence`; and the native Kafka
  report drained 2,400 commands, projected 3,200/3,200 broker events, used 32
  command/event topic partitions, and recorded
  `scalarCompatibilityAllocator=sql-event-partition-offsets`. Dry-run-first
  cleanup removed the generated NATS streams, Kafka topics, and disposable
  Postgres schemas.
- Kafka/Redpanda partition-only report evidence now includes a
  `scalarCompatibilityAudit` section that reads
  `event_scalar_offsets.broker_event_log` after the staged run. Both Kafka
  budgets require that audit and require
  `legacySqlScalarOffsetAfter == 0`, so older reports without the audit or
  runs that accidentally advance the legacy SQL scalar allocator fail the
  promotion check. The clean-tree Kafka gate on `198d6905` archived
  `artifacts/internet-scale/commandlog-native-kafka-report-198d6905-local-scalar-audit.json`,
  which drained 2,400 commands, projected 3,200/3,200 broker events, recorded
  `runtime.scalarCompatibilityAllocator == "sql-event-partition-offsets"`,
  recorded `scalarCompatibilityAudit.legacySqlScalarOffsetAfter == 0`, and
  passed the stricter Kafka budget. Dry-run-first cleanup then deleted the
  exact generated Kafka topics
  `budgie.commands.load.20260613200852_42915` and
  `budgie.events.load.20260613200852_42915`; the disposable Postgres schema
  `budgie_cmdlog_load_42946_1781381335921320000` was also verified gone.
- Added `cmd/budgie-internet-scale-preflight` plus
  `scripts/internet-scale-remote-staging-preflight.sh`, a cheap create/delete
  readiness probe for the remaining remote/shared staging handoff. It uses the
  same Go client paths and promoted load-prefix resource families as the full
  gates, verifies a disposable Postgres schema, generated NATS command/event
  load streams, and generated Kafka command/event load topics, and rejects
  loopback endpoints when remote staging mode is enabled. A local disposable
  smoke passed against localhost NATS/Kafka/Postgres with id
  `local-smoke-20260613`; Kafka topic cleanup, Postgres schema cleanup, and an
  empty NATS stream listing verified the probe cleaned up after itself. This
  does not replace remote evidence, but gives the environment owner a fast
  preflight before running the full internet-scale staging bundle.
- The full `scripts/internet-scale-staging-gate.sh` bundle now runs that
  create/delete preflight automatically when
  `BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING=1` and selected targets include
  NATS or Kafka. It passes only the selected durable targets to the preflight,
  so a `gateway,nats` bundle does not unexpectedly probe Kafka, and exposes
  `BUDGIE_INTERNET_SCALE_GATE_SKIP_PREFLIGHT=1` only for controlled
  diagnostics.
- The remote staging preflight can now archive a sanitized JSON proof report.
  The bundle writes it as
  `artifacts/internet-scale/preflight-report-<shared-report-suffix>.json`,
  alongside the gateway, NATS, and Kafka reports, recording redacted endpoints,
  git state, selected targets, generated create/delete resources, and probe
  timing for handoff review.
- Added `cmd/budgie-internet-scale-preflight-report-check` and wired
  `scripts/internet-scale-report-check.sh` to require that preflight report for
  remote NATS/Kafka bundle verification. The read-only handoff checker now
  rejects failed preflight probes, dirty git evidence, missing generated
  resources, wrong target sets, unsanitized endpoint evidence, and loopback
  endpoint evidence before accepting the archived gateway or command-log
  reports.
- Added `cmd/budgie-internet-scale-bundle-report-check` and wired the archived
  bundle verifier to run it after individual report checks. Handoff
  verification now rejects mixed bundles whose preflight, gateway, NATS, or
  Kafka artifacts were produced from different git revisions or from dirty
  worktrees.
- The main `scripts/internet-scale-staging-gate.sh` now runs
  `scripts/internet-scale-report-check.sh` after the selected preflight,
  gateway, NATS, and Kafka gates complete, passing the same target set,
  shared report suffix, and remote-staging mode. A staging bundle now has to
  pass the aggregate archived-artifact verifier before the wrapper declares
  the evidence bundle passed.
- Successful staging bundles now also emit
  `artifacts/internet-scale/bundle-manifest-<shared-report-suffix>.json` from
  the aggregate verifier. The manifest indexes the selected report paths with
  target set, remote/local staging mode, tool evidence, clean git revision, and
  SHA-256 hashes so remote handoff can transfer one machine-readable evidence
  index with the artifacts.
- Bundle manifests now have a read-only transfer verification path. Reviewers
  can run `cmd/budgie-internet-scale-bundle-report-check -verify-manifest` or
  set `BUDGIE_INTERNET_SCALE_REPORT_CHECK_VERIFY_MANIFEST` on
  `scripts/internet-scale-report-check.sh`; the verifier reopens every
  referenced report, recomputes SHA-256 hashes, checks the report labels against
  the target set, and confirms tool, clean git, and revision evidence still
  match the manifest without rewriting it. The existing local self-check bundle
  manifest for suffix `43116413-local-selfcheck-20260613` passed both the
  direct verifier and the wrapper read-back path.

## Suggested Execution Order

1. Finish the existing multi-node milestone and establish IS0 baselines.
2. Ship IS1 early: it is low-risk and every later phase benefits from decoupled
   delivery.
3. Ship IS2 before any storage replacement so partition semantics become a
   product and protocol concept, not a broker accident.
4. Use IS3 as the Postgres proving ground for per-partition correctness.
5. Move to Redpanda/Kafka in IS4 only after replay parity is automated.
6. Move reactions/presence off the ordered path in IS5 before chasing extreme
   write volume.
7. Build async global views in IS6 so global features stop pulling order back
   into writes.
8. Scale connections with IS7 and hot scopes with IS8.
9. Treat IS9 as the production-readiness gate for true internet-scale operation.

## Final Definition Of Done

- There is no single global serialization point on the write path.
- Ordering is linearizable inside the aggregate that needs it.
- Cross-aggregate views are asynchronous, lag-aware, and rebuildable.
- Reactions, votes, presence, and typing do not load the ordered event log.
- Live delivery reaches connected clients with low latency through gateways and
  fanout, while replay remains authoritative.
- Clients can resume by partition cursor and tolerate duplicate live events.
- Hot boards and threads have documented split and rebalance paths.
- Small deployments can still run the simple SQLite or Postgres topology.
