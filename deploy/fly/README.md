# Budgie BBS on Fly.io

Best fit if you prefer **container / git-push style deploys** over managing a
VPS. Fly runs the image close to users, terminates TLS at the edge, and — unlike
most PaaS — supports the **raw TCP** service the SSH TUI needs. The catch for a
SQLite app: it must be **a single machine bound to a single volume** (SQLite is
single-writer); don't scale past one.

## Prerequisites

- [`flyctl`](https://fly.io/docs/flyctl/install/) installed, and `fly auth login`.
- `openssl` (to generate the JWT secret).

## Deploy

From the repo root:

```bash
FLY_APP=my-budgie FLY_REGION=iad ./deploy/fly/deploy.sh
```

The script renders `fly.toml`, creates the app, a 3 GB `budgie_data` volume
(holds the SQLite DB, JWT secret, SSH host key), and the `BUDGIE_JWT_SECRET`
secret — each only if missing — then deploys a single machine with `--ha=false`.
Re-run any time to ship an update; data and sessions persist on the volume.

- Web: `https://my-budgie.fly.dev` — first registered user becomes admin.
- SSH TUI: `ssh -p 2222 <handle>@my-budgie.fly.dev`

## Custom domain

```bash
fly certs add bbs.example.com -a my-budgie
# add an A/AAAA (or CNAME) record per the command's output, then:
fly secrets set BUDGIE_PUBLIC_URL=https://bbs.example.com -a my-budgie
```

## Notes

- **Single instance only.** The volume is mounted by one machine; scaling to
  more would corrupt SQLite. Use the Postgres/multi-node path
  (`docker-compose.yml`, `deployment-multi-node.md`) if you outgrow one host.
- **Backups:** `fly volumes snapshots` covers the volume, but also export the
  DB periodically (the JWT secret lives on the volume too — losing it logs
  everyone out).
- **Outbound SMTP (port 25) is blocked**; use a relay for email verification.
