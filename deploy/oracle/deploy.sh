#!/usr/bin/env bash
# Deploy Budgie BBS to an Oracle Cloud instance.
#
# Defaults to arm64 (the Always-Free Ampere A1 shape). The default SSH user on
# OCI Ubuntu images is `ubuntu`, which has passwordless sudo:
#
#   BUDGIE_HOST=ubuntu@<ip> BUDGIE_DOMAIN=bbs.example ./deploy/oracle/deploy.sh
set -euo pipefail
export BUDGIE_ARCH="${BUDGIE_ARCH:-arm64}"
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")/../common" && pwd)/deploy-vps.sh"
