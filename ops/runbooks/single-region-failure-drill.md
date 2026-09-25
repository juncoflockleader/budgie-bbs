# Single-Region Failure Drill

This drill proves the IS9 operating model for a single authoritative write
region with optional regional read/API/gateway nodes. It intentionally does not
prove active-active writes.

Run this in staging first. In production, run each injection only inside an
approved maintenance window and stop after the first unexpected symptom.

## Required Inputs

Set these before starting:

```sh
export BUDGIE_API="https://api.example.com"
export BUDGIE_REGIONAL_API="https://region.example.com"
export BUDGIE_WRITE_REGION_URL="https://write.use1.example.com"
export BUDGIE_ADMIN_TOKEN="replace-me"
export BUDGIE_USER_TOKEN="replace-me"
export BUDGIE_POSTGRES_DSN="postgres://budgie:secret@db.internal:5432/budgie?sslmode=require"
export BUDGIE_NATS_URL="nats://nats.internal:4222"
export BUDGIE_ALERT_RULES="ops/prometheus/budgie-internet-scale-alerts.yml"
```

The account behind `BUDGIE_USER_TOKEN` must be allowed to create a thread on
`general`. The account behind `BUDGIE_ADMIN_TOKEN` must be an admin.

Set this when the preflight should verify current alert state through the
Prometheus API:

```sh
export BUDGIE_PROMETHEUS_URL="https://prometheus.example.com"
```

## Evidence Log

Create one evidence file per drill:

```sh
export DRILL_ID="budgie-is9-$(date -u +%Y%m%dT%H%M%SZ)"
export DRILL_LOG="/tmp/${DRILL_ID}.log"
{
  echo "drill=${DRILL_ID}"
  date -u
  git rev-parse HEAD 2>/dev/null || true
} | tee "$DRILL_LOG"
```

For each phase below, append:

- start/end UTC timestamp
- command output
- relevant alert state
- expected metric deltas
- pass/fail decision
- rollback performed

## Preflight

Run the preflight wrapper first. It validates the required environment, checks
the write and regional `/readyz` endpoints, verifies the IS9 alert rules named
below, fails if `BUDGIE_PROMETHEUS_URL` reports a firing critical Budgie alert
from `/api/v1/alerts`, captures baseline metrics, and runs
`./scripts/cluster-smoke.sh`:

```sh
./scripts/single-region-failure-drill-preflight.sh | tee -a "$DRILL_LOG"
```

1. Confirm all serving nodes are healthy:

```sh
curl -fsS "$BUDGIE_API/readyz" | tee -a "$DRILL_LOG"
curl -fsS "$BUDGIE_REGIONAL_API/readyz" | tee -a "$DRILL_LOG"
```

2. Confirm the IS9 alert rules are loaded in Prometheus:

```sh
test -f "$BUDGIE_ALERT_RULES"
grep -E 'Budgie(RemoteDeliveryLagHigh|WriteRegionProxyFailures|ProjectionLagHigh|CommandLogWriterLagHigh)' \
  "$BUDGIE_ALERT_RULES" | tee -a "$DRILL_LOG"
```

If `BUDGIE_PROMETHEUS_URL` is not set for the wrapper, confirm no critical
Budgie alert is already firing with the equivalent Prometheus endpoint before
continuing:

```sh
curl -fsS "https://prometheus.example.com/api/v1/alerts" |
  jq -r '.data.alerts[]? | select(.state=="firing") | select(.labels.severity=="critical") | select((.labels.alertname // "") | startswith("Budgie")) | .labels.alertname' |
  tee -a "$DRILL_LOG"
```

3. Run the cluster smoke test against the staging cluster:

```sh
./scripts/cluster-smoke.sh | tee -a "$DRILL_LOG"
```

4. Capture baseline metrics from every role:

```sh
curl -fsS "$BUDGIE_API/metrics" | tee "/tmp/${DRILL_ID}-api.metrics" >/dev/null
curl -fsS "$BUDGIE_REGIONAL_API/metrics" | tee "/tmp/${DRILL_ID}-regional.metrics" >/dev/null
```

Required pass criteria:

- `/readyz` is `200` for write and regional endpoints.
- `./scripts/cluster-smoke.sh` passes.
- No critical Budgie alert is already firing.

## Phase 1 - Regional Write Route Outage

Purpose: prove regional nodes keep reads local while writes fail retryably when
the authoritative write region is unreachable.

Injection:

```sh
# Pick one: point the regional node at a blocked write-region URL, remove the
# write-region target from its load balancer, or block regional egress to the
# write-region target.
echo "Inject write-region route outage now" | tee -a "$DRILL_LOG"
```

Probe local reads:

```sh
curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  "$BUDGIE_REGIONAL_API/api/v1/boards" | tee -a "$DRILL_LOG"
```

Probe mutating write failure:

```sh
curl -sS -o /tmp/write-region-outage.json -w '%{http_code}\n' \
  -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Command-Id: ${DRILL_ID}-write-region-outage" \
  -d '{"title":"regional outage probe","body":"should fail retryably"}' \
  "$BUDGIE_REGIONAL_API/api/v1/boards/general/threads" | tee -a "$DRILL_LOG"
cat /tmp/write-region-outage.json | tee -a "$DRILL_LOG"
```

Expected:

- regional `GET /api/v1/boards` succeeds
- regional write returns `502`
- response error code is `write_region_unavailable`
- `budgie_write_region_proxy_failures_total` increases
- `BudgieWriteRegionProxyFailures` fires if the outage lasts longer than the
  alert window

Rollback:

```sh
echo "Restore regional route to $BUDGIE_WRITE_REGION_URL" | tee -a "$DRILL_LOG"
```

Post-rollback pass:

```sh
curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Command-Id: ${DRILL_ID}-write-region-recovered" \
  -d '{"title":"regional recovery probe","body":"write route restored"}' \
  "$BUDGIE_REGIONAL_API/api/v1/boards/general/threads" | tee -a "$DRILL_LOG"
```

## Phase 2 - Projection Lag And Read-Your-Writes

Purpose: prove clients can detect stale regional projections and recover after
processor/backfill catch-up.

Injection:

Pause the processor for one derived view, or deliberately set a staging
watermark behind the durable head using the admin/DB tool your environment
allows. Do not do manual watermark changes in production.

Probe:

```sh
HEAD_SEQ="$(curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  "$BUDGIE_API/api/v1/events?after=0&limit=1" | jq -r '.head')"

curl -sS -o /tmp/projection-stale.json -w '%{http_code}\n' \
  -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  -H "X-Budgie-Min-Seq: ${HEAD_SEQ}" \
  "$BUDGIE_REGIONAL_API/api/v1/rankings/boards" | tee -a "$DRILL_LOG"
cat /tmp/projection-stale.json | tee -a "$DRILL_LOG"
```

Expected:

- stale projection returns `425`
- response error code is `projection_stale`
- body includes `meta.view`, `meta.headSeq`, `meta.appliedSeq`, and `minSeq`
- `budgie_derived_view_lag_events{view=...}` is above baseline
- `BudgieProjectionLagHigh` fires if lag persists past the alert window

Rollback:

```sh
./budgied -storage postgres -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -backfill-derived-views rankings.boards | tee -a "$DRILL_LOG"
```

Post-rollback pass:

```sh
curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  -H "X-Budgie-Min-Seq: ${HEAD_SEQ}" \
  "$BUDGIE_REGIONAL_API/api/v1/rankings/boards" | tee -a "$DRILL_LOG"
```

The final response must be `200` with
`X-Budgie-Read-Your-Writes: satisfied`.

## Phase 3 - Live Broker Outage

Purpose: prove a NATS delivery outage delays live fanout but does not lose
durable events.

Injection:

```sh
echo "Stop or firewall NATS for the staging cluster now: $BUDGIE_NATS_URL" | tee -a "$DRILL_LOG"
```

Probe durable write through the write region:

```sh
curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Command-Id: ${DRILL_ID}-nats-outage-write" \
  -d '{"title":"broker outage probe","body":"durable write should land"}' \
  "$BUDGIE_API/api/v1/boards/general/threads" | tee -a "$DRILL_LOG"
```

Expected:

- write returns success or a durable pending command-log ack
- `budgie_events_remote_publish_failures_total` increases on affected nodes
- clients that miss live delivery repair by `/api/v1/events?after=<cursor>` or
  reconnect replay

Rollback:

```sh
echo "Restart or un-firewall NATS now" | tee -a "$DRILL_LOG"
./scripts/cluster-smoke.sh | tee -a "$DRILL_LOG"
```

Post-rollback pass:

- `BudgieRemoteDeliveryLagHigh` clears
- `./scripts/cluster-smoke.sh` passes
- no durable events are missing from replay

## Phase 4 - Command Writer Crash Or Rebalance

Purpose: prove authoritative command-log writers recover after a writer crash or
assignment change without duplicate materialization.

Skip this phase if the cluster is not running `-command-log-authoritative` and
`-command-log-worker`.

Injection:

```sh
echo "Stop one command-log writer process now" | tee -a "$DRILL_LOG"
```

Probe:

```sh
curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Command-Id: ${DRILL_ID}-writer-crash" \
  -d '{"title":"writer crash probe","body":"must materialize once"}' \
  "$BUDGIE_REGIONAL_API/api/v1/boards/general/threads" | tee -a "$DRILL_LOG"
```

Expected:

- `budgie_command_partition_lag` rises for the affected partition
- if ownership changes, `budgie_command_log_assignment_losses_total` may rise
- a surviving writer drains lag after lease expiry/reassignment
- retrying the same `X-Command-Id` returns the same result or pending receipt
- exactly one thread/post materializes

Rollback:

```sh
echo "Restart stopped writer and wait for lag to drain" | tee -a "$DRILL_LOG"
```

Post-rollback pass:

- `budgie_command_partition_lag_max` returns to baseline
- no duplicate command result exists for `${DRILL_ID}-writer-crash`

## Exit Criteria

The drill passes only when all executed phases satisfy their post-rollback pass
criteria and the following are true:

- `GET /readyz` is healthy for write and regional endpoints
- `./scripts/cluster-smoke.sh` passes
- critical Budgie alerts are clear or have a written follow-up owner
- projection reads with `X-Budgie-Min-Seq` return `satisfied`
- regional mutating writes succeed after write-route recovery
- command-log lag is drained if authoritative command-log mode is enabled

Attach `$DRILL_LOG`, captured metric files, and alert screenshots to the change
record for the drill.
