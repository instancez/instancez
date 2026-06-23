# install/

The install one-liners and the Cloudflare Worker that serves them at
`get.instancez.ai`.

```sh
# macOS / Linux
curl -fsSL https://get.instancez.ai | sh

# Windows (PowerShell 5.1+)
irm https://get.instancez.ai/windows | iex
```

## What's here

- `install.sh`: POSIX installer for macOS and Linux.
- `install.ps1`: PowerShell installer for Windows.
- `src/index.js`: the Worker. Serves `install.sh` at `/` and `install.ps1` at
  `/windows`.
- `gen.mjs`: base64-encodes the two scripts into `src/scripts.generated.js`,
  which the Worker decodes at runtime. This keeps the literal `curl | sh` /
  `irm | iex` patterns out of the deploy upload, which Cloudflare's WAF blocks
  as remote-code-execution signatures. `deploy.sh` runs it automatically, so
  the `.sh` / `.ps1` files stay the single source of truth.
- `wrangler.toml`: Worker config, bound to `get.instancez.ai`.

The scripts download binaries directly from GitHub Releases, so the Worker
holds no secrets.

## How "latest" resolves

The release job in `.github/workflows/ci.yml` uploads a stable, version-less
copy of every binary (`inz_linux_amd64`, `inz_windows_arm64.exe`, and so on)
next to the versioned ones. That lets the scripts fetch a fixed URL:

```
https://github.com/instancez/instancez/releases/latest/download/inz_<os>_<arch>
```

No GitHub API call, no JSON parsing, no rate limits. Pinning a release
(`INSTANCEZ_VERSION=1.2.3`) swaps `latest` for `download/v1.2.3` and reuses the
same asset name.

## Prerequisite

Downloads only work once the repo's **releases are public**. A script the
public runs can't carry a token, so private release assets are out of reach
until the repo (or at least its releases) is public.

## Deploy

Deployment is manual and on purpose: this Worker rarely changes, so there's no
CI job for it. Needs a Cloudflare account with the `instancez.ai` zone on it.
The `custom_domain` route makes Cloudflare manage the DNS record, so there's no
separate CNAME to add.

`deploy.sh` reads a Cloudflare API token from the environment and pushes the
Worker. Create a token scoped to "Workers Scripts: Edit" at
<https://dash.cloudflare.com/profile/api-tokens>, then:

```sh
cd install
export CLOUDFLARE_API_TOKEN=...
# If the token can see more than one account, also:
# export CLOUDFLARE_ACCOUNT_ID=...
./deploy.sh
```

The script installs wrangler on first run. `npm run deploy` does the same thing.

## Test locally

```sh
cd install
npm run dev          # serves on http://localhost:8787

# In another shell, against a published release:
curl -fsSL http://localhost:8787 | sh
```

To change either installer, edit the `.sh` / `.ps1` file and run `npm run
deploy` again.
