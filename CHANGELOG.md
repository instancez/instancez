# Changelog

All notable changes to instancez are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/), and the project aims to follow [semantic versioning](https://semver.org/).

## [Unreleased]

<!-- Add entries here as you merge changes. Move them under a version heading when you cut a release. -->

### Added

### Changed

- Requests running as `anon` or `authenticated` can no longer read or write `auth.*` tables, which matches Supabase. RLS policies or `security: invoker` RPCs that query `auth.users` directly now get `permission denied`; move that data into your own table or use a `security: definer` RPC. `auth.uid()`, `auth.role()`, `auth.email()`, `auth.jwt()` and foreign keys to `auth.users.id` are unaffected.
- Startup now re-applies database privilege fixes on every boot, including `inz serve` without `--migrate`, and boot fails if the privilege fix fails.
- **Breaking:** session JWTs now carry Supabase's top-level `aal` and `amr` (`[{method, timestamp}]`) claims, with a `session_id` that stays the same across refreshes. `aal` is no longer written into `app_metadata`; RLS policies reading `auth.jwt()->'app_metadata'->>'aal'` must switch to `auth.jwt()->>'aal'`.
- `GET /auth/v1/user` and session responses now include `user.factors`, so supabase-js `mfa.listFactors()` and `mfa.getAuthenticatorAssuranceLevel()` work.

### Fixed

- supabase-js `linkIdentity()` works: `/user/identities/authorize` now accepts GET, which supabase-js sends, as well as POST.
- `verifyOtp({ type: 'magiclink' })` now marks the email confirmed, as GoTrue does, so magic-link users can later link a Google login by email.
- Upgrading instancez with an unchanged `instancez.yaml` now adds new `auth.*` columns and indexes at boot. Before, they only reached a database when the config changed. This also adds the missing `attempts` column to `auth.one_time_tokens` on databases that enabled email auth after first boot.

### Security

- RPC and REST select/filter/order identifiers and embed columns/aliases are now strictly validated, so unsafe or undeclared identifiers return 400, and enum/pattern CHECK literals are escaped.
- Every auth service query (signup, sign-in, tokens, password/email flows, MFA, admin) now runs as `service_role` regardless of the caller's session, and fails closed if the role can't be set instead of running unpinned.
- `anon` and `authenticated` could read **and modify** every `auth.*` table. That covered password hashes, refresh tokens, TOTP secrets and the JWT signing private keys in `auth.jwt_keys`. The worst case: a client holding only the publishable key could insert its own signing key into `auth.jwt_keys` and mint accepted tokens for any user, or overwrite `password_hash`. They could also read `_instancez_migrations`, which stores the resolved config including OAuth client secrets, S3 keys, the Resend API key and function env values. `auth.jwt_keys` is now revoked from all three API roles (`anon`, `authenticated`, `service_role`); `_instancez_migrations` is revoked from those three plus the seed role used by `run_sql`, if one is configured. Existing databases are fixed on the next boot, without a config change, provided the migration (owner) login owns the `auth` tables — Postgres only warns, and does not error, when a non-owner role runs `REVOKE`, so a misconfigured owner DSN silently leaves the old grants in place.
- A retired JWT signing key stops verifying once `jwt_expiry` has passed since its retirement. Before this change it verified forever.
- A transient database error while loading the signing key no longer mints a replacement key, which used to sign every user out.
- OAuth and Google ID-token logins no longer take over accounts by email. A returning login matches on the provider user ID first. A new login links by email only when the provider marks the email verified (Google `verified_email`/`email_verified`, GitHub's verified email list instead of the public profile email), and never to an unverified account that has a password, a session or another identity. That case now returns 422 `email_exists`; an unverified provider email returns 422 `provider_email_needs_verification`. OAuth sign-up now honors `allow_signup: false` (403 `signup_disabled`). The admin-update duplicate-email 422 now carries code `email_exists` instead of `PGRST000`.
- MFA `verify` now requires a `challenge_id` and spends the challenge's attempt atomically (closing a check-then-act race on the 5-attempt cap), and rejects a TOTP code whose 30-second step was already used on that factor, which GoTrue does not. Once a factor is verified, enrolling another factor or unenrolling that factor now needs an `aal2` session (403/422 `insufficient_aal`), closing an aal1-to-aal2 self-elevation path. Verifying a factor for the first time signs out the user's other aal1 sessions; a step-up verify of an already-verified factor only revokes the current session's aal1 row.
- MFA challenge creation is now capped at 10 per factor per 5-minute window (429 `over_request_rate_limit`), closing a TOTP brute-force path where an aal1 session could cycle unlimited challenges instead of reusing one under `maxMFAAttempts`. Unenrolling a factor is now also conditional in SQL on its verified status, closing a race where an aal1 session could delete a factor the instant it was promoted to verified.
- `linkIdentity` is now bound to the browser that started it. Before, anyone could send a signed-in user's link URL to a victim; finishing it attached the victim's provider identity to the sender's account, and since logins now match on provider identity first, the victim's next OAuth login landed in the sender's account. `/user/identities/authorize` now sets an HttpOnly, SameSite=Lax `oauth_link_state` cookie (over HTTPS it's Secure and named `__Host-oauth_link_state`, so a sibling subdomain can't overwrite it), and the callback refuses the link without a matching cookie (400 `bad_oauth_state` when there's no redirect target). The link state is now single use under concurrency. The cookie only reaches the browser when the frontend calls the API on the same origin, so a cross-origin frontend can't complete `linkIdentity` (a documented limitation, not a bug). An anonymous account is now never linked to an OAuth login by email.
- Email OTP hardening. Concurrent wrong guesses can no longer exceed the 5-attempt cap on a 6-digit code, and a code or link now works exactly once under concurrency (before, parallel requests could each consume it). `/otp`, `/resend` and `/recover` now send at most one email per user and purpose every 60 seconds; `admin.generateLink` starts that same cooldown. `/resend` answers a repeat with 429 `over_email_send_rate_limit`, matching GoTrue; this is the one exception to the enumeration protection below, since a 429 confirms the account exists. `/otp` and `/recover` answer with the same empty 200 as an unknown address instead, so those two don't reveal anything. `/otp` no longer creates users when `allow_signup` is `false`. A code burned by wrong guesses stays blocked, link included, and still counts toward the cooldown. `/verify` with `type: magiclink` now accepts only signup and magic-link tokens, so a recovery or email-change token can't be used to sign in. `/resend` sends nothing for `signup` when the address is already confirmed, or for `email_change`, since email changes apply immediately and none is ever pending. Both cases return an empty 200.
- `signUp` under `email.verify_email: true` no longer hands out a session for the unconfirmed account; it now returns the user with no session, matching Supabase. `updateUser({ password })` now signs out the account's other sessions, keeping only the caller's.
- Refresh-token reuse is now detected: replaying an already-rotated token revokes its whole session family. A 10-second grace window returns the session's live token instead, so two tabs refreshing at the same moment don't knock each other out.
- `banned_until` is now enforced on every sign-in and refresh path (403 `user_banned`). GoTrue returns 400 on the password grant specifically; instancez returns 403 everywhere, a deliberate deviation for one uniform status. The dashboard's "disable user" action now bans the user instead of only revoking their tokens.
- JWT verification now pins `RS256`/`HS256` (`jwt.WithValidMethods`), and `/auth/v1/token/verify` shares the same verifier as the request middleware instead of its own unpinned parse. `oauth_state`/`oauth_redirect_to` cookies are now `Secure` and `SameSite=Lax` over HTTPS, with a `__Host-` prefix, matching `oauth_link_state`.

### Upgrading

- Keep the mixed-version window short. The first upgraded instance revokes anon's access to `auth.*`. From then on, older instances run their refresh-token, signup-confirmation, and one-time-token (resend) writes as `anon` and lose access, so sign-in, refresh, and signup confirmation/resend routed to them fail until the rollout finishes.
- Rolling back to an older release needs a manual step: re-grant the old privileges yourself. An older binary with an unchanged config never re-runs the grants.
- `aal` moved out of `app_metadata` into a top-level JWT claim. Before rolling out, update any RLS policy or client code that reads `auth.jwt()->'app_metadata'->>'aal'` or `user.app_metadata.aal` to `auth.jwt()->>'aal'` and the top-level `aal` claim instead.

## [0.0.3]

### Added

- Traces and logs now export over OTLP, driven by the standard `OTEL_*` environment variables. Export stays off unless `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_TRACES_EXPORTER`, or `OTEL_LOGS_EXPORTER` is set, so deployments that set none of them are unaffected. Spans cover HTTP requests, Postgres queries, code function calls, Resend, and S3, and request logs carry `trace_id` and `span_id`. Stdout logging keeps working either way. Code inside the Node worker and metrics aren't covered yet. See the Observability page in the docs.

### Security

- Bumped `go.opentelemetry.io/otel` to 1.43.0, the OpenTelemetry log modules to 0.19.0, and `golang.org/x/crypto` to 0.52.0, clearing ten advisories: CVE-2026-39882 and CVE-2026-39883 in OpenTelemetry, and eight ssh ones in `x/crypto`.

## [0.0.2]

### Changed

- Migrations now block renames that would silently drop data. A rename that isn't declared in `instancez.yaml` is treated as a drop-and-recreate and gated behind an explicit destructive-change confirmation instead of quietly discarding the column or table.

### Fixed

- REST writes now return 422 instead of 500 when a nested object is sent for a scalar column (e.g. `{"col":{"not":false}}`), with the PostgREST error envelope.

## [0.0.1]

First tagged release.

### Added

- Auth: password, magic link, email OTP, anonymous sign-in, OAuth (Google, GitHub), and TOTP MFA, wire-compatible with `@supabase/supabase-js`
- PostgREST-style REST API (`/rest/v1`): filters, embeds, upsert, CSV export
- SQL functions (RPC) at `/rest/v1/rpc/:name`
- JavaScript code functions (Node.js workers) at `/functions/v1/:name`
- Storage: local or S3-backed buckets, RLS on objects, signed URLs, image transforms
- Row-level security as the authorization layer, enforced through a two-login Postgres role model
- YAML-driven schema: `instancez.yaml` diffed against the live database and migrated on boot
- Dashboard (`@instancez/console`): manage tables, auth, storage, functions, RPC, and providers
- CLI: `init`, `dev`, `serve`, `validate`, `bundle`, `doctor`, `status`, `login`, `logout`, `whoami`, `deploy`, `cloud`
- Deployment targets: Docker, Docker Compose, Kubernetes (Helm chart), AWS Lambda
- A `@supabase/supabase-js` wire-compatibility test suite that runs on every commit

[Unreleased]: https://github.com/instancez/instancez/compare/v0.0.3...HEAD
[0.0.3]: https://github.com/instancez/instancez/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/instancez/instancez/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/instancez/instancez/releases/tag/v0.0.1
