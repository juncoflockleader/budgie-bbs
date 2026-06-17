# Internet-Scale Sustained Soak Profile (sketch)

The gates in [`internet-scale-benchmarks.md`](internet-scale-benchmarks.md) are
**capacity** tests: short, peak-shaped runs that prove per-shape throughput,
fanout, and zero-loss budgets. They do **not** characterize what happens when
the system runs at load for hours. This document sketches the **soak** profile
that closes that gap. It is a design sketch, not yet an implemented harness.

## What soak adds over the capacity gates

A capacity gate can pass while the system still has a slow leak, an unbounded
queue, or a latency creep that only shows after the broker compacts, the heap
fragments, or connection churn accumulates. Soak targets exactly those:

| Failure class | Why a short gate misses it | Soak signal |
|---|---|---|
| Memory / goroutine leak | Too short for the slope to clear noise | RSS / goroutine count regression slope over hours |
| Latency creep | p99 drifts slowly under sustained pressure | `budgie_command_latency_ms` p99 vs. a rolling baseline |
| Unbounded lag / backpressure | Drains fully in a short burst | `budgie_command_partition_lag_max` sustained > 0 |
| Broker accumulation | JetStream/Kafka pending grows over time | consumer pending / `budgie_command_log_receipt_oldest_age_ms` |
| Reconnect-storm recovery | No churn in a one-shot run | `budgie_gateway_reconnects_total` vs. `…replay_repairs_total` |
| DB resource exhaustion | Connections/bloat build slowly | PG connection count, dead-tuple growth |

## Workload profile

- **Arrival rate:** a *fraction of gated peak* (target ~30–50%, i.e. a sustained
  steady rate well inside the proven capacity), held constant — soak is about
  duration at a safe rate, not finding the ceiling.
- **Mix (not write-only):** createThread / appendPost across many board
  partitions + a hot thread, reactions/votes, board-mail, plus a **read** load
  (feed/thread/board slices) and **live subscribers** (WS/SSE) so fanout,
  replay, and the derived-view path are all exercised together.
- **Churn:** a steady background of connect/disconnect and deliberate
  slow-consumer sessions, so reconnect + replay-repair runs continuously rather
  than once.
- **Topology:** ≥2 API nodes + 1 worker (leader-elected) against shared Postgres
  + NATS JetStream, matching `deployment-multi-node.md`, so cross-node wakeup and
  leader fail-over are in scope.

## Duration tiers

| Tier | Duration | Use |
|---|---|---|
| `smoke` | 30 min | CI-friendly leak/creep tripwire on every release candidate |
| `standard` | 4 h | Pre-promotion sign-off |
| `endurance` | 24 h | Quarterly / pre-major-release; catches slow accumulation |

## Instrumented SLOs and tripwires

Sample `/metrics` (and OS-level RSS) at a fixed interval (e.g. 15 s) for the
whole run. Fail the soak if any tripwire fires for a sustained window (not a
single spike):

- **Latency:** `budgie_command_latency_ms` p99 stays within +X% of the run's
  first-hour baseline (no monotonic creep). Same for
  `budgie_gateway_ws_send_latency_ms` / `…_sse_send_latency_ms`.
- **Durability/ordering:** `budgie_command_partition_lag_max` and
  `…_lag_total` return to 0 between bursts; `…_assignment_losses_total` and
  bus/gateway `…_dropped_sends_total` stay flat (delta ≈ 0 after warmup, beyond
  intended slow-consumer drops).
- **Freshness:** `budgie_remote_wakeup_lag_ms` and
  `budgie_derived_view_lag_events` bounded; `budgie_command_log_receipt_oldest_age_ms`
  does not grow without bound.
- **Recovery:** `budgie_gateway_replay_repairs_total` keeps pace with
  `…_reconnects_total` (reconnects get repaired, gap doesn't widen).
- **Leak detection:** linear-regression slope of process RSS and goroutine
  count over the run is ≈ 0 (within a small tolerance). This is the headline
  soak signal. **Gap:** the process exposes custom `/metrics` but **not**
  `net/http/pprof` or a goroutine/heap gauge today — soak tooling should add
  `net/http/pprof` on the ops listener (behind a flag) plus a
  `budgie_goroutines` / `budgie_heap_inuse_bytes` gauge so the slope is scrapeable
  rather than OS-sampled only.
- **Resource:** Postgres active connections bounded; autovacuum keeps dead
  tuples flat; NATS JetStream consumer pending bounded; Redis memory bounded.

## Failure-injection variants (run after a clean baseline soak)

- Kill the leader worker mid-run → assert a standby acquires leadership
  (`budgie_worker_is_leader` flips) and lag recovers to 0.
- Bounce a broker connection → assert reconnect + replay repairs, no missing
  records.
- Pause a slow consumer past its queue capacity → assert it drops (bounded
  `budgie_gateway_connection_queue_depth` ≤ capacity) without backpressuring
  publishers.

## Harness sketch (to build)

The current load generators are **bounded** (fixed command counts), so they
self-terminate — they can't soak as-is. Proposed additions:

1. A sustained mode: `-duration <dur>` + `-target-rate <cmd/s>` driving a steady
   arrival (token-bucket) instead of a fixed count, reusing the existing
   submit/drain path in `cmd/budgie-commandlog-loadgen` /
   `internal/core/partition_write_load.go`.
2. `scripts/internet-scale-soak.sh`: starts a real multi-node cluster (reuse
   `docker-compose` / `scripts/cluster-smoke.sh` plumbing), drives the mixed
   workload + churn for the chosen tier, scrapes `/metrics` + RSS on an interval
   into a time series, and at the end evaluates the tripwires above against a
   `ops/internet-scale-soak-budgets.example.json` (slopes, sustained-violation
   windows, p99-creep bounds) — mirroring how the gate scripts evaluate the
   capacity budgets.
3. Archive the time series + a pass/fail summary under `artifacts/internet-scale/`
   (gitignored), re-checkable like the capacity reports.

## Relationship to IS0

This operationalizes the IS0 baseline + tripwire intent in
[`milestones-internet-scale.md`](../milestones-internet-scale.md) (writer lock
wait, command latency by kind, wakeup lag, replay counts, subscriber drops, open
sessions) as a **time-based** check, complementing the point-in-time capacity
gates. Build it once the multi-node compose cluster is the standard test
substrate.
