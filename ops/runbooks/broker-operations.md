# Broker Operations Runbook

Budgie's durable source of truth remains Postgres until a broker event log is
explicitly promoted. Broker delivery is best-effort; durable replay must always
repair missed live delivery.

Today the implemented broker backends are NATS JetStream and a
Redpanda/Kafka-compatible command/event staging path. NATS remains the live
delivery and JetStream/KV operations baseline; Redpanda/Kafka is the
internet-scale target for production-authoritative command/event logs and must
still pass the checklist here before promotion.

## Broker Roles

| Role | Current backend | Purpose |
|------|-----------------|---------|
| Live delivery | NATS core subjects | Cross-node WS/SSE/long-poll fanout without Postgres wakeup reads |
| Event-log shadow | NATS JetStream | Broker-shaped copy of durable events for parity and rebuild tests |
| Command-log shadow | NATS JetStream | Broker-shaped command stream populated while SQL remains authoritative |
| Authoritative command log | NATS JetStream | Public nodes append command receipts for writer nodes to drain |
| Writer assignment | NATS JetStream KV | Shared command-partition ownership generation and overrides |
| Staging event/command logs | Redpanda/Kafka | Target production broker path; local durable gates are available while remote promotion evidence is collected |

## Preflight

1. Confirm all Budgie nodes use the same `BUDGIE_NATS_URL` when NATS is enabled.

```sh
export BUDGIE_NATS_URL="nats://nats.internal:4222"
export BUDGIE_EVENT_LOG_STREAM="BUDGIE_EVENT_LOG"
export BUDGIE_COMMAND_LOG_STREAM="BUDGIE_COMMAND_LOG"
export BUDGIE_COMMAND_ASSIGNMENT_BUCKET="BUDGIE_COMMAND_ASSIGNMENTS"
```

2. Confirm Budgie metrics are clean:

```sh
curl -fsS "$BUDGIE_API/metrics" |
  grep -E 'budgie_events_remote_publish_failures_total|budgie_events_remote_decode_failures_total|budgie_event_log_shadow_parity_failures_total|budgie_command_partition_lag|budgie_command_log_assignment_losses_total'
```

3. Confirm the cluster smoke passes with NATS enabled:

```sh
BUDGIE_NATS_URL="$BUDGIE_NATS_URL" ./scripts/cluster-smoke.sh
```

## Live Delivery NATS Operations

Budgie publishes full event envelopes to scope subjects under
`budgie.events.scope.<encoded-scope>`. Nodes subscribe only to scopes with local
clients and dedupe multi-scope events before local fanout.

Expected healthy behavior:

- `budgie_events_published_remote_total` increases when writes occur on a NATS
  enabled node.
- `budgie_events_ingested_remote_total` increases on sibling nodes with matching
  subscribers.
- `budgie_events_remote_publish_failures_total` stays flat.
- `budgie_events_remote_decode_failures_total` stays flat.
- `budgie_remote_wakeup_lag_ms` p95 stays under the alert threshold.

NATS outage behavior:

- durable writes continue through Postgres or the authoritative command log
- live cross-node delivery may lag or miss messages
- clients repair missed durable events through replay on reconnect or gap
  detection
- `BudgieRemoteDeliveryLagHigh` or publish-failure counters may fire

Recovery:

```sh
echo "Restore NATS service or regional network route"
BUDGIE_NATS_URL="$BUDGIE_NATS_URL" ./scripts/cluster-smoke.sh
curl -fsS "$BUDGIE_API/metrics" |
  grep -E 'budgie_events_remote_publish_failures_total|budgie_gateway_replay_repairs_total|budgie_remote_wakeup_lag_ms'
```

## JetStream Event-Log Shadow

Run this before promoting any broker event-log path:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -event-log-shadow nats \
  -event-log-shadow-nats-stream "$BUDGIE_EVENT_LOG_STREAM" \
  -event-log-shadow-interval 30s
```

Expected stream properties:

- direct reads enabled
- subject coverage includes `budgie.eventlog.>`
- one logical event partition per subject
- Budgie partition offsets stored in each broker record
- fresh deployments use a fresh stream or seed checkpoints from SQL heads

Promotion gate:

- `budgie_event_log_shadow_append_failures_total` remains flat
- `budgie_event_log_shadow_parity_failures_total` remains zero
- broker replay returns the same scalar and partition order as SQL replay
- `budgied -check-event-log-promotion-readiness` exits `0` against the NATS
  stream with `-event-log-shadow-partition-limit 0`
- projection rebuild from `-rebuild-source nats` or `-rebuild-source kafka`
  succeeds in staging, depending on the candidate broker event source
- worker projection from `-event-store-projection nats` or
  `-event-store-projection kafka` advances
  `event-store-projection:*` rows in `derived_view_watermarks` while SQL
  projections match broker replay

Repair if parity fails:

1. Do not promote the broker event log.
2. Stop the shadow writer for the affected stream.
3. Preserve the stream for inspection.
4. Rebuild projections from SQL, not NATS.
5. Create a fresh shadow stream and let parity catch up from SQL heads.

## JetStream Command Log

Shadow command log:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-shadow nats \
  -command-log-shadow-nats-stream "$BUDGIE_COMMAND_LOG_STREAM"
```

Authoritative command submission:

```sh
./budgied \
  -roles api,gateway,ssh,nntp \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-authoritative nats \
  -command-log-authoritative-nats-stream "$BUDGIE_COMMAND_LOG_STREAM"
```

Writer drain:

```sh
./budgied \
  -roles writer \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-worker nats \
  -command-log-worker-nats-stream "$BUDGIE_COMMAND_LOG_STREAM" \
  -command-log-worker-ownership nats-kv \
  -command-log-worker-assignment-bucket "$BUDGIE_COMMAND_ASSIGNMENT_BUCKET" \
  -command-log-worker-id writer-a \
  -command-log-worker-group-members writer-a,writer-b
```

Expected stream properties:

- command subjects under `budgie.commandlog.>`
- commit marker subjects under `budgie.commandcommit.>`
- direct reads enabled
- duplicate window configured for command receipt idempotency
- `Nats-Msg-Id` set from the command receipt id

Healthy writer signals:

- `budgie_command_partition_tail_offset` advances as public nodes accept
  commands
- `budgie_command_partition_committed_offset` catches up as writers drain
- `budgie_command_partition_lag` returns to the steady-state budget
- `budgie_command_log_assignment_losses_total` is flat outside planned
  rebalances

## Durable Native Command/Event Staging Gate

Run this before promoting NATS-backed authoritative command submission or
native writer execution beyond a trial. It must use a disposable staging
Postgres schema and distinct JetStream streams for commands and broker-native
events; a temporary SQLite materialization database is not promotion evidence.

Required inputs:

```sh
export BUDGIE_NATS_URL="nats://nats.internal:4222"
export BUDGIE_POSTGRES_DSN="postgres://budgie@postgres.internal:5432/budgie_staging?sslmode=require"
```

Optional stream names. When omitted, the wrapper generates unique per-run
JetStream names under the same promoted prefixes:

```sh
export BUDGIE_COMMAND_LOG_LOAD_STREAM="BUDGIE_COMMAND_LOG_LOAD_STAGING"
export BUDGIE_EVENT_LOG_LOAD_STREAM="BUDGIE_EVENT_LOG_LOAD_STAGING"
```

Optional replica counts. The defaults are `1`, which is enough for disposable
localhost validation; shared staging can raise them to match the staging
JetStream cluster:

```sh
export BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS=3
export BUDGIE_COMMANDLOG_GATE_EVENT_REPLICAS=3
```

Preferred promotion command:

```sh
./scripts/commandlog-native-nats-gate.sh
```

Run throughput signoff from a host colocated with NATS, Postgres, and Redis, or
from an equivalently low-jitter path. The remote-staging budget rejects
loopback endpoint evidence, but it cannot prove the client path is suitable for
throughput measurement. A noisy LAN can make the same healthy services look
blocked because the load generator waits on acknowledged broker/database
operations. In staging diagnostics, the app-path load generator ran at `4,013`
submit cmd/s and `1,577` drain cmd/s on host loopback, while the same services
looked roughly two orders of magnitude slower from a jittery workstation path.
Treat high-jitter remote runs as wiring diagnostics, not promotion throughput
evidence.

The script writes
`artifacts/internet-scale/commandlog-native-nats-report.json` by default, then
runs `cmd/budgie-commandlog-report-check` against the same budget before
exiting. Tune load size with `BUDGIE_COMMANDLOG_GATE_BOARDS`,
`BUDGIE_COMMANDLOG_GATE_COMMANDS`, `BUDGIE_COMMANDLOG_GATE_REPLIES`,
`BUDGIE_COMMANDLOG_GATE_WRITERS`, `BUDGIE_COMMANDLOG_GATE_BATCH_SIZE`, and
`BUDGIE_COMMANDLOG_GATE_TIMEOUT`; tune stream replication with
`BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS` and
`BUDGIE_COMMANDLOG_GATE_EVENT_REPLICAS`. The wrapper generates unique default
command and event stream names for each run and still requires any explicit
stream overrides to use the promoted `BUDGIE_COMMAND_LOG_LOAD_` and
`BUDGIE_EVENT_LOG_LOAD_` prefixes. The wrapper rejects weaker-than-budget load
overrides before connecting to staging: at least 8 boards, 100 commands per
board, 2 replies per thread, 8 writers, batch size 25, and 2400 total commands.
Replica overrides must be positive integers. It does not accept extra load
generator flags, so the promoted executor, backend, stream, schema, budget,
load-shape, and replica arguments cannot be overridden after wrapper preflight.
It pins the budget file to
`ops/internet-scale-budgets.example.json`; use the expanded command below for
experimental budgets. It resolves the Go tool from `PATH` or common install
locations for both the load generator and report checker, rather than accepting
a separate `GO_BIN` override. It also refuses to replace an existing report
unless `BUDGIE_COMMANDLOG_GATE_ALLOW_OVERWRITE=1` is set. Reports are written
to a temporary file first; the final report path is archived only after
`cmd/budgie-commandlog-report-check` accepts the artifact. Custom report paths
must stay under `artifacts/internet-scale/`, which is ignored by git. The
wrapper requires a clean git worktree before launching the load because the
promoted budget requires archived report evidence to show
`evidence.gitModified == false`.

For Redpanda/Kafka staging, use the matching wrapper once a disposable broker
pair is available:

```sh
export BUDGIE_KAFKA_BROKERS="redpanda-a.internal:9092,redpanda-b.internal:9092"
export BUDGIE_POSTGRES_DSN="postgres://budgie@postgres.internal:5432/budgie_staging?sslmode=require"
# Optional for credentialed staging clusters:
export BUDGIE_KAFKA_TLS=1
export BUDGIE_KAFKA_SASL_MECHANISM="scram-sha-512"
export BUDGIE_KAFKA_SASL_USER="budgie"
export BUDGIE_KAFKA_SASL_PASSWORD="..."
./scripts/commandlog-native-kafka-gate.sh
```

The Kafka wrapper generates unique `budgie.commands.load.*` and
`budgie.events.load.*` topics plus a unique consumer group, requires at least
32 command and 32 event topic partitions, creates missing topics with
`-kafka-topic-replicas` defaulting to `1`, preflights broker metadata with
auto-create disabled, runs `-kafka-scalar-allocator
sql-event-partition-offsets`, pins
`ops/internet-scale-kafka-budgets.example.json`, and archives
`artifacts/internet-scale/commandlog-native-kafka-report.json` only after
`cmd/budgie-commandlog-report-check` accepts the temporary report. For shared
or remote staging signoff, set `BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1`; the
wrapper then uses
`ops/internet-scale-kafka-remote-staging-budgets.example.json`, which rejects
loopback Kafka broker and Postgres endpoint evidence.

The Kafka staging credentials must allow create and describe on the generated
`budgie.commands.load.*` and `budgie.events.load.*` topics. If a topic already
exists with fewer partitions than the wrapper requested, the load generator
fails before submitting commands so broker auto-create defaults cannot produce
misleading promotion evidence. Raise
`BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS` for shared staging clusters that
require more than the disposable default replication factor.

Kafka runtime clients, topic preflight, and load-topic cleanup all honor
`BUDGIE_KAFKA_TLS`, `BUDGIE_KAFKA_TLS_CA_FILE`,
`BUDGIE_KAFKA_TLS_SERVER_NAME`, `BUDGIE_KAFKA_SASL_MECHANISM`,
`BUDGIE_KAFKA_SASL_USER`, and `BUDGIE_KAFKA_SASL_PASSWORD`. Supported SASL
mechanisms are `plain`, `scram-sha-256`, and `scram-sha-512`. Load reports
record only non-secret evidence such as `runtime.kafkaTls` and
`runtime.kafkaSaslMechanism`.

The promoted Kafka gate now opts out of the global SQL scalar allocator with
`-kafka-scalar-allocator sql-event-partition-offsets`. The writer still uses SQL
to reserve per-logical-partition event offsets for disposable staging, but it no
longer reserves `sql-event-scalar-offsets`; reports must show
`runtime.scalarCompatibilityAllocator == "sql-event-partition-offsets"` and
`scalarCompatibilityAudit.legacySqlScalarOffsetAfter == 0`.

The Kafka gate preserves generated load topics for inspection. Before rerunning
or after the report is archived, use the dry-run-first cleanup helper to delete
only promoted load-prefix topics:

```sh
./scripts/commandlog-native-kafka-cleanup.sh
./scripts/commandlog-native-kafka-cleanup.sh --execute
```

The cleanup wrapper defaults broker list/delete calls to a `30s` timeout. For
slower shared staging brokers, set
`BUDGIE_COMMANDLOG_KAFKA_CLEANUP_TIMEOUT=2m` before both the dry run and
`--execute` pass so the inspected and deleted topic set comes from the same
bounded metadata window.

The NATS command/event adapters bind broad subjects:
`budgie.commandlog.>`, `budgie.commandcommit.>`, and `budgie.eventlog.>`.
Because JetStream rejects overlapping subject ownership in the same account,
one NATS account or domain can host only one active load stream pair for this
gate at a time. The wrapper's unique stream names avoid stale stream reuse, but
they do not permit a second active pair with the same broad subjects. When the
`nats` CLI is installed and can list streams, the wrapper preflights
`budgie.commandlog.>`, `budgie.commandcommit.>`, and `budgie.eventlog.>` before
launching the load. If the CLI is installed outside the service shell's `PATH`,
set `NATS_BIN=/path/to/nats`; the scripts also check common Homebrew locations.
Before rerunning, use a fresh isolated NATS account/domain or delete only the
prior load streams after archiving the report:

```sh
./scripts/commandlog-native-nats-cleanup.sh
./scripts/commandlog-native-nats-cleanup.sh --execute
```

Equivalent direct cleanup commands:

```sh
nats --server "$BUDGIE_NATS_URL" stream rm --force BUDGIE_COMMAND_LOG_LOAD_...
nats --server "$BUDGIE_NATS_URL" stream rm --force BUDGIE_EVENT_LOG_LOAD_...
```

Do not delete non-load streams. If a staging account cannot grant list
permission to the CLI, the wrapper falls through to the load generator's
JetStream create/open validation; use
`BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT=1` only for controlled test or
diagnostic runs where the account is already known to be isolated.

Expanded promotion command:

```sh
mkdir -p artifacts/internet-scale
go run ./cmd/budgie-commandlog-loadgen \
  -command-log-worker-executor native \
  -command-log-backend nats \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-nats-stream "$BUDGIE_COMMAND_LOG_LOAD_STREAM" \
  -command-log-nats-replicas 1 \
  -event-log-nats-stream "$BUDGIE_EVENT_LOG_LOAD_STREAM" \
  -event-log-nats-replicas 1 \
  -require-postgres \
  -authoritative-submit \
  -assignment-mode snapshot-assignment \
  -replies-per-thread 2 \
  -directed-replies \
  -boards 8 \
  -commands-per-board 100 \
  -writers 8 \
  -batch-size 25 \
  -budget-file ops/internet-scale-budgets.example.json \
  > artifacts/internet-scale/commandlog-native-nats-report.json
```

Required report evidence:

- `runtime.commandLogBackend == "nats"`
- `runtime.eventLogBackend == "nats"`
- `runtime.materializationStore == "postgres"`
- `runtime.natsEndpoint` names the staging NATS endpoint without credentials
- `runtime.postgresEndpoint` names the staging Postgres endpoint without credentials
- `runtime.requirePostgres == true`
- `runtime.postgresSchema` starts with `budgie_cmdlog_load_`
- `runtime.keepPostgresSchema == false`
- `runtime.durableStaging == true`
- `runtime.commandNatsStream` and `runtime.eventNatsStream` are distinct
- `runtime.commandNatsStream` starts with `BUDGIE_COMMAND_LOG_LOAD_`
- `runtime.eventNatsStream` starts with `BUDGIE_EVENT_LOG_LOAD_`
- `runtime.commandNatsReplicas >= 1`
- `runtime.eventNatsReplicas >= 1`
- Redpanda/Kafka reports instead show `runtime.commandLogBackend == "kafka"`,
  `runtime.eventLogBackend == "kafka"`, redacted `runtime.kafkaBrokers`,
  distinct generated command/event topics under `budgie.commands.load.*` and
  `budgie.events.load.*`, `runtime.kafkaCommandPartitions >= 32`, and
  `runtime.kafkaEventPartitions >= 32`
- `config.executorMode == "native"`
- `config.authoritativeSubmit == true`
- `config.assignmentMode == "snapshot-assignment"`
- `config.directedReplies == true`
- `config.repliesPerThread >= 2`
- `config.boards >= 8`
- `config.commandsPerBoard >= 100`
- `config.writers >= 8`
- `config.batchSize >= 25`
- `totalCommands >= 2400`
- `evidence.tool == "budgie-commandlog-loadgen"`
- `evidence.budgetFile` matches the budget's `requiredReportBudgetFile`
- `evidence.budgetSha256` matches the checked budget file
- `evidence.gitRevision` names the checked revision
- `evidence.gitModified == false`
- `promotionReadiness.ready == true`
- `promotionReadiness.partitionLimitExceeded == false`
- `eventProjection.enabled == true`
- `eventProjection.partitionLimitExceeded == false`
- `eventProjection.expectedEvents` matches the staged command shape, and
  `eventProjection.appliedEvents == eventProjection.expectedEvents`
- `materializationAudit.complete == true`
- command lag, assignment losses, claim losses, commit failures, failed
  commands, missing materialization, retrying committed commands, and missing
  command-log records are all zero

If the shared budget reports `commandLogDrain.requireDurableStaging`, the run
used memory, SQLite, or an incomplete native NATS shape. Do not promote from
that artifact. Re-run with `BUDGIE_POSTGRES_DSN`, `-require-postgres`, distinct
command/event streams, and `-command-log-backend nats`.

Archive the JSON report with the release evidence, then re-check it before
promotion approval:

```sh
go run ./cmd/budgie-commandlog-report-check \
  -report-file artifacts/internet-scale/commandlog-native-nats-report.json \
  -budget-file ops/internet-scale-budgets.example.json
```

For remote/shared staging signoff, generate or re-check evidence against the
stricter remote budget. The wrapper selects that budget when
`BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1` is set, keeps the same promoted load
shape, and fails before the load if `BUDGIE_NATS_URL` or
`BUDGIE_POSTGRES_DSN` is loopback:

```sh
BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1 \
  ./scripts/commandlog-native-nats-gate.sh
```

The archived report can then be re-checked with:

```sh
go run ./cmd/budgie-commandlog-report-check \
  -report-file artifacts/internet-scale/commandlog-native-nats-report.json \
  -budget-file ops/internet-scale-remote-staging-budgets.example.json
```

The checker strictly decodes the saved report and reuses the same
`commandLogDrain` budget evaluation as the load generator. When
`requireDurableStaging` is enabled, that evaluation checks the runtime metadata
too: NATS command log, Postgres materialization, a recorded command stream, and
for native runs a NATS event log with a distinct event stream. The promoted
budget also pins the fail-closed Postgres flag and native gate config:
executor mode, authoritative submit, snapshot assignment, directed replies, and
reply coverage. It requires a disposable generated Postgres schema
(`budgie_cmdlog_load_*`) that is not kept after the run, plus staging command
and event streams under `BUDGIE_COMMAND_LOG_LOAD_*` and
`BUDGIE_EVENT_LOG_LOAD_*`, with recorded command/event stream replica counts.
It records redacted NATS and Postgres endpoints so reviewers can confirm the
artifact came from staging without archiving credentials. The promoted budget
also requires the default minimum staging load size: 8 boards, 100 commands per
board, 8 writers, batch size 25, and 2400 total commands. When
`requireReportEvidence` is enabled, it also requires the producing tool, budget
file, budget SHA-256, git revision, and clean git state; when the budget sets
`requiredReportBudgetFile`, the saved report must name that exact promoted
budget path, and the report checker compares `evidence.budgetSha256` against
the checked budget file. A passing archived artifact therefore proves the
durable staging gate still satisfies the promoted budget.

## NATS KV Assignment

Use `-command-log-worker-ownership nats-kv` when proving broker-backed
assignment before native broker consumer groups exist.

Rules:

- every writer uses the same `-command-log-worker-group-members`
- overrides use `kind/key=writer`
- override owners must be members
- changing members or overrides bumps assignment generation
- workers that lose ownership stop before committing more command offsets

Validation:

```sh
curl -fsS "$BUDGIE_API/metrics" |
  grep -E 'budgie_command_partition_assigned|budgie_command_partition_assignment_generation|budgie_command_log_assignment_losses_total'
```

Native Redpanda/Kafka assignment should feed broker rebalance snapshots into
the core assignment boundary instead of scanning every command partition on each
drain. A snapshot generation revoke must remove lost partitions before the
writer executes or commits another command offset; assigned snapshots may list
only the local writer's owned logical command partitions.
Before a native adapter exists, run `cmd/budgie-commandlog-loadgen` with
`-assignment-mode snapshot-assignment` to prove the command-log writer path can
drain from owned partition snapshots instead of a global partition scan.

## Redpanda/Kafka Promotion Checklist

Do not treat Redpanda/Kafka as production-authoritative until an adapter proves
all of these properties:

- logical partition keys map deterministically to broker partitions or topics
- per-logical-partition replay is ordered by Budgie partition offset
- scalar compatibility replay can be reconstructed for legacy clients
- duplicate command receipts resolve to the original command record
- duplicate event appends resolve to the original event record
- direct or equivalent bounded replay is available for rebuilds
- command writer consumer-group rebalance stops old owners before committing
  offsets after ownership loss
- command writer consumer-group rebalance stops old owners before writing
  applied/failed/retrying receipts after ownership loss
- command writer finalization appends decided events and commits the consumed
  command offset through the `CommandEventTransactionStore` boundary, or proves
  idempotent replay for any gap
- native command/event writer configuration uses distinct command and event
  streams/topics; a same-process NATS native writer/projector must configure
  `-event-store-projection-nats-stream` to match
  `-command-log-worker-event-nats-stream`, while Kafka/Redpanda writer and
  projector roles must agree on the same event topic and partition count
- NATS staging of that boundary is treated as replay-safe, not atomic:
  duplicate event ids must resolve to the original event before a retried
  command-offset commit can pass
- native command decisions are promoted command-by-command; the current native
  path covers basic `createThread` plus plain and safe directed `appendPost`,
  including inline post attachment metadata, metadata-only standalone
  `attachPost`, standard `editPost`, standard `setPostFlag`, standard
  single-post `redactPost`/`restorePost`, standard `setThreadTitle`/`lockThread`,
  standard `moveThread`, standard `redactPostRange`/`restorePostRange`,
  standard `clearBoardJunk`, standard `purgePost`, read-only/member board
  admission checks, locked/no-reply moderator bypasses, poll-bearing posts from
  trusted users,
  quoted replies, article mail-back replies, deterministic random signature
  snapshots, mention-bearing posts, watched-thread notifications that recover
  through broker-projected `post.committed` work, and relay-enabled board
  deliveries projected from native `post.appended` events, plus anonymous
  create/reply posts with public `Anonymous` identity and hidden commit actor
  metadata, `setContentFilter` configuration events with native admin,
  filter-id, pattern, and board-scope checks, content-filter review events with
  sanitized generated Filter-board logs for public boards, user `flagPost` and
  moderator `resolveReview` review events with sanitized generated
  0Moderation-board audit logs for public boards, generated vote-board
  `publishPollResult` records with native poll author and delegated board
  poll-manager checks, `grantRole`/`revokeRole` events with generated
  syssecurity-board audit records, `publishSystemNotice` records for generated
  notepad/GiveupNotice/bbsnet notices, and staged post attachment blob
  promotion through broker projection;
  poll-bearing post edits are command-level unsupported in both executors and
  must continue to fail closed instead of falling through to partial broker
  events
- `budgied -command-log-worker-executor native` is used only on dedicated
  broker writer nodes with `-command-log-worker nats|kafka`; SQL-backed
  execution remains the default until command receipts and projections are
  proven for the promoted command subset
- `cmd/budgie-commandlog-loadgen -command-log-worker-executor native` passes
  against the target broker shape, including zero command lag, a ready
  command-log promotion report, and a caught-up `eventProjection` section
- Redpanda/Kafka promotion reports are checked against
  `ops/internet-scale-kafka-budgets.example.json`, which pins Kafka
  command/event backends, disposable command/event topic prefixes, explicit
  topic partition counts, the partition-only SQL event allocator, Postgres
  materialization, exact native event counts, and the same zero-lag/zero-loss
  thresholds as the NATS gate
- `cmd/budgie-commandlog-loadgen -command-log-worker-executor native
  -replies-per-thread N -directed-replies` passes before promoting native
  `appendPost`, with promotion readiness covering both board and generated
  thread command partitions
- broker-event projection workers advance source/partition watermarks in the
  same SQL transaction as projection writes and fail closed on partition offset
  gaps
- broker-event projection workers enqueue `post.committed` outbox jobs for
  projected `post.appended` events in the same transaction, so post-commit
  trust and notification work can catch up from the projected broker source
- projection workers drain broker event partitions in bounded batches until
  caught up; a too-low partition limit fails the projection pass before partial
  materialization, and stale source/partition watermarks indicate visible SQL
  projections are behind the broker source
- projection rebuilds from broker source match SQL rebuilds
- `BudgieEventLogShadowParityFailure` remains clear under load

Operationally, Redpanda/Kafka must expose the same effective controls as the
current JetStream backend: retention policy, duplicate/idempotency window,
consumer lag, partition reassignment, direct replay, and disaster-recovery
restore procedure.

## Exit Criteria

Broker operations are healthy when:

- `./scripts/cluster-smoke.sh` passes with `BUDGIE_NATS_URL` set
- remote publish/decode failures are not increasing
- event-log shadow parity failures are zero
- command-log partition lag is within budget
- assignment losses are absent outside planned rebalances
- projection rebuild from the selected broker source succeeds in staging
- live delivery misses are repaired by durable replay
