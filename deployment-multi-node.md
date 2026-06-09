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
               | (single primary)|   LISTEN/NOTIFY wakeups,
               +-----------------+   advisory write lock
```

Every node runs the same binary and can serve reads and accept writes. There is
no separate message broker: cross-node live delivery uses Postgres
`LISTEN/NOTIFY`, and global write ordering uses a Postgres advisory lock. Adding
NATS/Redis is possible (`NATSBus` exists) but is not wired into `budgied`; the
supported multi-node path today is Postgres-only.

## How it stays consistent

- **Durable events** are appended to Postgres and assigned a strictly increasing
  `seq`. The event log is authoritative; the broker only carries wakeups.
- **Write serialization**: in Postgres mode every mutating command is wrapped in
  a `pg_advisory_lock`, so only one node mutates the log and projections at a
  time. The lock attempt has a 30s timeout; on failure the command returns a
  retryable `lock_unavailable` error.
- **Cross-node wakeups**: after a commit, the writing node issues `pg_notify`
  with `{seq, event, scopes}`. Other nodes receive it, fetch the authoritative
  event from Postgres by `seq`, and publish it into their local bus. Chat lines
  (ephemeral, no `seq`) carry an `eid`; the receiving node fetches the row by id.
- **At-least-once + gap recovery**: live delivery may duplicate or drop. Clients
  dedupe by `seq`. WebSocket connections track the last delivered `seq`; on a gap
  they replay the missing range from the durable log before delivering the new
  event. Self-originated notifications are skipped by `node_id`.

The practical consequence: a missed wakeup is self-healing. The worst case is a
slightly delayed event, corrected on the next event or on reconnect — never a
permanently lost durable event.

## Prerequisites

- A reachable Postgres instance (one primary). Read replicas are not required and
  not used by the wakeup path.
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

`-storage postgres` requires the DSN (flag `-postgres-dsn` or the env var). As a
backwards-compatibility convenience, supplying a DSN while `-storage` is still
the default `sqlite` is treated as Postgres mode.

Startup logs the resolved storage mode and DSN with the password redacted:

```
INFO starting budgied storage=postgres dsn=postgres://****@db.internal:5432/budgie
```

Each process generates its own in-memory `node_id` (logged, and used to skip
self-notifications). Give each node its own SSH host key file so SSH clients see
a stable key per node, or front SSH with a single node.

## Runtime roles

A node runs only the roles you give it via `-roles` (comma-separated, default
`api,ssh,worker,nntp` = everything, which is the single-node behavior):

| Role | What it runs |
|------|--------------|
| `api` | HTTP REST + WebSocket + SSE + web SPA on `-http`. |
| `ssh` | SSH TUI server on `-ssh`. |
| `nntp` | NNTP gateway (only when `-nntp <addr>` is also set). |
| `worker` | Background jobs: outbox processing + daily stats (leader-elected). |

Regardless of roles, **every node** runs the command handler (so it can accept
writes), the Postgres `LISTEN/NOTIFY` listener, and an HTTP listener that always
serves `GET /healthz`, `/readyz`, and `/metrics` — so load balancers and
scrapers can reach any node even when it has no `api` role. Non-`api` nodes serve
**only** those ops endpoints on `-http` (API routes return 404).

Example topology:

```sh
# API nodes (behind the load balancer)
./budgied -roles api    -storage postgres -http :8080
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

- Route HTTP/WS/SSE to any node; **no sticky sessions**.
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
| `budgie_events_published_remote_total` | counter | Events forwarded to the broker (NATS path). |
| `budgie_events_ingested_remote_total` | counter | Events received from a sibling and republished. |
| `budgie_bus_dropped_sends_total` | counter | Live events dropped to a full subscriber channel. |
| `budgie_remote_wakeup_lag_ms` | histogram | Sibling event timestamp → local receipt delay. |
| `budgie_replay_total` / `budgie_replay_batch_size` | counter / histogram | Gap-recovery replays and their size. |
| `budgie_command_latency_ms` | histogram | Command handler execution time. |
| `budgie_writer_lock_wait_ms` | histogram | Time waiting for the global write lock. |
| `budgie_outbox_jobs{status}` | gauge | Outbox jobs by status (pending/running/done/dead). |
| `budgie_worker_is_leader` | gauge | 1 on the active background-worker leader, else 0. |

Watch `budgie_writer_lock_wait_ms` (write contention across nodes) and
`budgie_remote_wakeup_lag_ms` (cross-node delivery latency; target p95 < 500ms on
a LAN). A rising `budgie_bus_dropped_sends_total` paired with `budgie_replay_*`
is normal backpressure recovery; sustained high drops mean slow consumers.

## Failure modes and recovery

| Failure | Behavior | Recovery |
|---------|----------|----------|
| A node crashes | Its WS/SSH clients disconnect. Other nodes unaffected; writes continue. | LB stops routing on failed `/readyz`. Clients reconnect to another node and replay from their cursor. |
| Postgres briefly unavailable | Writes fail with retryable errors; `/readyz` returns `503`. | Nodes recover automatically when Postgres returns; the `pq.Listener` reconnects (re-`LISTEN`s) on its own. |
| `LISTEN/NOTIFY` wakeup missed (reconnect window) | A live event may not push to sibling nodes. | Self-healing: the next event or a client reconnect triggers gap detection and durable replay. No durable loss. |
| Advisory lock held too long / timeout | Command returns `lock_unavailable` (retryable). | Client retries; investigate the slow command via `budgie_command_latency_ms`. |
| Node restart | In-memory node registry and presence reset for that node. | Durable state intact in Postgres. Presence recovers as clients reconnect. |

Broker note: the "broker" here is Postgres itself. There is no separate broker
process to restart. The listener (`github.com/lib/pq`'s `Listener`) reconnects
automatically with backoff; missed notifications during the gap are covered by
replay, so no manual intervention is needed after a Postgres blip.

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

## Cluster smoke test

After bringing up the cluster (or before promoting it), prove cross-node
delivery with the bundled script. It starts two local nodes against your Postgres
DSN, creates a thread and a like on node A, and verifies node B observes both:

```sh
BUDGIE_POSTGRES_DSN="postgres://budgie:secret@localhost:5432/budgie?sslmode=disable" \
  ./scripts/cluster-smoke.sh
```

See [`scripts/cluster-smoke.sh`](scripts/cluster-smoke.sh) for options. The script
requires `curl` and `jq`. A passing run is the operational definition of "the
cluster delivers cross-node events."

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

## Definition of done (operational)

- Two+ nodes run against one Postgres database.
- A user on any node receives durable events created through any other node.
- Writes are globally ordered (strictly increasing `seq`).
- Reconnect and replay work across nodes; duplicates are tolerated.
- Chat works across nodes.
- No sticky sessions required.
- `./scripts/cluster-smoke.sh` passes.
