#!/bin/sh
# Deploy the get.instancez.ai Worker to Cloudflare.
#
# Auth with either `npx wrangler login`, or a Cloudflare API token scoped to
# "Workers Scripts: Edit":
#   export CLOUDFLARE_API_TOKEN=...
#   ./deploy.sh
#
# If the token can see more than one Cloudflare account, also set
# CLOUDFLARE_ACCOUNT_ID so wrangler knows which one to target. wrangler reads
# both variables straight from the environment.
set -eu

cd "$(dirname "$0")"

# Pull in wrangler the first time it runs.
if [ ! -d node_modules ]; then
  npm install
fi

# whoami exits 0 even when logged out, so match its output.
if [ -z "${CLOUDFLARE_API_TOKEN:-}" ] && ! npx wrangler whoami 2>&1 | grep -q "You are logged in"; then
  echo "error: not authenticated with Cloudflare." >&2
  echo "Run 'npx wrangler login', or export CLOUDFLARE_API_TOKEN with" >&2
  echo "'Workers Scripts: Edit' from https://dash.cloudflare.com/profile/api-tokens" >&2
  exit 1
fi

# Regenerate the base64-bundled scripts so the upload stays in sync and clears
# the WAF (see gen.mjs), then ship it.
node gen.mjs
exec npx wrangler deploy
