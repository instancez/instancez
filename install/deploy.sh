#!/bin/sh
# Deploy the get.instancez.ai Worker to Cloudflare.
#
# Create a Cloudflare API token scoped to "Workers Scripts: Edit", then:
#   export CLOUDFLARE_API_TOKEN=...
#   ./deploy.sh
#
# If the token can see more than one Cloudflare account, also set
# CLOUDFLARE_ACCOUNT_ID so wrangler knows which one to target. wrangler reads
# both variables straight from the environment.
set -eu

cd "$(dirname "$0")"

if [ -z "${CLOUDFLARE_API_TOKEN:-}" ]; then
  echo "error: CLOUDFLARE_API_TOKEN is not set." >&2
  echo "Create one with 'Workers Scripts: Edit' at" >&2
  echo "  https://dash.cloudflare.com/profile/api-tokens" >&2
  echo "then run: export CLOUDFLARE_API_TOKEN=... && ./deploy.sh" >&2
  exit 1
fi

# Pull in wrangler the first time it runs.
if [ ! -d node_modules ]; then
  npm install
fi

# Regenerate the base64-bundled scripts so the upload stays in sync and clears
# the WAF (see gen.mjs), then ship it.
node gen.mjs
exec npx wrangler deploy
