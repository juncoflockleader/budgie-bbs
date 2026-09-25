#!/usr/bin/env bash
#
# Build + push Budgie BBS to a provisioned Linux VPS (Hetzner / DigitalOcean /
# Oracle / any Debian-or-Ubuntu host set up by provision.sh or a cloud-init).
#
# The host must already have: the `budgie` user, /opt/budgie + /var/lib/budgie,
# the budgied systemd unit, and Caddy (the cloud-inits and provision.sh do this).
# This script only ships fresh artifacts and restarts the service.
#
# Required:
#   BUDGIE_HOST=user@host     SSH target with passwordless sudo (e.g. root@1.2.3.4)
# Optional:
#   BUDGIE_ARCH=amd64|arm64   target CPU (default amd64; use arm64 for Ampere/CAX)
#   BUDGIE_DOMAIN=bbs.example wire the domain into Caddy + BUDGIE_PUBLIC_URL
#   BUDGIE_SSH_OPTS="..."     extra ssh/rsync options (keys, ports)
set -euo pipefail

: "${BUDGIE_HOST:?set BUDGIE_HOST=user@host (an account with sudo)}"
ARCH="${BUDGIE_ARCH:-amd64}"
DOMAIN="${BUDGIE_DOMAIN:-}"
SSH_OPTS="${BUDGIE_SSH_OPTS:-}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"

# 1. Build the artifacts.
BUDGIE_ARCH="$ARCH" "$HERE/build.sh" "$ARCH"

# 2. Upload to a staging dir on the host.
echo "==> uploading to $BUDGIE_HOST"
# shellcheck disable=SC2086
rsync -az --delete -e "ssh $SSH_OPTS" "$ROOT/deploy/dist/" "$BUDGIE_HOST:/tmp/budgie-deploy/"

# 3. Install + (optionally) wire the domain + restart, all under sudo.
echo "==> installing + restarting on remote"
# shellcheck disable=SC2086
ssh $SSH_OPTS "$BUDGIE_HOST" "sudo BUDGIE_DOMAIN='$DOMAIN' bash -s" <<'REMOTE'
set -euo pipefail
install -d -o budgie -g budgie /opt/budgie /opt/budgie/scripts /var/lib/budgie /var/log/budgie
# Ship the binary + launcher without --delete (preserves budgie.env); refresh
# web assets with --delete so removed files don't linger.
rsync -a /tmp/budgie-deploy/budgied            /opt/budgie/budgied
rsync -a --delete /tmp/budgie-deploy/scripts/  /opt/budgie/scripts/
rsync -a --delete /tmp/budgie-deploy/web/      /opt/budgie/web/
chmod +x /opt/budgie/budgied /opt/budgie/scripts/run-single-node.sh
chown -R budgie:budgie /opt/budgie

if [ -n "${BUDGIE_DOMAIN:-}" ]; then
  sed -i "s#YOUR_DOMAIN#${BUDGIE_DOMAIN}#g" /etc/caddy/Caddyfile 2>/dev/null || true
  [ -f /opt/budgie/budgie.env ] && sed -i "s#YOUR_DOMAIN#${BUDGIE_DOMAIN}#g" /opt/budgie/budgie.env || true
  systemctl reload caddy 2>/dev/null || systemctl restart caddy 2>/dev/null || true
fi

systemctl daemon-reload
systemctl enable budgied >/dev/null 2>&1 || true
systemctl restart budgied
sleep 1
systemctl is-active budgied
REMOTE

echo "==> done."
if [ -n "$DOMAIN" ]; then
  echo "    https://$DOMAIN/healthz   (give Caddy a few seconds for the first certificate)"
  echo "    SSH TUI: ssh -p 2222 <handle>@$DOMAIN"
fi
