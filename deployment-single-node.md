# Deployment: Single Node

This is the default way to run BudgieBBS: one `budgied` process backed by a
local SQLite database. It is the right choice for development, a hobby board, or
any deployment that fits comfortably on one machine.

For running two or more nodes against shared Postgres, see
[`deployment-multi-node.md`](deployment-multi-node.md).

## Single-host simplification

On one host, BudgieBBS does not need the internet-scale deployment machinery.
The durable event log, projections, background jobs, HTTP API, live transports,
and SSH TUI can all live inside one `budgied` process backed by one SQLite
database file.

You can skip:

- Postgres
- NATS / JetStream
- Redis
- Kafka / Redpanda
- command-log writer nodes
- API/gateway/worker role splitting
- internet-scale staging gates
- a multi-node load balancer

The production shape is:

```txt
Internet
  |
TLS reverse proxy
  |
budgied :8080 and :2222
  |
SQLite /var/lib/budgie/budgie.db
```

A reverse proxy is still recommended for TLS, compression, access logs, and
network-level protection of `/metrics`, but it can proxy to the single local
process.

## What one node serves

A single `budgied` process exposes every transport at once:

- HTTP REST + WebSocket + SSE, and (optionally) the web SPA — one listener.
- SSH TUI — a second listener.
- NNTP gateway — optional, off unless `-nntp` is set.

By default a single process runs all roles (`-roles api,ssh,worker,nntp`), so
no extra configuration is needed. The same binary can run a subset of roles per
node in a cluster — see "Runtime roles" in
[`deployment-multi-node.md`](deployment-multi-node.md).

## Build

```sh
go build -o budgied ./cmd/budgied
```

To serve the web SPA from the same process, build it first:

```sh
./scripts/build-web.sh
# produces web/dist; budgied auto-detects ./web/dist, or pass -web <path>
```

Set `BUDGIE_WEB_INSTALL=1` when dependencies need to be installed first:

```sh
BUDGIE_WEB_INSTALL=1 ./scripts/build-web.sh
```

## Run

For a production single-host install, create persistent data and backup
directories, set a real JWT secret, and keep the SQLite database plus SSH host
key outside the repo checkout:

```sh
sudo mkdir -p /var/lib/budgie /backup
sudo chown "$(id -un)" /var/lib/budgie /backup
export BUDGIE_JWT_SECRET="$(openssl rand -base64 48)"
```

The repo includes a launcher that pins this SQLite-only shape and unsets stale
Postgres/NATS/Redis/Kafka environment variables for the child process:

```sh
BUDGIE_SINGLE_NODE_DATA_DIR=/var/lib/budgie \
  ./scripts/run-single-node.sh
```

Without `BUDGIE_SINGLE_NODE_DATA_DIR`, it uses ignored local state under
`artifacts/single-node/`, which is useful for a quick trial:

```sh
./scripts/run-single-node.sh
```

```sh
./budgied \
  -storage sqlite \
  -db /var/lib/budgie/budgie.db \
  -http :8080 \
  -ssh 2222 \
  -hostkey /var/lib/budgie/budgie_host_key \
  -jwt-secret "$BUDGIE_JWT_SECRET" \
  -web ./web/dist
```

`-storage sqlite` and `-db budgie.db` are the defaults, so the minimal form is
just `./budgied`.

For a local trial, this is enough. For an internet-facing host, pass explicit
paths and set `BUDGIE_JWT_SECRET` so restarts do not invalidate sessions or
move state into the working directory.

### Flags

| Flag | Default | Purpose |
|------|---------|---------|
| `-storage` | `sqlite` | `sqlite` or `postgres`. |
| `-db` | `budgie.db` | SQLite database path. |
| `-http` | `:8080` | HTTP / WebSocket / SSE listen address. |
| `-ssh` | `2222` | SSH TUI listen port. |
| `-hostkey` | _(auto)_ | SSH host key path; generated at `~/.ssh/budgie_host_key` if empty. |
| `-jwt-secret` | _(random)_ | JWT signing secret. Also read from `BUDGIE_JWT_SECRET`. |
| `-web` | _(auto)_ | Path to `web/dist`. Auto-detects `./web/dist` if present. |
| `-nntp` | _(off)_ | NNTP listen address, e.g. `:1190`. Omit to disable. |
| `-doors` | _(off)_ | Path to `doors.json` for door games. |
| `-auto-stats` | `true` | Publish the daily stats snapshot. |

### Environment variables

- `BUDGIE_JWT_SECRET` — JWT signing secret (overridden by `-jwt-secret`).
  **Set this in production.** The built-in fallback is a fixed dev default and
  is not safe for real deployments.
- `BUDGIE_POSTGRES_DSN` — only relevant in Postgres mode.

## Health and metrics

Three unauthenticated endpoints are exposed on the HTTP listener:

- `GET /healthz` — liveness; always `200 ok` while the process is up.
- `GET /readyz` — readiness; `200` when the database responds to a ping, `503`
  otherwise. Point your supervisor or load balancer at this.
- `GET /metrics` — Prometheus text exposition format (see the metric list in
  `deployment-multi-node.md`).

Restrict `/metrics` and the health endpoints at the network layer if the node is
internet-facing; they carry no auth.

## systemd unit

```ini
# /etc/systemd/system/budgie.service
[Unit]
Description=BudgieBBS
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=budgie
Group=budgie
WorkingDirectory=/opt/budgie
Environment=BUDGIE_JWT_SECRET=change-me-to-a-long-random-string
ExecStart=/opt/budgie/budgied \
  -storage sqlite \
  -db /var/lib/budgie/budgie.db \
  -http :8080 \
  -ssh 2222 \
  -hostkey /var/lib/budgie/budgie_host_key \
  -web /opt/budgie/web/dist
Restart=on-failure
RestartSec=2
# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/budgie
ProtectHome=true

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now budgie
sudo systemctl status budgie
journalctl -u budgie -f
```

## launchd service (macOS)

For a single macOS host, install the built binary and web bundle somewhere
stable, then install the launchd service. The installer writes
`/Library/LaunchDaemons/com.budgie.bbs.plist` by default with `RunAtLoad` and
`KeepAlive`, so Budgie starts after reboot and restarts if the process exits.

```sh
go build -o budgied ./cmd/budgied

BUDGIE_SINGLE_NODE_DATA_DIR=/var/lib/budgie \
  ./scripts/install-single-node-launchd.sh

sudo launchctl print system/com.budgie.bbs
tail -f /var/log/budgie/budgied.log
```

Preview the generated plist without installing:

```sh
BUDGIE_LAUNCHD_DRY_RUN=1 ./scripts/install-single-node-launchd.sh
```

Remove the service without deleting data or logs:

```sh
./scripts/install-single-node-launchd.sh uninstall
```

Use a local reverse proxy such as Caddy, nginx, or a cloud load balancer to
terminate TLS and forward HTTP traffic to `127.0.0.1:8080`. Expose SSH TUI on
`2222` directly or through your normal firewall rules.

## Container

```sh
docker run -d --name budgie \
  -p 8080:8080 -p 2222:2222 \
  -e BUDGIE_JWT_SECRET="change-me" \
  -v budgie-data:/var/lib/budgie \
  budgie:latest \
  -storage sqlite -db /var/lib/budgie/budgie.db -http :8080 -ssh 2222 \
  -hostkey /var/lib/budgie/budgie_host_key
```

Persist `/var/lib/budgie` so the SQLite database and SSH host key survive
restarts. If the host key is regenerated, SSH clients will see a changed
host-key warning.

## Backup and restore (SQLite)

Stop the process or use the SQLite online backup so you copy a consistent file.
The database runs in WAL mode, so a naive `cp` of just the `.db` file can miss
in-flight writes.

```sh
# Consistent online backup (process can stay running):
sqlite3 /var/lib/budgie/budgie.db ".backup '/backup/budgie-$(date +%F).db'"

# Restore: stop budgied, replace the file, restart.
systemctl stop budgie
cp /backup/budgie-2026-06-08.db /var/lib/budgie/budgie.db
systemctl start budgie
```

For single-host production, schedule that `.backup` command daily and copy the
result off the host. The SQLite event log is the source of truth for rebuilds
and future Postgres migration.

The event log is the source of truth. If projection tables are ever suspected to
be stale or corrupt, rebuild them from the durable events without losing data:

```sh
./budgied -storage sqlite -db /var/lib/budgie/budgie.db -rebuild-projections
```

## Migrating to Postgres later

When you outgrow one node, migrate the SQLite event log into Postgres and switch
storage modes — no data loss, the event log replays into Postgres projections:

```sh
./budgied -migrate-sqlite-to-postgres \
  -db /var/lib/budgie/budgie.db \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN"
```

Then follow [`deployment-multi-node.md`](deployment-multi-node.md).
