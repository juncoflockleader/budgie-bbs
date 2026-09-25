#!/usr/bin/env bash
# Deploy Budgie BBS to a DigitalOcean Droplet (x86 → amd64).
#
#   BUDGIE_HOST=root@<ip> BUDGIE_DOMAIN=bbs.example ./deploy/digitalocean/deploy.sh
set -euo pipefail
export BUDGIE_ARCH="${BUDGIE_ARCH:-amd64}"
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")/../common" && pwd)/deploy-vps.sh"
