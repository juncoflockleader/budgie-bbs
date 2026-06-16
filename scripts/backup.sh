#!/usr/bin/env bash
#
# Consistent, restorable backup of a BudgieBBS instance.
#
# The database is the source of truth: it holds the durable event log as well as
# the derived projections, so a backup of the database (plus the signing secrets)
# is a complete backup. Projections can also be rebuilt from the event log with
# the projection-rebuild tools if ever needed (see doc/backup-and-restore.md).
#
# Mode is auto-detected:
#   * postgres  — when BUDGIE_POSTGRES_DSN is set (multi-node deployments)
#   * sqlite    — otherwise (single-node); uses the BUDGIE_SINGLE_NODE_* paths
#
# Usage:
#   scripts/backup.sh [--out DIR] [--keep N]
#
# Env:
#   BUDGIE_BACKUP_DIR              output dir (default ./backups), over/--out
#   BUDGIE_BACKUP_KEEP            retain newest N backups (default 14), or --keep
#   BUDGIE_POSTGRES_DSN          selects postgres mode + the source database
#   BUDGIE_SINGLE_NODE_DATA_DIR  sqlite data dir (default artifacts/single-node)
#   BUDGIE_SINGLE_NODE_DB        sqlite db path (default $DATA_DIR/budgie.db)
#   BUDGIE_SINGLE_NODE_SECRET_FILE / BUDGIE_SINGLE_NODE_HOSTKEY  included if present
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

OUT_DIR="${BUDGIE_BACKUP_DIR:-${ROOT}/backups}"
KEEP="${BUDGIE_BACKUP_KEEP:-14}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) OUT_DIR="$2"; shift 2 ;;
    --keep) KEEP="$2"; shift 2 ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "ERROR: unknown argument: $1" >&2; exit 2 ;;
  esac
done

TS="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$OUT_DIR"

# checksum writes a .sha256 sidecar next to the artifact.
checksum() {
  local f="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    ( cd "$(dirname "$f")" && sha256sum "$(basename "$f")" >"$(basename "$f").sha256" )
  elif command -v shasum >/dev/null 2>&1; then
    ( cd "$(dirname "$f")" && shasum -a 256 "$(basename "$f")" >"$(basename "$f").sha256" )
  fi
}

# prune keeps only the newest $KEEP artifacts matching the glob (plus sidecars).
prune() {
  local glob="$1" n=0 f
  # shellcheck disable=SC2012
  ls -1t ${glob} 2>/dev/null | while read -r f; do
    n=$((n + 1))
    if (( n > KEEP )); then
      rm -f "$f" "$f.sha256"
      echo "    pruned old backup: $(basename "$f")"
    fi
  done
}

backup_postgres() {
  local dsn="$BUDGIE_POSTGRES_DSN"
  command -v pg_dump >/dev/null 2>&1 || { echo "ERROR: pg_dump not found" >&2; exit 3; }
  local dest="${OUT_DIR}/budgie-postgres-${TS}.dump"
  echo "==> backing up Postgres database (custom format)"
  # Custom format (-Fc) is compressed and restorable selectively with pg_restore.
  pg_dump --format=custom --no-owner --no-privileges --file="$dest" "$dsn"
  chmod 600 "$dest"
  checksum "$dest"
  echo "    wrote $dest ($(du -h "$dest" | cut -f1))"
  prune "${OUT_DIR}/budgie-postgres-*.dump"
}

backup_sqlite() {
  command -v sqlite3 >/dev/null 2>&1 || { echo "ERROR: sqlite3 not found" >&2; exit 3; }
  local data_dir db secret hostkey
  data_dir="${BUDGIE_SINGLE_NODE_DATA_DIR:-${ROOT}/artifacts/single-node}"
  db="${BUDGIE_SINGLE_NODE_DB:-${data_dir}/budgie.db}"
  secret="${BUDGIE_SINGLE_NODE_SECRET_FILE:-${data_dir}/jwt_secret}"
  hostkey="${BUDGIE_SINGLE_NODE_HOSTKEY:-${data_dir}/budgie_host_key}"

  [[ -f "$db" ]] || { echo "ERROR: sqlite database not found: $db" >&2; exit 3; }

  local stage dest
  stage="$(mktemp -d)"
  trap "rm -rf '$stage'" EXIT
  echo "==> backing up SQLite database (consistent VACUUM INTO snapshot)"
  # VACUUM INTO takes a read lock and writes a single defragmented, WAL-free copy
  # — safe on a live (WAL-mode) database, unlike a plain file copy.
  sqlite3 "$db" "VACUUM INTO '${stage}/budgie.db'"

  # Include the signing secrets so a restore is self-contained. NOTE: this makes
  # the archive sensitive — store it encrypted / access-controlled.
  for f in "$secret" "$hostkey"; do
    if [[ -f "$f" ]]; then cp -p "$f" "$stage/"; fi
  done

  local dest="${OUT_DIR}/budgie-sqlite-${TS}.tar.gz"
  tar -czf "$dest" -C "$stage" .
  chmod 600 "$dest"
  checksum "$dest"
  echo "    wrote $dest ($(du -h "$dest" | cut -f1))"
  echo "    contents: $(tar -tzf "$dest" | tr '\n' ' ')"
  prune "${OUT_DIR}/budgie-sqlite-*.tar.gz"
}

if [[ -n "${BUDGIE_POSTGRES_DSN:-}" ]]; then
  backup_postgres
else
  backup_sqlite
fi

echo "==> backup complete (retaining newest ${KEEP} in ${OUT_DIR})"
echo "    verify integrity later with: scripts/restore.sh --verify <artifact>"
