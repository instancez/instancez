---
title: instancez Cloud
description: Deploy a project to instancez Cloud with inz cloud login and inz cloud deploy.
---

instancez Cloud runs your project as a managed service. You keep editing `instancez.yaml` locally, then push it to a hosted project with `inz cloud deploy`. The CLI handles auth, uploads the config, and deploys straight to the branch you choose.

## Sign in

```bash
inz cloud login
```

This runs a device-code flow: the CLI prints a one-time code, opens your browser to confirm it, and stores a Personal Access Token at `~/.instancez/credentials`. Later commands reuse that token, so you sign in once per machine. Pass `--force` to re-authenticate.

If you run `inz cloud deploy` or `inz cloud status` while signed out on an interactive terminal, the CLI offers to sign you in first. In a non-interactive session (CI, scripts) it stops and tells you to run `inz cloud login`, since it can't open a browser.

## Link a project

A cloud project is identified by `project.cloud.project_id` in your `instancez.yaml`. Create the project and write that field in one step:

```bash
inz init --with-cloud
```

On an existing project this adds the `project.cloud` block without touching the rest of your config:

```yaml
project:
  name: my-app
  cloud:
    project_id: <generated-by-init>
```

Re-running `inz init --with-cloud` over a config that already has a `project_id` reuses the existing project rather than creating a second one.

## Deploy

```bash
inz cloud deploy --branch draft
inz cloud deploy --branch production
```

`--branch` (default `draft`) selects which environment is written. Deploy
writes directly to that branch — there is no separate promote step:

1. Uploads function sources (if the project declares any) to the target branch.
2. For `--branch production` only: shows a page-free diff of what would
   change, then prompts `Deploy to production? [y/N]`. A bare Enter is
   treated as "no". Pass `--yes` (`-y`) to skip the prompt in scripts.
3. Uploads your local YAML to the target branch and triggers a rebuild.

`--branch draft` never prompts — writing to draft has never required
confirmation, and that stays true here.

### Code functions

If your project declares code functions, `inz cloud deploy` uploads your
function sources to the same branch as the yaml, and the cloud builds the
bundle. You do not need an S3 bucket or a local npm step for deployment.

`--functions-bundle-dest` no longer exists on `inz cloud deploy`. For self-hosted projects using `inz serve --bundle`, use `inz bundle --output s3://my-bucket/functions/` to build and upload the bundle yourself.

## Continuous deployment (GitHub Actions)

`inz cloud deploy` needs a Personal Access Token, but the device-code flow behind `inz cloud login` needs a browser, and CI can't open one. Instead, sign in once from your machine, copy the token, and hand CI the value directly:

```bash
inz cloud login
cat ~/.instancez/credentials   # copy the "pat" value
```

Store that value as a repository secret named `INSTANCEZ_CLOUD_PAT` (**Settings → Secrets and variables → Actions**). The CLI reads `INSTANCEZ_CLOUD_PAT` directly — no credentials file needs to be written in CI. Treat it like a password: it authenticates as whichever account ran `inz cloud login`, so revoke it from the instancez Cloud dashboard if it leaks.

This workflow deploys to draft on every pull request (for review) and to production on every push to `main`:

```yaml
# .github/workflows/deploy.yml
name: Deploy to instancez Cloud

on:
  pull_request:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install inz
        run: |
          curl -fsSL https://get.instancez.ai | sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"

      # Pull requests: deploy to draft for review.
      - name: Deploy draft
        if: github.event_name == 'pull_request'
        run: inz cloud deploy --branch draft
        env:
          INSTANCEZ_CLOUD_PAT: ${{ secrets.INSTANCEZ_CLOUD_PAT }}

      # main: deploy straight to production. --yes skips the confirmation
      # prompt, since the runner has no terminal to answer it anyway.
      - name: Deploy to production
        if: github.ref == 'refs/heads/main' && github.event_name == 'push'
        run: inz cloud deploy --branch production --yes
        env:
          INSTANCEZ_CLOUD_PAT: ${{ secrets.INSTANCEZ_CLOUD_PAT }}
```

Both jobs share the same `project.cloud.project_id` in `instancez.yaml`, so "draft" here means the project's draft/production split described above, not a second project.

If you'd rather deploy dev and production as two genuinely separate cloud projects (separate databases, separate URLs), skip the `project_id` in `instancez.yaml` and pass `--project` per job instead, pointing at different project ids held in their own secrets:

```yaml
      - name: Deploy to dev project
        if: github.event_name == 'pull_request'
        run: inz cloud deploy --project "$DEV_PROJECT_ID" --branch draft
        env:
          INSTANCEZ_CLOUD_PAT: ${{ secrets.INSTANCEZ_CLOUD_PAT }}
          DEV_PROJECT_ID: ${{ secrets.INSTANCEZ_DEV_PROJECT_ID }}

      - name: Deploy to production project
        if: github.ref == 'refs/heads/main' && github.event_name == 'push'
        run: inz cloud deploy --project "$PROD_PROJECT_ID" --branch production --yes
        env:
          INSTANCEZ_CLOUD_PAT: ${{ secrets.INSTANCEZ_CLOUD_PAT }}
          PROD_PROJECT_ID: ${{ secrets.INSTANCEZ_PROD_PROJECT_ID }}
```

`--project` targets a project for that run only; it never edits `instancez.yaml`, so both jobs can check out the exact same file.

## Check status

```bash
inz cloud status
```

This prints the project name, id, and URL, the production deploy status, and whether the local draft has unpublished changes relative to production. It's separate from `inz doctor`, which checks your local environment rather than the cloud project.

## Other commands

```bash
inz cloud whoami    # print the signed-in account's email
inz cloud logout    # forget the local Personal Access Token
```

`inz cloud logout` removes the token from `~/.instancez/credentials`. The token stays valid on the server until you revoke it from the dashboard.

## Pointing at a different API

The CLI talks to `https://my.instancez.ai/api` by default. To target a different instancez Cloud API, set `INSTANCEZ_CLOUD_API`:

```bash
export INSTANCEZ_CLOUD_API=https://cloud.example.com/api
```

You can also pin it per project under `project.cloud.api_url`, which takes precedence over the environment variable for commands that run inside a project (`cloud deploy`, `cloud status`).
