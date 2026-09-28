# Changelog

All notable changes to instancez are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/), and the project aims to follow [semantic versioning](https://semver.org/).

## [Unreleased]

<!-- Add entries here as you merge changes. Move them under a version heading when you cut a release. -->

### Added

- Per-table `rls_enabled` in `instancez.yaml`. `true` enables and forces RLS even with no policies (deny-all for `anon`/`authenticated`), and with `true` set, removing the last policy no longer turns RLS off. `false` disables RLS and is rejected when the table declares policies. Leaving it unset keeps the old behavior (on only if the table has policies, off the moment the last policy is removed), so existing projects migrate with no DDL change. `inz init`, the examples, and the dashboard's new-table flow now write it explicitly, defaulting to `true`.
- `inz validate` and `inz dev` print non-blocking warnings: a table without `rls_enabled`, a table with RLS disabled, and `server.max_limit: 100` (the old default).

### Changed

- Requests running as `anon` or `authenticated` can no longer read or write `auth.*` tables, which matches Supabase. RLS policies or `security: invoker` RPCs that query `auth.users` directly now get `permission denied`; move that data into your own table or use a `security: definer` RPC. `auth.uid()`, `auth.role()`, `auth.email()`, `auth.jwt()` and foreign keys to `auth.users.id` are unaffected.
- Startup now re-applies database privilege fixes on every boot, including `inz serve` without `--migrate`, and boot fails if the privilege fix fails.
- **Breaking:** session JWTs now carry Supabase's top-level `aal` and `amr` (`[{method, timestamp}]`) claims, with a `session_id` that stays the same across refreshes. `aal` is no longer written into `app_metadata`; RLS policies reading `auth.jwt()->'app_metadata'->>'aal'` must switch to `auth.jwt()->>'aal'`.
- `GET /auth/v1/user` and session responses now include `user.factors`, so supabase-js `mfa.listFactors()` and `mfa.getAuthenticatorAssuranceLevel()` work.
- **Breaking:** REST reads no longer stop at 20 rows when no `limit` is given, matching PostgREST. `server.max_limit` (new default 1000, `-1` to disable) now caps table reads, setof RPCs and top-level has-many embeds, the way PostgREST's `db-max-rows` does. Clients that relied on the 20-row default now get up to 1000 rows; see Upgrading if your YAML has `max_limit: 100`.
- Storage: a legacy `/api/storage` `GET` on a public bucket with a present but invalid `Authorization: Bearer` token now returns 401 instead of falling back to anonymous access.
- Storage: `createSignedUrls` with an empty path list or more than 1000 paths now returns 400.
- Storage: `/storage/v1/object/info/authenticated/<bucket>` with no path now returns 400 (was 404).
- **Breaking:** a filter on a non-`!inner` embed (`users.username=eq.x`) no longer drops parent rows. Like PostgREST, it only filters the embed: an unmatched to-one embed is `null`, a to-many embed is `[]`, and `count` follows. Use `!inner` to filter the parents.
- **Breaking:** the JSON query plan (`Accept: application/vnd.pgrst.plan+json`) is now returned bare, as in PostgREST: `[{"Plan": ...}]`, not `[{"QUERY PLAN": [{"Plan": ...}]}]`. See Upgrading.
- **Breaking:** `POST /storage/v1/object/sign/...` now returns a relative `signedURL` (`/object/sign/<bucket>/<path>?token=...`), as Supabase does, instead of an absolute S3 or `file://` URL. It is redeemed at `GET /storage/v1/object/sign/...` with no apikey: on S3 that 302s to a presigned URL valid for at most 60 seconds, and on the local provider instancez streams the object. Rotating the JWT signing key invalidates every outstanding signed download URL. A bucket named `sign` can no longer be read with `.download()`. See Upgrading.

### Fixed

- supabase-js `.explain()` now returns the query plan for the secret key. The `for` and `options` Accept parameters are honored, media types match case-insensitively, and plan transactions are always rolled back. `options=wal` without `analyze` returns 400 `22023` instead of 500.
- `.explain()` on insert, upsert, update, delete and rpc now returns 406 `PGRST107` before auth or any SQL runs, even when the Accept list also offers another type. Before, a secret-key `.delete().explain()` ran the delete and returned rows.
- supabase-js `linkIdentity()` works: `/user/identities/authorize` now accepts GET, which supabase-js sends, as well as POST.
- supabase-js `error.code` now returns the auth error code (`user_banned`, `insufficient_aal`, …). `/auth/v1` error bodies carry GoTrue's `error_code` field next to `code`; REST errors are unchanged.
- `verifyOtp({ type: 'magiclink' })` now marks the email confirmed, as GoTrue does, so magic-link users can later link a Google login by email.
- `count=exact` / `planned` / `estimated` now count what the query actually returns. They include `!inner` embeds and `!inner` embed filters, work on non-public schemas, run in the same transaction as the rows, and return an error instead of silently dropping the count.
- Upgrading instancez with an unchanged `instancez.yaml` now adds new `auth.*` columns and indexes at boot. Before, they only reached a database when the config changed. This also adds the missing `attempts` column to `auth.one_time_tokens` on databases that enabled email auth after first boot.
- A code function stuck in a CPU-bound loop no longer permanently takes out a worker. After a timeout the worker is health-checked and replaced if unresponsive. Function responses over 6 MB now return 502 instead of being buffered without limit.
- Reloading functions (dev hot reload, `serve --watch` bundle change) no longer kills calls in flight. The old runtime drains for up to 30s before its workers stop.
- Storage: batch signed URLs, copy, move, remove, `emptyBucket` and the legacy `/api/storage` routes now respect `storage.objects` RLS. Previously some of them signed, copied or deleted objects the caller couldn't read or delete.
- Storage: object keys with `..` segments are rejected, and the local provider can no longer write outside its directory.
- An aggregate next to an embed (`select=count(),users(username)`, `select=amount.sum(),...customers(name)`) returned 500. The embed is now a group key, as in PostgREST, for belongs-to, has-many, and spread embeds.
- An aggregate next to an embed on an RPC result (`rpc/fn?select=count(),messages(id)`) now returns 400 instead of 500.
- Aggregates with plain columns on setof RPC results (`rpc/fn?select=status,count()`) now group by those columns instead of returning 500. As in PostgREST, `count` on an aggregate query (table or RPC) counts the rows matching the filters and ignores `GROUP BY` and `having`; table aggregates used to count the groups.
- `select=*` with an aggregate (`select=*,count()`) returns 400 on tables and RPCs. Before, it returned 500 on tables and dropped the aggregate on RPCs.
- A Postgres grouping error (`42803`) now returns 400 instead of 500.
- An empty page now sends `Content-Range: */*` (or `*/N` with a count) on tables and RPCs, as PostgREST does, instead of `0-0/*` or `0-0/N`. An `offset` past the end sends `*/N` instead of `10-10/N`.
- A filter on a spread embed (`select=id,...users(username)&users.status=eq.x`) keeps every parent row; unmatched spread columns are `null`.
- Storage: downloading an object whose row exists but whose file is gone from the local provider (including a path under a file) now returns 404 `not_found` instead of 500.
- Storage: downloads send `nosniff`, and HTML/SVG/XML/JS are served as attachments.
- Storage: image transforms are capped at 2500px, 25MB and 50MP. Signed URL expiry is capped at 7 days. Uploads no longer hold a database connection while the body streams. Multipart and signed uploads record real sizes, and signed uploads enforce the bucket's MIME allowlist. S3 copies of keys with special characters work.
- Storage: `createSignedUploadUrl`'s response `url` now includes `?token=`, so `@supabase/supabase-js`'s `uploadToSignedUrl()` and `createSignedUploadUrl()` work end-to-end (storage-js reads the token from the URL).
- Storage: `bucket.info()` now works, served at `/storage/v1/object/info/<bucket>/<path>`.
- Storage: an upload commit failure no longer deletes the object's bytes; on update/upsert the row already pointed at the key that was just overwritten, so the old delete destroyed live data whether or not the commit actually failed.
- A column-list to-one embed (`author(name)`) with no matching row is now `null` instead of `{"name": null}`.
- Storage: a restrictive RLS policy on one bucket no longer denies every other bucket. `storage.objects` is shared across buckets, so a restrictive policy scoped with `bucket_id = X AND (...)` ANDs against every other bucket's rows too; it's now scoped with `bucket_id <> X OR (...)` so the restriction applies only to its own bucket. Existing databases get the fixed policy on the next boot, without a config change.
- Storage: presigned download URLs now set S3 response overrides for `Content-Type`, `Content-Disposition` (attachment for active content) and `Cache-Control`.
- supabase-js `createSignedUrl` and `createSignedUrls` now return URLs that work. Before, storage-js glued the absolute S3 URL onto the API URL, which broke them. The `download` option (`true` or a filename) now sets `Content-Disposition`, safely encoded.
- Storage: `?download=` on public and authenticated downloads now sets `Content-Disposition`, so `getPublicUrl(path, { download })` and `download=` links work.
- A setof RPC with `Prefer: count=exact` runs the function once instead of twice, so VOLATILE functions no longer apply their side effects twice. `count=planned` and `count=estimated` on RPCs now return an estimate instead of `*`.

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
- **Breaking:** `signUp` under `email.verify_email: true` no longer hands out a session for the unconfirmed account; it now returns the user with no session, matching Supabase. `updateUser({ password })` now signs out the account's other sessions, keeping only the caller's.
- Refresh-token reuse is now detected: replaying an already-rotated token revokes its whole session family. A 10-second grace window returns the session's live token instead, so two tabs refreshing at the same moment don't knock each other out.
- `banned_until` is now enforced on every sign-in and refresh path (403 `user_banned`). GoTrue returns 400 on the password grant specifically; instancez returns 403 everywhere, a deliberate deviation for one uniform status. The dashboard's "disable user" action now bans the user instead of only revoking their tokens.
- JWT verification now pins `RS256`/`HS256` (`jwt.WithValidMethods`), and `/auth/v1/token/verify` shares the same verifier as the request middleware instead of its own unpinned parse. `oauth_state`/`oauth_redirect_to` cookies are now `Secure` and `SameSite=Lax` over HTTPS, with a `__Host-` prefix, matching `oauth_link_state`.
- Query plans (`Accept: application/vnd.pgrst.plan+json|text`) are only returned to the secret key. Anon and user tokens could read execution plans before, which exposed table sizes and index layout.
- `server.timeouts.db_query` (default 10s) is now a Postgres `statement_timeout` on every API transaction, not just list reads. It holds for anon, authenticated and service_role, so one slow RPC or filter can no longer pin a pool connection.
- `server.timeouts.request` (default 25s) now bounds how long `/rest/v1` and `/auth/v1` requests may take to read or write, which stops slow-body and slow-reader clients from holding connections open. Storage and functions are exempt.
- `/metrics` labels now use route templates (`/rest/v1/rpc/:name`), with `unmatched` for unknown paths and `OTHER` for non-standard methods. Before, every distinct URL added a series that was never freed. The mislabelled `quantile="0.5"` (it was a mean) is gone, and `_count`/`_sum` are now true cumulative totals.
- A table with `rls_enabled: true` and no policies is deny-all for `anon`/`authenticated` (reads return zero rows, writes fail), rather than having RLS left off. Removing a table's last policy while `rls_enabled: true` stays set no longer opens the table back up. The dashboard's new-table default is `rls_enabled: true`, so new tables start deny-all until you add policies.
- Storage uploads now check the caller's RLS policy before spooling the request body to disk, so an RLS-denied or anonymous caller can no longer force up to `max_size` bytes of disk I/O per request.
- Storage downloads through an authenticated route (not the `public/` route) on a public bucket now get `Cache-Control: private`, not `public`, since RLS on that route can still be per-caller.
- Storage downloads with a `multipart/*` content type (e.g. `multipart/x-mixed-replace`) now download as an attachment instead of rendering inline.

### Upgrading

- Keep the mixed-version window short. The first upgraded instance revokes anon's access to `auth.*`. From then on, older instances run their refresh-token, signup-confirmation, and one-time-token (resend) writes as `anon` and lose access, so sign-in, refresh, and signup confirmation/resend routed to them fail until the rollout finishes.
- Rolling back to an older release needs a manual step: re-grant the old privileges yourself. An older binary with an unchanged config never re-runs the grants.
- Check `server.max_limit` in your `instancez.yaml`. Dashboard saves from older releases wrote the old default, `max_limit: 100`, which was never enforced. It is now a hard cap, so every read, `.range()` included, silently stops at 100 rows (only `Content-Range` shows it). Remove the line to get the 1000 default, or set the cap you want. `inz validate` and `inz dev` warn about this value.
- `aal` moved out of `app_metadata` into a top-level JWT claim. Before rolling out, update any RLS policy or client code that reads `auth.jwt()->'app_metadata'->>'aal'` or `user.app_metadata.aal` to `auth.jwt()->>'aal'` and the top-level `aal` claim instead.
- Sessions that were aal2 before the upgrade come back as `aal1` on their first refresh, because existing refresh tokens get the new `aal` column with default `aal1`. MFA-gated RLS denies them until the user verifies a factor again.
- With `email.verify_email: true`, `signUp` now returns the user with `session: null`. Frontends that signed users straight in must show a "check your email" step and sign in after confirmation.
- Over HTTPS, an OAuth login that starts on one version and finishes on the other fails (during a rolling deploy, or across the upgrade). The state cookie is now `__Host-oauth_state`, so the callback can't find the cookie it expects. Users just retry the login.
- Code that reads JSON plans from raw HTTP must drop the `QUERY PLAN` wrapper: read `body[0].Plan` instead of `body[0]["QUERY PLAN"][0].Plan`. supabase-js `.explain({ format: 'json' })` needed no wrapper and now just works.
- Raw HTTP clients of `POST /storage/v1/object/sign/...` must prefix the returned `signedURL` with `<api url>/storage/v1`, the same as for Supabase. supabase-js does this already. Signed download URLs minted before the upgrade are absolute presigned S3 URLs and keep working until they expire. Rotating the JWT signing key now invalidates signed download URLs, as it already did for signed upload tokens. Anyone holding one gets 400 `invalid_token` and must request a new URL.
- During a rolling deploy, a signed download URL minted on an upgraded instance fails if an older instance redeems it (the old router reads `sign` as a bucket name and asks for an apikey). Keep the window short. Rename a bucket called `sign` before upgrading if you use `.download()` on it.

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
