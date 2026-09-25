#!/usr/bin/env bash
#
# Restore (or verify) a BudgieBBS backup produced by scripts/backup.sh.
#
# Mode is inferred from the artifact name:
#   budgie-sqlite-*.tar.gz  -> SQLite restore (extracts db + secrets to a data dir)
#   budgie-postgres-*.dump  -> Postgres restore (pg_restore into a DSN)
#
# ALWAYS stop the budgied service before restoring over a live data directory or
# database.
#
# Usage:
#   scripts/restore.sh --verify <artifact>            # check checksum only
#   scripts/restore.sh <artifact.tar.gz> [--to DIR] [--force]
#   scripts/restore.sh <artifact.dump>   [--dsn DSN] [--force]
#
# Env: BUDGIE_SINGLE_NODE_DATA_DIR (sqlite target), BUDGIE_POSTGRES_DSN (pg target)
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ARTIFACT=""
TO_DIR="${BUDGIE_SINGLE_NODE_DATA_DIR:-${ROOT}/artifacts/single-node}"
DSN="${BUDGIE_POSTGRES_DSN:-}"
FORCE=0
VERIFY_ONLY=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --verify) VERIFY_ONLY=1; ARTIFACT="$2"; shift 2 ;;
    --to) TO_DIR="$2"; shift 2 ;;
    --dsn) DSN="$2"; shift 2 ;;
    --force) FORCE=1; shift ;;
    -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
    *) ARTIFACT="$1"; shift ;;
  esac
done

[[ -n "$ARTIFACT" ]] || { echo "ERROR: no artifact given" >&2; exit 2; }
[[ -f "$ARTIFACT" ]] || { echo "ERROR: artifact not found: $ARTIFACT" >&2; exit 2; }

verify_checksum() {
  local f="$1"
  if [[ ! -f "${f}.sha256" ]]; then
    echo "    (no .sha256 sidecar; skipping integrity check)"
    return 0
  fi
  echo "==> verifying checksum"
  if command -v sha256sum >/dev/null 2>&1; then
    ( cd "$(dirname "$f")" && sha256sum -c "$(basename "$f").sha256" )
  elif command -v shasum >/dev/null 2>&1; then
    ( cd "$(dirname "$f")" && shasum -a 256 -c "$(basename "$f").sha256" )
  else
    echo "    (no sha256 tool; skipping)"
  fi
}

verify_checksum "$ARTIFACT"
if [[ "$VERIFY_ONLY" == "1" ]]; then
  echo "==> verify OK"
  exit 0
fi

restore_sqlite() {
  command -v sqlite3 >/dev/null 2>&1 || { echo "ERROR: sqlite3 not found" >&2; exit 3; }
  mkdir -p "$TO_DIR"
  local existing="${TO_DIR}/budgie.db"
  if [[ -f "$existing" && "$FORCE" != "1" ]]; then
    echo "ERROR: $existing already exists. Stop budgied and re-run with --force." >&2
    exit 4
  fi
  echo "==> restoring SQLite backup into $TO_DIR"
  local stage; stage="$(mktemp -d)"; trap "rm -rf '$stage'" EXIT
  tar -xzf "$ARTIFACT" -C "$stage"
  # Place the database and verify it is well-formed before swapping it in.
  [[ -f "$stage/budgie.db" ]] || { echo "ERROR: archive has no budgie.db" >&2; exit 3; }
  if [[ "$(sqlite3 "$stage/budgie.db" 'PRAGMA integrity_check;')" != "ok" ]]; then
    echo "ERROR: restored database failed integrity_check" >&2; exit 3
  fi
  cp -p "$stage/budgie.db" "$existing"
  for f in jwt_secret budgie_host_key; do
    if [[ -f "$stage/$f" ]]; then install -m 600 "$stage/$f" "${TO_DIR}/$f"; fi
  done
  echo "==> restore complete. Start budgied against $TO_DIR."
}

restore_postgres() {
  command -v pg_restore >/dev/null 2>&1 || { echo "ERROR: pg_restore not found" >&2; exit 3; }
  [[ -n "$DSN" ]] || { echo "ERROR: set --dsn or BUDGIE_POSTGRES_DSN" >&2; exit 2; }
  echo "==> restoring Postgres backup into the target database"
  local clean=()
  if [[ "$FORCE" == "1" ]]; then
    echo "    --force: dropping/recreating objects (--clean --if-exists)"
    clean=(--clean --if-exists)
  else
    echo "    restoring into an empty database (use --force to overwrite an existing one)"
  fi
  pg_restore --no-owner --no-privileges "${clean[@]}" --dbname="$DSN" "$ARTIFACT"
  echo "==> restore complete."
}

case "$ARTIFACT" in
  *.tar.gz) restore_sqlite ;;
  *.dump)   restore_postgres ;;
  *) echo "ERROR: cannot infer mode from '$ARTIFACT' (want *.tar.gz or *.dump)" >&2; exit 2 ;;
esac
