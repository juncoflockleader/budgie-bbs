#!/usr/bin/env bash
#
# Provision a fresh Debian/Ubuntu host for a single-node Budgie BBS.
#
# Idempotent and self-contained (safe to curl|bash from cloud-init). Installs
# Caddy, creates the `budgie` service user + data dirs, drops the systemd unit,
# Caddyfile and env template, and opens the firewall (ufw). It does NOT install
# the binary — run deploy/common/deploy-vps.sh from your workstation afterwards.
#
# Firewall handling: this opens ports with ufw, which works on the Ubuntu images
# all three VPS providers offer. On Oracle you must ALSO open 80/443/2222 in the
# VCN Security List in the console (see deploy/oracle/README.md).
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive

FW="${BUDGIE_FIREWALL:-ufw}"   # ufw | iptables (Oracle) | skip

echo "==> installing packages"
apt-get update -y
apt-get install -y rsync curl debian-keyring debian-archive-keyring apt-transport-https gnupg

echo "==> installing Caddy"
if ! command -v caddy >/dev/null 2>&1; then
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
    | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
    > /etc/apt/sources.list.d/caddy-stable.list
  apt-get update -y
  apt-get install -y caddy
fi

echo "==> creating service user + directories"
id -u budgie >/dev/null 2>&1 || useradd --system --home-dir /opt/budgie --shell /usr/sbin/nologin budgie
install -d -o budgie -g budgie /opt/budgie /opt/budgie/scripts /var/lib/budgie /var/log/budgie

echo "==> installing systemd unit"
cat >/etc/systemd/system/budgied.service <<'UNIT'
[Unit]
Description=Budgie BBS (single-node, SQLite)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=budgie
Group=budgie
WorkingDirectory=/opt/budgie
EnvironmentFile=-/opt/budgie/budgie.env
Environment=BUDGIE_SINGLE_NODE_DATA_DIR=/var/lib/budgie
Environment=BUDGIE_SINGLE_NODE_BINARY=/opt/budgie/budgied
Environment=BUDGIE_SINGLE_NODE_WEB=/opt/budgie/web/dist
Environment=BUDGIE_SINGLE_NODE_HTTP=127.0.0.1:8081
Environment=BUDGIE_SINGLE_NODE_SSH=2222
ExecStart=/opt/budgie/scripts/run-single-node.sh
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/budgie

[Install]
WantedBy=multi-user.target
UNIT

echo "==> installing env template (only if absent)"
if [ ! -f /opt/budgie/budgie.env ]; then
  cat >/opt/budgie/budgie.env <<'ENVF'
# Set this to your real https origin so sitemap/robots/canonical + email links
# are correct. The deploy script rewrites YOUR_DOMAIN when you pass BUDGIE_DOMAIN.
BUDGIE_PUBLIC_URL=https://YOUR_DOMAIN
#BUDGIE_SITEMAP_INTERVAL=6h
ENVF
  chown budgie:budgie /opt/budgie/budgie.env
fi

echo "==> installing Caddyfile (only if absent)"
if [ ! -f /etc/caddy/Caddyfile ] || ! grep -q reverse_proxy /etc/caddy/Caddyfile 2>/dev/null; then
  cat >/etc/caddy/Caddyfile <<'CADDY'
YOUR_DOMAIN {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8081
}
CADDY
fi

echo "==> opening firewall ($FW): 22, 80, 443, 2222"
case "$FW" in
  ufw)
    apt-get install -y ufw
    for p in 22 80 443 2222; do ufw allow "$p"/tcp >/dev/null; done
    ufw --force enable >/dev/null
    ;;
  iptables)
    # Oracle Linux/Ubuntu images ship a restrictive INPUT chain that DROPs
    # everything but SSH; insert ACCEPTs at the top + persist them.
    apt-get install -y iptables-persistent netfilter-persistent
    for p in 80 443 2222; do
      iptables -C INPUT -p tcp --dport "$p" -j ACCEPT 2>/dev/null \
        || iptables -I INPUT -p tcp --dport "$p" -m conntrack --ctstate NEW -j ACCEPT
    done
    netfilter-persistent save || true
    ;;
  skip | none)
    echo "    firewall: skipped (configure the provider/cloud firewall yourself)"
    ;;
  *)
    echo "    unknown BUDGIE_FIREWALL=$FW; skipping" >&2
    ;;
esac

systemctl daemon-reload
systemctl enable caddy >/dev/null 2>&1 || true
systemctl restart caddy || true

cat <<'DONE'
==> host provisioned.
    Next, from your workstation (in the repo):
      BUDGIE_HOST=root@<this-host-ip> BUDGIE_DOMAIN=<your-domain> \
        ./deploy/common/deploy-vps.sh
    (use BUDGIE_ARCH=arm64 on Ampere/Hetzner CAX hosts)
DONE
