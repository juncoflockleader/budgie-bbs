#!/usr/bin/env bash
#
# Deploy Budgie BBS to Fly.io as a single machine + volume (SQLite single-host).
# Requires flyctl (https://fly.io/docs/flyctl/install) and `fly auth login`.
#
#   FLY_APP=my-budgie [FLY_REGION=iad] ./deploy/fly/deploy.sh
#
# Idempotent: creates the app, the data volume, and the JWT secret only if
# missing, then deploys. Re-run to ship updates (data + sessions persist).
set -euo pipefail

: "${FLY_APP:?set FLY_APP=your-app-name (must be globally unique)}"
REGION="${FLY_REGION:-iad}"
VOL_SIZE="${FLY_VOLUME_SIZE_GB:-3}"

command -v flyctl >/dev/null 2>&1 || { echo "flyctl not found; install it first" >&2; exit 1; }

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
CFG="$HERE/fly.toml"

echo "==> rendering fly.toml"
sed -e "s/YOUR_APP_NAME/$FLY_APP/g" -e "s/YOUR_REGION/$REGION/g" "$HERE/fly.toml.template" > "$CFG"

echo "==> ensuring app exists"
flyctl apps create "$FLY_APP" 2>/dev/null || true

echo "==> ensuring data volume"
if ! flyctl volumes list -a "$FLY_APP" 2>/dev/null | grep -q budgie_data; then
  flyctl volumes create budgie_data --region "$REGION" --size "$VOL_SIZE" -a "$FLY_APP" --yes
fi

echo "==> ensuring JWT secret"
if ! flyctl secrets list -a "$FLY_APP" 2>/dev/null | grep -q BUDGIE_JWT_SECRET; then
  flyctl secrets set BUDGIE_JWT_SECRET="$(openssl rand -base64 48)" -a "$FLY_APP" --stage
fi

echo "==> deploying (single machine; SQLite is single-writer)"
flyctl deploy "$ROOT" --config "$CFG" --ha=false

echo "==> done."
echo "    https://$FLY_APP.fly.dev   (first registered user becomes admin)"
echo "    SSH TUI: ssh -p 2222 <handle>@$FLY_APP.fly.dev"
echo
echo "    NOTE: for a custom domain, run 'fly certs add bbs.example.com' and set"
echo "    BUDGIE_PUBLIC_URL accordingly (fly secrets set or [env] in fly.toml)."
