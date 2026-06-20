# Deployment: Single Node

This is the default way to run BudgieBBS: one `budgied` process backed by a
local SQLite database. It is the right choice for development, a hobby board, or
any deployment that fits comfortably on one machine.

For running two or more nodes against shared Postgres, see
[`deployment-multi-node.md`](deployment-multi-node.md).

**Hosting on a provider?** [`deploy/`](deploy/) has copy-paste templates and
scripts (cloud-init + a `deploy.sh`) for Hetzner, DigitalOcean, Oracle Cloud,
and Fly.io — each provisions Caddy + TLS, the systemd service, and the data dir
for you. Start at [`deploy/README.md`](deploy/README.md). This document is the
underlying flag/endpoint reference those quickstarts build on.

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
| `-public-url` (`BUDGIE_PUBLIC_URL`) | _(derived)_ | Public base URL, e.g. `https://bbs.example`. Used for email links and absolute sitemap/robots URLs. If unset, derived per-request from the `Host` header. |
| `-sitemap-interval` (`BUDGIE_SITEMAP_INTERVAL`) | `6h` | How often `/sitemap.xml` is regenerated (cache TTL). |

### Environment variables

- `BUDGIE_JWT_SECRET` — JWT signing secret (overridden by `-jwt-secret`).
  **Set this in production.** The built-in fallback is a fixed dev default and
  is not safe for real deployments.
- `BUDGIE_POSTGRES_DSN` — only relevant in Postgres mode.
- `BUDGIE_PUBLIC_URL` — public base URL for email links and sitemap/robots.
- `BUDGIE_SITEMAP_INTERVAL` — sitemap regeneration interval, e.g. `6h`.

## Search engines (robots.txt + sitemap.xml)

Two unauthenticated endpoints make the public, guest-readable surface
crawlable:

- `GET /robots.txt` — allows crawling, keeps `/api/` out of the index, and
  points at the sitemap.
- `GET /sitemap.xml` — lists the homepage plus every guest-readable board
  (`/b/{id}`) and its threads (`/t/{id}`). Member-only and admin-hidden boards
  are excluded (same rules as web guest browsing). It is regenerated at most
  once per `-sitemap-interval` (served stale-while-revalidate so requests never
  block on generation).

Those `/b/{id}` and `/t/{id}` URLs are real, shareable deep links: the SPA
resolves them on a cold load (a logged-out visitor lands in read-only guest
mode), and sets a per-page `<title>` and `<link rel="canonical">`. Set
`-public-url` so the sitemap and canonical URLs are absolute and correct behind
a proxy.

## Health and metrics

Three unauthenticated endpoints are exposed on the HTTP listener:

- `GET /healthz` — liveness; always `200 ok` while the process is up.
- `GET /readyz` — readiness; `200` when the database responds to a ping, `503`
  otherwise. Point your supervisor or load balancer at this.
- `GET /metrics` — Prometheus text exposition format (see the metric list in
  `deployment-multi-node.md`).

Restrict `/metrics` and the health endpoints at the network layer if the node is
internet-facing; they carry no auth.

## Signup hardening: captcha and email verification

Both are off by default. The public, unauthenticated `GET /api/v1/auth/policy`
endpoint reports what's enabled so the web client renders the right fields.

### Captcha

| Flag (or `BUDGIE_CAPTCHA_*` env) | Purpose |
|------|---------|
| `-captcha-mode` | `off` (default), `native`, or `provider`. |
| `-captcha-provider` | `recaptcha`, `hcaptcha`, or `turnstile` (provider mode). |
| `-captcha-site-key` | Public site key (exposed via the policy endpoint). |
| `-captcha-secret` | Provider secret / native HMAC key. Defaults to the JWT secret for native. |

- `native` self-hosts a distorted-text SVG challenge (no third party). The client
  fetches `GET /api/v1/auth/captcha` and submits the challenge id + answer.
- `provider` verifies a token against the provider's siteverify API.

### Email verification

Email verification turns itself on or off based on whether outbound email is
configured, so the default is always sensible:

- **No mailer** (no `-mail-from`) → verification is **off**; signup needs no
  email and login is never gated. (`-require-email-verification` is ignored with
  a log note, since nothing can be sent.)
- **Mailer configured** (local catcher or real provider) → verification is **on**
  by default. Disable with `-require-email-verification=false`.

When enabled, new accounts must confirm their email before they can log in. The
admin bootstrap account and all pre-existing accounts stay verified, so turning
it on never locks anyone out.

| Flag (or env) | Purpose |
|------|---------|
| `-mail-from` (`BUDGIE_MAIL_FROM`) | From address. **Setting this enables outbound email.** |
| `-mail-mode` (`BUDGIE_MAIL_MODE`) | `direct` (to the recipient's MX), `relay`, or `off`. Defaults to `relay` when `-smtp-host` is set, else `direct`. |
| `-smtp-host` / `-smtp-port` / `-smtp-user` / `-smtp-password` (`BUDGIE_SMTP_*`) | Relay endpoint and auth. This is also how an email service provider (SendGrid/SES/Postmark) is wired in — point it at the ESP's SMTP. |
| `-require-email-verification` | Enforce verification before login (default `true` when a mailer is configured). |
| `-public-url` (`BUDGIE_PUBLIC_URL`) | Base URL used to build the verification link, e.g. `https://bbs.example`. |
| `-mail-inbox-url` (`BUDGIE_MAIL_INBOX_URL`) | Local SMTP-catcher web inbox to surface in the signup UI. Auto-set to `http://localhost:8025` (mailpit) when the relay host is loopback. |

Signup returns `202 verification_required` (no token), the worker delivers a
single-use 24h link, and login returns `email_not_verified` until the user opens
`GET /api/v1/auth/verify-email?token=…`. `POST /api/v1/auth/resend-verification`
re-issues the email.

**Direct-to-MX has poor deliverability** (no SPF/DKIM/reputation — mail usually
lands in spam). For real deployments use a relay/ESP.

### Reaching real inboxes (Gmail, etc.)

Mailbox providers like Gmail score inbound mail on signals that direct-to-MX from
an arbitrary host can't satisfy, so those messages get rejected or junked:

- **SPF / DKIM / DMARC** — DNS records on the *From* domain that authorize the
  sending IPs and cryptographically sign the message. Gmail effectively requires
  all three. They only exist for a domain **you own and control**.
- **IP reputation + reverse DNS** — a warmed, well-regarded sending IP with
  matching PTR. Fresh/residential/cloud IPs have none.
- **Outbound port 25** is blocked by default on most ISPs and clouds, so
  direct-to-MX often can't even connect.

The practical path is an **email service provider** (Amazon SES, Postmark,
SendGrid, Mailgun, Resend, …). They send from reputable IPs, DKIM-sign for you,
and give you the SPF/DKIM/DMARC records to publish on your domain. In Budgie this
is just the relay flags pointed at the ESP's SMTP — no code change:

```sh
./budgied \
  -mail-mode relay \
  -smtp-host smtp.sendgrid.net -smtp-port 587 \
  -smtp-user apikey -smtp-password "$SENDGRID_API_KEY" \
  -mail-from no-reply@yourdomain.com \
  -public-url https://yourdomain.com
```

(Same shape for SES/Postmark/Mailgun — only the host and credentials differ.)
Steps: own a domain → create an ESP account → add the SPF/DKIM/DMARC DNS records
it gives you and verify the domain → point the relay flags at it. Free/cheap
tiers (e.g. SES ~$0.10/1k emails, Postmark/SendGrid free tiers) cover typical
transactional volume.

Self-hosting a mail server that reaches Gmail is possible but a real undertaking
(static unblocked-port-25 IP, your own SPF/DKIM/DMARC + PTR, reputation warm-up);
an ESP does that work for you.

### Testing email locally with mailpit

[mailpit](https://github.com/axllent/mailpit) is a tiny local SMTP server with a
web inbox — ideal for seeing verification emails without sending real mail.

```sh
brew install mailpit              # or download a release binary
mailpit --listen 127.0.0.1:8025 --smtp 127.0.0.1:1025 &

./budgied \
  -mail-mode relay -smtp-host localhost -smtp-port 1025 \
  -mail-from no-reply@budgie.local \
  -public-url http://localhost:8080
```

Register an account, then open the inbox at <http://localhost:8025> to read the
email and click the verification link. (A `dest.test`-style fake recipient domain
will fail to deliver — use any address; mailpit accepts them all.)

Because the relay host is loopback, budgied recognizes this as a local catcher:
it logs `local SMTP catcher in use … inbox=http://localhost:8025` at startup and
the signup UI shows a **"Dev mode: emails are captured locally — open the inbox"**
link on the "Check your email" screen. Override the inbox URL (e.g. a non-default
mailpit web port) with `-mail-inbox-url` / `BUDGIE_MAIL_INBOX_URL`. A real
provider relay (a remote host) is never treated as dev and shows no such link.

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
