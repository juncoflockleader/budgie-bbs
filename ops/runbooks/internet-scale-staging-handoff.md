# Internet-Scale Durable Staging Handoff

This handoff is for provisioning or reusing the external environment needed to
produce internet-scale deployment evidence. It assumes familiarity with this
repo, the IS4 command/event log path, and the existing broker operations
runbook.

## Goal

Provide a reachable, disposable staging broker plus Postgres pair so the
durable staging gate can run, plus archived gateway fanout evidence for the
million-connection budget shape. The currently proven durable path is NATS
JetStream:

```sh
./scripts/commandlog-native-nats-gate.sh
```

The Redpanda/Kafka target path now has a matching wrapper:

```sh
./scripts/commandlog-native-kafka-gate.sh
```

Gateway fanout evidence uses the synthetic wrapper:

```sh
./scripts/gateway-fanout-gate.sh
```

For a full evidence bundle, prefer the orchestration wrapper:

```sh
./scripts/internet-scale-staging-gate.sh
```

The command-log scripts run the native authoritative command-log
drain/projection gate, archive a JSON report under `artifacts/internet-scale/`,
and verify the report against the matching promoted budget:
`ops/internet-scale-budgets.example.json` for NATS and
`ops/internet-scale-kafka-budgets.example.json` for Redpanda/Kafka.
The gateway fanout wrapper archives a JSON report under
`artifacts/internet-scale/` and records `evidence.budgetFile` against either
`ops/internet-scale-budgets.example.json` or, when remote staging mode is set,
`ops/internet-scale-remote-staging-budgets.example.json`.

## Required Outputs

For the NATS gate, provide these two environment variables, preferably by
writing them to a local ignored file such as
`artifacts/internet-scale/staging.env`:

```sh
export BUDGIE_NATS_URL='nats://USER:PASSWORD@nats-staging.example:4222'
export BUDGIE_POSTGRES_DSN='postgres://USER:PASSWORD@postgres-staging.example:5432/budgie_staging?sslmode=require'
```

For the Redpanda/Kafka gate, provide:

```sh
export BUDGIE_KAFKA_BROKERS='redpanda-a-staging.example:9092,redpanda-b-staging.example:9092'
export BUDGIE_POSTGRES_DSN='postgres://USER:PASSWORD@postgres-staging.example:5432/budgie_staging?sslmode=require'
# Optional for credentialed staging clusters:
export BUDGIE_KAFKA_TLS='1'
export BUDGIE_KAFKA_SASL_MECHANISM='scram-sha-512'
export BUDGIE_KAFKA_SASL_USER='USER'
export BUDGIE_KAFKA_SASL_PASSWORD='PASSWORD'
```

`/artifacts/` is gitignored. Do not commit this file or paste production
credentials into tracked docs.

## Localhost Exception

A localhost-only gate is acceptable for the next disposable durable-path check.
For that case, `BUDGIE_NATS_URL` may point at an unauthenticated local NATS
server, for example `nats://127.0.0.1:4222`, and `BUDGIE_POSTGRES_DSN` may point
at a local disposable Postgres database.

Treat that as local validation evidence, not remote staging credential evidence.
For any shared, remote, or promotion-signoff staging environment, use
staging-scoped credentials and keep the service isolated from production
streams and databases.

## Execution Topology

For throughput signoff, run the gate from a host colocated with the staging
broker, Redis, and Postgres services, or from a network path with low and stable
latency. The gate performs many acknowledged broker/database operations; a
jittery client path can under-report capacity even when the services are
healthy.

A June 14, 2026 staging diagnostic showed this clearly. A high-jitter
workstation-to-staging path under-reported throughput, while running the same
app-path load generator on the staging host against `127.0.0.1`
NATS/Postgres/Redis produced:

- Redis-indexed native NATS/Postgres gate shape:
  `4,013` submit cmd/s, `1,577` drain cmd/s, zero lag after drain.
- Native NATS/Postgres without Redis index:
  `20,690` submit cmd/s, `1,928` drain cmd/s, zero lag after drain.

Use a remote/LAN client path for create/delete preflight and wiring checks if
needed, but do not use a high-jitter client path as the final throughput
diagnosis.

## NATS Requirements

- NATS must have JetStream enabled.
- For shared or remote staging, the credentials in `BUDGIE_NATS_URL` must be
  staging-scoped.
- The account must be allowed to create and use streams under:
  - `BUDGIE_COMMAND_LOG_LOAD_*`
  - `BUDGIE_EVENT_LOG_LOAD_*`
- The account must allow the subjects used by the command/event log adapters:
  - `budgie.commandlog.>`
  - `budgie.commandcommit.>`
  - `budgie.eventlog.>`
- Direct stream reads must be available.
- The gate generates unique command/event stream names by default; fixed stream
  names are not required.
- Do not point this at production command or event streams.

The promoted budget currently requires the archived report to record at least
one command stream replica and one event stream replica. The wrapper defaults
both to `1`, which is fine for disposable localhost validation. For shared or
remote staging, set `BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS` and
`BUDGIE_COMMANDLOG_GATE_EVENT_REPLICAS` to the replica count expected for that
JetStream cluster before running the gate.

## Rerun And Cleanup

The command/event adapters claim broad subjects:
`budgie.commandlog.>`, `budgie.commandcommit.>`, and `budgie.eventlog.>`.
JetStream allows only one stream to own a matching subject in the same account
or domain, so one NATS account/domain can host only one active load stream pair
for this gate at a time. The gate's unique stream names prevent stale fixed
stream reuse, but they do not allow overlapping subjects.

Before rerunning the gate in the same NATS account, either use a fresh isolated
account/domain or delete only the prior load streams after archiving the JSON
report:

```sh
./scripts/commandlog-native-nats-cleanup.sh
./scripts/commandlog-native-nats-cleanup.sh --execute
```

Equivalent direct cleanup commands:

```sh
nats --server "$BUDGIE_NATS_URL" stream rm --force BUDGIE_COMMAND_LOG_LOAD_...
nats --server "$BUDGIE_NATS_URL" stream rm --force BUDGIE_EVENT_LOG_LOAD_...
```

Do not delete non-load streams. When the `nats` CLI is available and can list
streams, `./scripts/commandlog-native-nats-gate.sh` preflights those subjects
and fails early with this cleanup guidance before launching the load. If the
CLI is installed outside Codex's service shell `PATH`, set
`NATS_BIN=/path/to/nats`; the scripts also check common Homebrew locations. If
the account intentionally cannot list streams, the wrapper continues to the
load-generator JetStream validation; reserve
`BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT=1` for controlled diagnostics.

## Redpanda/Kafka Requirements

- `BUDGIE_KAFKA_BROKERS` must point at disposable staging brokers, not
  production.
- TLS is supported with `BUDGIE_KAFKA_TLS=1`; use
  `BUDGIE_KAFKA_TLS_CA_FILE` for a staging CA bundle and
  `BUDGIE_KAFKA_TLS_SERVER_NAME` only when the broker certificate requires an
  override.
- SASL is supported with `BUDGIE_KAFKA_SASL_MECHANISM` set to `plain`,
  `scram-sha-256`, or `scram-sha-512`, plus `BUDGIE_KAFKA_SASL_USER` and
  `BUDGIE_KAFKA_SASL_PASSWORD`.
- Archived reports record only non-secret Kafka security evidence such as
  `runtime.kafkaTls` and `runtime.kafkaSaslMechanism`; they must not include
  SASL usernames or passwords.
- The brokers must allow creating and using generated load topics with prefixes:
  - `budgie.commands.load.`
  - `budgie.events.load.`
- The credentials must be allowed to describe those topics. The load generator
  creates missing topics, accepts `TOPIC_ALREADY_EXISTS`, then runs a metadata
  preflight with auto-create disabled to prove each topic has the requested
  partition count.
- The wrapper generates distinct command/event topics and a unique consumer
  group by default.
- Topic replication defaults to `1`, which is suitable for disposable
  localhost validation. For shared staging, set
  `BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS` to the staging broker's desired
  replication factor before running the gate.
- Do not point the wrapper at production command/event topics.
- The promoted Kafka budget requires the archived report to record at least 32
  command-topic partitions and 32 event-topic partitions. The wrapper defaults
  both to `32`; use `BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS` and
  `BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS` only to raise those counts.
- The Kafka report must show
  `runtime.scalarCompatibilityAllocator == "sql-event-partition-offsets"` and
  `scalarCompatibilityAudit.legacySqlScalarOffsetAfter == 0`, proving the
  partition-only gate did not advance the legacy SQL scalar event offset.

## Postgres Requirements

- `BUDGIE_POSTGRES_DSN` must point at a disposable staging database, not
  production.
- The role must be able to create and drop schemas in that database.
- The gate creates a schema named like `budgie_cmdlog_load_*` and drops it at
  the end of a successful run.
- The DSN may include credentials and query parameters in the env var; the
  generated report redacts endpoint evidence before archiving.

## Connectivity Check

Before handing back, verify from the same machine or network path Codex will
use:

```sh
source artifacts/internet-scale/staging.env
```

For Go, verify the tool is available from `PATH` or a common install location
such as `/opt/homebrew/bin/go`; the gate does not accept a separate `GO_BIN`
override for promotion evidence runs.

```sh
go version
```

For Postgres, any normal connection test is fine, for example:

```sh
psql "$BUDGIE_POSTGRES_DSN" -c 'select 1'
```

For NATS, any JetStream-capable check is fine, for example listing streams with
the same credentials. Avoid creating production-like stream names while testing.

For Redpanda/Kafka, any broker metadata check is fine, for example listing
brokers or topics with the staging credentials. Avoid creating production-like
topic names while testing.

The repo also provides a cheap create/delete preflight that uses the same
client libraries and promoted load-prefix resource families as the full gates.
Run it by itself when checking credentials before a formal evidence run:

```sh
source artifacts/internet-scale/staging.env
# Required for shared/remote staging signoff:
export BUDGIE_INTERNET_SCALE_PREFLIGHT_REMOTE_STAGING=1
# Optional: archive sanitized create/delete proof for handoff.
export BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT='artifacts/internet-scale/preflight-report.json'
./scripts/internet-scale-remote-staging-preflight.sh
```

The preflight auto-detects NATS and Kafka from `BUDGIE_NATS_URL` and
`BUDGIE_KAFKA_BROKERS`, always verifies the Postgres DSN needed by durable
command-log staging, and then creates/deletes only generated resources:

- `budgie_cmdlog_load_preflight_*` Postgres schema
- `BUDGIE_COMMAND_LOG_LOAD_PREFLIGHT_*` and
  `BUDGIE_EVENT_LOG_LOAD_PREFLIGHT_*` NATS streams
- `budgie.commands.load.preflight.*` and
  `budgie.events.load.preflight.*` Kafka topics

Use `BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS=nats,kafka` or
`BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS=postgres` when checking a partial
environment. When `BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT` is set, the
preflight writes a sanitized JSON report with redacted endpoint evidence, git
state, selected targets, generated resource names, and per-probe pass/fail
timing. The full bundle sets this automatically to
`artifacts/internet-scale/preflight-report-<shared-report-suffix>.json` so the
create/delete proof travels with the gateway and command-log reports.

Before rerunning or after archiving Kafka staging evidence, clean up only the
generated load topics with the dry-run-first helper:

```sh
./scripts/commandlog-native-kafka-cleanup.sh
./scripts/commandlog-native-kafka-cleanup.sh --execute
```

The helper lists broker topics through the repo's Kafka client path and deletes
only topics with `budgie.commands.load.` or `budgie.events.load.` prefixes.
It defaults broker metadata calls to `30s`; set
`BUDGIE_COMMANDLOG_KAFKA_CLEANUP_TIMEOUT=2m` before both commands if shared
staging topic listing or deletion needs a longer bounded window.

## What To Run

Run the gate from a clean git tree:

```sh
source artifacts/internet-scale/staging.env
# Recommended deployment evidence entrypoint. Run it from the staging host or a
# low-jitter client path. It always runs gateway fanout, auto-adds NATS/Kafka
# when BUDGIE_NATS_URL or BUDGIE_KAFKA_BROKERS is set, and writes reports with
# one shared suffix. In remote staging mode it runs the create/delete preflight
# first for selected NATS/Kafka targets.
# Required for shared/remote staging signoff:
# export BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING=1
./scripts/internet-scale-staging-gate.sh

# Individual NATS gate:
# Optional when nats is installed outside the service shell PATH:
# export NATS_BIN=/opt/homebrew/bin/nats
# Required for shared/remote staging signoff:
# export BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1
./scripts/commandlog-native-nats-gate.sh
# Required for shared/remote staging gateway-fanout signoff:
# export BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING=1
./scripts/gateway-fanout-gate.sh
```

For Redpanda/Kafka staging:

```sh
source artifacts/internet-scale/staging.env
# Required for shared/remote staging signoff:
# export BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1
./scripts/commandlog-native-kafka-gate.sh
```

The wrapper will:

- validate `BUDGIE_NATS_URL` and `BUDGIE_POSTGRES_DSN`
- generate distinct `BUDGIE_COMMAND_LOG_LOAD_*` and
  `BUDGIE_EVENT_LOG_LOAD_*` streams
- require native executor, authoritative submit, snapshot assignment, directed
  replies, and the promoted minimum load shape
- create and later drop a disposable Postgres schema
- write a temporary JSON report first
- run `cmd/budgie-report-check commandlog`
- move only a passing report to
  `artifacts/internet-scale/commandlog-native-nats-report.json`

The Kafka wrapper performs the same shape checks, but generates distinct
`budgie.commands.load.*` and `budgie.events.load.*` topics, passes
`-command-log-backend kafka`, creates/preflights both generated topics before
opening the runtime clients, and verifies the report against
`ops/internet-scale-kafka-budgets.example.json` or, with
`BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1`,
`ops/internet-scale-kafka-remote-staging-budgets.example.json`.

The gateway fanout wrapper has no broker or Postgres dependency; it validates a
synthetic local-fanout capacity shape. It requires a clean git tree, rejects
under-budget subscriber/target overrides, writes the report only under
`artifacts/internet-scale/`, and uses
`BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING=1` to select
`ops/internet-scale-remote-staging-budgets.example.json` so remote staging
evidence cannot be confused with local budget evidence.

The bundle wrapper uses `BUDGIE_INTERNET_SCALE_GATE_TARGETS` when explicit
target control is needed. Supported values are `gateway`, `nats`, `kafka`, or
`all`, separated by commas. If no durable target is detected from the
environment, the wrapper refuses to run a gateway-only bundle unless
`BUDGIE_INTERNET_SCALE_GATE_TARGETS=gateway` is set explicitly. In remote
staging mode, it also runs `./scripts/internet-scale-remote-staging-preflight.sh`
before the full load gates unless `BUDGIE_INTERNET_SCALE_GATE_SKIP_PREFLIGHT=1`
is set for controlled diagnostics. After the selected gates finish, the bundle
wrapper runs `./scripts/internet-scale-report-check.sh` with the same target
set, shared suffix, and remote-staging mode before declaring the evidence
bundle passed. That check writes
`artifacts/internet-scale/bundle-manifest-<shared-report-suffix>.json`, which
records the target set, remote/local staging mode, and selected reports with
their paths, tools, git revision, clean-tree state, and SHA-256 hashes.
`BUDGIE_INTERNET_SCALE_GATE_SKIP_REPORT_CHECK=1` is only for diagnostics where
archived evidence is not being signed off.

## Success Criteria

The archived report must pass the shared `commandLogDrain` budget and show:

- `runtime.commandLogBackend == "nats"`
- `runtime.eventLogBackend == "nats"`
- `runtime.materializationStore == "postgres"`
- redacted NATS and Postgres endpoint evidence
- disposable `runtime.postgresSchema` with prefix `budgie_cmdlog_load_`
- `runtime.keepPostgresSchema == false`
- distinct command/event streams with the promoted load prefixes
- `runtime.commandNatsReplicas >= 1`
- `runtime.eventNatsReplicas >= 1`
- `evidence.gitModified == false`
- zero command lag, assignment losses, claim losses, commit failures, failed
  commands, missing materialization, retrying committed commands, and missing
  command-log records
- event projection enabled, untruncated, caught up, and with
  `eventProjection.appliedEvents == eventProjection.expectedEvents`

For remote/shared staging signoff, the archived report should also pass:

```sh
go run ./cmd/budgie-report-check commandlog \
  -report-file artifacts/internet-scale/commandlog-native-nats-report.json \
  -budget-file ops/internet-scale-remote-staging-budgets.example.json
```

That stricter budget rejects loopback `runtime.natsEndpoint` and
`runtime.postgresEndpoint` evidence. When
`BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1` is set, the wrapper uses this stricter
budget for both report generation and verification, so disposable localhost
runs can remain useful local proof without being mistaken for shared staging
signoff.

For Redpanda/Kafka reports, the archived report must also show:

- `runtime.commandLogBackend == "kafka"`
- `runtime.eventLogBackend == "kafka"`
- redacted `runtime.kafkaBrokers`
- distinct command/event topics with the promoted load prefixes
- `runtime.kafkaCommandPartitions >= 32`
- `runtime.kafkaEventPartitions >= 32`

The Kafka remote budget rejects loopback `runtime.kafkaBrokers` and
`runtime.postgresEndpoint` evidence.

For gateway fanout reports, the archived report must also show:

- `evidence.tool == "budgie-gateway-loadgen"`
- `evidence.budgetFile` matching the selected promoted budget
- `evidence.budgetSha256`
- `evidence.gitRevision`
- `evidence.gitModified == false`
- `subscribers >= 100000`
- `hotScopeSubscribers >= 10000`
- `queuedDeliveries >= 10000`
- `targetConnections >= 1000000`
- `gatewayNodesForTarget <= 20`

Validate archived gateway fanout reports with:

```sh
go run ./cmd/budgie-report-check gateway \
  -report-file artifacts/internet-scale/gateway-fanout-report.json \
  -budget-file ops/internet-scale-remote-staging-budgets.example.json
```

When the reports were produced by `./scripts/internet-scale-staging-gate.sh`,
re-check the whole archived bundle by suffix:

```sh
BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING=1 \
BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX=<shared-report-suffix> \
./scripts/internet-scale-report-check.sh
```

`BUDGIE_INTERNET_SCALE_GATE_REPORT_SUFFIX=<shared-report-suffix>` is also
accepted here, so the same suffix environment used for the staging gate can be
reused during handoff verification. In remote staging mode, this read-only
bundle check also requires and verifies
`artifacts/internet-scale/preflight-report-<shared-report-suffix>.json` for
selected NATS/Kafka targets by running
`cmd/budgie-report-check preflight`; it rejects failed probes,
dirty git evidence, missing generated resources, unsanitized endpoint evidence
with credentials/query strings/fragments, and loopback endpoints before checking
the gateway or command-log reports. After the individual budget checks pass, the
same wrapper runs `cmd/budgie-report-check bundle` across the
selected artifacts and rejects bundles whose reports were produced from
different git revisions or dirty worktrees.

After a bundle is copied to another workspace or host, verify the transferred
manifest without rewriting it:

```sh
BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING=1 \
BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX=<shared-report-suffix> \
BUDGIE_INTERNET_SCALE_REPORT_CHECK_VERIFY_MANIFEST=artifacts/internet-scale/bundle-manifest-<shared-report-suffix>.json \
./scripts/internet-scale-report-check.sh
```

For a direct manifest-only read-back, run:

```sh
go run ./cmd/budgie-report-check bundle \
  -verify-manifest artifacts/internet-scale/bundle-manifest-<shared-report-suffix>.json \
  -targets gateway,nats,kafka \
  -remote-staging
```

The manifest read-back reopens every referenced report, recomputes each
SHA-256 hash, checks report labels against the target set, and confirms the
tool, git revision, and clean-tree evidence still match the manifest.

## Handoff Back

When the environment is ready, hand back:

- confirmation that the env file exists locally, or provide the two env vars
  through the secure channel used for this workspace
- any required network/VPN step needed before running the gate
- which broker gate to run: NATS or Redpanda/Kafka
- whether the NATS staging account or Kafka load topics should be cleaned up
  after the run
- whether Codex should run
  `./scripts/commandlog-native-kafka-cleanup.sh --execute` after Kafka evidence
  is archived
- the shared report suffix or the generated
  `artifacts/internet-scale/bundle-manifest-<shared-report-suffix>.json`

Do not send production credentials. Do not pre-create fixed command/event
streams unless there is a specific reason; unique per-run streams are preferred.
