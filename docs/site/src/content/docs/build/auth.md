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

  # Allowlist of frontend origins that post-auth flows (OAuth, magic link,
  # password recovery) may redirect the user's browser back to. See "OAuth
  # (Google, GitHub)" below for how this differs from oauth.<name>.redirect_url.
  redirect_urls:
    - https://myapp.example.com

  email:
    # When true, signup emails must be confirmed before a session is issued.
    # Requires an email provider under providers.email.
    verify_email: false

  # OAuth providers are keyed by name under oauth. The name (google, github, …)
  # selects the built-in provider implementation.
  oauth:
    google:
      client_id: YOUR_GOOGLE_CLIENT_ID
      client_secret: YOUR_GOOGLE_CLIENT_SECRET
      redirect_url: https://api.myapp.example.com/auth/v1/callback/google

    github:
      client_id: YOUR_GITHUB_CLIENT_ID
      client_secret: YOUR_GITHUB_CLIENT_SECRET
      redirect_url: https://api.myapp.example.com/auth/v1/callback/github
```

All keys are optional. Omit `auth:` entirely and JWT auth still works with the defaults (1h expiry, no refresh tokens, sign-up open).

The dashboard's **Auth** page edits these too: the Registration toggles map to `allow_signup` / `allow_anonymous`, and the Redirect URLs list maps to `redirect_urls`. When sign-up is off, the anonymous toggle is disabled, since anonymous sign-in is blocked along with it.

## Auth methods

instancez exposes the same auth API as Supabase, so any Supabase client library works. The examples below use `@supabase/supabase-js` — the same client the integration tests run against — but the Python, Swift, Flutter, and other clients work the same way.

**Email + password** — `supabase.auth.signUp()` / `supabase.auth.signInWithPassword()`

When `email.verify_email` is `false` (the default), `signUp` returns a session immediately. Set it to `true` and configure an email provider to require confirmation first.

**Magic link / Email OTP** — `supabase.auth.signInWithOtp()` / `supabase.auth.verifyOtp()`

Requires an email provider under `providers.email`. Without one, `signInWithOtp` returns a 200 with an empty response body.

**OAuth (Google, GitHub)** — `supabase.auth.signInWithOAuth({ provider: 'google' })`

There are two different URLs involved, and they are not interchangeable:

- **`auth.oauth.<name>.redirect_url`** (config, fixed) — the URL the *provider* redirects back to once the user approves consent. This must be instancez's own callback route, always shaped `<base URL>/auth/v1/callback/<name>` (e.g. `/auth/v1/callback/google`), and must exactly match what's registered in that provider's console (Google Cloud Console, GitHub OAuth Apps, …) — providers reject any other value. It always points at your **API server**, not your frontend.
- **`redirectTo`** (client-supplied, dynamic) — where the *app* should land once instancez finishes the exchange, passed as `options.redirectTo` to `signInWithOAuth()`. It must match an origin listed in `auth.redirect_urls`, and it points at your **frontend**.

```js
const { error } = await supabase.auth.signInWithOAuth({
  provider: 'google',
  options: { redirectTo: window.location.origin },
})
```

instancez has no `SITE_URL`-style default for `redirectTo` (unlike hosted Supabase, whose GoTrue falls back to the project's Site URL when the client omits it). If you don't pass `redirectTo`, or it doesn't match `auth.redirect_urls`, the callback returns the session as a raw JSON body instead of redirecting the browser back into the app — so pass it explicitly.

The full round trip:

```
browser  → GET /auth/v1/authorize?provider=google&redirect_to=<frontend URL>
instancez → 307 to Google, using auth.oauth.google.redirect_url as redirect_uri
Google   → user consents → redirects to auth.oauth.google.redirect_url (fixed)
instancez → exchanges the code, then redirects to the original redirect_to
            with the session in the URL fragment (#access_token=…)
```

By default this is the implicit flow (tokens in the URL fragment, which supabase-js parses automatically via `detectSessionInUrl`). PKCE is also supported: create the client with `createClient(url, key, { auth: { flowType: 'pkce' } })` and supabase-js adds `code_challenge`/`code_challenge_method` to `/authorize` for you, getting back an auth code on the redirect instead of tokens directly.

**Anonymous** — `supabase.auth.signInAnonymously()`

Issues a JWT with `is_anonymous: true` and the `anon` Postgres role. Set `allow_anonymous: false` to disable. Anonymous users can be promoted to a full account by calling `signUp` or linking an OAuth identity.

**Session management** — `getSession()`, `onAuthStateChange()`, `signOut()` all work as documented by supabase-js. `signOut` invalidates the refresh token server-side.

**TOTP MFA** — the full `auth.mfa` surface is implemented: `enroll`, `challenge`, `verify`, `unenroll`, `listFactors`. A successful `verify` re-issues the session JWT with `aal: aal2`.


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

See [RLS Policies](/instancez/build/rls/) for the full policy reference.

## Managing users in the dashboard

The dashboard's **Users** section (top-level nav item) provides a full admin UI for user management:

- **List users** — paginated table showing email, confirmed status, last sign-in, and ban status
- **Create user** — email + password, with optional automatic email confirmation
- **Edit user** — change email or password, ban/unban with one toggle
- **Delete user** — gated by typing the user's email to confirm

All operations go through the Supabase-compatible `/auth/v1/admin/users` endpoints using the admin key. The same endpoints work directly via `supabase-js` using the `admin` client surface (requires the service role key).

## What's next

- [RLS Policies](/instancez/build/rls/) — write access rules in SQL expressions
- [Tables / Schema](/instancez/build/schema/) — declare tables and fields in YAML
- [Storage](/instancez/build/storage/) — file uploads wired to the same JWT
