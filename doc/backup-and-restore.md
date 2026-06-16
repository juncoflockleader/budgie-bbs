# Backup and Restore

This document is the canonical backup/restore procedure for a BudgieBBS
deployment. It covers what to back up, how, how to restore, and how to **drill**
a restore so you know the backups actually work.

## What is the source of truth

The **database is the source of truth.** It holds both the durable event log and
the projections derived from it. Attachments are stored as blobs *inside* the
database, so there is no separate uploads directory to back up.

Because projections are derived, a database backup is a complete backup of
content. If projections are ever lost or corrupted but the event log survives,
they can be rebuilt with the projection-rebuild tooling
(see [ops/runbooks/projection-search-rebuild.md](../ops/runbooks/projection-search-rebuild.md))
— but a normal restore brings back both, so you don't rely on that.

Two things live **outside** the database and must also be preserved:

| Item | SQLite single-node | Postgres multi-node |
|------|--------------------|---------------------|
| Database | `…/budgie.db` | the Postgres database |
| JWT signing secret | `…/jwt_secret` file | `BUDGIE_JWT_SECRET` env / secrets manager |
| SSH host key | `…/budgie_host_key` file | per-node `-hostkey` / secrets manager |

Losing the **JWT secret** only invalidates existing sessions (users re-log in);
losing the **SSH host key** makes SSH clients re-confirm the host fingerprint.
Neither loses data, but back them up for a seamless restore.

## Backing up

Use [`scripts/backup.sh`](../scripts/backup.sh). It auto-detects the mode:

- **Postgres** when `BUDGIE_POSTGRES_DSN` is set — runs `pg_dump --format=custom`.
- **SQLite** otherwise — takes a consistent `VACUUM INTO` snapshot (safe on a live
  WAL-mode database; a plain `cp` of a live SQLite file can be corrupt) and bundles
  it with the `jwt_secret` / `budgie_host_key` files into a `tar.gz`.

```bash
# SQLite single-node (uses the same BUDGIE_SINGLE_NODE_* paths as the runner):
BUDGIE_SINGLE_NODE_DATA_DIR=/var/lib/budgie \
  scripts/backup.sh --out /var/backups/budgie --keep 30

# Postgres multi-node:
BUDGIE_POSTGRES_DSN="postgres://…" \
  scripts/backup.sh --out /var/backups/budgie --keep 30
```

Each run writes a UTC-timestamped artifact plus a `.sha256` sidecar, `chmod 600`,
and prunes to the newest `--keep` (default 14).

> **The SQLite archive contains your signing secrets** — store backups encrypted
> and access-controlled, and keep at least one copy off-host (3-2-1: 3 copies, 2
> media, 1 off-site). For Postgres, the dump is content only; manage secrets via
> your secrets manager.

### Scheduling

Cron (daily at 03:30):

```cron
30 3 * * *  BUDGIE_SINGLE_NODE_DATA_DIR=/var/lib/budgie /opt/budgie/scripts/backup.sh --out /var/backups/budgie --keep 30 >> /var/log/budgie-backup.log 2>&1
```

systemd timer: a `budgie-backup.service` (Type=oneshot running the script) plus a
`budgie-backup.timer` (`OnCalendar=*-*-* 03:30:00`, `Persistent=true`).

For Postgres, consider continuous archiving (WAL archiving / PITR) or your
provider's managed snapshots in addition to logical `pg_dump` backups.

## Restoring

**Stop `budgied` before restoring over a live data directory or database.**

Use [`scripts/restore.sh`](../scripts/restore.sh); the mode is inferred from the
artifact name (`*.tar.gz` → SQLite, `*.dump` → Postgres).

```bash
# Verify an artifact's checksum without restoring:
scripts/restore.sh --verify /var/backups/budgie/budgie-sqlite-20260616T0330Z.tar.gz

# SQLite restore (refuses to overwrite an existing db unless --force):
scripts/restore.sh budgie-sqlite-….tar.gz --to /var/lib/budgie --force

# Postgres restore (into an empty db; --force does --clean --if-exists):
BUDGIE_POSTGRES_DSN="postgres://…" scripts/restore.sh budgie-postgres-….dump --force
```

The SQLite restore verifies the checksum, runs `PRAGMA integrity_check` on the
restored database before swapping it in, and re-installs the secret/host-key files
with `0600` perms.

## Restore drill (do this regularly — an untested backup is not a backup)

1. Take/choose a recent artifact and **verify** it: `scripts/restore.sh --verify <artifact>`.
2. Restore into a scratch location, not production:
   - SQLite: `scripts/restore.sh <artifact> --to /tmp/budgie-drill`
   - Postgres: restore into a throwaway database (`createdb budgie_drill`; restore with `--dsn`).
3. Boot a throwaway instance against the restored data and confirm it serves:
   - SQLite: `BUDGIE_SINGLE_NODE_DATA_DIR=/tmp/budgie-drill BUDGIE_SINGLE_NODE_HTTP=:18080 scripts/run-single-node.sh`, then `curl -fsS localhost:18080/readyz` and spot-check `/api/v1/boards`.
   - Postgres: point a non-production `budgied` at the restored DSN and hit `/readyz`.
4. Record the drill outcome. Schedule it (e.g. monthly) the same way as backups.

This drill is the companion to the failure runbook
([ops/runbooks/single-region-failure-drill.md](../ops/runbooks/single-region-failure-drill.md)):
backups prove you can recover *data*; that runbook proves you can recover *service*.

## Disaster-recovery summary

- **Corruption / bad deploy / accidental delete:** stop budgied, restore the latest
  good artifact, restart. RPO = your backup interval.
- **Host loss:** provision a new host, install budgied, restore the artifact from
  off-site storage, restore secrets, start.
- **Projection drift only (event log intact):** rebuild projections with the
  rebuild tooling instead of a full restore.
