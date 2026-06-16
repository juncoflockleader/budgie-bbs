# Deployment: Multiple Nodes

This describes running two or more `budgied` processes against one shared
Postgres database, so a user connected to any node sees posts, likes, poll
votes, notifications, and chat created through any other node — without sticky
sessions.

For a single SQLite process, see
[`deployment-single-node.md`](deployment-single-node.md).

## Topology

```
              +-------------------+
              | Load balancer     |   (terminates TLS, health-checks /readyz)
              +---------+---------+
                        |
        +---------------+----------------+
        |                                |
 +------+-------+                 +------+-------+
 | budgied #1   |                 | budgied #2   |   ... more nodes
 | HTTP/WS/SSH  |                 | HTTP/WS/SSH  |
 +------+-------+                 +------+-------+
        |                                |
        +---------------+----------------+
                        |
               +--------+--------+
               | Postgres        |   events + projections,
               | (single primary)|   advisory write lock
               +--------+--------+
                        |
               +--------+--------+
               | NATS optional   |   live cross-node delivery
               | delivery bus    |   (`-nats`, best-effort)
               +-----------------+
```

Every node runs the same binary and can serve reads and accept writes. There is
no separate message broker by default: cross-node live delivery uses Postgres
`LISTEN/NOTIFY`, and Postgres write execution is routed by command partition.
For larger clusters, pass `-nats <url>` (or `BUDGIE_NATS_URL`) on every node to
move cross-node live delivery to NATS while keeping Postgres as the durable log.

## How it stays consistent

- **Durable events** are appended to Postgres and assigned a strictly increasing
  `seq`. The event log is authoritative; the broker only carries wakeups.
- **Replay cursors**: HTTP poll and long-poll responses include both the global
  head cursor and a delivered cursor. The head cursor carries current
  per-partition offsets for freshness checks; scoped clients should persist the
  delivered cursor so they only advance through partitions actually returned by
  that request. Partition-aware clients can send a partition-only cursor to
  replay from those offsets. WebSocket and SSE streams accept cursor envelopes
  on resume and track delivered per-partition offsets internally; browser
  `Last-Event-ID` and scalar `after` remain compatibility fallbacks.
- **Write serialization**: in Postgres mode every mutating command is classified
  into a write-ordering partition and wrapped in a partition advisory lock.
  Durable event appends also take a transaction-scoped scalar `seq` append gate,
  which serializes compatibility cursor assignment through commit/rollback so
  old clients cannot observe out-of-order commits. The command lock attempt has
  a 30s timeout; on failure the command returns a
  retryable `lock_unavailable` error.
- **Cross-node wakeups**: by default, after a commit, the writing node issues
  `pg_notify` with `{seq, event, scopes}`. Other nodes receive it, fetch the
  authoritative event from Postgres by `seq`, and publish it into their local
  bus. Chat lines (ephemeral, no `seq`) carry an `eid`; the receiving node
  fetches the row by id.
- **NATS delivery mode**: when `-nats` is set, each local event is published to
  scope-specific NATS subjects under `budgie.events.scope.<encoded-scope>` with
  its full event body and scopes. Each node subscribes upstream only to the union
  of its local client scopes; if a multi-scope event matches more than one local
  upstream subscription, the receiving process dedupes it before local fanout.
  Other nodes skip self-originated messages by `node_id` and publish the event
  local-only. Postgres `LISTEN/NOTIFY` wakeups are disabled in this mode, so
  sibling nodes do not fetch by `seq` on the live delivery path.
- **At-least-once + gap recovery**: live delivery may duplicate or drop.
  Existing clients can still dedupe by `seq`; partition-aware clients dedupe by
  the event cursor. WebSocket and SSE connections track the delivered durable
  cursor and repair scalar or per-partition gaps from the durable log before
  delivering the new event. Self-originated notifications are skipped by
  `node_id`.

The practical consequence: a missed wakeup is self-healing. The worst case is a
slightly delayed event, corrected on the next event or on reconnect — never a
permanently lost durable event.

## Prerequisites

- A reachable Postgres instance (one primary). Read replicas are not required and
  not used by the wakeup path.
- Optional: a reachable NATS server when using `-nats`.
- The same `BUDGIE_JWT_SECRET` on every node, so a token minted on one node is
  accepted on all of them.
- A load balancer that health-checks `GET /readyz` and does **not** require
  sticky sessions.

## First-time setup

Schema is applied automatically on startup (`ApplyPostgresMigrations`). To seed
an existing SQLite board into Postgres, run the one-shot migration once from any
host:

```sh
export BUDGIE_POSTGRES_DSN="postgres://budgie:secret@db.internal:5432/budgie?sslmode=require"

./budgied -migrate-sqlite-to-postgres -db ./budgie.db -postgres-dsn "$BUDGIE_POSTGRES_DSN"
```

For a fresh cluster with no prior data, skip the migration. Schema creation is
serialized by a transaction-level advisory lock, so nodes may be started
concurrently without racing. To apply the schema explicitly as a one-shot step
(useful in orchestration before nodes start), run:

```sh
./budgied -init-db -storage postgres -postgres-dsn "$BUDGIE_POSTGRES_DSN"
```

## Run a node

Identical invocation on every node (only listen addresses differ if co-located):

```sh
export BUDGIE_POSTGRES_DSN="postgres://budgie:secret@db.internal:5432/budgie?sslmode=require"
export BUDGIE_JWT_SECRET="the-same-long-random-string-on-every-node"

./budgied \
  -storage postgres \
  -http :8080 \
  -ssh 2222 \
  -hostkey /var/lib/budgie/budgie_host_key
```

To use NATS for live delivery, add the same NATS URL on every node:

```sh
export BUDGIE_NATS_URL="nats://nats.internal:4222"

./budgied \
  -storage postgres \
  -nats "$BUDGIE_NATS_URL" \
  -http :8080 \
  -ssh 2222 \
  -hostkey /var/lib/budgie/budgie_host_key
```

`-storage postgres` requires the DSN (flag `-postgres-dsn` or the env var). As a
backwards-compatibility convenience, supplying a DSN while `-storage` is still
the default `sqlite` is treated as Postgres mode.

`-nats` requires Postgres storage. It is deliberately not supported with SQLite,
because SQLite nodes do not share the durable event log needed for replay repair.

To trial the IS5 durable non-SQL counter store for high-volume reactions and
poll votes, point every node that serves canonical reads or executes local
reaction/vote commands at the same JetStream KV bucket:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -counter-store nats-kv \
  -counter-store-nats-bucket BUDGIE_COUNTER_STORE \
  -counter-store-shards 64
```

The default remains `-counter-store sql`. The `nats-kv` backend stores
per-user reaction identity, aggregate reaction shards, per-user poll votes,
aggregate poll-option vote shards, and received-reaction counters in JetStream
KV. Counter checkpoints read aggregate counts from that store, so worker nodes
using `-counter-checkpoint-interval` must use the same counter-store backend as
the serving tier.

To trial the IS5 durable non-SQL presence store, point every node that serves
online roster reads, chat room roster/count reads, accepts authenticated
`setPresence` commands, or accepts public guest presence pings at the same
JetStream KV bucket:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -presence-store nats-kv \
  -presence-store-nats-bucket BUDGIE_PRESENCE_STORE \
  -presence-store-nats-ttl 15m
```

The default remains `-presence-store sql`. The `nats-kv` backend stores one
authenticated presence-session record per user/session plus one anonymous guest
session record per guest session in JetStream KV. Online roster reads filter
authenticated sessions from that bucket and decorate users from SQL profile and
relationship tables at read time. `GET /api/v1/stats` overlays live online
user/guest counts and guest login/logout totals from the active presence store,
so the SQL roster tables can remain empty while the side store is enabled.
`typing` remains live-only.

To trial the IS5 durable non-SQL chat history store, point every node that
accepts `sendChatLine` commands or serves chat room/recent-history reads at the
same JetStream KV bucket:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -chat-store nats-kv \
  -chat-store-nats-bucket BUDGIE_CHAT_STORE
```

The default remains `-chat-store sql`. The `nats-kv` backend stores bounded
recent chat transcript records and room metadata in JetStream KV, retaining the
same 200-line-per-room recent-history policy as SQL. Live `chat.line` delivery
remains ephemeral; only the bounded transcript side store moves.

To trial the IS6 hot read-cache layer for stable-watermark views, point API and
gateway nodes at the same Redis endpoint:

```sh
export BUDGIE_REDIS_URL="redis://redis.internal:6379"

./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -read-cache redis \
  -redis "$BUDGIE_REDIS_URL" \
  -read-cache-ttl 1m
```

The default remains read-cache off. The Redis adapter currently caches
`feeds.latest` responses only when the view has a stable applied/head
watermark; misses, Redis restarts, and expired entries fall back to the
materialized SQL read path. Redis is therefore a hot-serving layer, not a
source of truth for feed membership or replay positions.

For disposable remote/LAN staging, run Redis with authentication even when the
host is otherwise isolated. Use a dedicated Redis instance, bind only loopback
plus the staging interface, keep runtime files outside the repo, and export a
gitignored URL such as:

```sh
export BUDGIE_REDIS_URL="redis://:<staging-password>@redis.staging.internal:6379"
```

For broker operations, outage response, JetStream event/command stream checks,
NATS KV assignment validation, and the Redpanda/Kafka promotion checklist, use
[`ops/runbooks/broker-operations.md`](ops/runbooks/broker-operations.md).

To exercise the IS4 shadow/parity path before a real partitioned log backend is
available, enable the in-memory shadow log:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -event-log-shadow memory \
  -event-log-shadow-interval 30s
```

`memory` is a tail-checking development backend: startup seeds parity
checkpoints from current SQL partition heads by default
(`-event-log-shadow-start-at-head=true`), then committed durable events are
mirrored from the post-commit bus and checked with bounded partition replay.
Keep watching `budgie_event_log_shadow_*` counters; any increase means the
shadow log is not promotion-ready.

To exercise the same path against a durable broker log, use the NATS JetStream
shadow backend on a Postgres node that already has `-nats` configured:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -event-log-shadow nats \
  -event-log-shadow-nats-stream BUDGIE_EVENT_LOG \
  -event-log-shadow-interval 30s
```

The `nats` backend creates or updates the JetStream stream with direct reads
enabled, writes one subject per logical event partition, and stores Budgie's
partition offset inside each broker record. Startup still seeds tail checkpoints
from SQL by default, so use a fresh stream or keep the stream aligned with the
same SQL event log when comparing promotion readiness.

Before using the NATS event log as a rebuild source, run the read-only
promotion-readiness report directly:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -event-log-shadow-nats-stream BUDGIE_EVENT_LOG \
  -event-log-shadow-replay-limit 500 \
  -event-log-shadow-partition-limit 0 \
  -check-event-log-promotion-readiness
```

The command prints JSON and exits `0` only when SQL and the candidate NATS
event log have full partition coverage and matching bounded partition replay.
Use `-event-log-shadow-partition-limit 0` for full-stream promotion checks; a
positive limit remains useful for diagnostics, but the readiness report fails
closed if the limit truncates partition coverage.

Once a NATS shadow stream has been populated, projections can be rebuilt from
that broker log instead of the SQL events table:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -rebuild-projections \
  -rebuild-source nats \
  -event-log-shadow-nats-stream BUDGIE_EVENT_LOG
```

The rebuild path opens the JetStream event-log stream read-only, then runs a
promotion-readiness preflight against the SQL event log before clearing
projections. The preflight lists every SQL and broker event partition, replays
each partition in bounded windows, verifies logical event content, partition
offsets, and SQL compatibility `seq`, and runs an extra empty window to catch
broker-only tails. It fails closed if the stream is missing, lacks the
`budgie.eventlog.>` subject, does not allow direct reads, or has any coverage or
parity issue, which protects operators from accidentally rebuilding projections
from an empty, stale, or misaligned broker stream.

Command-log shadowing can be enabled independently to populate the future writer
input stream while keeping the existing SQL command path authoritative:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-shadow nats \
  -command-log-shadow-nats-stream BUDGIE_COMMAND_LOG
```

In this mode each ordered submitted command is appended to the partitioned
command log before the current SQL handler runs. High-volume unordered commands
(`sendChatLine`, `setPresence`, `reactPost`, `unreactPost`, `votePoll`, and
per-user read/preference side-store mutations) are not shadowed, because their
live events and side-store writes are deliberately outside the ordered write
log. Shadow append failures are logged and do not change the user-visible
command result. The JetStream command stream stores both
command records (`budgie.commandlog.>`) and writer commit markers
(`budgie.commandcommit.>`); writer consumers are still a promotion step, so do
not treat this stream as authoritative yet.

Before promoting authoritative command-log submitters, run the synthetic
writer-drain load gate. By default it uses a temporary SQLite database and a
broker-shaped in-memory command log; pass `-postgres-dsn` to exercise the same
SQL-backed materialization path against a throwaway Postgres schema:

```sh
BUDGIE_POSTGRES_DSN="postgres://postgres@localhost:5432/budgie?sslmode=disable" \
  go run ./cmd/budgie-commandlog-loadgen \
    -boards 8 \
    -commands-per-board 100 \
    -writers 4 \
    -batch-size 25 \
    -min-drain-commands-per-sec 75 \
    -budget-file ops/internet-scale-budgets.example.json
```

Add `-authoritative-submit` to exercise the public-node enqueue contract as
well as the writer drain. In that mode the fixture creates its setup boards
locally, then submits the measured `createThread` commands through
`Core.ExecCmd` with an authoritative command log enabled. The submit stage only
counts pending command-log acknowledgements as successful; the same drain and
promotion-readiness sections then prove those queued receipts materialized.
Use `-command-log-backend nats` with `-nats` and
`-command-log-nats-stream` when the gate should exercise a real JetStream
command stream instead of the in-memory broker-shaped fixture:

```sh
BUDGIE_POSTGRES_DSN="postgres://postgres@localhost:5432/budgie?sslmode=disable" \
  go run ./cmd/budgie-commandlog-loadgen \
    -command-log-backend nats \
    -nats "$BUDGIE_NATS_URL" \
    -command-log-nats-stream BUDGIE_COMMAND_LOG_LOAD_STAGING \
    -require-postgres \
    -authoritative-submit \
    -assignment-mode snapshot-assignment \
    -boards 8 \
    -commands-per-board 100 \
    -writers 4 \
    -batch-size 25 \
    -budget-file ops/internet-scale-budgets.example.json
```

For synthetic NATS runs, use a throwaway command-log stream or the same SQL
database that already materialized the stream being checked. Promotion readiness
intentionally lists all command partitions in the stream and fails closed when
older partitions lack matching SQL materialization evidence.
Add `-command-log-worker-executor native` when the gate should exercise the
broker-native writer path instead of the SQL-backed command executor. The native
gate appends broker events, commits command offsets through the command/event
transaction boundary, projects those events back into SQL, and reports an
`eventProjection` section:

```sh
BUDGIE_POSTGRES_DSN="postgres://postgres@localhost:5432/budgie?sslmode=disable" \
  go run ./cmd/budgie-commandlog-loadgen \
    -command-log-worker-executor native \
    -boards 8 \
    -commands-per-board 100 \
    -writers 4 \
    -batch-size 25 \
    -budget-file ops/internet-scale-budgets.example.json
```

Add `-replies-per-thread` to include native `appendPost` commands in the same
gate. The runner drains and projects created threads first, then discovers the
projected thread/root-post ids and enqueues reply commands on thread command
partitions. Use `-directed-replies` when the gate should exercise the safe
directed-reply subset too:

```sh
BUDGIE_POSTGRES_DSN="postgres://postgres@localhost:5432/budgie?sslmode=disable" \
  go run ./cmd/budgie-commandlog-loadgen \
    -command-log-worker-executor native \
    -replies-per-thread 2 \
    -directed-replies \
    -boards 8 \
    -commands-per-board 100 \
    -writers 8 \
    -batch-size 25 \
    -budget-file ops/internet-scale-budgets.example.json
```

When replies are enabled, the promotion-readiness report must cover the board
command partitions plus every generated thread command partition.

For durable staging streams, pair the native executor with NATS command and
event logs:

```sh
BUDGIE_POSTGRES_DSN="postgres://postgres@localhost:5432/budgie?sslmode=disable" \
  go run ./cmd/budgie-commandlog-loadgen \
    -command-log-worker-executor native \
    -command-log-backend nats \
    -nats "$BUDGIE_NATS_URL" \
    -command-log-nats-stream BUDGIE_COMMAND_LOG_LOAD_STAGING \
    -event-log-nats-stream BUDGIE_EVENT_LOG_LOAD_STAGING \
    -require-postgres \
    -assignment-mode snapshot-assignment \
    -boards 8 \
    -commands-per-board 100 \
    -writers 4 \
    -batch-size 25 \
    -budget-file ops/internet-scale-budgets.example.json
```

Add `-authoritative-submit`, `-replies-per-thread`, and `-directed-replies` to
exercise the full public-submit plus native directed-reply promotion shape
against durable streams:

```sh
BUDGIE_POSTGRES_DSN="postgres://postgres@localhost:5432/budgie?sslmode=disable" \
  go run ./cmd/budgie-commandlog-loadgen \
    -command-log-worker-executor native \
    -command-log-backend nats \
    -nats "$BUDGIE_NATS_URL" \
    -command-log-nats-stream BUDGIE_COMMAND_LOG_LOAD_STAGING \
    -event-log-nats-stream BUDGIE_EVENT_LOG_LOAD_STAGING \
    -require-postgres \
    -authoritative-submit \
    -assignment-mode snapshot-assignment \
    -replies-per-thread 2 \
    -directed-replies \
    -boards 8 \
    -commands-per-board 100 \
    -writers 8 \
    -batch-size 25 \
    -budget-file ops/internet-scale-budgets.example.json
```

`-require-postgres` fails fast if `BUDGIE_POSTGRES_DSN` or `-postgres-dsn` is
missing, preventing a durable-stream staging gate from accidentally producing
SQLite-only materialization evidence. Native NATS runs also require distinct
command and event stream names.

For drain experiments that need cheaper partition discovery, add
`-command-log-index redis -redis "$BUDGIE_REDIS_URL"` to wrap the durable
NATS/Kafka command log with a Redis tail/commit index. The index is a
recoverable scheduling aid, not the command log; if Redis is cold or lost,
writers can fall back to broker replay/listing and rebuild observable offsets
from durable command records plus committed command offsets. The
`scripts/commandlog-native-nats-gate.sh` wrapper exposes this as
`BUDGIE_COMMANDLOG_GATE_COMMAND_INDEX=redis` plus `BUDGIE_REDIS_URL`.
Redis indexing is optional promotion evidence, not an authoritative durability
requirement. On an isolated staging host, the Redis-indexed native
NATS/Postgres app path ran the promoted 2,400-command shape at roughly
`4,013` submit cmd/s and `1,577` drain cmd/s with zero lag after drain. The
same host without Redis indexing submitted faster (`20,690` cmd/s) while drain
remained in the same order of magnitude (`1,928` cmd/s), which confirms Redis
is a scheduling/read-speed layer rather than the durable bottleneck.

Collect throughput evidence from a host colocated with the staging services, or
from a path with similarly low and stable latency. A jittery remote client path
can under-report the promoted shape by orders of magnitude, so that topology is
useful for wiring checks but not for throughput signoff.
For tiny-partition workloads, `-partition-concurrency N` on
`budgie-commandlog-loadgen` and
`BUDGIE_COMMANDLOG_GATE_PARTITION_CONCURRENCY=N` on the native NATS/Kafka gate
let each synthetic writer drain multiple assigned command partitions at once.
The default remains `1`, preserving strictly sequential per-worker partition
drain behavior unless an experiment opts in.

The default synthetic writer ownership mode is `hash-assignment`, which scans
known command partitions and assigns them deterministically across writer ids.
Use `-assignment-mode snapshot-assignment` when the gate should exercise the
native consumer-group adapter shape: the fixture snapshots produced command
partitions once, each writer lists only its owned partitions, and the worker
still rechecks ownership before execution and offset commits.

The report separates command-log production from writer drain, shows max
partition lag before and after the drain, records commit, assignment, and
command failures, and includes command-log promotion readiness. Readiness
requires every listed partition to have `tailOffset == committedOffset`, then
walks committed command offsets and verifies each committed command has either
a processed-command result or a terminal failure receipt. Copy
`ops/internet-scale-budgets.example.json`, tune the `commandLogDrain` values
from staging runs, and require promotion readiness to pass before public nodes
depend on authoritative command-log submission. The readiness/audit coverage is
fail-closed: if `-command-log-worker-partition-limit` truncates the listed
command partitions, the report sets `partitionLimitExceeded` and the budget gate
fails instead of blessing a partial stream sample. For native writer gates, the
same budget also requires event projection to run, enforces projected-event
throughput/duration thresholds, and fails on command ownership claim losses.

For an already-populated NATS command log, run the same promotion-readiness
gate directly against the configured stream without starting a writer loop:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-worker nats \
  -command-log-worker-nats-stream BUDGIE_COMMAND_LOG \
  -command-log-worker-partition-limit 1000 \
  -command-log-worker-batch-size 500 \
  -check-command-log-promotion-readiness
```

The readiness check opens the NATS command stream read-only, prints JSON, exits
`0` only when there is no uncommitted tail lag and every committed offset is
explained by SQL materialization evidence, and exits nonzero if writers are
behind or committed offsets outran processed-command results or terminal
failure receipts. `-audit-command-log-materialization` remains available as the
narrower materialization-only diagnostic.

Once the command-log writer path is caught up and monitored, public API,
live-gateway, and SSH nodes can be promoted to authoritative command-log
submission:

```sh
./budgied \
  -roles api,gateway,ssh,nntp \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-authoritative nats \
  -command-log-authoritative-nats-stream BUDGIE_COMMAND_LOG
```

In this mode ordered submitted commands are appended to the partitioned command
log and the local SQL handler does not run on the public node. Successful
responses are pending acknowledgements: `result.id` and `result.commandId` are
the command receipt, `result.status` is `pending`, and
`result.commandPartitionKind`, `result.commandPartitionKey`, and
`result.commandOffset` identify the durable log position that writer nodes must
drain. Clients should keep using a stable `X-Command-Id` across retries and
observe materialized results through replay, SSE/WebSocket events, projection
reads, or the authenticated command receipt read:

```sh
curl -fsS \
  -H "Authorization: Bearer $TOKEN" \
  "$BUDGIE_API/api/v1/commands/$COMMAND_ID?commandPartitionKind=board&commandPartitionKey=general&commandOffset=318"
```

The receipt reports `pending` until the writer tries the command, `retrying`
with the latest retryable writer error when an attempted drain stopped before
offset commit, `applied` with the materialized ack result after successful
execution, `failed` with the terminal command error after a committed
non-retryable rejection, or `committed` when the command offset is past the
writer checkpoint but no result/error receipt is available. Successful drains
persist an `applied` receipt before the worker commits the command-log offset;
if `committedOffset` is still lower than `commandOffset`, SQL materialization
has succeeded and replay will use the idempotency cache until the offset
checkpoint is committed. A later successful drain clears the command's prior
`retrying` receipt so receipt gauges represent outstanding writer trouble, not
resolved history. Status reads validate the command id against the requested
partition and offset; a stale or wrong offset returns `404`. Browser thread and
reply compose flows poll this status endpoint before clearing drafts or
navigating when an authoritative ack is pending.
High-volume unordered commands bypass the command log even in authoritative
mode. `sendChatLine`, `setPresence`, `reactPost`, `unreactPost`, `votePoll`,
`markBoardRead`/`restoreBoardRead`, `markFavoriteFolderRead`/
`restoreFavoriteFolderRead`, `markThreadRead`/`restoreThreadRead`, and
`markPostRead`, plus `setThreadPref`, execute immediately through their chat,
presence, counter-store, read-marker, or thread-preference side-store paths and
return ordinary non-pending acknowledgements without a command offset.
Chat history also has an internal `ChatStore` boundary. Production nodes still
default to SQL bounded history for `sendChatLine` and recent chat reads, while
the command handlers no longer depend directly on SQL chat projections.
`-chat-store nats-kv` moves bounded recent transcript state and room metadata to
JetStream KV.
Within `setPresence`, `typing` is live-only: it publishes `presence.update` to
subscribed gateways but does not overwrite persisted online roster state or
generate login-watch/stat-history work.
Identical online authenticated and guest presence pings are coalesced for a
short interval before they touch SQL; status changes, location changes, and
hidden/offline transitions still write immediately.
Presence also has an internal `PresenceStore` boundary. Production nodes still
default to SQL, but `-presence-store nats-kv` can move authenticated
`setPresence` roster writes, public guest presence writes, online-roster reads,
chat room roster/count reads, and live stats counts to JetStream KV without
changing command handlers.
Browser thread actions that use `/api/v1/commands` for moderation, title, and
lock changes also resolve pending receipts before treating the action as done,
so the latest durable result sequence can feed regional read freshness. For
REST alias writes, the browser unwraps HTTP ack envelopes and polls the same
receipt endpoint whenever the ack is still `pending`, so helper calls do not
surface queued authoritative writes as completed mutations. REST aliases that
normally perform a post-command readback, such as favorite imports, return the
pending ack instead of reading stale local state; browser helpers that expose
readback payloads resolve the ack and reread with the materialized sequence.
Multipart binary attachment uploads on authoritative command-log submitters stage
the bytes in the shared SQL `attachment_blob_staging` table under the future
attachment id, then enqueue the replayable attachment metadata with
`stagedBlobId`. The writer promotes those staged bytes into the final blob table
in the same transaction that records the attachment event. If a writer cannot
see the staged bytes it records retryable `blob_staging_required` and leaves the
command offset uncommitted. Regional nodes that do not share blob staging with
writers should route multipart uploads to the write region. Worker-role nodes
prune expired staged blobs in bounded batches under the existing background
leader election; watch the `budgie_attachment_blob_staging_*` gauges for rows
that stop clearing.
For `-roles gateway`, WebSocket command frames are accepted only in this
authoritative mode; without it, the gateway role remains live-transport-only.
`-command-log-shadow` and `-command-log-authoritative` are mutually exclusive.

SSH/TUI sessions on public nodes follow the same contract. When a command-log
authoritative node accepts a write, the TUI reports it as queued and waits for
live replay or refreshed projections before navigating to newly created
resources. The command receipt id is not treated as a post or thread id.

Regional read/API nodes that share projection storage with the cluster but must
not execute local writes can proxy mutating HTTP requests to the authoritative
write region:

```sh
./budgied \
  -roles api,gateway \
  -storage postgres \
  -postgres-dsn "$BUDGIE_REGIONAL_READ_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -write-region-url https://write.use1.example.com
```

With `-write-region-url` (or `BUDGIE_WRITE_REGION_URL`) enabled, `GET`, `HEAD`,
and `OPTIONS` requests stay local, while mutating `/api/v1/*` requests are
reverse-proxied to the configured write-region base URL with the original
authorization headers, body, query string, and `X-Command-Id`. The proxy marks
forwarded requests with `X-Budgie-Write-Region-Routed: 1`,
`X-Budgie-Write-Region: <host>`, `X-Forwarded-Host`, and
`X-Forwarded-Proto`. If the write region is unreachable, the regional node
returns retryable `502 write_region_unavailable`.

Full API nodes with `-write-region-url` do not accept WebSocket command frames
locally unless `-command-log-authoritative` is also enabled; clients should send
mutating commands over the HTTP API so they flow through the write-region proxy,
or use an authoritative command-log gateway.

HTTP reads can enforce read-your-writes freshness for regional gateways and
clients that have already observed a durable event sequence. Send
`X-Budgie-Min-Seq: <seq>` or `?minSeq=<seq>` on canonical durable reads
(including board/thread/post, mail, direct-message, and notification reads)
plus stats/history, ranking, digest/search, board-summary, latest-feed, resident-feed,
unread-thread, and post-search reads. If the local read store or named derived
view has not applied the requested sequence, the API returns `425` with
retryable error code `projection_stale`, freshness metadata, `minSeq`,
`Retry-After: 1`, and `X-Budgie-Read-Your-Writes: stale`. Once the read is fresh
enough, the normal `200` response includes
`X-Budgie-Read-Your-Writes: satisfied` alongside `X-Projection-*` headers.
Gateways should set `minSeq` from the latest durable event sequence observed by
the author, then retry, wait for replay, or route to a fresher projection region
when they receive `projection_stale`. The browser client tracks the latest
durable result `seq` observed through command-envelope acks, resolved
command-log status reads, resolved HTTP ack envelopes from REST aliases, and
bare REST `AckResult` responses, then adds `X-Budgie-Min-Seq` to these
lag-aware reads.
It also retries bounded
`projection_stale` responses using the server's retry delay before surfacing an
error, so regional UI refreshes do not silently fall behind the author's own
writes.

To split reply writes for a hot thread, pass the same split map to every public
command-submitting node and every command-log writer:

```sh
./budgied \
  -roles api,gateway,ssh,nntp \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-authoritative nats \
  -hot-thread-splits thr_hot=4

./budgied \
  -roles writer \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-worker nats \
  -hot-thread-splits thr_hot=4
```

For the complete operator flow, including hot-partition signals, drain checks,
`blockingPartitions`, emergency `force` rules, command-log writer reassignment
overrides, rollback, and exit criteria, use
[`ops/runbooks/partition-split-reassignment.md`](ops/runbooks/partition-split-reassignment.md).

The split only changes the command-log partition used by new `appendPost`
commands; posts still materialize into the original thread and readers keep the
normal `created_seq` order. Command-log writers accept the target thread's base
partition and reply subpartitions for already-enqueued `appendPost` records, so
polling skew during a config change does not strand old commands.

Admins can inspect and change the persisted split map on a running full API
node:

```sh
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BUDGIE_API/api/v1/admin/hot-thread-splits"

curl -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"shards":4}' \
  "$BUDGIE_API/api/v1/admin/hot-thread-splits/thr_hot"

curl -X DELETE -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BUDGIE_API/api/v1/admin/hot-thread-splits/thr_hot"
```

The response includes `local: true` because it reports the current process'
effective map, and `persistent: true` because `PUT`/`DELETE` store changes in
the shared database. Running nodes poll the persisted map and merge it into
their local routing state. A `-hot-thread-splits` startup flag is a deliberate
per-node override and wins over the persisted value for that thread, so remove
the flag when you want a node to follow cluster-wide admin changes.

By default, `PUT` and `DELETE` check authoritative command-log offsets and
return `409 conflict` with `blockingPartitions` when the base thread partition
or affected reply subpartitions still have writer lag. Wait for
`budgie_command_partition_lag{kind="thread",key=~"thr_hot(#reply-.*)?"}` to
drain, then retry. In an emergency, add `?force=1` or send `"force": true` in
the `PUT` body to bypass the guard; use that only when operators have accepted
the replay/validation risk for any still-queued commands.

An experimental command-log writer role can drain that stream through the
current SQL-backed command executor:

```sh
./budgied \
  -roles writer \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-worker nats \
  -command-log-worker-nats-stream BUDGIE_COMMAND_LOG \
  -command-log-worker-ownership sql-lease \
  -command-log-worker-claim-ttl 30s
```

Run this only as a dedicated writer process while IS4 is in progress. Do not
combine `-roles writer` with public command-producing roles and
`-command-log-shadow` in the same process; startup rejects that shape because a
locally submitted command would both execute through SQL immediately and be
drained from the command log later. Startup also rejects a process that combines
public command-producing roles, `-command-log-authoritative`, and
`-command-log-worker`; keep public submission and writer draining as separate
node roles for promotion tests. Dedicated writer nodes coordinate with
short-lived SQL leases in `command_partition_leases`: a writer must claim a
command partition before it drains records from that partition, refreshes the
lease before command execution and before offset commit, and stops without
committing more offsets if another writer owns the partition. Another writer can
take over after `-command-log-worker-claim-ttl` expires. This is a temporary
promotion guardrail, not final exactly-once ownership; set the TTL longer than
the expected worst-case single command execution. Broker consumer-group
partition assignment still needs to replace SQL leases before the command log
becomes authoritative.
Workers also refresh ownership after command execution and before recording any
`applied`, `failed`, or `retrying` command receipt. If a writer loses ownership
in that window, it leaves both the receipt and command offset untouched; the
replacement writer replays the same command through the idempotency bridge and
owns finalization.
Internally the post-decision finalization step is now a replaceable
`CommandLogFinalizer`: production still uses the SQL receipt finalizer, while
`CommandEventTransactionFinalizer` can append decided events to the
broker-shaped event log and commit the consumed command offset through the same
worker loop. A real Redpanda/Kafka adapter must back that
`CommandEventTransactionStore` before SQL materialization leaves the write hot
path. The NATS adapter implements the same boundary with append-first,
idempotent replay semantics for staging and promotion fixtures: if the command
offset commit fails after event append, replaying the same stable event ids
resolves duplicate appends before retrying the commit.
`CommandLogNativeDecisionExecutor` can exercise that boundary for basic
`createThread` commands by producing deterministic broker `thread.new` and
`post.appended` events without SQL projection writes. It also covers basic
plain and safe directed `appendPost` replies once the target thread is visible
in SQL projections, including mention-bearing posts, poll-bearing posts from
trusted users, quoted replies, inline post attachment metadata, and
metadata-only standalone `attachPost` commands on boards that allow attachments,
with deterministic random signature snapshots. Standard `editPost` commands also
produce deterministic broker `post.edited` events and rely on event-store
projection for body and search-index catch-up. Standard `setPostFlag` commands
produce deterministic broker `post.flags_set` events with native curator,
author, and board-thread moderation checks. Standard single-post `redactPost`
and `restorePost` commands produce deterministic broker `post.redacted` and
`post.restored` events with native author-window and board-post moderation
checks. Standard `redactPostRange` and `restorePostRange` commands produce
deterministic range `post.redacted` and `post.restored` event batches with
native board-post moderation checks and moderator recycle-bin projection
semantics. Standard `clearBoardJunk` commands produce deterministic
`post.deletion_cleared` batches for selected or whole-board junk-bin clears.
Standard `purgePost` commands produce deterministic `post.purged` events and
broker projection clears post bodies, resident/latest feeds, and local FTS rows.
Standard `setThreadTitle`, `lockThread`, and `moveThread` commands produce
deterministic broker `thread.title_set`, `thread.locked`, and `thread.moved`
events with native board-thread moderation checks. Native
`createThread`/`appendPost` admission mirrors SQL board policy for read-only
boards, member-scoped boards, and moderator bypasses for locked or no-reply
threads/posts. Directed
replies validate the parent post, flatten reply-to depth the same way the SQL
handler does, and produce deterministic `mail.sent` events for article
mail-back replies; reply, mention, and watched-thread notifications catch up
through the broker projection's `post.committed` outbox work. Relay-enabled
boards also get deterministic pending relay deliveries from native
`post.appended` projection. Anonymous native posts mirror SQL board policy,
publish public `Anonymous` identity, and carry hidden commit actor metadata for
projection-side activity and notification recovery. Standard
`setContentFilter` commands produce deterministic `content_filter.set` events
with native admin, filter-id, pattern, and board-scope checks. Content-filter
matches produce deterministic `post.flagged` review events plus sanitized
generated Filter-board log events for public boards. User-submitted `flagPost`
and moderator `resolveReview` commands produce deterministic
`post.flagged`/`review.resolved` events plus sanitized generated
0Moderation-board audit log events for public boards. Standard
`publishPollResult` commands produce deterministic generated vote-board
`board.created`, `thread.new`, and `post.appended` records with native poll
author and delegated board poll-manager checks. Standard `grantRole` and
`revokeRole` commands produce deterministic `role.granted`/`role.revoked`
events plus generated syssecurity-board audit records with native admin and
target-user checks. Standard `publishSystemNotice` commands produce
deterministic generated notepad/GiveupNotice/bbsnet board, thread, and post
records with native admin, board-name, title, body, and source checks. Native
`attachPost` validates staged blob availability and lets broker projection
promote bytes into final attachment storage after inserting metadata. Treat it
as a promotion experiment only: poll-bearing post edits remain command-level
unsupported in both SQL-backed and native executors; keep that validation
parity until a future poll-edit product design defines option/vote migration
semantics.
Dedicated NATS writer nodes can opt into that path with
`-command-log-worker-executor native`. The native executor uses the configured
command stream plus `-command-log-worker-event-nats-stream` as the broker event
log, appends decided events, and commits the consumed command offset through the
NATS command/event transaction boundary. Keep `-command-log-worker-executor sql`
as the default while public authoritative submitters still require the current
SQL materialization receipts. Native retrying and terminal outcomes are still
recorded as command-log receipts for the existing status endpoint.
Startup requires the native command stream and event stream to be distinct. If a
node combines native writer and broker projection roles in one process, startup
also requires `-event-store-projection-nats-stream` to match
`-command-log-worker-event-nats-stream`, so the local projector tails the stream
the writer actually appends.

```bash
budgied \
  -roles writer \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-worker nats \
  -command-log-worker-executor native \
  -command-log-worker-event-nats-stream BUDGIE_EVENT_LOG
```

For a compact single-node trial that drains native commands and projects the
broker events in the same process, keep those event-stream flags identical:

```bash
budgied \
  -roles writer,worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-worker nats \
  -command-log-worker-executor native \
  -command-log-worker-event-nats-stream BUDGIE_EVENT_LOG \
  -event-store-projection nats \
  -event-store-projection-nats-stream BUDGIE_EVENT_LOG \
  -event-store-projection-source nats
```

`MaterializeEventStorePartition` can then replay the broker event partition
into SQL projections with a source/partition watermark committed in the same
transaction as the projection writes. This gives the current promotion path a
complete basic `createThread` loop: enqueue command, decide broker events,
commit command offset, and materialize the resulting thread/post projections
without rerunning the SQL command handler. Projected `post.appended` events
also enqueue the existing `post.committed` outbox job in that transaction, so
post-created trust updates and post notification side effects can be processed
after the broker projection commits.
`EventStoreProjectionWorker` wraps that materializer as a bounded worker loop:
it discovers broker event partitions, drains saturated batches until caught up,
and advances the same source/partition watermarks before waiting for the next
interval. Its `-event-store-projection-partition-limit` setting is a coverage
guard: if the broker has more event partitions than the limit, the worker fails
that projection pass instead of materializing only a prefix. Raise the limit, or
split projection ownership explicitly, before promotion.
Run it on worker nodes with an explicit read-only broker event-log source:

```bash
budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -event-store-projection nats \
  -event-store-projection-nats-stream BUDGIE_EVENT_LOG \
  -event-store-projection-source nats
```

For assignment/rebalance experiments, dedicated writer nodes can replace the
SQL lease guardrail with deterministic hash ownership:

```bash
budgied \
  -roles writer \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-worker nats \
  -command-log-worker-ownership hash-assignment \
  -command-log-worker-id writer-a \
  -command-log-worker-group-members writer-a,writer-b
```

Every writer in the simulated group must use the same
`-command-log-worker-group-members` list. This mode is useful for proving that
the worker honors broker-style partition assignment and stops on rebalance, but
it is still a deterministic simulator; a real broker consumer-group assignment
must replace it before authoritative command-log writes are promoted.

The next experimental ownership mode stores the assignment group in a NATS
JetStream key-value bucket:

```bash
budgied \
  -roles writer \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-worker nats \
  -command-log-worker-ownership nats-kv \
  -command-log-worker-id writer-a \
  -command-log-worker-group-members writer-a,writer-b
```

All writer nodes in the group must use the same member list. The first writer to
start creates or updates the KV assignment record for the command-log stream;
later worker heartbeats read that broker-backed generation before executing and
committing command offsets. Changing the member list bumps the generation and
causes workers that no longer own a partition to stop before committing further
offsets. This is still a stepping stone toward native broker consumer-group
assignment, but unlike `hash-assignment` the ownership record is broker-backed
and shared across nodes.

The worker core also accepts broker assignment snapshots that list only the
logical command partitions owned by the current writer. A future
Redpanda/Kafka adapter should feed this snapshot from consumer-group rebalance
callbacks, with generation bumps on revoke/assign events. In that mode the
writer does not need a global command-partition scan before every drain; it
drains only the broker-assigned partition set and still refreshes ownership
before execution and offset commits.

To reassign a hot command partition to a specific writer, add the same override
map on every writer in the assignment group:

```bash
budgied \
  -roles writer \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-worker nats \
  -command-log-worker-ownership nats-kv \
  -command-log-worker-id writer-a \
  -command-log-worker-group-members writer-a,writer-b,writer-hot \
  -command-log-worker-assignment-overrides thread/thr_hot#reply-0=writer-hot
```

The override syntax is `kind/key=writer`, comma-separated. The owner must be in
`-command-log-worker-group-members`; startup rejects stale or misspelled owner
ids. In `nats-kv` mode, changing the override map updates the shared KV record
and bumps the assignment generation. Workers that lose ownership observe the
new generation through the existing heartbeat path and stop before committing
further command offsets. Use `budgie_command_partition_assigned*`,
`budgie_command_partition_assignment_generation`, and
`budgie_command_log_assignment_losses_total` to watch the rebalance settle, then
confirm `budgie_command_partition_lag{kind="thread",key="thr_hot#reply-0"}`
falls on the reassigned writer.

Startup logs the resolved storage mode and DSN with the password redacted:

```
INFO starting budgied storage=postgres dsn=postgres://****@db.internal:5432/budgie
```

Each process generates its own in-memory `node_id` (logged, and used to skip
self-notifications). Give each node its own SSH host key file so SSH clients see
a stable key per node, or front SSH with a single node.

## Runtime roles

A node runs only the roles you give it via `-roles` (comma-separated). The
default `api,ssh,worker,nntp` keeps the single-node behavior; `gateway` and
`writer` are split-out production/promotion roles:

| Role | What it runs |
|------|--------------|
| `api` | Full HTTP REST + WebSocket + SSE + web SPA on `-http`. |
| `gateway` | Live HTTP transports only: WebSocket plus poll/long-poll/SSE event replay and ops probes on `-http`. No REST reads/writes, auth endpoints, or SPA. WebSocket command frames are rejected unless `-command-log-authoritative` is enabled so commands become broker-log receipts instead of local writes. |
| `ssh` | SSH TUI server on `-ssh`. |
| `nntp` | NNTP gateway (only when `-nntp <addr>` is also set). |
| `worker` | Background jobs: outbox processing + daily stats (leader-elected), plus optional derived-view watermark sync. |
| `writer` | Experimental IS4 command-log writer consumer (`-command-log-worker`). |

Regardless of roles, every node opens the Core, the configured cross-node
delivery listener, and an HTTP listener that always serves `GET /healthz`,
`/readyz`, and `/metrics` — so load balancers and scrapers can reach any node
even when it has no `api` role. Non-`api` and non-`gateway` nodes serve only
those ops endpoints on `-http` (API routes return 404).

Example topology:

```sh
# API nodes (behind the load balancer)
./budgied -roles api    -storage postgres -http :8080
# Live HTTP gateway nodes (scale WS/SSE/long-poll without adding REST capacity)
./budgied -roles gateway -storage postgres -http :8081
# SSH TUI node
./budgied -roles ssh    -storage postgres -ssh 2222
# Worker node (no public transports; only ops endpoints on -http)
./budgied -roles worker -storage postgres -http :8080
```

## Background worker and leader election

The `worker` role owns the outbox worker and the daily stats snapshot. To make
these safe when more than one node carries the `worker` role, the worker is a
**leader-elected singleton**: each worker node contends for a Postgres advisory
lock (`pg_leader` key, distinct from the write-serialization lock), and only the
holder runs the jobs. If the leader dies, Postgres releases the session-scoped
lock automatically and another worker takes over within a few seconds.

- Run the `worker` role on **at least one** node. Running it on several is safe —
  exactly one is active at a time, giving automatic failover.
- The current leader reports `budgie_worker_is_leader 1` in `/metrics`; followers
  report `0`.
- Outbox job claims are also transactionally guarded, and the stats command is
  idempotent (`auto-stats-<date>` cid), so brief leadership overlap during
  failover cannot double-process or duplicate.
- IS5 unordered counter checkpoints can be emitted by the same singleton worker
  with `-counter-checkpoint-interval=5m`. Leave it at `0` to disable automatic
  durable `counter.checkpointed` snapshots.
- The optional derived-view watermark sync is idempotent; prefer enabling it on
  one worker/projection node while IS6 is in progress.

While global views are still synchronously maintained by the SQL projection
path, a worker node can opt into IS6 compatibility watermark sync:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -derived-view-watermarks all \
  -derived-view-watermark-interval 5s
```

This periodically marks the selected named views as applied through the current
durable event head. It does not rebuild projection contents; use
`-backfill-derived-views` for repair after projection loss or index deletion.
The flag is intentionally off by default, and startup requires the `worker` role
so public API and dedicated writer nodes do not silently own projection
freshness.

For promoted IS6 processor ownership, prefer the grouped worker flag:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -derived-view-processors all
```

`-derived-view-processors <group[,group]|all>` accepts `search`, `feeds`,
`summaries`, `rankings`, `community`, or `all`. `search` enables
`-async-post-search`, `-post-search-processor`, and
`-digest-search-processor`; `community` enables
`-community-stats-processor` and `-async-community-stat-history`; the other
groups enable their matching dedicated processors. The grouped flag requires
the `worker` role and remains incompatible with `-derived-view-watermarks` for
the same selected views, so a promoted worker owns those watermarks through its
processors. Command-producing API/gateway nodes still need the async write-path
flags shown below during split-role promotion. Keep the individual processor
flags when a rollout needs per-view intervals, batch sizes, or only one view at
a time.

`search.posts` can now move one step further: command-producing nodes may stop
updating the post-search index in their write transactions, while a worker node
replays durable events into `posts_fts` and advances the `search.posts`
watermark:

```sh
# Public API/gateway node: writes durable events but does not update posts_fts inline.
./budgied \
  -roles api,gateway,ssh,nntp \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -async-post-search

# Worker/projection node: owns search.posts catch-up from the event log.
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -async-post-search \
  -post-search-processor \
  -post-search-processor-interval 1s \
  -post-search-processor-batch-size 500
```

When `-post-search-processor` is enabled, do not include `search.posts` in
`-derived-view-watermarks`; startup rejects that combination. The compatibility
watermark worker is only a bridge for views that are still synchronously updated
and do not yet have their dedicated processor enabled. During a partial
promotion, keep only those not-yet-promoted views in the compatibility list.
Once the IS6 processors below are enabled, the worker should run without
`-derived-view-watermarks`.
During promotion, watch `budgie_derived_view_lag_events{view="search.posts"}`;
it should converge to zero under normal write load.

For internet-scale post search, API and worker nodes can use an external
Meilisearch index instead of the SQL `posts_fts` compatibility table. The
search service returns candidate post IDs only; BudgieBBS still hydrates posts
from SQL and applies redaction, counter overlays, scoped-board checks, and
member-read policy at read time.

```sh
# API/gateway nodes read search candidates from Meilisearch and keep post
# search mutations out of command transactions.
./budgied \
  -roles api,gateway,ssh,nntp \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -post-search-index meilisearch \
  -meilisearch-url "$BUDGIE_MEILISEARCH_URL" \
  -meilisearch-api-key "$BUDGIE_MEILISEARCH_API_KEY" \
  -meilisearch-index budgie_posts

# Worker/projection node owns the external index feed from the durable log.
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -post-search-index meilisearch \
  -meilisearch-url "$BUDGIE_MEILISEARCH_URL" \
  -meilisearch-api-key "$BUDGIE_MEILISEARCH_API_KEY" \
  -meilisearch-index budgie_posts \
  -post-search-processor \
  -post-search-processor-interval 1s \
  -post-search-processor-batch-size 500
```

Configure the Meilisearch index so `board_id` is filterable before enabling
scoped board searches. `-meilisearch-task-timeout` and
`-meilisearch-task-poll-interval` control how long the worker waits for
accepted document mutations before moving the `search.posts` watermark.

The global latest feed now has a materialized `latest_feed_posts` candidate
index owned by the `feeds.latest` processor. The API falls back to the
compatibility SQL projection until the index has rows, then serves latest posts
from the materialized candidates while applying current board visibility,
generated-board, stats-excluded, and zap policy at read time:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -latest-feed-processor \
  -latest-feed-processor-interval 1s \
  -latest-feed-processor-batch-size 500
```

When `-latest-feed-processor` is enabled, do not include `feeds.latest` in
`-derived-view-watermarks`; startup rejects that combination. Use
`-backfill-derived-views feeds.latest` after deleting or rebuilding the
materialized feed table.

Community stats now have a materialized `community_stats_snapshot` row owned by
the `community_stats` processor. Because login, online-time, presence, and
unordered reaction counters can change without a new durable event, the
processor refreshes its snapshot on its interval even when no event has arrived.
Reaction totals read `post_reaction_count_shards` first and only fall back to
`post_reactions` identity rows during compatibility or repair windows:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -community-stats-processor \
  -community-stats-processor-interval 1s \
  -community-stats-processor-batch-size 500
```

When `-community-stats-processor` is enabled, do not include
`community_stats` in `-derived-view-watermarks`; startup rejects that
combination. Use `-backfill-derived-views community_stats` after deleting or
rebuilding the materialized snapshot row.

The resident-board aggregate feed now has a materialized
`resident_feed_posts` read index owned by the `feeds.resident` processor. The
API falls back to the older SQL join until the index has rows, but worker nodes
can own steady-state catch-up and rebuild from durable events:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -resident-feed-processor \
  -resident-feed-processor-interval 1s \
  -resident-feed-processor-batch-size 500
```

When `-resident-feed-processor` is enabled, do not include `feeds.resident` in
`-derived-view-watermarks`; startup rejects that combination. Use
`-backfill-derived-views feeds.resident` after deleting or rebuilding the
materialized feed table.

Board summaries now have a materialized `board_summary_stats` read model owned
by the `summaries.boards` processor. The API falls back to the legacy SQL
aggregation until the table has rows. Once populated, global board activity
counts come from the materialized table while favorites, zaps, read markers,
policy flags, and online presence stay current at read time:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -board-summaries-processor \
  -board-summaries-processor-interval 1s \
  -board-summaries-processor-batch-size 500
```

When `-board-summaries-processor` is enabled, do not include
`summaries.boards` in `-derived-view-watermarks`; startup rejects that
combination. Use `-backfill-derived-views summaries.boards` after deleting or
rebuilding the materialized summary table.

Unread thread summaries now have a materialized `unread_thread_summary_stats`
candidate table owned by the `summaries.unread_threads` processor. The API
falls back to the legacy SQL query until the table has rows. Once populated,
global thread candidates come from the materialized table while board
visibility, favorite folders, zaps, read markers, unread post counts, and
first-unread navigation stay current at read time:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -unread-thread-summaries-processor \
  -unread-thread-summaries-processor-interval 1s \
  -unread-thread-summaries-processor-batch-size 500
```

When `-unread-thread-summaries-processor` is enabled, do not include
`summaries.unread_threads` in `-derived-view-watermarks`; startup rejects that
combination. Use `-backfill-derived-views summaries.unread_threads` after
deleting or rebuilding the materialized candidate table.

Board rankings now have a materialized `board_ranking_stats` read model owned by
the `rankings.boards` processor. The `/api/v1/rankings/boards` API falls back
to the compatibility SQL aggregation until the table has rows, then applies
current board settings and membership rules at read time over the materialized
stats:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -board-rankings-processor \
  -board-rankings-processor-interval 1s \
  -board-rankings-processor-batch-size 500
```

When `-board-rankings-processor` is enabled, do not include
`rankings.boards` in `-derived-view-watermarks`; startup rejects that
combination. Use `-backfill-derived-views rankings.boards` after deleting or
rebuilding the materialized ranking table.

Thread rankings now have a materialized `thread_ranking_stats` read model owned
by the `rankings.threads` processor. Because hot-thread scores include
reaction counts, and reactions are unordered side-store writes, this processor
refreshes its materialized table on its interval even when no new durable event
has arrived. Both fallback and materialized thread ranking counts read
`post_reaction_count_shards` first, then fall back to `post_reactions` identity
rows if shard rows are missing:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -thread-rankings-processor \
  -thread-rankings-processor-interval 1s \
  -thread-rankings-processor-batch-size 500
```

When `-thread-rankings-processor` is enabled, do not include
`rankings.threads` in `-derived-view-watermarks`; startup rejects that
combination. Use `-backfill-derived-views rankings.threads` after deleting or
rebuilding the materialized ranking table.

Reply rankings now have a materialized `reply_ranking_posts` read model owned
by the `rankings.replies` processor. The processor materializes candidate reply
post ids from durable events while the API still joins current post body,
thread, board, and membership policy at read time:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -reply-rankings-processor \
  -reply-rankings-processor-interval 1s \
  -reply-rankings-processor-batch-size 500
```

When `-reply-rankings-processor` is enabled, do not include
`rankings.replies` in `-derived-view-watermarks`; startup rejects that
combination. Use `-backfill-derived-views rankings.replies` after deleting or
rebuilding the materialized ranking table.

User rankings now have a materialized `user_ranking_stats` read model owned by
the `rankings.users` processor. Because user rankings include post counts,
reaction counts, login counts, online seconds, and trust level, the processor
refreshes its materialized table on its interval even when no new durable event
has arrived. Received-reaction ranking counts use `post_reaction_count_shards`
first, with `post_reactions` identity rows retained as the compatibility
fallback:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -user-rankings-processor \
  -user-rankings-processor-interval 1s \
  -user-rankings-processor-batch-size 500
```

When `-user-rankings-processor` is enabled, do not include `rankings.users` in
`-derived-view-watermarks`; startup rejects that combination. Use
`-backfill-derived-views rankings.users` after deleting or rebuilding the
materialized ranking table.

Blessing rankings now have a materialized `blessing_ranking_stats` read model
owned by the `rankings.blessings` processor:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -blessing-rankings-processor \
  -blessing-rankings-processor-interval 1s \
  -blessing-rankings-processor-batch-size 500
```

When `-blessing-rankings-processor` is enabled, do not include
`rankings.blessings` in `-derived-view-watermarks`; startup rejects that
combination. Use `-backfill-derived-views rankings.blessings` after deleting or
rebuilding the materialized ranking table.

Archive rankings now have a materialized `archive_ranking_stats` read model
owned by the `rankings.archives` processor. The API falls back to the legacy SQL
aggregation until the table has rows, then applies current board settings and
membership rules over the materialized archive path counts:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -archive-rankings-processor \
  -archive-rankings-processor-interval 1s \
  -archive-rankings-processor-batch-size 500
```

When `-archive-rankings-processor` is enabled, do not include
`rankings.archives` in `-derived-view-watermarks`; startup rejects that
combination. Use `-backfill-derived-views rankings.archives` after deleting or
rebuilding the materialized ranking table.

Digest curation now emits replayable durable events for entry, directory, and
subtree path mutations. A worker can own the `search.digest` watermark by
scanning those events:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -digest-search-processor \
  -digest-search-processor-interval 1s \
  -digest-search-processor-batch-size 500
```

When `-digest-search-processor` is enabled, do not include `search.digest` in
`-derived-view-watermarks`; startup rejects that combination. Until digest search
is backed by an external index, the processor advances the event-consumer
watermark while the SQL digest tables remain the serving read model and can be
repaired by event-log rebuild.

`community_stat_history` can also move off inline presence/logout/stat-publish
calls. Command-producing nodes enqueue a coalesced daily snapshot job, and the
worker outbox materializes the row and advances the `community_stat_history`
watermark:

```sh
# Public API/gateway node: presence/logout paths enqueue stat-history refresh work.
./budgied \
  -roles api,gateway,ssh,nntp \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -async-community-stat-history

# Worker/projection node: the normal worker outbox drains stat-history jobs and
# grouped processors own every promoted IS6 derived view on default cadence.
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -derived-view-processors all
```

When `-async-community-stat-history` is enabled, do not include
`community_stat_history` in `-derived-view-watermarks`; startup rejects that
combination on nodes using the async mode. Watch
`budgie_derived_view_lag_events{view="community_stat_history"}` for outbox
backlog or stalled snapshot ownership.

## Docker / docker-compose cluster

A ready-to-run cluster lives in `docker-compose.yml` (nginx LB → 2 API nodes +
1 SSH node + 1 worker node + Postgres):

```sh
docker compose up -d --build      # build the image and start the cluster
curl -fsS http://localhost:8080/readyz   # LB -> an API node
```

The compose file uses a one-shot `init` service (`budgied -init-db`) that applies
the schema before the nodes start, an nginx config (`deploy/nginx.conf`) with
WebSocket upgrade handling and no sticky sessions, and a shared
`BUDGIE_JWT_SECRET` across nodes.

To prove cross-node delivery against the compose cluster end-to-end:

```sh
./scripts/compose-cluster-smoke.sh
```

It brings the stack up, waits for two API nodes to report ready, runs the
cross-node smoke (write on api1 must reach a live stream on api2), and tears the
stack down. Requires Docker (with the compose plugin), `curl`, and `jq`.

## systemd unit (per node)

```ini
# /etc/systemd/system/budgie.service
[Unit]
Description=BudgieBBS node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=budgie
Group=budgie
WorkingDirectory=/opt/budgie
Environment=BUDGIE_JWT_SECRET=the-same-long-random-string-on-every-node
Environment=BUDGIE_POSTGRES_DSN=postgres://budgie:secret@db.internal:5432/budgie?sslmode=require
ExecStart=/opt/budgie/budgied -storage postgres -http :8080 -ssh 2222 \
  -hostkey /var/lib/budgie/budgie_host_key
Restart=on-failure
RestartSec=2
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/budgie

[Install]
WantedBy=multi-user.target
```

## Load balancer

- Route REST/API traffic to `api` nodes. Route WS/SSE/long-poll event traffic
  to `api` or `gateway` nodes; **no sticky sessions**.
- Health check `GET /readyz` — a node returns `503` until Postgres is reachable,
  which keeps half-started nodes out of rotation.
- WebSocket upgrades must be allowed (long-lived connections; disable idle
  timeouts shorter than the 30s heartbeat or set them generously).
- SSH (port 2222) is TCP; balance it with a TCP load balancer or pin SSH to one
  node. Mixed host keys across nodes will trip SSH client host-key checks unless
  you share one key file.

## Observability

`GET /metrics` on each node serves Prometheus text. Scrape every node; the
cluster view is the aggregate. Key series:

| Metric | Type | Meaning |
|--------|------|---------|
| `budgie_ws_connections` | gauge | Open WebSocket connections on the node. |
| `budgie_ssh_sessions` | gauge | Open SSH TUI sessions on the node. |
| `budgie_bus_local_subscribers` | gauge | Active local event-bus subscriptions. |
| `budgie_events_published_local_total` | counter | Events delivered to a local subscriber. |
| `budgie_events_published_remote_total` | counter | Events forwarded to the broker. |
| `budgie_events_ingested_remote_total` | counter | Events received from a sibling and republished. |
| `budgie_events_remote_publish_failures_total` | counter | Failed broker publishes. |
| `budgie_events_remote_decode_failures_total` | counter | Broker messages that could not be decoded. |
| `budgie_event_log_shadow_append_failures_total` | counter | Shadow event-log appends that failed while SQL remained authoritative. |
| `budgie_event_log_shadow_parity_failures_total` | counter | Shadow event-log replay, coverage, or append comparisons that did not match SQL. |
| `budgie_bus_dropped_sends_total` | counter | Live events dropped to a full subscriber channel. |
| `budgie_gateway_dropped_sends_total{scope}` | counter | Live events dropped to a full gateway connection queue, split by event scope. |
| `budgie_gateway_connection_queue_depth{stat}` | gauge | Current total and max queued live events across local gateway connection buffers. |
| `budgie_gateway_connection_queue_capacity{stat}` | gauge | Current total and max gateway connection buffer capacity on the node. |
| `budgie_gateway_scope_subscribers{scope}` | gauge | Local gateway subscribers by event scope, capped to the hottest sampled scopes. |
| `budgie_gateway_reconnects_total` | counter | WS/SSE stream resumes with a non-zero durable cursor. |
| `budgie_gateway_replay_repairs_total` | counter | WS/SSE live-delivery gaps repaired from the durable event log. |
| `budgie_gateway_ws_send_latency_ms` | histogram | WebSocket gateway JSON write latency. |
| `budgie_gateway_sse_send_latency_ms` | histogram | SSE gateway event write latency. |
| `budgie_write_region_routed_requests_total{method}` | counter | Mutating HTTP API requests proxied to the authoritative write region. |
| `budgie_write_region_proxy_failures_total` | counter | Mutating HTTP API requests that could not reach the authoritative write region. |
| `budgie_remote_wakeup_lag_ms` | histogram | Sibling event timestamp → local receipt delay. |
| `budgie_replay_total` / `budgie_replay_batch_size` | counter / histogram | Gap-recovery replays and their size. |
| `budgie_command_latency_ms` | histogram | Command handler execution time. |
| `budgie_writer_lock_wait_ms` | histogram | Time waiting for the writer advisory lock. |
| `budgie_writer_partition_lock_wait_count{kind,key}` | counter | Command-lock acquisitions by write-ordering partition, capped to the hottest partitions. |
| `budgie_writer_partition_lock_wait_ms_sum{kind,key}` | counter | Total command-lock wait time by write-ordering partition. |
| `budgie_writer_partition_lock_wait_ms_max{kind,key}` | gauge | Maximum observed command-lock wait time by write-ordering partition. |
| `budgie_scalar_append_gate_wait_ms` | histogram | Time waiting for scalar `seq` append ordering. |
| `budgie_event_partition_offset{kind,key}` | gauge | Latest durable event offset by write-ordering partition. |
| `budgie_event_partition_count` | gauge | Number of write-ordering partitions with durable events. |
| `budgie_event_partition_offset_skew` | gauge | Difference between hottest and coolest partition offsets. |
| `budgie_command_partition_tail_offset{kind,key}` | gauge | Latest produced command-log offset by write-ordering partition. |
| `budgie_command_partition_committed_offset{kind,key}` | gauge | Latest writer-committed command-log offset by write-ordering partition. |
| `budgie_command_partition_lag{kind,key}` | gauge | Produced minus writer-committed command-log offsets by write-ordering partition. |
| `budgie_hot_partition_candidate{kind,key,signal}` | gauge | Hot write-ordering partition candidate with value set to the signal magnitude. Signals include `command_lag`, `command_count`, `writer_lock_wait_ms_max`, `gateway_subscribers`, and `gateway_drops`. |
| `budgie_command_partition_count` | gauge | Number of command-log partitions sampled for writer lag metrics. |
| `budgie_command_partition_lag_total` | gauge | Total sampled command-log writer lag across partitions. |
| `budgie_command_partition_lag_max` | gauge | Largest sampled command-log writer lag on one partition. |
| `budgie_command_partition_lag_skew` | gauge | Difference between largest and smallest sampled command-log writer lag. |
| `budgie_command_partition_assigned{kind,key,owner_id}` | gauge | 1 when this writer owns the sampled command-log assignment, else 0. |
| `budgie_command_partition_assignment_generation{kind,key,owner_id}` | gauge | Assignment generation observed for a sampled command-log partition. |
| `budgie_command_partition_assigned_count` | gauge | Number of sampled command-log partitions currently assigned to this writer. |
| `budgie_command_log_assignment_losses_total` | counter | Writer drains stopped because partition assignment was lost. |
| `budgie_command_log_receipts{status}` | gauge | Command-log receipt rows by status (`applied`, `retrying`, `failed`, and any custom states). |
| `budgie_command_log_receipt_oldest_age_ms{status}` | gauge | Age of the oldest command-log receipt row by status. |
| `budgie_attachment_blob_staging_blobs{kind,state}` | gauge | Staged post/mail attachment blob rows waiting for writer promotion or cleanup, split into `total` and `expired`. |
| `budgie_attachment_blob_staging_bytes{kind,state}` | gauge | Bytes held in staged post/mail attachment blobs, split into `total` and `expired`. |
| `budgie_attachment_blob_staging_oldest_age_ms{kind}` | gauge | Age of the oldest staged post/mail attachment blob row. |
| `budgie_derived_view_applied_seq{view}` | gauge | Durable event seq each named derived view has applied. |
| `budgie_derived_view_lag_events{view}` | gauge | Durable event head minus each named derived view's applied seq. |
| `budgie_outbox_jobs{status}` | gauge | Outbox jobs by status (pending/running/done/dead). |
| `budgie_worker_is_leader` | gauge | 1 on the active background-worker leader, else 0. |

Watch `budgie_writer_lock_wait_ms` (write contention across nodes/partitions),
`budgie_writer_partition_lock_wait_*` (which partitions are hottest),
`budgie_scalar_append_gate_wait_ms` (scalar cursor ordering pressure),
`budgie_command_partition_lag*` (whether experimental command-log writers are
keeping up), `budgie_hot_partition_candidate` (which partitions are candidates
for split, reassignment, or writer-capacity action; use `rate()` over the
`command_count` signal for command-rate alerts), `budgie_command_partition_assigned*` plus
`budgie_command_log_assignment_losses_total` (whether writer assignment and
rebalance are stable), `budgie_command_log_receipt_oldest_age_ms{status="retrying"}`
(whether retryable command-log failures are clearing), `budgie_derived_view_lag_events`
(whether search, rankings, stats, and other global views are caught up),
`budgie_gateway_connection_queue_depth{stat="max"}` plus
`budgie_gateway_dropped_sends_total` (whether slow gateway clients are forcing
drop-and-replay recovery), `budgie_gateway_replay_repairs_total` (whether those
drops are being repaired by replay), gateway send latency histograms, and
`budgie_remote_wakeup_lag_ms` (cross-node delivery latency; target p95 < 500ms
on a LAN), and `budgie_write_region_proxy_failures_total` (regional write-route
health). A rising `budgie_bus_dropped_sends_total` paired with
`budgie_gateway_replay_repairs_total` and `budgie_replay_*` is normal
backpressure recovery; sustained high drops mean slow consumers.

### Internet-scale SLO alerts

Prometheus alert rules for the IS9 capacity and SLO gates live in
[`ops/prometheus/budgie-internet-scale-alerts.yml`](ops/prometheus/budgie-internet-scale-alerts.yml).
Load that file into the same Prometheus that scrapes `/metrics` on every node.
The rules cover:

- live-delivery p95 (`budgie_remote_wakeup_lag_ms`)
- gateway queue saturation, drops, and replay repair
- derived-view lag for async projections
- command-log writer lag, assignment loss, and stuck retrying receipts
- regional write-route failures
- command latency and partition lock wait
- dead outbox jobs
- event-log shadow parity failures
- hot partition candidates for capacity, split, or reassignment action

The matching Grafana dashboard lives at
[`ops/grafana/budgie-internet-scale-dashboard.json`](ops/grafana/budgie-internet-scale-dashboard.json).
Import it with your Prometheus datasource and keep it next to the alert rules.
It graphs the same signals by role and partition: connection capacity
(`budgie_ws_connections`, gateway queue capacity/depth, hot subscriber scopes),
write capacity (`budgie_command_partition_lag*`,
`budgie_writer_partition_lock_wait_*`, `budgie_hot_partition_candidate`,
retrying command-log receipts),
broker egress/repair (`budgie_events_published_remote_total`,
`budgie_events_ingested_remote_total`, replay repairs), and regional write
egress (`budgie_write_region_routed_requests_total`). Those panels make the
cost of extra gateways, writers, hot-thread splits, and cross-region write
routing visible before changing topology.

### Distributed tracing (OpenTelemetry)

Each node can export OpenTelemetry traces over OTLP/HTTP. Enable it with the
`-otel-tracing` flag, or simply by setting `OTEL_EXPORTER_OTLP_ENDPOINT` (the
flag auto-enables when that env var is present). All the standard `OTEL_*`
environment variables are honored for endpoint, headers, and protocol; head
sampling is `-otel-sample-ratio` (default 1.0).

When enabled, the HTTP server is wrapped with `otelhttp`, so every request
becomes a server span (continuing any inbound `traceparent`), and the
write-region proxy hop propagates W3C trace context — so a write routed from an
edge node to the write region shows up as a single distributed trace. Spans
carry `service.name=budgied`, `service.version`, and a `budgie.node_id` resource
attribute (the hostname) so you can slice traces by node. Tracing is off by
default and has no cost when disabled; an unreachable collector is non-fatal
(spans are dropped, the server keeps serving).

Point the endpoint at any OTLP collector (e.g. an OpenTelemetry Collector,
Jaeger, Tempo):

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
budgied -roles api ...   # tracing auto-enabled
```

## Failure modes and recovery

| Failure | Behavior | Recovery |
|---------|----------|----------|
| A node crashes | Its WS/SSH clients disconnect. Other nodes unaffected; writes continue. | LB stops routing on failed `/readyz`. Clients reconnect to another node and replay from their cursor. |
| Postgres briefly unavailable | Writes fail with retryable errors; `/readyz` returns `503`. | Nodes recover automatically when Postgres returns; the `pq.Listener` reconnects (re-`LISTEN`s) on its own. |
| `LISTEN/NOTIFY` wakeup missed (reconnect window) | A live event may not push to sibling nodes. | Self-healing: the next event or a client reconnect triggers gap detection and durable replay. No durable loss. |
| Advisory lock held too long / timeout | Command returns `lock_unavailable` (retryable). | Client retries; investigate the slow command via `budgie_command_latency_ms`. |
| Node restart | In-memory node registry and presence reset for that node. | Durable state intact in Postgres. Presence recovers as clients reconnect. |

Broker note: without `-nats`, the "broker" here is Postgres itself. There is no
separate broker process to restart. The listener (`github.com/lib/pq`'s
`Listener`) reconnects automatically with backoff; missed notifications during
the gap are covered by replay, so no manual intervention is needed after a
Postgres blip.

With `-nats`, a NATS outage affects live cross-node delivery but not durable
writes. Publish failures increment
`budgie_events_remote_publish_failures_total`. Clients recover missed durable
events through replay on reconnect or the next detected gap.

### Single-region failure drill

The executable IS9 drill lives at
[`ops/runbooks/single-region-failure-drill.md`](ops/runbooks/single-region-failure-drill.md).
Run it in staging before promoting regional read/API nodes or authoritative
command-log writers, and repeat it after changing NATS, write-region routing,
projection processors, writer assignment, or hot-partition split policy.

The drill covers:

- regional write-route outage and retryable `write_region_unavailable`
- stale projection detection with `X-Budgie-Min-Seq` and `projection_stale`
- NATS/live broker outage with durable replay recovery
- command-log writer crash or reassignment recovery
- evidence capture, rollback, and exit criteria

## Backup and restore (Postgres)

Standard Postgres backups apply. The event log (`events`) plus `event_scopes` are
the source of truth; projection tables can always be rebuilt from them.

```sh
# Logical backup (per-database):
pg_dump --format=custom --file=budgie-$(date +%F).dump "$BUDGIE_POSTGRES_DSN"

# Restore into an empty database:
pg_restore --clean --if-exists --dbname "$BUDGIE_POSTGRES_DSN" budgie-2026-06-08.dump
```

For point-in-time recovery, use continuous archiving (`pg_basebackup` + WAL
archiving) per your Postgres operations standard.

After any restore where projections might lag the event log, rebuild them (run
once from any single node, ideally with the cluster quiesced):

```sh
./budgied -storage postgres -postgres-dsn "$BUDGIE_POSTGRES_DSN" -rebuild-projections
# Optionally replay only events after a known-good seq:
./budgied -storage postgres -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -rebuild-projections -rebuild-from-seq 12345
```

For lagging async/global views, rebuild the projection source and advance only
the named derived-view watermarks:

```sh
./budgied -storage postgres -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -backfill-derived-views search

# Or repair every known derived view from a broker shadow event log:
./budgied -storage postgres -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -backfill-derived-views all \
  -rebuild-source nats \
  -event-log-shadow-nats-stream BUDGIE_EVENT_LOG
```

Broker-source derived-view backfills run the same promotion-readiness preflight
as full projection rebuilds before applying watermarks.

`-backfill-derived-views` accepts individual view names, `all`, and operational
groups: `search`, `rankings`, `summaries`, `community`, and `feeds`. Use
[`ops/runbooks/projection-search-rebuild.md`](ops/runbooks/projection-search-rebuild.md)
for the routine repair checklist, validation steps, NATS-shadow rebuild path,
and rollback/escalation flow.

### Counter-store repair

By default, high-volume reaction and poll-vote traffic is off the ordered
durable event log but still stored in SQL side-store tables: `post_reactions`
and `poll_votes`, with derived aggregate shards in
`post_reaction_count_shards` and `poll_vote_count_shards`. Treat identity rows
as backup/PITR-protected state; shard rows can be rebuilt from that identity
state. The command path talks to a backend-neutral `CounterStore` mutation
lifecycle, while the SQL adapter hides SQL transactions inside that store.
`MemoryCounterStore` is available for non-SQL adapter fixtures and tests.
`-counter-store nats-kv` is the first durable distributed adapter and stores
identity plus aggregate shards in JetStream KV; keep every serving/writing node
on the same backend during a rollout. Counter checkpoints read aggregate counts
from the configured counter store. Board membership `minScore` and
`minBoardMarkCount` admission checks also read authored-post reaction counts
from the active counter store rather than SQL reaction identity rows. Admin hard
account deletion snapshots and removes the deleted user's reaction and poll-vote
identity from the active counter store, decrementing aggregate shards and
received-reaction counters. Projection rebuilds preserve existing unordered SQL
rows and restore SQL shard aggregates, but ordered event replay cannot recreate
reaction or poll-vote identity after the active counter store's identity rows
are lost.
When `-counter-store nats-kv` identity keys are intact but aggregate shard keys
are missing or stale, run a single repair owner with writes paused or routed
away from the affected bucket:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -counter-store nats-kv \
  -counter-store-nats-bucket BUDGIE_COUNTER_STORE \
  -counter-store-shards 64 \
  -repair-counter-store-aggregates
```

Use [`ops/runbooks/counter-store-repair.md`](ops/runbooks/counter-store-repair.md)
to repair `user_activity.reactions_recv`, restore SQL side-store rows or a NATS
KV bucket from backup, rebuild projections, backfill affected `rankings`,
`search`, and `community` views, and separate today's SQL repair from the
counter-shard failure drill.

### Chat-store repair

Chat history is high-volume unordered state and does not enter the durable
ordered event log. SQL remains the default bounded-history store, while
`-chat-store nats-kv` stores room metadata and recent transcript records in
JetStream KV. Keep every serving/writing node on the same backend during a
rollout. Ordered replay cannot recreate recent chat transcript rows after the
active chat store is lost; restore the SQL tables or NATS KV bucket from backup
when operators need transcript continuity.

After the one-shot repair, keep a worker node's compatibility sync enabled only
for selected views that are still updated synchronously by Budgie itself and are
not owned by dedicated processors. A fully promoted worker does not need
`-derived-view-watermarks`. If a staged rollout deliberately leaves a view on
the old synchronous path for one deploy, keep only that not-yet-promoted view in
the temporary list:

```sh
./budgied -roles worker -storage postgres -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -derived-view-watermarks summaries.boards
```

## Cluster smoke test

After bringing up the cluster (or before promoting it), prove cross-node
delivery with the bundled script. It starts two local nodes against your Postgres
DSN, creates a thread and a like on node A, and verifies node B observes both:

```sh
BUDGIE_POSTGRES_DSN="postgres://budgie:secret@localhost:5432/budgie?sslmode=disable" \
  ./scripts/cluster-smoke.sh
```

To validate the NATS delivery path, set `BUDGIE_NATS_URL`; the smoke script
passes the environment through to both local nodes:

```sh
BUDGIE_POSTGRES_DSN="postgres://budgie:secret@localhost:5432/budgie?sslmode=disable" \
BUDGIE_NATS_URL="nats://localhost:4222" \
  ./scripts/cluster-smoke.sh
```

See [`scripts/cluster-smoke.sh`](scripts/cluster-smoke.sh) for options. The script
requires `curl` and `jq`. A passing run is the operational definition of "the
cluster delivers cross-node events."

## Gateway fanout load fixture

IS7 includes a synthetic gateway fanout fixture that exercises one hot scope
alongside many idle subscriber scopes without requiring a browser farm. For
operator-facing capacity runs, use the JSON-emitting command:

```sh
go run ./cmd/budgie-gateway-loadgen \
  -hot-subscribers 10000 \
  -idle-subscribers 100000 \
  -buffer-size 2 \
  -events 1 \
  -target-connections 1000000 \
  -max-publish-ms 100 \
  -budget-file ops/internet-scale-budgets.example.json
```

To prove slow-client queues drop instead of blocking fanout, publish more events
than each per-connection buffer can hold:

```sh
go run ./cmd/budgie-gateway-loadgen \
  -hot-subscribers 10000 \
  -idle-subscribers 100000 \
  -buffer-size 2 \
  -events 3 \
  -target-connections 1000000 \
  -min-drops 10000 \
  -budget-file ops/internet-scale-budgets.example.json
```

The normal test suite runs a modest default; staged runs can also scale it by
setting:

```sh
BUDGIE_GATEWAY_LOAD_HOT_SUBSCRIBERS=10000 \
BUDGIE_GATEWAY_LOAD_IDLE_SUBSCRIBERS=100000 \
BUDGIE_GATEWAY_LOAD_BUFFER_SIZE=2 \
GOCACHE=/private/tmp/budgie-go-cache go test ./internal/core \
  -run TestGatewayFanoutManyIdleSubscribers -count=1 -v
```

The fixture proves a hot event queues exactly once for matching local
subscribers while idle subscribers remain empty, reports publish timing for the
selected synthetic size, and can estimate bounded-queue drops when hot
subscribers are intentionally left slow. `-target-connections` adds the staged
connection target to the JSON report, including the projected gateway node count
needed at the measured per-node subscriber capacity. When the flag is omitted,
`gatewayFanout.targetConnections` from the budget file supplies the same target.
Copy `ops/internet-scale-budgets.example.json` to a deployment-specific file and
tune the `gatewayFanout` values before treating `-budget-file` as a release
gate.

## Validating Postgres mode in CI

The full Go test suite can run against a real Postgres instead of SQLite, which
exercises every command, projection, and read path against the production
backend. Set `BUDGIE_TEST_POSTGRES_DSN`; each test provisions an isolated schema
and tears it down afterward:

```sh
BUDGIE_TEST_POSTGRES_DSN="postgres://postgres@localhost:5432/budgie?sslmode=disable" \
  go test ./internal/core/... ./internal/httpapi/...
```

The DSN's role needs permission to `CREATE SCHEMA` / `DROP SCHEMA` on the target
database (a throwaway database is recommended). Without the env var the suite
runs on SQLite as usual.

For the IS0/IS3 write-throughput gate, run the dedicated load fixture against a
throwaway Postgres database. It provisions an isolated schema, executes the same
number of `createThread` commands in one board partition and across many board
partitions, then emits JSON with writes/sec, latency percentiles, failures, and
spread-partition speedup:

```sh
BUDGIE_POSTGRES_DSN="postgres://postgres@localhost:5432/budgie?sslmode=disable" \
  go run ./cmd/budgie-loadgen \
    -boards 8 \
    -writes-per-board 100 \
    -concurrency 32 \
    -min-spread-speedup 1.5 \
    -budget-file ops/internet-scale-budgets.example.json
```

Use `-min-spread-writes-per-sec` once a cluster-specific budget is established.
For broader promotion checks, copy `ops/internet-scale-budgets.example.json`,
tune the `postgresWrites` values from repeated staging runs, and pass that file
with `-budget-file`.
Use `-keep-schema` only for forensic inspection; the default drops the temporary
schema after the report is printed. To exercise the same gate inside Go tests:

```sh
BUDGIE_TEST_POSTGRES_DSN="postgres://postgres@localhost:5432/budgie?sslmode=disable" \
BUDGIE_TEST_POSTGRES_LOAD=1 \
BUDGIE_TEST_POSTGRES_MIN_SPREAD_SPEEDUP=1.5 \
  go test ./internal/core -run TestPostgresPartitionWriteLoadReportsSpreadThroughput -count=1 -v
```

## Definition of done (operational)

- Two+ nodes run against one Postgres database.
- A user on any node receives durable events created through any other node.
- Writes are globally ordered (strictly increasing `seq`).
- Reconnect and replay work across nodes; duplicates are tolerated.
- Chat works across nodes.
- No sticky sessions required.
- `./scripts/cluster-smoke.sh` passes.
