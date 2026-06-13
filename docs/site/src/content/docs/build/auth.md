---
title: Auth
description: Password, magic link, OTP, OAuth, anonymous sign-in, and TOTP MFA — all wired to Postgres RLS.
---

instancez auth is wire-compatible with `@supabase/supabase-js` — the same client you already know works unchanged.

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

All keys under `auth:` are optional. If you omit `auth:` entirely, JWT auth still works but uses the defaults (1 h expiry, no refresh tokens, sign-up open).

## Email and password

```js
import { createClient } from '@supabase/supabase-js'

const supabase = createClient('http://localhost:8080', 'YOUR_ANON_KEY')

// Sign up
const { data, error } = await supabase.auth.signUp({
  email: 'user@example.com',
  password: 'hunter2',
})

// Sign in
const { data, error } = await supabase.auth.signInWithPassword({
  email: 'user@example.com',
  password: 'hunter2',
})
```

When `email.verify_email` is `false` (the default), `signUp` returns a session immediately. Set it to `true` and configure an email provider to require email confirmation before the user can sign in.

## Magic link and email OTP

```js
// Send a magic link / OTP to the address
const { error } = await supabase.auth.signInWithOtp({
  email: 'user@example.com',
})

// After the user clicks the link or pastes the 6-digit code:
const { data, error } = await supabase.auth.verifyOtp({
  email: 'user@example.com',
  token: '123456',
  type: 'email',
})
```

Magic links require an email provider under `providers.email`. Without one, `signInWithOtp` returns a 500 because no email can be sent.

## OAuth

Google and GitHub are the two supported OAuth providers. Add the relevant block to your `auth:` config (see [Configuration](#configuration) above), then use the standard supabase-js call:

```js
// Redirect the browser to the provider's consent screen
const { data, error } = await supabase.auth.signInWithOAuth({
  provider: 'google', // or 'github'
  options: {
    redirectTo: 'https://myapp.example.com/auth/callback',
  },
})
```

After the OAuth callback completes, the session is returned via PKCE. Add the callback origin to `auth.redirect_urls` in `instancez.yaml`.

## Anonymous sign-in

```js
const { data, error } = await supabase.auth.signInAnonymously()
```

The user gets a JWT with `is_anonymous: true` and the `anon` Postgres role. They can later be promoted to a full account by calling `signUp` or linking an OAuth identity. Set `allow_anonymous: false` in `instancez.yaml` to disable this path.

## Session management

```js
// Read the current session
const { data: { session } } = await supabase.auth.getSession()

// React to sign-in / sign-out events
supabase.auth.onAuthStateChange((event, session) => {
  console.log(event, session)
})

// Sign out (invalidates the refresh token server-side)
const { error } = await supabase.auth.signOut()
```

## TOTP MFA

instancez implements the `auth.mfa` surface in supabase-js: enroll, challenge, verify, unenroll, and list factors.

```js
// 1. Enroll a new TOTP factor — returns a QR code URI
const { data, error } = await supabase.auth.mfa.enroll({
  factorType: 'totp',
  friendlyName: 'My Authenticator',
})
// data.totp.qr_code — render this as an image
// data.totp.uri     — or pass directly to an authenticator app

// 2. Create a challenge
const { data: challenge, error } = await supabase.auth.mfa.challenge({
  factorId: data.id,
})

// 3. Verify the one-time code from the authenticator app
const { data: verified, error } = await supabase.auth.mfa.verify({
  factorId: data.id,
  challengeId: challenge.id,
  code: '123456',
})

// List enrolled factors
const { data: factors } = await supabase.auth.mfa.listFactors()

// Remove a factor
const { error } = await supabase.auth.mfa.unenroll({ factorId: data.id })
```

The first successful `verify` call marks the factor as `verified` and re-issues the session JWT with `aal: aal2`.

## Using auth in RLS

Every request carries the user's JWT. The middleware switches the Postgres role and writes the user ID into a session GUC before running any query, so your RLS policies can call `auth.uid()` and `auth.is_authenticated()` directly:

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
      # Anyone can read
      - operations: [select]
        check: "true"
      # Only the author can insert or update
      - operations: [insert, update]
        check: "auth.uid() = user_id"
      # Only the author can delete
      - operations: [delete]
        check: "auth.uid() = user_id"
```

To restrict a table to signed-in users only:

```yaml
rls:
  - operations: [select]
    check: "auth.is_authenticated()"
```

See [RLS Policies](/build/rls) for the full policy reference.

## What's next

- [RLS Policies](/build/rls) — write access rules in SQL expressions
- [Schema](/build/schema) — declare tables and fields in YAML
- [Storage](/build/storage) — file uploads wired to the same JWT
