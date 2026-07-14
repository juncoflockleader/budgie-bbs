# Internet-Scale Benchmarks — Published Results

This document records reproducible load/capacity evidence for the internet-scale
claims in [`design-internet-scale-writes.md`](../design-internet-scale-writes.md)
and the historical
[`milestones-internet-scale.md`](archive/milestones-internet-scale.md). The
budgets defined by that work are enforced as machine-checked gates; this page
publishes the numbers a run of those gates produced.

**Scope honesty:** these are **load / throughput-capacity gates** at the promoted
workload shape, not sustained multi-hour **soak** runs. They prove the system
meets its per-shape throughput, fanout, and zero-loss budgets; they do not yet
characterize long-duration stability (memory growth, GC behavior, broker
compaction over hours). A dedicated soak profile is listed under *Follow-ups*.

## Provenance

| Field | Value |
|---|---|
| Git revision | `d30f29e8` (clean tree; gates refuse to run on a dirty tree) |
| Date | 2026-06-16 |
| Command-log / gateway budget | `ops/internet-scale-budgets.example.json` (sha256 `d0fe4a92…46d69c9`) |
| Host | Mac mini (Apple Silicon), **colocated loopback** — NATS JetStream 2.x, Kafka 4.2 (KRaft), PostgreSQL 16, Redis, Go 1.26 |
| Colocation rationale | The design requires throughput evidence from a host **colocated** with the brokers/DB (low jitter). All results below ran on the mini over loopback against real NATS JetStream / Kafka + Postgres. (An earlier laptop run of the write-scaling pillar against ephemeral PG 17 showed a higher absolute throughput and a 1.93× speedup — cross-host corroboration of the ratio.) |

Raw reports are archived under `artifacts/internet-scale/` (gitignored) and can
be re-verified without re-running load via
`scripts/internet-scale-report-check.sh`.

## Results

### 1. Postgres write scaling (per-aggregate vs. global lock) — ✅ passed

`cmd/budgie-loadgen`, 3,200 `createThread` writes per shape, mini loopback PG 16.
Validates that spreading writes across independent board partitions relieves the
single global write lock.

| Metric | Same partition (1 hot board) | Spread (16 boards) | Budget |
|---|---|---|---|
| Throughput | 323 writes/s | **543 writes/s** | — |
| p95 latency | 101.4 ms | **78.8 ms** | ≤150 ms (spread) |
| p99 latency | 103.8 ms | 89.1 ms | — |
| Failed writes | 0 | 0 | 0 |
| **Spread speedup** | — | **1.68×** | ≥1.5× |

Absolute throughput is host-bound (the mini concurrently runs other services);
the **speedup ratio** is the portable claim and clears the budget on both hosts
(1.68× colocated here, 1.93× on the idle laptop).

### 2. Gateway fanout capacity — ✅ passed (mini, loopback)

`scripts/gateway-fanout-gate.sh` → `cmd/budgie-gateway-loadgen`. One event
published to a hot scope with 100,000 attached subscribers; measures publish
latency, slow-client drop behavior, and scope isolation.

| Metric | Result | Budget |
|---|---|---|
| Subscribers | 100,000 (10,000 on the hot scope) | ≥100,000 / ≥10,000 hot |
| Hot-scope publish duration | **2.84 ms** | ≤100 ms |
| Queued deliveries | 10,000 | ≥10,000 |
| Estimated slow-client drops | 0 | ≤20,000 |
| Max per-connection queue depth | 1 | ≤2 |
| Idle-scope deliveries | 0 (scope isolation holds) | 0 |
| Projected capacity | **1,000,000 connections across 10 gateway nodes** | ≤20 nodes for 1M |

### 3. Durable command-log drain (NATS JetStream) — ✅ passed (mini, loopback)

`scripts/commandlog-native-nats-gate.sh` → `cmd/budgie-commandlog-loadgen`,
native decider executor, snapshot assignment, authoritative submit, real NATS
JetStream command + event logs with Postgres materialization. 2,400 commands
across 8 board partitions (8 writers, batch size 25).

| Metric | Result | Budget |
|---|---|---|
| Total commands | 2,400 | ≥2,400 |
| Submit throughput | **20,000 cmd/s** (120 ms) | ≥100 |
| Drain throughput | **1,487 cmd/s** (8 partitions, 5 rounds, 1,614 ms) | ≥75 |
| Event projection | **3,800 events/s** (3,200 events, 842 ms) | ≥75 |
| Partition lag after drain | **0** | 0 |
| Failed / commit / assignment / claim losses | 0 / 0 / 0 / 0 | all 0 |
| Missing materialization / missing records | 0 / 0 | 0 / 0 |

The submit rate (20k cmd/s, Redis command-index disabled) is consistent with the
`milestones-internet-scale.md` "Current Slice Status" figure of 20,690 cmd/s for
the same configuration — independent corroboration at a later commit.

### 4. Durable command-log drain (Kafka / KRaft) — ✅ passed (mini, loopback)

`scripts/commandlog-native-kafka-gate.sh`, same shape as §3, against a real
Kafka 4.2.0 (KRaft) broker with Postgres materialization.

| Metric | Result | Budget |
|---|---|---|
| Total commands | 2,400 | ≥2,400 |
| Submit throughput | **18,182 cmd/s** (132 ms) | ≥100 |
| Drain throughput | **340 cmd/s** (222 rounds, 7,052 ms) | ≥75 |
| Event projection | **406 events/s** (3,200 events) | ≥75 |
| Partition lag after drain | **0** | 0 |
| Failed / commit / assignment / claim losses | 0 / 0 / 0 / 0 | all 0 |

This initially failed (`no worker progress after 3 rounds with lag 200`): the
drain's no-progress detector gave up after three 25 ms "fast drain" polls — far
sooner than the Kafka consumer group's first join/sync completes (member-id
round trip + cooperative-sticky rebalance, ~1 s). NATS has no group-join step,
so it was unaffected. Fixed by absorbing the join latency in the first fetch
(`ReadyTimeout`) before reverting to fast steady-state polling. The Kafka drain
runs at a lower rate than NATS here (more poll rounds per batch); closing that
throughput gap is a separate optimization, not a correctness issue.

## Reproducing

On a host colocated with the brokers (loopback), with a clean checkout:

```bash
export BUDGIE_NATS_URL="nats://127.0.0.1:4222"
export BUDGIE_POSTGRES_DSN="postgres://budgie:…@127.0.0.1:5432/budgie_staging?sslmode=disable"

./scripts/gateway-fanout-gate.sh            # pillar 2
BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT=1 \
  ./scripts/commandlog-native-nats-gate.sh  # pillar 3

# pillar 1 (broker-independent):
go run ./cmd/budgie-loadgen -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -boards 16 -writes-per-board 200 -concurrency 32 \
  -budget-file ops/internet-scale-budgets.example.json
```

Each gate refuses a dirty git tree, writes a signed report (git rev + budget
sha256) to `artifacts/internet-scale/`, and re-verifies it against the promoted
budget file. For the full bundle + manifest archival, use
`scripts/internet-scale-staging-gate.sh` (remote-staging mode) and
`scripts/internet-scale-report-check.sh`.

## Follow-ups

- **Sustained soak profile:** a multi-hour run at a fraction of peak to surface
  memory growth, GC pauses, broker compaction, and reconnect-storm recovery —
  distinct from these capacity gates. Sketched in
  [`internet-scale-soak-profile.md`](archive/internet-scale-soak-profile.md),
  with active implementation work tracked in the
  [repository plan](roadmap.md#31-implement-the-soak-harness).
- **Kafka drain throughput** (§4): the Kafka backend drains correctly but at a
  lower rate than NATS (more poll rounds per batch) — a throughput optimization,
  not a correctness gap.
