---
title: instancez Cloud
description: Deploy a project to instancez Cloud with inz cloud login and inz cloud deploy.
---

instancez Cloud runs your project as a managed service. You keep editing `instancez.yaml` locally, then push it to a hosted project with `inz cloud deploy`. The CLI handles auth, uploads the config, shows you what the migration will change, and promotes it to production once you confirm.

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
inz cloud deploy
```

Deploy reads the `project_id` from `instancez.yaml` and then:

1. Uploads your local YAML as the project's draft.
2. Shows a migration preview of what promoting the draft would change in production.
3. Prompts `Promote draft → production? [y/N]`. A bare Enter is treated as "no", so promoting is always an explicit choice.
4. Promotes the draft to production and prints the new version id.

Declining at the prompt leaves the draft uploaded but unpublished, so you can review it in the dashboard before promoting. Pass `--yes` (`-y`) to skip the prompt in scripts.

### Code functions

If your project declares code functions, `inz cloud deploy` uploads your function sources and the cloud builds the bundle. You do not need an S3 bucket or a local npm step for deployment.

`--functions-bundle-dest` no longer exists on `inz cloud deploy`. For self-hosted projects using `inz serve --bundle`, use `inz bundle --output s3://my-bucket/functions/` to build and upload the bundle yourself.

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
