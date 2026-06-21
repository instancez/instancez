---
title: Configuration
description: Complete instancez.yaml schema reference. Every key, type, default, and example.
---

`instancez.yaml` is the single source of truth for your project. On boot, the server diffs it against the live database and applies migrations automatically.

Env vars are interpolated using `${VAR}` or `${VAR:-default}`. They are resolved at load time; references are never stored in the database.

## version

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `version` | `integer` | yes | Schema version. Currently `1`. |

## project

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `project.name` | `string` | — | Display name shown in the dashboard. |
| `project.description` | `string` | — | Optional project description. |

## extensions

```yaml
extensions:
  - pgcrypto
  - pg_trgm
```

List of Postgres extensions to enable. Each entry is passed to `CREATE EXTENSION IF NOT EXISTS`. Order is preserved.

## database

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `database.pool.max` | `integer` | `20` | Maximum connections in the request pool. |
| `database.pool.min` | `integer` | `5` | Minimum idle connections. |
| `database.pool.idle_timeout` | `duration` | `300s` | How long idle connections are held before closing. |

## server

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `server.port` | `integer` | `8080` | HTTP listen port. |
| `server.max_body_size` | `string` | `1MB` | Maximum request body size for non-upload endpoints. |
| `server.max_limit` | `integer` | `100` | **Not currently enforced.** Configuration value is defined but not validated on REST queries. Default query limit is 20. |
| `server.docs_ui` | `boolean` | `devMode` | Enable the built-in API docs UI at `/docs`. Defaults to `true` in `inz dev`, `false` in `inz serve`. When explicitly set, overrides the mode default. |

### server.cors

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `server.cors.origins` | `string[]` | `[]` | Allowed origins. Use `["*"]` to allow all. |
| `server.cors.methods` | `string[]` | `[]` | Allowed HTTP methods. |
| `server.cors.headers` | `string[]` | `[]` | Allowed request headers. |
| `server.cors.credentials` | `boolean` | `false` | Set `Access-Control-Allow-Credentials`. |
| `server.cors.max_age` | `integer` | `0` | Preflight cache duration in seconds. |

### server.timeouts

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `server.timeouts.request` | `duration` | `30s` | Per-request deadline. |
| `server.timeouts.db_query` | `duration` | `10s` | Per-query deadline. |
| `server.timeouts.upload` | `duration` | `5m` | Deadline for file upload requests. |
| `server.timeouts.shutdown` | `duration` | `30s` | Graceful shutdown window. |

Duration strings use Go format: `30s`, `5m`, `1h`.

## providers

### providers.email

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `providers.email.type` | `string` | — | Email provider type. Currently `"resend"`. |
| `providers.email.api_key` | `string` | — | Provider API key. Supports `${VAR}`. |
| `providers.email.default_from_email` | `string` | — | Default sender address. |

Set `providers.email: null` to disable email sending.

### providers.storage

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `providers.storage.type` | `string` | — | `"local"` or `"s3"`. |
| `providers.storage.path` | `string` | — | (`local` only) Directory for local file storage. |
| `providers.storage.bucket` | `string` | — | (`s3` only) S3 bucket name. |
| `providers.storage.region` | `string` | — | (`s3` only) AWS region. |
| `providers.storage.access_key_id` | `string` | — | (`s3` only) AWS access key. Supports `${VAR}`. |
| `providers.storage.secret_access_key` | `string` | — | (`s3` only) AWS secret key. Supports `${VAR}`. |
| `providers.storage.endpoint` | `string` | — | (`s3` only) Custom endpoint URL for S3-compatible stores. |

## auth

Omit the `auth:` block entirely to disable authentication endpoints.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `auth.jwt_expiry` | `duration` | `15m` | Access token lifetime. |
| `auth.refresh_tokens` | `boolean` | `false` | Enable refresh token issuance. |
| `auth.refresh_token_expiry` | `duration` | `7d` | Refresh token lifetime (only used when `refresh_tokens: true`). |
| `auth.allow_signup` | `boolean` | `true` | Allow public `POST /auth/v1/signup`. Set to `false` for invite-only. |
| `auth.allow_anonymous` | `boolean` | `true` | Allow anonymous sign-in (empty-body signup). |
| `auth.redirect_urls` | `string[]` | `[]` | Allowlist of origins for post-auth redirects (OAuth, email verification). The server's own origin is always allowed. |

### auth.email

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `auth.email.verify_email` | `boolean` | `false` | Require email verification before the user can sign in. Requires a configured email provider. |
| `auth.email.templates` | `map` | — | Override built-in email templates by name (e.g. `confirm`, `recovery`). |
| `auth.email.templates.<name>.subject` | `string` | — | Email subject line. |
| `auth.email.templates.<name>.body` | `string` | — | Inline HTML/text body. |
| `auth.email.templates.<name>.body_file` | `string` | — | Path to a file containing the body (alternative to `body`). |

### auth.oauth.&lt;name&gt;

OAuth provider configuration. Providers are keyed by name under `auth.oauth`; the
name (`google`, `github`, …) selects the built-in provider implementation.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `auth.oauth.<name>.client_id` | `string` | — | OAuth client ID. Supports `${VAR}`. |
| `auth.oauth.<name>.client_secret` | `string` | — | OAuth client secret. Supports `${VAR}`. |
| `auth.oauth.<name>.redirect_url` | `string` | — | OAuth callback URL registered with the provider. |

## tables

Tables map to Postgres tables in the `public` schema by default. The migrator diffs this block against the live database on each boot.

```yaml
tables:
  posts:
    schema: public       # optional; default "public"
    fields:
      - name: id
        type: bigserial
        primary_key: true
      - ...
    indexes:
      - ...
    rls:
      - ...
```

### tables.\<name\>.fields

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `fields[].name` | `string` | required | Column name. |
| `fields[].type` | `string` | required | Postgres type (e.g. `text`, `bigint`, `uuid`, `timestamptz`, `text[]`). |
| `fields[].primary_key` | `boolean` | `false` | Mark as primary key. |
| `fields[].required` | `boolean` | `false` | Add `NOT NULL` constraint. |
| `fields[].unique` | `boolean` | `false` | Add `UNIQUE` constraint. |
| `fields[].default` | `any` | — | Column default. Supported: literal values, `now()`, `current_date`, `current_time`. The shorthand `uuid_v7()` and `uuid_v4()` are normalized to `gen_random_uuid()`. |
| `fields[].enum` | `string[]` | — | Restrict values to this list (creates a `CHECK` constraint). |
| `fields[].pattern` | `string` | — | Regex pattern for a `CHECK` constraint. |
| `fields[].min` | `number` | — | Minimum numeric value (inclusive). |
| `fields[].max` | `number` | — | Maximum numeric value (inclusive). |
| `fields[].check` | `string` | — | Raw SQL `CHECK` expression. |
| `fields[].foreign_key.references` | `string` | — | `table.column` or `schema.table.column`. |
| `fields[].foreign_key.on_delete` | `string` | — | `cascade`, `restrict`, or `set_null`. |
| `fields[].ref` | `string` | — | Storage reference in the form `storage.<bucket>`. |
| `fields[].on_delete` | `string` | — | (`ref` only) `cascade` or `keep` — whether to delete the object on row deletion. |

No columns are injected automatically. Every column, including primary keys, must be declared.

### tables.\<name\>.indexes

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `indexes[].columns` | `string[]` | required | Columns to index. |
| `indexes[].unique` | `boolean` | `false` | Create a unique index. |
| `indexes[].where` | `string` | — | Partial index condition (SQL expression). |

### tables.\<name\>.rls

RLS is the only authorization layer. Declare policies here; instancez applies `ENABLE ROW LEVEL SECURITY` and creates the policies automatically.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `rls[].operations` | `string[]` | required | One or more of `select`, `insert`, `update`, `delete`. |
| `rls[].check` | `string` | required | SQL boolean expression evaluated per row. |
| `rls[].type` | `string` | `permissive` | `permissive` or `restrictive`. |

Useful SQL helpers available in RLS expressions:

- `auth.uid()` — UUID of the authenticated user (`null` for anonymous).
- `auth.is_authenticated()` — true when the request carries a valid user JWT.

## storage

Bucket definitions. Buckets cannot be created, modified, or deleted at runtime; only `instancez.yaml` changes take effect.

```yaml
storage:
  avatars:
    public: false
    max_size: 5MB
    types:
      - image/png
      - image/jpeg
    rls:
      - operations: [select]
        check: "true"
```

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `storage.<name>.public` | `boolean` | `false` | Allow unauthenticated downloads via `/storage/v1/object/public/...`. |
| `storage.<name>.max_size` | `string` | `50MB` | Maximum file size per upload (e.g. `5MB`, `1GB`). |
| `storage.<name>.types` | `string[]` | `[]` | Allowed MIME types. Wildcards like `image/*` are accepted. Empty means all types allowed. |
| `storage.<name>.rls` | `RLSPolicy[]` | `[]` | Same policy shape as table RLS, applied to `storage.objects`. |

## rpc

Postgres stored procedures exposed at `/rest/v1/rpc/<name>`.

```yaml
rpc:
  search_posts:
    description: Full-text search
    auth_required: false
    language: plpgsql      # sql | plpgsql (default: plpgsql)
    volatility: stable     # volatile | stable | immutable (default: volatile)
    security: invoker      # invoker | definer (default: invoker)
    args:
      - name: query
        type: text
        required: true
    returns:
      type: "setof posts"
    body: |
      SELECT * FROM posts WHERE body @@ plainto_tsquery(query);
```

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `rpc.<name>.description` | `string` | — | Documentation string. |
| `rpc.<name>.auth_required` | `boolean` | `false` | Reject unauthenticated callers. |
| `rpc.<name>.language` | `string` | `plpgsql` | `sql` or `plpgsql`. |
| `rpc.<name>.volatility` | `string` | `volatile` | `volatile`, `stable`, or `immutable`. Only stable/immutable can be called with `GET`. |
| `rpc.<name>.security` | `string` | `invoker` | `invoker` or `definer`. |
| `rpc.<name>.args[].name` | `string` | required | Argument name. |
| `rpc.<name>.args[].type` | `string` | required | Postgres type. |
| `rpc.<name>.args[].required` | `boolean` | `false` | Return 400 if argument is absent. |
| `rpc.<name>.args[].default` | `any` | — | Postgres DEFAULT value for optional args. |
| `rpc.<name>.returns.type` | `string` | — | Return type: `void`, a scalar type, or `setof <table>`. |
| `rpc.<name>.body` | `string` | required | Function body (PL/pgSQL or SQL). |

## functions

JavaScript code functions served at `/functions/v1/<name>`. Distinct from `rpc:` (which declares Postgres stored procedures).

```yaml
functions:
  send_notification:
    runtime: node
    file: functions/send_notification.js
    auth_required: true
    timeout: 15s
    env:
      WEBHOOK_URL: https://hooks.example.com/notify
      API_KEY: ${INSTANCEZ_ENV_NOTIFY_API_KEY}
```

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `functions.<name>.runtime` | `string` | required | `"node"`. |
| `functions.<name>.file` | `string` | required | Path to the JS handler file, relative to the config root. |
| `functions.<name>.auth_required` | `boolean` | `false` | Require a valid JWT. Returns 401 otherwise. |
| `functions.<name>.timeout` | `duration` | `30s` | Per-request timeout. Exceeded requests return 504. |
| `functions.<name>.env` | `map` | `{}` | Env values injected as `ctx.env`. Values may be string literals or `${INSTANCEZ_ENV_*}` references. |

`INSTANCEZ_ENV_*` references are resolved from the process environment. Plain `${VAR}` references (without the `INSTANCEZ_ENV_` prefix) are not supported in `env:` — use them elsewhere in the config file for other values.

## functions_bundle

Internal field written by `inz deploy`. Points to the pre-built functions bundle URI (e.g. `s3://bucket/key#<sha256>`). Do not set this manually.

---

## Full example

```yaml
version: 1

project:
  name: My App
  description: Example instancez project

extensions:
  - pgcrypto

server:
  port: 8080
  max_body_size: 5MB
  max_limit: 500
  docs_ui: true
  cors:
    origins: ["https://app.example.com"]
    methods: [GET, POST, PATCH, DELETE, OPTIONS]
    headers: [Authorization, Content-Type, apikey]
    credentials: true
    max_age: 86400
  timeouts:
    request: 30s
    db_query: 10s
    upload: 5m
    shutdown: 30s

database:
  pool:
    max: 20
    min: 5
    idle_timeout: 300s

providers:
  email:
    type: resend
    api_key: ${RESEND_API_KEY}
    default_from_email: noreply@example.com
  storage:
    type: s3
    bucket: my-app-storage
    region: us-east-1
    access_key_id: ${AWS_ACCESS_KEY_ID}
    secret_access_key: ${AWS_SECRET_ACCESS_KEY}

auth:
  jwt_expiry: 1h
  refresh_tokens: true
  refresh_token_expiry: 30d
  allow_signup: true
  allow_anonymous: false
  redirect_urls:
    - https://app.example.com
  email:
    verify_email: true

tables:
  profiles:
    fields:
      - name: id
        foreign_key:
          references: auth.users.id
          on_delete: cascade
        primary_key: true
      - name: display_name
        type: text
      - name: avatar_url
        type: text
        ref: storage.avatars
        on_delete: keep
    rls:
      - operations: [select]
        check: "true"
      - operations: [insert, update]
        check: "auth.uid() = id"

storage:
  avatars:
    public: true
    max_size: 2MB
    types: [image/png, image/jpeg, image/webp]
    rls:
      - operations: [insert]
        check: "auth.uid() IS NOT NULL"
      - operations: [delete]
        check: "auth.uid() IS NOT NULL"

rpc:
  profile_search:
    language: sql
    volatility: stable
    args:
      - name: q
        type: text
        required: true
    returns:
      type: "setof profiles"
    body: |
      SELECT * FROM profiles WHERE display_name ILIKE '%' || q || '%';

functions:
  resize_avatar:
    runtime: node
    file: functions/resize_avatar.js
    auth_required: true
    timeout: 20s
    env:
      MAX_WIDTH: "512"
```
