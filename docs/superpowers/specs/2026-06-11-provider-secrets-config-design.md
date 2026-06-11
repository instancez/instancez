# Provider & Auth Secrets Config — Dashboard Editing

**Date:** 2026-06-11

## Overview

Introduce a consistent, secure model for configuring provider credentials (email API keys, storage credentials, OAuth client secrets) through the dashboard. Secrets never live in `instancez.yaml` or transit the config API — they always come from environment variables. The YAML stores explicit `${VAR}` references so the file is self-documenting. In dev mode the dashboard can optionally write values directly to the `.*.env` file via `--dashboard-write-dotenv`.

## Goals

- Secrets (API keys, OAuth credentials) never written to YAML as literal values
- YAML remains self-documenting: `${VAR}` refs tell the operator exactly what to set
- Dashboard GET/PUT never exposes secret values
- Dev-mode UX: dashboard can write values to `.development.env` when `--dashboard-write-dotenv` is passed
- Consistent model across email providers, storage providers, and OAuth providers

## YAML Structure

When a provider is enabled via the dashboard, it writes the full `${VAR}` ref block. Fixed var names per provider:

```yaml
providers:
  email:
    type: resend
    api_key: ${INSTANCEZ_RESEND_API_KEY}
    default_from_email: "Acme <noreply@acme.com>"   # not a secret, editable directly
  storage:
    type: s3
    bucket: ${INSTANCEZ_S3_BUCKET}
    region: ${AWS_REGION}                            # optional — SDK also reads natively
    # access_key_id / secret_access_key omitted → AWS SDK default credential chain
    # OR with explicit credentials toggle on:
    # access_key_id: ${AWS_ACCESS_KEY_ID}
    # secret_access_key: ${AWS_SECRET_ACCESS_KEY}

auth:
  google:
    client_id: ${INSTANCEZ_GOOGLE_CLIENT_ID}
    client_secret: ${INSTANCEZ_GOOGLE_CLIENT_SECRET}
    redirect_url: ${INSTANCEZ_GOOGLE_REDIRECT_URL}
  github:
    client_id: ${INSTANCEZ_GITHUB_CLIENT_ID}
    client_secret: ${INSTANCEZ_GITHUB_CLIENT_SECRET}
    redirect_url: ${INSTANCEZ_GITHUB_REDIRECT_URL}
```

Other storage providers follow the same pattern:

| Provider | Vars |
|----------|------|
| SendGrid | `INSTANCEZ_SENDGRID_API_KEY` |
| GCS | `INSTANCEZ_GCS_CREDENTIALS`, `INSTANCEZ_GCS_BUCKET` |
| MinIO | `INSTANCEZ_MINIO_ENDPOINT`, `INSTANCEZ_MINIO_ACCESS_KEY`, `INSTANCEZ_MINIO_SECRET_KEY`, `INSTANCEZ_MINIO_BUCKET` |
| Local | `INSTANCEZ_LOCAL_STORAGE_PATH` (already exists) |

AWS S3 credential vars (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`) use native AWS SDK names deliberately — the SDK reads them automatically, making the explicit-credentials toggle optional rather than required.

## Backend Changes

### A) `GET /config` — return raw unresolved config

`handleGetConfig` currently returns `h.liveConfig()` (fully env-var-resolved). Change it to read raw bytes from `h.configSource.Read()` and parse with a new `ParseBytesRaw` function that skips env var interpolation entirely. The dashboard always sees `${INSTANCEZ_RESEND_API_KEY}`, never the actual key value. Secret values never transit the API.

The live runtime config (used by all request handlers) remains env-var-resolved — only the dashboard API path uses the raw form.

### B) Domain struct changes

**`domain.EmailProvider`:**
```go
type EmailProvider struct {
    Type             string `yaml:"type" json:"type"`
    APIKey           string `yaml:"api_key" json:"api_key"`
    DefaultFromEmail string `yaml:"default_from_email" json:"default_from_email"`
}
```

**`domain.StorageProvider`** — add provider-specific credential fields, all optional:
```go
type StorageProvider struct {
    Type            string `yaml:"type" json:"type"`
    Bucket          string `yaml:"bucket" json:"bucket"`
    Region          string `yaml:"region" json:"region"`
    // S3 / MinIO explicit credentials (omit to use SDK default chain)
    AccessKeyID     string `yaml:"access_key_id" json:"access_key_id"`
    SecretAccessKey string `yaml:"secret_access_key" json:"secret_access_key"`
    // MinIO
    Endpoint        string `yaml:"endpoint" json:"endpoint"`
    // GCS
    Credentials     string `yaml:"credentials" json:"credentials"`
}
```

**`domain.OAuthProvider`** — no struct change needed; existing `ClientID`, `ClientSecret`, `RedirectURL` fields hold the `${VAR}` strings as-is.

### C) `GET /config/env-vars` endpoint

The server derives the relevant var list by scanning the raw YAML source for all `${VAR}` references (using the existing `config.EnvRefs` function) and checking each against `os.LookupEnv`. Returns set/missing status without exposing values:

```json
{
  "vars": {
    "INSTANCEZ_RESEND_API_KEY": { "set": true },
    "INSTANCEZ_GOOGLE_CLIENT_ID": { "set": false },
    "AWS_ACCESS_KEY_ID": { "set": true }
  }
}
```

Dashboard fetches this on load and after each config save to drive ✓/✗ status badges.

### D) `PUT /config/dotenv` endpoint

Only available when `--dashboard-write-dotenv` is active (returns 403 otherwise). Accepts a map of var names to values and writes them to the dotenv file path configured at startup (`.development.env` in `inz dev`; an explicit `--dotenv-path` argument required when passing `--dashboard-write-dotenv` to `inz serve`). Never writes to YAML.

If the YAML write (PUT `/config`) succeeds but the dotenv write fails, the dashboard surfaces the dotenv error separately — the YAML change is kept since it contains no secrets. The user is left with `${VAR}` refs in YAML and the dotenv still needing manual update, which is a safe degraded state.

### E) `--dashboard-write-dotenv` flag

New flag on both `inz serve` and `inz dev`. Defaults to **on** in `inz dev` (`.development.env` is already dev-only). Off by default in `inz serve`; requires `--dotenv-path` when enabled there.

`ParseBytesRaw` skips all env var interpolation including `${VAR:-default}` — defaults are not applied, the raw ref string is preserved as-is.

When active, `GET /config/status` includes `"dotenv_writable": true` so the dashboard knows to render editable inputs.

## Dashboard Changes

### Providers page

**Email section:** selecting a provider type writes the `${VAR}` ref block into the config. The existing env var badges become status-aware: `INSTANCEZ_RESEND_API_KEY ✓` or `✗`. Fixed message below the badges: *"Set these in your `.development.env` file or deployment environment."* `default_from_email` is a regular editable text field (not a secret).

**Storage section:** same status-badge pattern. S3 gains an **"Explicit credentials"** toggle — off means `access_key_id`/`secret_access_key` are omitted from the YAML (AWS SDK default credential chain); on writes `${AWS_ACCESS_KEY_ID}` / `${AWS_SECRET_ACCESS_KEY}` refs.

When `dotenv_writable: true`: each var badge expands to an editable input. Save becomes two sequential writes — PUT `/config` (YAML with `${VAR}` refs) then PUT `/config/dotenv` (values to `.development.env`).

### Auth page

The three OAuth text inputs (`client_id`, `client_secret`, `redirect_url`) are replaced with the var-badge-with-status pattern. Editable inputs only appear when `dotenv_writable: true`. When Google or GitHub OAuth is toggled on, the YAML gets the full `${VAR}` ref block for that provider.

### F) TypeScript types

`Providers` interface in `dashboard/src/lib/types.ts` needs updating — `email` and `storage` currently typed as `{ type: string } | null`. Expand to match the new domain structs (adding `api_key`, `default_from_email` for email; `bucket`, `region`, `access_key_id`, `secret_access_key`, `endpoint`, `credentials` for storage).

## Invariant

Regardless of mode or flag, the YAML **always** contains `${VAR}` refs for secret fields — never literal values. The `--dashboard-write-dotenv` flag controls only whether the dashboard can write values to the `.env` file; it does not change the YAML serialization behavior.
