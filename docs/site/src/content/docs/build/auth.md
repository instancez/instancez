---
title: Auth
description: Password, magic link, OTP, OAuth, anonymous sign-in, and TOTP MFA — all wired to Postgres RLS.
---

## Configuration

The `auth:` block in `instancez.yaml` controls JWT lifetime, sign-up permissions, and OAuth providers:

```yaml
auth:
  jwt_expiry: 1h
  refresh_token_expiry: 7d

  # Set to false to disable public sign-up (the secret key can still create users)
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
      client_secret: ${INSTANCEZ_ENV_GOOGLE_CLIENT_SECRET}
      redirect_url: https://api.myapp.example.com/auth/v1/callback/google

    github:
      client_id: YOUR_GITHUB_CLIENT_ID
      client_secret: ${INSTANCEZ_ENV_GITHUB_CLIENT_SECRET}
      redirect_url: https://api.myapp.example.com/auth/v1/callback/github
```

All keys are optional. Auth is always provisioned, even if `auth:` is omitted entirely — JWT auth works with the defaults (15m expiry, 7d refresh token expiry, sign-up open). Refresh tokens are always issued; the old `auth.refresh_tokens` toggle is deprecated and ignored. After a signing-key rotation, tokens signed by the old key keep verifying until `jwt_expiry` (plus 30 seconds of clock skew) has passed, and are rejected after that. On a multi-instance deployment, a key retired on one instance can keep verifying on another for up to one extra key-cache reload interval (about 30 seconds) past that.

The dashboard's **Auth** page edits these too: the Registration toggles map to `allow_signup` / `allow_anonymous`, and the Redirect URLs list maps to `redirect_urls`. When sign-up is off, the anonymous toggle is disabled, since anonymous sign-in is blocked along with it.

## Auth methods

instancez exposes the same auth API as Supabase, so any Supabase client library works. The examples below use `@supabase/supabase-js` — the same client the integration tests run against — but the Python, Swift, Flutter, and other clients work the same way.

**Email + password** — `supabase.auth.signUp()` / `supabase.auth.signInWithPassword()`

When `email.verify_email` is `false` (the default), `signUp` returns a session immediately. Set it to `true` and configure an email provider: `signUp` then returns the user with no session, same as Supabase, until the address is confirmed.

`updateUser({ password })` signs out the account's other sessions, keeping only the one that made the call.

**Magic link / Email OTP** — `supabase.auth.signInWithOtp()` / `supabase.auth.verifyOtp()`

Requires an `auth.email` block in the config — without it, the OTP endpoint isn't mounted at all and the call 404s. With the block present but no email provider configured to actually send it, `signInWithOtp` returns a 200 with an empty response body. Verifying a magic-link or signup code marks the email confirmed. A 6-digit code allows 5 wrong guesses. After that the code and its link stop working, and the code still counts toward the cooldown below. Each address gets at most one email per purpose (magic link, signup, recovery) every 60 seconds, and `admin.generateLink` starts that same cooldown. Inside that window, `signInWithOtp` and `resetPasswordForEmail` return an empty 200 and send nothing, so neither reveals whether the account exists; `resend` returns 429 `over_email_send_rate_limit` instead, which does reveal it, matching GoTrue. With `allow_signup: false`, `signInWithOtp` only signs in existing users. `resend({ type: 'signup' })` sends nothing once the address is confirmed, and `resend({ type: 'email_change' })` never sends, because email changes apply immediately.

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

When `redirectTo` is omitted (or fails the `auth.redirect_urls` check), instancez lands the browser on the first entry of `auth.redirect_urls` — its stand-in for Supabase's project Site URL. Only when `auth.redirect_urls` is empty too is there nowhere to send the browser, and the callback returns the session as a raw JSON body instead. Passing `redirectTo` explicitly is still the clearest path.

If the exchange fails (bad client secret, provider outage), the callback redirects to that same target with GoTrue-style error params in the fragment — `#error=server_error&error_code=unexpected_failure&error_description=…` — which supabase-js surfaces through `detectSessionInUrl`. The underlying cause stays in the server logs rather than the browser.

The full round trip:

```
browser  → GET /auth/v1/authorize?provider=google&redirect_to=<frontend URL>
instancez → 307 to Google, using auth.oauth.google.redirect_url as redirect_uri
Google   → user consents → redirects to auth.oauth.google.redirect_url (fixed)
instancez → exchanges the code, then redirects to the original redirect_to
            with the session in the URL fragment (#access_token=…)
```

By default this is the implicit flow (tokens in the URL fragment, which supabase-js parses automatically via `detectSessionInUrl`). PKCE is also supported: create the client with `createClient(url, key, { auth: { flowType: 'pkce' } })` and supabase-js adds `code_challenge`/`code_challenge_method` to `/authorize` for you, getting back an auth code on the redirect instead of tokens directly.

How an OAuth login finds its account:

1. A returning login matches on the provider's user ID, even if the email at the provider changed.
2. A first login links to an existing account with the same email (case-insensitive), but only when the provider says the email is verified. It links to a verified account, or to an unverified one nobody can sign in to yet (for example an invited user). An unverified account that has a password, a session, another identity, or is anonymous is never linked; the login fails with `email_exists` so a squatter can't capture the real owner's OAuth login. Confirm the email on that account, or sign in to it and link the provider from there.
3. Otherwise a new user is created, unless `allow_signup` is `false`, which returns `signup_disabled`.

An unverified provider email that matches no existing identity fails with `provider_email_needs_verification`. GitHub logins use the verified address from GitHub's email list, not the public profile email.

**Linking an identity** — `supabase.auth.linkIdentity({ provider: 'google' })`

The signed-in user's browser must finish the link. `/auth/v1/user/identities/authorize` sets an HttpOnly `oauth_link_state` cookie (`__Host-oauth_link_state` over HTTPS, so another subdomain can't plant one), and the provider callback links the identity only when that cookie matches the link's state, so a link URL sent to someone else can't attach their account to yours. A missing or wrong cookie fails with `bad_oauth_state` (an error redirect when there's a redirect target, otherwise 400). Browsers keep that cookie only when the frontend calls the API on the same origin (the default when instancez hosts the frontend, with the API on the same origin). A frontend on a different origin can't complete `linkIdentity`.

**Anonymous** — `supabase.auth.signInAnonymously()`

Issues a JWT with `is_anonymous: true` and the `anon` Postgres role. Set `allow_anonymous: false` to disable. Anonymous users can be promoted to a full account by calling `signUp` or linking an OAuth identity.

**Session management** — `getSession()`, `onAuthStateChange()` and `signOut()` all work as documented by supabase-js. `signOut` invalidates the refresh token server-side. Refresh tokens rotate on every use. Re-using an old one within 10 seconds (two tabs refreshing at once) is allowed; after that it revokes the whole session. A banned user (`admin.updateUserById(id, { ban_duration })` or the dashboard's disable/ban) can't sign in or refresh (`403 user_banned`). Access tokens already issued stay valid until they expire (`jwt_expiry`).

**TOTP MFA** — the full `auth.mfa` surface is implemented: `enroll`, `challenge`, `verify`, `unenroll`, `listFactors`, `challengeAndVerify` and `getAuthenticatorAssuranceLevel`. Session JWTs carry Supabase's top-level `aal` (`aal1`/`aal2`) and `amr` (`[{ method, timestamp }]`) claims plus `session_id`; a successful `verify` re-issues the same session at `aal2`, and refreshes keep it there. Use `auth.jwt()->>'aal'` in RLS policies to require MFA. `verify` requires a `challengeId`; each challenge allows 5 attempts and a TOTP code can only be used once. Creating challenges for one factor is capped at 10 per 5 minutes (429 `over_request_rate_limit`). Once a factor is verified, enrolling another factor or unenrolling a verified one needs an `aal2` session (`insufficient_aal`), and verifying the first factor signs out the user's other sessions. Sign-in with a password still returns an `aal1` session (same as Supabase); your app decides when to step up.


## Using auth in RLS

Every request carries the user's JWT. The middleware switches the Postgres role and writes the user ID into a session GUC before running any query, so RLS policies can call `auth.uid()` and `auth.is_authenticated()` directly:

```yaml
tables:
  posts:
    rls_enabled: true
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
        using: "true"
      - operations: [insert]
        with_check: "auth.uid() = user_id"
      - operations: [update]
        using: "auth.uid() = user_id"
        with_check: "auth.uid() = user_id"
      - operations: [delete]
        using: "auth.uid() = user_id"
```

To restrict a table to signed-in users only:

```yaml
rls:
  - operations: [select]
    using: "auth.is_authenticated()"
```

See [RLS Policies](/instancez/build/rls/) for the full policy reference.

## Managing users in the dashboard

The dashboard's **Users** section (top-level nav item) provides a full admin UI for user management:

- **List users** — paginated table showing email, confirmed status, last sign-in, and ban status
- **Create user** — email + password, with optional automatic email confirmation
- **Edit user** — change email or password, ban/unban with one toggle
- **Delete user** — gated by typing the user's email to confirm

All operations go through the Supabase-compatible `/auth/v1/admin/users` endpoints using the secret key. The same endpoints work directly via `supabase-js` using the `admin` client surface (requires the secret key).

## What's next

- [RLS Policies](/instancez/build/rls/) — write access rules in SQL expressions
- [Tables / Schema](/instancez/build/schema/) — declare tables and fields in YAML
- [Storage](/instancez/build/storage/) — file uploads wired to the same JWT
