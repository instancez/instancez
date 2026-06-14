---
title: Auth
description: Password, magic link, OTP, OAuth, anonymous sign-in, and TOTP MFA — all wired to Postgres RLS.
---

## Configuration

The `auth:` block in `instancez.yaml` controls JWT lifetime, refresh tokens, sign-up permissions, and OAuth providers:

```yaml
auth:
  jwt_expiry: 1h
  refresh_tokens: true
  refresh_token_expiry: 7d

  # Set to false to disable public sign-up (admin key can still create users)
  allow_signup: true
  # Set to false to block anonymous sign-in
  allow_anonymous: true

  # Allowlist of origins that post-auth flows may redirect to
  redirect_urls:
    - https://myapp.example.com

  email:
    # When true, signup emails must be confirmed before a session is issued.
    # Requires an email provider under providers.email.
    verify_email: false

  google:
    client_id: YOUR_GOOGLE_CLIENT_ID
    client_secret: YOUR_GOOGLE_CLIENT_SECRET
    redirect_url: https://myapp.example.com/auth/callback

  github:
    client_id: YOUR_GITHUB_CLIENT_ID
    client_secret: YOUR_GITHUB_CLIENT_SECRET
    redirect_url: https://myapp.example.com/auth/callback
```

All keys are optional. Omit `auth:` entirely and JWT auth still works with the defaults (1h expiry, no refresh tokens, sign-up open).

## Auth methods

instancez implements the full `@supabase/supabase-js` auth surface. Call it exactly as you would against Supabase — no client changes needed.

**Email + password** — `supabase.auth.signUp()` / `supabase.auth.signInWithPassword()`

When `email.verify_email` is `false` (the default), `signUp` returns a session immediately. Set it to `true` and configure an email provider to require confirmation first.

**Magic link / Email OTP** — `supabase.auth.signInWithOtp()` / `supabase.auth.verifyOtp()`

Requires an email provider under `providers.email`. Without one, `signInWithOtp` returns a 500.

**OAuth (Google, GitHub)** — `supabase.auth.signInWithOAuth({ provider: 'google' })`

Add the provider block to `auth:` in `instancez.yaml` (see above). Add the callback origin to `auth.redirect_urls`. Session is returned via PKCE after the OAuth callback.

**Anonymous** — `supabase.auth.signInAnonymously()`

Issues a JWT with `is_anonymous: true` and the `anon` Postgres role. Set `allow_anonymous: false` to disable. Anonymous users can be promoted to a full account by calling `signUp` or linking an OAuth identity.

**Session management** — `getSession()`, `onAuthStateChange()`, `signOut()` all work as documented by supabase-js. `signOut` invalidates the refresh token server-side.

**TOTP MFA** — the full `auth.mfa` surface is implemented: `enroll`, `challenge`, `verify`, `unenroll`, `listFactors`. A successful `verify` re-issues the session JWT with `aal: aal2`.

See the [Auth API reference](/api-reference/auth/) for the endpoint listing and JWT claims structure.

## Using auth in RLS

Every request carries the user's JWT. The middleware switches the Postgres role and writes the user ID into a session GUC before running any query, so RLS policies can call `auth.uid()` and `auth.is_authenticated()` directly:

```yaml
tables:
  posts:
    fields:
      - name: id
        type: bigserial
        primary_key: true
      - name: user_id
        foreign_key:
          references: auth.users.id
          on_delete: cascade
      - name: body
        type: text
        required: true
    rls:
      - operations: [select]
        check: "true"
      - operations: [insert, update, delete]
        check: "auth.uid() = user_id"
```

To restrict a table to signed-in users only:

```yaml
rls:
  - operations: [select]
    check: "auth.is_authenticated()"
```

See [RLS Policies](/build/rls/) for the full policy reference.

## What's next

- [RLS Policies](/build/rls/) — write access rules in SQL expressions
- [Schema](/build/schema/) — declare tables and fields in YAML
- [Storage](/build/storage/) — file uploads wired to the same JWT
