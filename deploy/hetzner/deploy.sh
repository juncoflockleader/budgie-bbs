#!/usr/bin/env bash
# Deploy Budgie BBS to a Hetzner Cloud host.
#
#   BUDGIE_HOST=root@<ip> BUDGIE_DOMAIN=bbs.example ./deploy/hetzner/deploy.sh
#
# Defaults to arm64 (the cheaper CAX/Ampere line). For a CX (x86) server:
#   BUDGIE_ARCH=amd64 BUDGIE_HOST=... ./deploy/hetzner/deploy.sh
set -euo pipefail
export BUDGIE_ARCH="${BUDGIE_ARCH:-arm64}"
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")/../common" && pwd)/deploy-vps.sh"
