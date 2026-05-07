# Auth — Feature PRD

## Overview

Auth provides authentication (who are you?) for Ultrabase applications. It includes user registration, login, JWT tokens, email verification, password reset, and OAuth. Authorization is handled entirely by RLS — there is no HTTP-level RBAC or role system.

---

## YAML Syntax

Presence of the `auth:` section enables auth. Each auth method is a separate key — presence enables that method.

```yaml
auth:
  jwt_expiry: 15m
  refresh_tokens: true
  refresh_token_expiry: 7d
  allow_signup: true        # default; set false for admin-only projects
  allow_anonymous: true     # default; set false to disable anonymous sign-in

  fields:
    display_name: { type: text, required: true }
    avatar_url: { type: text }

  email:
    verify_email: true
    templates:
      verify:
        subject: "Verify your email — {{project.name}}"
        body_file: templates/verify.html
      reset:
        subject: "Reset your password"
        body: |
          Hi {{data.display_name}},
          Click here to reset: {{link}}

  google:
    client_id: "${GOOGLE_CLIENT_ID}"
    client_secret: "${GOOGLE_CLIENT_SECRET}"
    redirect_url: "https://app.example.com/auth/callback/google"

  github:
    client_id: "${GITHUB_CLIENT_ID}"
    client_secret: "${GITHUB_CLIENT_SECRET}"
    redirect_url: "https://app.example.com/auth/callback/github"
```

### Auth Settings

| Setting | Type | Default | Description |
|---|---|---|---|
| `jwt_expiry` | duration | `15m` | Access token lifetime |
| `refresh_tokens` | bool | `false` | Enable refresh token flow |
| `refresh_token_expiry` | duration | `7d` | Refresh token lifetime |
| `allow_signup` | bool | `true` | When `false`, `POST /auth/v1/signup` returns `403` with code `signup_disabled`. Admin-keyed `POST /auth/v1/admin/users` and `POST /auth/v1/invite` are unaffected. |
| `allow_anonymous` | bool | `true` | When `false`, anonymous sign-in (empty body to `/signup`) is rejected. Implied false when `allow_signup: false`. |
| `fields` | map | — | Custom columns added to `users` table |

### What's NOT Here

- No `enabled: true` — presence of `auth:` enables it
- No `roles` — dropped entirely; use RLS with `auth.uid()` and `auth.is_authenticated()`
- No `rate_limits` — delegated to reverse proxy
- No `session_strategy` — bearer tokens only
- No `api_keys` — single admin key via `ULTRABASE_ADMIN_KEY` env var
- No `mfa` — deferred
- No `lockout` — deferred

---

## Auto-Created `users` Table

The `users` table is auto-created from the `auth:` section. It is NOT declared under `tables:`.

```sql
CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255),          -- NULL for OAuth-only users
    email_verified BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- custom fields from auth.fields are added here:
    display_name TEXT NOT NULL,
    avatar_url TEXT
);
```

The name `users` is reserved and cannot be used for another table. Foreign keys reference `users.id` (not `_auth_users.id`).

### Supporting Tables

```sql
-- Refresh tokens (if enabled)
CREATE TABLE _auth_refresh_tokens (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(255) UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Email verification tokens
CREATE TABLE _auth_email_verifications (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token VARCHAR(255) UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Password reset tokens
CREATE TABLE _auth_password_reset_tokens (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(255) UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- OAuth identities
CREATE TABLE _user_identities (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider VARCHAR(50) NOT NULL,
    provider_user_id VARCHAR(255) NOT NULL,
    email VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(provider, provider_user_id)
);
```

---

## API Endpoints

### POST /api/auth/signup

```json
// Request
{ "email": "user@example.com", "password": "securepassword", "display_name": "Jane" }

// Response (201)
{ "id": 1, "email": "user@example.com", "email_verified": false, "display_name": "Jane" }
```

If `verify_email: true`, a verification email is sent and login is blocked until verified.

### POST /api/auth/login

```json
// Request
{ "email": "user@example.com", "password": "securepassword" }

// Response (200)
{
  "user": { "id": 1, "email": "user@example.com", "display_name": "Jane" },
  "access_token": "eyJ...",
  "refresh_token": "eyJ..."    // only if refresh_tokens: true
}
```

### POST /api/auth/logout

Revokes the refresh token (if present). Access JWT stays valid until natural expiry.

```json
// Request
{ "refresh_token": "eyJ..." }

// Response (200)
{ "message": "Logged out" }
```

### POST /api/auth/refresh

Exchange refresh token for new token pair. Refresh tokens are single-use — reuse detection invalidates all user tokens.

```json
// Request
{ "refresh_token": "eyJ..." }

// Response (200)
{ "access_token": "eyJ...", "refresh_token": "eyJ..." }
```

### GET /api/auth/me

Returns the current authenticated user.

```json
// Response (200)
{ "id": 1, "email": "user@example.com", "display_name": "Jane", "email_verified": true }
```

### POST /api/auth/reset-request

Always returns 200 (prevents email enumeration). Sends reset email if account exists.

```json
// Request
{ "email": "user@example.com" }

// Response (200)
{ "message": "If an account exists, a reset email has been sent" }
```

Requires an email provider in `providers:`. Gated at boot — warn if password reset is configured but no email provider.

### POST /api/auth/reset

```json
// Request
{ "token": "abc123", "password": "newsecurepassword" }

// Response (200)
{ "message": "Password reset successfully" }
```

Tokens expire after 1 hour. Single-use.

### GET /api/auth/verify?token=...

Email verification landing. Marks user as verified.

---

## OAuth (v1)

### Supported Providers

- **Google** — `auth.google` key
- **GitHub** — `auth.github` key

Each provider needs `client_id`, `client_secret`, and `redirect_url` (per-provider).

### Endpoints

- `GET /api/auth/<provider>` — initiates OAuth flow (redirects to provider)
- `GET /api/auth/callback/<provider>` — handles provider callback, exchanges code, redirects to app's `redirect_url` with token

### Account Linking

- Provider returns **verified email** + matches existing user → **link accounts**
- Provider returns **unverified email** + matches existing user → **reject** (prevents takeover)
- No existing user → **create new user**
- Each user may have multiple OAuth identities (stored in `_user_identities`)

---

## JWT

```json
{
  "sub": 1,
  "email": "user@example.com",
  "iat": 1712016000,
  "exp": 1712016900
}
```

- Algorithm: HS256
- Secret: `JWT_SECRET` env var (auto-generated with warning in dev mode)
- Expiry: strict — reject as soon as `exp < server_now`, no leeway
- Bearer token in `Authorization: Bearer <token>` header

---

## RLS Helper Functions

Created as PostgreSQL functions in the `auth` schema:

```sql
CREATE OR REPLACE FUNCTION auth.uid() RETURNS BIGINT AS $$
  SELECT NULLIF(current_setting('app.user_id', true), '')::BIGINT;
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION auth.is_authenticated() RETURNS BOOLEAN AS $$
  SELECT COALESCE(current_setting('app.is_authenticated', true)::BOOLEAN, false);
$$ LANGUAGE SQL STABLE;
```

Session variables set per-request via `SET LOCAL` inside a transaction:

```sql
BEGIN;
SET LOCAL app.user_id = '42';
SET LOCAL app.is_authenticated = 'true';
-- execute query --
COMMIT;
```

Unauthenticated requests: `auth.uid()` returns NULL, `auth.is_authenticated()` returns false.

---

## Email Templates

Both inline and file-based:

```yaml
auth:
  email:
    templates:
      verify:
        subject: "Verify your email — {{project.name}}"
        body_file: templates/verify.html      # file-based
      reset:
        subject: "Reset your password"
        body: "Click here: {{link}}"          # inline
```

### Variables

| Variable | Description |
|---|---|
| `{{data.email}}` | User's email |
| `{{data.display_name}}` | User's display name (or other fields) |
| `{{project.name}}` | Project name from YAML |
| `{{link}}` | Verification or reset link |

Default templates are shipped with the binary and used if nothing is configured.

---

## Auth Events

- **Table-level events** (`users.insert`, `users.update`, `users.delete`) flow through WAL like any table — available in the `on:` trigger system.
- **Application-level actions** (password_reset, email_verification) are NOT dispatched through WAL. The auth handler calls the email/webhook handler directly. These are not durable — if the process crashes mid-action, the user retries.

---

## Seeding Users

Auth users are seeded via the standard `seeds.users` block:

```yaml
seeds:
  users:
    - email: admin@example.com
      password: secret123
      display_name: Admin User
      email_verified: true
```

The framework automatically hashes the `password` field using bcrypt before inserting.

---

## Security

1. **Password hashing:** bcrypt with configurable cost factor (sensible default).
2. **Token storage:** Refresh tokens and password reset tokens are SHA-256 hashed before storage.
3. **Email enumeration:** Registration and forgot-password endpoints do not reveal whether an email exists.
4. **JWT secret:** Must be set via env var in production. Dev mode auto-generates a random secret with a warning.
5. **Password reset tokens:** Single-use, expire in 1 hour, invalidated on password change.
6. **Refresh token rotation:** Each use generates a new token pair. Reuse detection invalidates all user tokens.
7. **CSRF:** Double-submit cookie pattern available via YAML flag in `auth:` block (off by default since bearer-only).

---

## Edge Cases

1. **No auth section:** If `auth:` is absent from YAML, no `users` table is created, no auth endpoints are mounted. Table endpoints remain reachable; access is decided by table-level grants and RLS policies (anon requests run as the `anon` Postgres role).
2. **OAuth without email method:** Users can sign up via OAuth only. The `password_hash` column is NULL for these users. Login endpoint rejects them (they must use OAuth).
3. **Email provider missing:** If `verify_email: true` or password reset is configured but no email provider exists in `providers:`, the framework warns at boot (does not block startup).
4. **User deletion:** Governed by FK `on_delete` settings on tables referencing `users.id`. No special cascade behavior.
