# Deploying Budgie BBS

Templates and scripts for standing up a **single-host** Budgie BBS on the major
providers. Budgie is one static Go binary with a SQLite file on disk, serving
HTTP/WS/SSE **plus** an SSH TUI — so the right shape is a small VPS (or a Fly
machine) with a persistent disk, not a stateless PaaS. It's very light; it runs
fine on a low-power Mac mini.

> Scaling past one host (Postgres + multiple nodes) is a different topic — see
> the root `docker-compose.yml` and `deployment-multi-node.md`.

## Pick a provider

| You want… | Go to | Default arch |
|---|---|---|
| Cheapest with full control (recommended) | [`hetzner/`](hetzner/) | arm64 (CAX) |
| Easiest, best docs | [`digitalocean/`](digitalocean/) | amd64 |
| Free | [`oracle/`](oracle/) | arm64 (Ampere) |
| Container / git-push deploys | [`fly/`](fly/) | (image) |

Each folder has a `README.md`, a provider config (`cloud-init.yaml` or
`fly.toml.template`), and a `deploy.sh`. The three VPS providers share the
machinery in [`common/`](common/).

## How a VPS deploy works (Hetzner / DigitalOcean / Oracle)

1. **Create the server** with the provider's `cloud-init.yaml` as user-data. On
   first boot, [`common/provision.sh`](common/provision.sh) installs Caddy,
   creates the `budgie` user + `/opt/budgie` + `/var/lib/budgie`, drops the
   systemd unit, and opens the firewall.
2. **Point DNS** at the server.
3. **Run the provider's `deploy.sh`** from your workstation. It
   ([`common/build.sh`](common/build.sh)) cross-compiles the static binary +
   builds the SPA, ships them, wires your domain into Caddy and
   `BUDGIE_PUBLIC_URL`, and starts the service. Re-run any time to update.

Resulting layout on the host:

```
/opt/budgie/budgied                 static binary
/opt/budgie/web/dist/               built SPA
/opt/budgie/scripts/run-single-node.sh   launcher (generates the JWT secret)
/opt/budgie/budgie.env              optional config (EnvironmentFile)
/var/lib/budgie/                    SQLite DB + jwt_secret + host key  ← back this up
```

Caddy terminates TLS and reverse-proxies `https://your-domain` → `127.0.0.1:8081`.
The SSH TUI is reached directly on `:2222` (`ssh -p 2222 <handle>@your-domain`).

### Manual setup (no cloud-init)

On a fresh Ubuntu/Debian host:

```bash
curl -fsSL https://raw.githubusercontent.com/juncoflockleader/budgie-bbs/main/deploy/common/provision.sh | sudo bash
# then, from your workstation:
BUDGIE_HOST=root@<ip> BUDGIE_DOMAIN=bbs.example.com ./deploy/common/deploy-vps.sh
```

## Shared concepts

- **No secret to manage.** The JWT secret is generated and persisted to
  `/var/lib/budgie/jwt_secret` on first start. Only set `BUDGIE_JWT_SECRET`
  yourself when migrating an existing instance.
- **Config** lives in `/opt/budgie/budgie.env` (see
  [`common/budgie.env.template`](common/budgie.env.template)) — most usefully
  `BUDGIE_PUBLIC_URL` so sitemap/robots/canonical URLs are correct.
- **Email is optional** and off by default. Outbound port 25 is blocked on every
  provider here, so use an SMTP relay if you enable it (see
  `deployment-single-node.md`).
- **AI integration** needs no server key — board moderators bring their own
  token in the UI (stored write-only); the master switch is in Admin → AI.
- **Back up `/var/lib/budgie`**, not just VM snapshots — the DB + JWT secret are
  the entire state.

See also: [`deployment-single-node.md`](../deployment-single-node.md) for the
full flag/endpoint reference.
