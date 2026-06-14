#!/usr/bin/env bash
#
# Install or remove a macOS launchd service for the single-node SQLite launcher.
#
# Usage:
#   ./scripts/install-single-node-launchd.sh
#   ./scripts/install-single-node-launchd.sh uninstall
#
# Optional env:
#   BUDGIE_LAUNCHD_LABEL        default com.budgie.bbs
#   BUDGIE_LAUNCHD_PLIST        default /Library/LaunchDaemons/<label>.plist
#   BUDGIE_LAUNCHD_USER         default sudo caller, then current user
#   BUDGIE_LAUNCHD_GROUP        default primary group for BUDGIE_LAUNCHD_USER
#   BUDGIE_LAUNCHD_LOG_DIR      default /var/log/budgie
#   BUDGIE_LAUNCHD_DRY_RUN      set 1 to print the plist and commands
#   BUDGIE_SINGLE_NODE_DATA_DIR default /var/lib/budgie
#   BUDGIE_SINGLE_NODE_BINARY   default ./budgied
#   BUDGIE_SINGLE_NODE_HTTP     default :8080
#   BUDGIE_SINGLE_NODE_SSH      default 2222
#   BUDGIE_SINGLE_NODE_WEB      default auto
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ACTION="${1:-install}"
if (( $# > 1 )); then
  echo "ERROR: install-single-node-launchd.sh accepts at most one action" >&2
  echo "       supported actions: install, uninstall" >&2
  exit 2
fi
case "$ACTION" in
  install|uninstall) ;;
  *)
    echo "ERROR: unsupported action: $ACTION" >&2
    echo "       supported actions: install, uninstall" >&2
    exit 2
    ;;
esac

LABEL="${BUDGIE_LAUNCHD_LABEL:-com.budgie.bbs}"
PLIST_PATH="${BUDGIE_LAUNCHD_PLIST:-/Library/LaunchDaemons/${LABEL}.plist}"
RUN_AS_USER="${BUDGIE_LAUNCHD_USER:-${SUDO_USER:-$(id -un)}}"
RUN_AS_GROUP="${BUDGIE_LAUNCHD_GROUP:-$(id -gn "$RUN_AS_USER" 2>/dev/null || id -gn)}"
LOG_DIR="${BUDGIE_LAUNCHD_LOG_DIR:-/var/log/budgie}"
DATA_DIR="${BUDGIE_SINGLE_NODE_DATA_DIR:-/var/lib/budgie}"
RUN_SCRIPT="${BUDGIE_LAUNCHD_RUN_SCRIPT:-$ROOT/scripts/run-single-node.sh}"
BINARY_PATH="${BUDGIE_SINGLE_NODE_BINARY:-$ROOT/budgied}"
HTTP_ADDR="${BUDGIE_SINGLE_NODE_HTTP:-:8080}"
SSH_PORT="${BUDGIE_SINGLE_NODE_SSH:-2222}"
WEB_ROOT="${BUDGIE_SINGLE_NODE_WEB:-auto}"
DRY_RUN="${BUDGIE_LAUNCHD_DRY_RUN:-0}"

if [[ ! -x "$RUN_SCRIPT" ]]; then
  echo "ERROR: single-node run script is not executable: $RUN_SCRIPT" >&2
  exit 2
fi
if [[ "$ACTION" == "install" && "$DRY_RUN" != "1" && ! -x "$BINARY_PATH" ]]; then
  echo "ERROR: budgied binary is not executable: $BINARY_PATH" >&2
  echo "       build one first: go build -o budgied ./cmd/budgied" >&2
  echo "       or set BUDGIE_SINGLE_NODE_BINARY=/path/to/budgied" >&2
  exit 2
fi

if [[ "$(id -u)" -eq 0 ]]; then
  SUDO=()
else
  SUDO=(sudo)
fi

xml_escape() {
  local value="$1"
  value="${value//&/&amp;}"
  value="${value//</&lt;}"
  value="${value//>/&gt;}"
  value="${value//\"/&quot;}"
  value="${value//\'/&apos;}"
  printf '%s' "$value"
}

plist_xml() {
  local label plist_user plist_group workdir run_script data_dir binary_path http_addr ssh_port web_root log_dir
  label="$(xml_escape "$LABEL")"
  plist_user="$(xml_escape "$RUN_AS_USER")"
  plist_group="$(xml_escape "$RUN_AS_GROUP")"
  workdir="$(xml_escape "$ROOT")"
  run_script="$(xml_escape "$RUN_SCRIPT")"
  data_dir="$(xml_escape "$DATA_DIR")"
  binary_path="$(xml_escape "$BINARY_PATH")"
  http_addr="$(xml_escape "$HTTP_ADDR")"
  ssh_port="$(xml_escape "$SSH_PORT")"
  web_root="$(xml_escape "$WEB_ROOT")"
  log_dir="$(xml_escape "$LOG_DIR")"
  printf '%s\n' \
'<?xml version="1.0" encoding="UTF-8"?>' \
'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"' \
'  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
'<plist version="1.0">' \
'<dict>' \
'  <key>Label</key>' \
"  <string>${label}</string>" \
'  <key>UserName</key>' \
"  <string>${plist_user}</string>" \
'  <key>GroupName</key>' \
"  <string>${plist_group}</string>" \
'  <key>WorkingDirectory</key>' \
"  <string>${workdir}</string>" \
'  <key>EnvironmentVariables</key>' \
'  <dict>' \
'    <key>BUDGIE_SINGLE_NODE_DATA_DIR</key>' \
"    <string>${data_dir}</string>" \
'    <key>BUDGIE_SINGLE_NODE_BINARY</key>' \
"    <string>${binary_path}</string>" \
'    <key>BUDGIE_SINGLE_NODE_HTTP</key>' \
"    <string>${http_addr}</string>" \
'    <key>BUDGIE_SINGLE_NODE_SSH</key>' \
"    <string>${ssh_port}</string>" \
'    <key>BUDGIE_SINGLE_NODE_WEB</key>' \
"    <string>${web_root}</string>" \
'  </dict>' \
'  <key>ProgramArguments</key>' \
'  <array>' \
"    <string>${run_script}</string>" \
'  </array>' \
'  <key>RunAtLoad</key>' \
'  <true/>' \
'  <key>KeepAlive</key>' \
'  <true/>' \
'  <key>ThrottleInterval</key>' \
'  <integer>5</integer>' \
'  <key>StandardOutPath</key>' \
"  <string>${log_dir}/budgied.log</string>" \
'  <key>StandardErrorPath</key>' \
"  <string>${log_dir}/budgied.err.log</string>" \
'</dict>' \
'</plist>'
}

print_install_plan() {
  echo "==> launchd install plan"
  echo "    label:   $LABEL"
  echo "    plist:   $PLIST_PATH"
  echo "    user:    $RUN_AS_USER:$RUN_AS_GROUP"
  echo "    data:    $DATA_DIR"
  echo "    binary:  $BINARY_PATH"
  echo "    script:  $RUN_SCRIPT"
  echo "    http:    $HTTP_ADDR"
  echo "    ssh:     $SSH_PORT"
  echo "    web:     $WEB_ROOT"
  echo "    logs:    $LOG_DIR"
}

install_service() {
  local tmp
  print_install_plan
  if [[ "$DRY_RUN" == "1" ]]; then
    echo "==> dry-run plist"
    plist_xml
    echo "==> dry-run commands"
    echo "mkdir -p '$DATA_DIR' '$LOG_DIR' '$(dirname "$PLIST_PATH")'"
    echo "install -m 0644 <plist> '$PLIST_PATH'"
    echo "launchctl bootstrap system '$PLIST_PATH'"
    echo "launchctl enable system/$LABEL"
    echo "launchctl kickstart -k system/$LABEL"
    return 0
  fi

  "${SUDO[@]}" mkdir -p "$DATA_DIR" "$LOG_DIR" "$(dirname "$PLIST_PATH")"
  "${SUDO[@]}" chown "$RUN_AS_USER:$RUN_AS_GROUP" "$DATA_DIR" "$LOG_DIR"
  tmp="$(mktemp "${TMPDIR:-/tmp}/budgie-launchd.XXXXXX.plist")"
  plist_xml >"$tmp"
  "${SUDO[@]}" install -m 0644 "$tmp" "$PLIST_PATH"
  rm -f "$tmp"
  "${SUDO[@]}" chown root:wheel "$PLIST_PATH"

  "${SUDO[@]}" launchctl bootout system "$PLIST_PATH" >/dev/null 2>&1 || true
  "${SUDO[@]}" launchctl bootstrap system "$PLIST_PATH"
  "${SUDO[@]}" launchctl enable "system/$LABEL"
  "${SUDO[@]}" launchctl kickstart -k "system/$LABEL"
  echo "==> installed and started launchd service: $LABEL"
}

uninstall_service() {
  echo "==> removing launchd service: $LABEL"
  if [[ "$DRY_RUN" == "1" ]]; then
    echo "launchctl bootout system '$PLIST_PATH'"
    echo "rm -f '$PLIST_PATH'"
    return 0
  fi
  "${SUDO[@]}" launchctl bootout system "$PLIST_PATH" >/dev/null 2>&1 || true
  "${SUDO[@]}" rm -f "$PLIST_PATH"
  echo "==> removed launchd service plist: $PLIST_PATH"
  echo "==> left data and logs intact: $DATA_DIR $LOG_DIR"
}

if [[ "$ACTION" == "uninstall" ]]; then
  uninstall_service
else
  install_service
fi
