---
title: Environment Variables
description: Complete environment variable reference for instancez.
---

## Required

Both API keys plus one database credential path — either the single superuser DSN or the scoped owner/auth pair — must be set before `inz serve` will start. `inz dev` generates the two keys for you on first run.

| Variable | Description |
|----------|-------------|
| `INSTANCEZ_DATABASE_URL` | Superuser Postgres DSN (e.g. `postgres://postgres:password@localhost:5432/mydb`). The CLI provisions all required roles (`instancez_owner`, `authenticator`, `anon`, `authenticated`, `service_role`) automatically on startup. This is what `inz init` scaffolds for local dev. |
| `INSTANCEZ_OWNER_DATABASE_URL` + `INSTANCEZ_AUTH_DATABASE_URL` | Alternative to `INSTANCEZ_DATABASE_URL`: a pre-provisioned `instancez_owner` DSN and a pre-provisioned `authenticator` DSN, used instead of a superuser login. This is the path `inz init` writes into `.production.env`, and the one typically used in production where a superuser DSN isn't available. |
| `INSTANCEZ_PUBLISHABLE_KEY` | Publishable API key (value like `inz_publishable_…`). Client-safe, maps to the `anon` role, and is what client apps pass. Sent in the `apikey` header. Also settable with `--publishable-key`. |
| `INSTANCEZ_SECRET_KEY` | Secret API key (value like `inz_secret_…`). Server-side only, maps to `service_role`, and unlocks the admin API and dashboard login. Sent in the `apikey` header. Also settable with `--secret-key`. Leave unset to disable the admin routes (they return 404). |

## Config and watch

| Variable | Flag equivalent | Default | Description |
|----------|----------------|---------|-------------|
| `INSTANCEZ_CONFIG` | `--config` | `instancez.yaml` | Config source. Accepts a local file path or an `s3://bucket/key` URI. |
| `INSTANCEZ_BUNDLE` | `--bundle` | — | Bundle source (config + function source in one archive), built with `inz bundle`. Accepts a local path or `s3://bucket/key`. Takes the place of `INSTANCEZ_CONFIG` when set. |
| `INSTANCEZ_WATCH` | `--watch` | `false` | Re-apply config when the source changes. For S3 sources, polls on the watch interval. |
| `INSTANCEZ_WATCH_INTERVAL` | `--watch-interval` | `60s` | Poll interval for S3 config sources. Minimum 10 s. |

## Server

| Variable | Flag equivalent | Default | Description |
|----------|----------------|---------|-------------|
| `INSTANCEZ_PORT` | `--port` | `8080` (from config) | HTTP listen port. Overrides the value in `instancez.yaml`. |
| `INSTANCEZ_BASE_URL` | — | `http://localhost:<port>` | Base URL used to build links in auth emails (verification, magic link, password recovery). Set this to your public URL in production. |

## Database role names

By default instancez maps the fixed JWT wire values (`anon`, `authenticated`, `service_role`) to Postgres roles of the same names. These variables let you use different Postgres role names while keeping the JWT wire protocol unchanged.

| Variable | Default Postgres role | Description |
|----------|-----------------------|-------------|
| `INSTANCEZ_DB_AUTHENTICATOR_ROLE` | `authenticator` | Login role for the request pool |
| `INSTANCEZ_DB_ANON_ROLE` | `anon` | Role assumed for unauthenticated requests |
| `INSTANCEZ_DB_AUTHENTICATED_ROLE` | `authenticated` | Role assumed for requests with a valid user JWT |
| `INSTANCEZ_DB_SERVICE_ROLE` | `service_role` | Role assumed for requests authenticated with the secret key; has `BYPASSRLS` |
| `INSTANCEZ_DB_SEED_ROLE` | — (empty) | Optional Postgres role to run seed statements as. Unlike the roles above, there's no default — leave unset unless you have a specific seeding role provisioned. |

The JWT `role` claim values (`anon`, `authenticated`, `service_role`) are part of the Supabase wire protocol and are never renamed, regardless of the Postgres role names you configure here.

## Dashboard

| Variable | Flag equivalent | Default | Description |
|----------|----------------|---------|-------------|
| `INSTANCEZ_DASHBOARD` | `--dashboard` | `disabled` | Dashboard mode: `disabled`, `readonly`, or `readwrite`. Enable `readwrite` only in trusted environments. |
| `INSTANCEZ_DASHBOARD_WRITE_DOTENV` | `--dashboard-write-dotenv` | `false` | Allow the dashboard to write secrets to a `.env` file. Requires `INSTANCEZ_DOTENV_PATH`. |
| `INSTANCEZ_DOTENV_PATH` | `--dotenv-path` | — | Path to the `.env` file the dashboard may write when `INSTANCEZ_DASHBOARD_WRITE_DOTENV` is set. |

## Storage providers

Storage provider credentials are set in `instancez.yaml` under `providers.storage`. Use `${VAR}` interpolation to reference environment variables without hardcoding secrets:

```yaml
providers:
  storage:
    type: s3
    bucket: ${INSTANCEZ_S3_BUCKET}
    region: ${INSTANCEZ_S3_REGION}
    access_key_id: ${INSTANCEZ_S3_ACCESS_KEY_ID}
    secret_access_key: ${INSTANCEZ_S3_SECRET_ACCESS_KEY}
    endpoint: ${INSTANCEZ_S3_ENDPOINT}  # optional; for S3-compatible stores
```

instancez interpolates `${VAR}` references in `instancez.yaml` at load time. Any environment variable name works; the names above are conventions.

One variable is read directly from the process environment (no YAML reference needed):

| Variable | Description |
|----------|-------------|
| `INSTANCEZ_STORAGE_KEY_PREFIX` | Optional prefix prepended to all object keys in the storage bucket. Useful for sharing a bucket across environments. |

## Email providers

Email provider credentials follow the same YAML interpolation pattern:

```yaml
providers:
  email:
    type: resend
    api_key: ${INSTANCEZ_RESEND_API_KEY}
```

Set `INSTANCEZ_RESEND_API_KEY` (or any name you choose) in the environment and reference it in `instancez.yaml`.

## Config S3 credentials

When `INSTANCEZ_CONFIG` points to an S3 URI, a separate set of variables controls the S3 client used to fetch the config file. These are distinct from the storage-provider credentials above.

| Variable | Description |
|----------|-------------|
| `S3_REGION` | AWS region of the config bucket |
| `S3_ENDPOINT` | Custom endpoint for S3-compatible stores |
| `S3_ACCESS_KEY_ID` | Access key ID |
| `S3_SECRET_ACCESS_KEY` | Secret access key |

If these are unset, the S3 client falls back to the standard credential chain (IAM role, `~/.aws/credentials`, etc.).

## Code function secrets

Environment variables passed to code functions use the `INSTANCEZ_ENV_` prefix. Reference them in `instancez.yaml` under a function's `env:` block:

```yaml
functions:
  my-function:
    runtime: node
    file: functions/my-function.js
    env:
      STRIPE_SECRET_KEY: ${INSTANCEZ_ENV_STRIPE_SECRET_KEY}
      DATABASE_URL: ${INSTANCEZ_ENV_DATABASE_URL}
```

Only variables matching the pattern `INSTANCEZ_ENV_*` are forwarded to function workers. They are never written to the worker process environment directly; they are passed via a secure in-memory channel. Set them in the host environment (or `.production.env`) and reference them with `${INSTANCEZ_ENV_YOUR_KEY}` in the YAML.

## Migrations

| Variable | Flag equivalent | Default | Description |
|----------|----------------|---------|-------------|
| `INSTANCEZ_MIGRATE` | `--migrate` | `false` | Run pending schema migrations on startup |
| `INSTANCEZ_ALLOW_DESTRUCTIVE` | `--allow-destructive` | `false` | Reserved for gating `DROP TABLE` / `DROP COLUMN` in migrations. **Not currently enforced** — drops apply regardless of this setting. |

## Cloud

| Variable | Description |
|----------|-------------|
| `INSTANCEZ_CLOUD_API` | Overrides the cloud API base URL used by `inz cloud` commands. Defaults to `https://my.instancez.ai/api`. |
| `INSTANCEZ_CLOUD_PAT` | Personal access token for `inz cloud` commands, read directly from the environment. Lets CI authenticate without a `~/.instancez/credentials` file — see [Cloud deploys](/instancez/deploy/cloud/). |
