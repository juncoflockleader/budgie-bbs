#!/usr/bin/env bash
# dev.sh — start backend + frontend dev server together
# Usage: ./dev.sh [extra budgied flags]
#   e.g. ./dev.sh -http :9090

set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
WEB="$ROOT/web"

# ── colours ───────────────────────────────────────────────────────────────
BLU='\033[0;34m'; GRN='\033[0;32m'; RST='\033[0m'
log_be() { echo -e "${BLU}[backend]${RST} $*"; }
log_fe() { echo -e "${GRN}[web]    ${RST} $*"; }

# ── build backend if needed ────────────────────────────────────────────────
if [[ ! -f "$ROOT/budgied" ]] || [[ "$ROOT/cmd/budgied/main.go" -nt "$ROOT/budgied" ]]; then
  log_be "building…"
  (cd "$ROOT" && go build -o budgied ./cmd/budgied)
  log_be "built ok"
fi

# ── clean up both children on exit ────────────────────────────────────────
cleanup() {
  echo ""
  log_be "stopping"; kill "$BE_PID" 2>/dev/null || true
  log_fe "stopping"; kill "$FE_PID" 2>/dev/null || true
  wait "$BE_PID" "$FE_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# ── start backend ──────────────────────────────────────────────────────────
log_be "starting on :8080  (SSH :2222)"
"$ROOT/budgied" "$@" 2>&1 | sed "s/^/$(echo -e "${BLU}[backend]${RST}") /" &
BE_PID=$!

# ── start frontend ─────────────────────────────────────────────────────────
log_fe "starting vite on http://localhost:5173"
(cd "$WEB" && npm run dev) 2>&1 | sed "s/^/$(echo -e "${GRN}[web]    ${RST}") /" &
FE_PID=$!

# ── wait ───────────────────────────────────────────────────────────────────
wait "$BE_PID" "$FE_PID"
