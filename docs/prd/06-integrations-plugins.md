# Providers — Feature PRD

## Overview

Providers connect Ultrabase to external services (email delivery, object storage). They are configured in the top-level `providers:` section with credentials supplied via environment variables.

**Plugins are dropped** — there is no plugin system in v1. All extensibility features are compiled into the single binary.

---

## YAML Syntax

```yaml
providers:
  email:
    type: resend
    # Credentials via ULTRABASE_EMAIL_API_KEY env var

  storage:
    type: s3
    # Credentials via AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION, S3_BUCKET
```

Each provider has a `type:` field naming the implementation. All credentials are supplied via environment variables — never in YAML.

### What's NOT a Provider

- **Database** — configured via `ULTRABASE_OWNER_DATABASE_URL` + `ULTRABASE_AUTH_DATABASE_URL` env vars, not in `providers:`
- There is no `database` provider type

---

## Built-in Providers

### Email

Used by: auth (verification emails, password reset) and `on:` email actions.

```yaml
providers:
  email:
    type: resend          # resend | sendgrid | ses
```

**Env vars by type:**

| Type | Required Env Var |
|---|---|
| `resend` | `ULTRABASE_EMAIL_API_KEY` |
| `sendgrid` | `ULTRABASE_SENDGRID_API_KEY` |
| `ses` | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION` |

`from`, `reply_to`, and other message-level settings are specified at action-use time (in `on:` email actions or auth templates), not in the provider config.

### Storage

Used by: the storage system (signed URLs, `_objects` table).

```yaml
providers:
  storage:
    type: s3              # s3 | gcs | minio | local
```

**Env vars by type:**

| Type | Required Env Vars |
|---|---|
| `s3` | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`, `S3_BUCKET` |
| `gcs` | `GOOGLE_APPLICATION_CREDENTIALS`, `GCS_BUCKET` |
| `minio` | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `S3_ENDPOINT`, `S3_BUCKET` |
| `local` | `ULTRABASE_LOCAL_STORAGE_PATH` (default: `./uploads`) |

`local` is for development only — files stored on the local filesystem.

---

## Provider Interfaces

```go
// domain/email.go
type EmailProvider interface {
    Send(ctx context.Context, msg EmailMessage) error
}

type EmailMessage struct {
    To      []string
    From    string
    ReplyTo string
    Subject string
    HTML    string
    Text    string
}

// domain/storage.go
type StorageProvider interface {
    SignUpload(ctx context.Context, key string, contentType string, expiry time.Duration) (string, error)
    SignDownload(ctx context.Context, key string, expiry time.Duration) (string, error)
    Delete(ctx context.Context, key string) error
    EnsureBucket(ctx context.Context, bucket string) error
}
```

New providers are added by implementing the interface and registering the adapter.

---

## Env Var Conventions

### Interpolation in YAML

```yaml
auth:
  google:
    client_id: "${GOOGLE_CLIENT_ID}"
    client_secret: "${GOOGLE_CLIENT_SECRET}"
```

Two syntaxes:
- `${VAR}` — required, fails if missing
- `${VAR:-default}` — uses default if VAR is unset

### Resolution Order

1. Real environment variables (highest priority)
2. `.env` file in project root (`ultrabase dev` only — `ultrabase serve` does NOT load `.env`)
3. Missing required variable → startup error

### Startup Behavior

The framework scans all `${VAR}` references in YAML at startup and reports the full list of missing variables at once (not one-by-one).

---

## Startup Validation

### Fail Fast

- Missing env vars for configured providers → **error, block startup**
- Provider health check fails (can't reach S3, email API key invalid) → **error, block startup**

### Warn Only

- Auth features need email (e.g., `verify_email: true`) but no email provider configured → **warn, don't block**
- `on:` email actions configured but no email provider → **warn, don't block**

This allows the server to start for development even with incomplete provider configuration.

---

## Edge Cases

1. **No providers section:** If `providers:` is absent, no providers are initialized. Features that require providers (storage buckets, email actions) will fail at runtime with clear error messages.
2. **Provider type mismatch:** If `type: unknown_provider` is specified, validation fails at boot with supported types listed.
3. **Local storage in production:** `type: local` works in `serve` mode but is not recommended. No warning is issued — the user's choice.
