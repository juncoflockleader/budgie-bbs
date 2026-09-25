#!/usr/bin/env bash
#
# Build the BudgieBBS web SPA with a stable Node/npm resolution order.
#
# Codex and some GUI shells can put an embedded Node binary ahead of Homebrew
# in PATH. npm's env-node shebang then runs the wrong Node, which can fail to
# load Rollup's native optional package on macOS. Prefer Homebrew npm when it is
# present, and prepend its directory so the matching node is used too.
#
# Optional env:
#   BUDGIE_WEB_NPM      absolute npm path or command name
#   BUDGIE_WEB_INSTALL  set 1 to run npm ci before building
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

resolve_npm() {
  local candidate
  if [[ -n "${BUDGIE_WEB_NPM:-}" ]]; then
    if candidate="$(command -v "$BUDGIE_WEB_NPM" 2>/dev/null)"; then
      echo "$candidate"
      return 0
    fi
    if [[ -x "$BUDGIE_WEB_NPM" ]]; then
      echo "$BUDGIE_WEB_NPM"
      return 0
    fi
    echo "ERROR: BUDGIE_WEB_NPM is not executable or on PATH: $BUDGIE_WEB_NPM" >&2
    return 1
  fi
  for candidate in /opt/homebrew/bin/npm /usr/local/bin/npm; do
    if [[ -x "$candidate" ]]; then
      echo "$candidate"
      return 0
    fi
  done
  command -v npm
}

NPM_CLI="$(resolve_npm)"
NPM_DIR="$(dirname "$NPM_CLI")"
export PATH="$NPM_DIR:$PATH"

echo "==> web build toolchain"
echo "    npm:  $NPM_CLI"
echo "    node: $(command -v node)"
echo "    node version: $(node --version)"
echo "    npm version:  $("$NPM_CLI" --version)"

cd "$ROOT/web"
if [[ "${BUDGIE_WEB_INSTALL:-0}" == "1" ]]; then
  "$NPM_CLI" ci
fi
"$NPM_CLI" run build
