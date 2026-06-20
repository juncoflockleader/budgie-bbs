#!/usr/bin/env bash
#
# Build the Linux deployment artifacts into deploy/dist/, laid out to mirror the
# server's /opt/budgie directory:
#
#   deploy/dist/budgied                      (static linux binary)
#   deploy/dist/web/dist/                     (built SPA)
#   deploy/dist/scripts/run-single-node.sh    (launcher)
#
# Usage:  build.sh [amd64|arm64]      (default: amd64, or $BUDGIE_ARCH)
# Skip the web build (reuse an existing web/dist) with BUDGIE_SKIP_WEB=1.
set -euo pipefail

ARCH="${1:-${BUDGIE_ARCH:-amd64}}"
case "$ARCH" in
  amd64 | arm64) ;;
  *) echo "ERROR: unsupported arch '$ARCH' (use amd64 or arm64)" >&2; exit 2 ;;
esac

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="$ROOT/deploy/dist"
cd "$ROOT"

if [[ "${BUDGIE_SKIP_WEB:-0}" != "1" ]]; then
  echo "==> building web/dist"
  ( cd web && npm ci --no-audit --no-fund && npm run build )
elif [[ ! -d "$ROOT/web/dist" ]]; then
  echo "ERROR: BUDGIE_SKIP_WEB=1 but web/dist does not exist; build it first" >&2
  exit 2
fi

echo "==> building budgied (linux/$ARCH, static; modernc SQLite needs no CGO)"
rm -rf "$OUT"
mkdir -p "$OUT/web" "$OUT/scripts"
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags='-s -w' -o "$OUT/budgied" ./cmd/budgied
cp -R web/dist "$OUT/web/dist"
cp scripts/run-single-node.sh "$OUT/scripts/run-single-node.sh"
chmod +x "$OUT/budgied" "$OUT/scripts/run-single-node.sh"

echo "==> artifacts ready in $OUT"
ls -la "$OUT"
