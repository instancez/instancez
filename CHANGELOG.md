# Changelog

All notable changes to instancez are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/), and the project aims to follow [semantic versioning](https://semver.org/).

## [Unreleased]

<!-- Add entries here as you merge changes. Move them under a version heading when you cut a release. -->

### Added

### Changed

- Requests running as `anon` or `authenticated` can no longer read or write `auth.*` tables, which matches Supabase. RLS policies or `security: invoker` RPCs that query `auth.users` directly now get `permission denied`; move that data into your own table or use a `security: definer` RPC. `auth.uid()`, `auth.role()`, `auth.email()`, `auth.jwt()` and foreign keys to `auth.users.id` are unaffected.
- Startup now re-applies database privilege fixes on every boot, including `inz serve` without `--migrate`, and boot fails if the privilege fix fails.
- Storage: a legacy `/api/storage` `GET` on a public bucket with a present but invalid `Authorization: Bearer` token now returns 401 instead of falling back to anonymous access.
- Storage: `createSignedUrls` with an empty path list or more than 1000 paths now returns 400.
- Storage: `/storage/v1/object/info/authenticated/<bucket>` with no path now returns 400 (was 404).

### Fixed

- Upgrading instancez with an unchanged `instancez.yaml` now adds new `auth.*` columns and indexes at boot. Before, they only reached a database when the config changed. This also adds the missing `attempts` column to `auth.one_time_tokens` on databases that enabled email auth after first boot.
- Storage: batch signed URLs, copy, move, remove, `emptyBucket` and the legacy `/api/storage` routes now respect `storage.objects` RLS. Previously some of them signed, copied or deleted objects the caller couldn't read or delete.
- Storage: object keys with `..` segments are rejected, and the local provider can no longer write outside its directory.
- Storage: downloads send `nosniff`, private buckets are no longer publicly cacheable, and HTML/SVG/XML/JS are served as attachments.
- Storage: image transforms are capped at 2500px, 25MB and 50MP. Signed URL expiry is capped at 7 days. Uploads no longer hold a database connection while the body streams. Multipart and signed uploads record real sizes, and signed uploads enforce the bucket's MIME allowlist. S3 copies of keys with special characters work.
- Storage: `createSignedUploadUrl`'s response `url` now includes `?token=`, so `@supabase/supabase-js`'s `uploadToSignedUrl()` and `createSignedUploadUrl()` work end-to-end (storage-js reads the token from the URL).
- Storage: `bucket.info()` now works, served at `/storage/v1/object/info/<bucket>/<path>`.
- Storage: an upload no longer deletes the just-written object bytes when the commit fails, since a failed commit is ambiguous and may have applied — deleting could destroy live bytes under a committed row, especially on an update.

### Security

- RPC and REST select/filter/order identifiers and embed columns/aliases are now strictly validated, so unsafe or undeclared identifiers return 400, and enum/pattern CHECK literals are escaped.
- Every auth service query (signup, sign-in, tokens, password/email flows, MFA, admin) now runs as `service_role` regardless of the caller's session, and fails closed if the role can't be set instead of running unpinned.
- `anon` and `authenticated` could read **and modify** every `auth.*` table. That covered password hashes, refresh tokens, TOTP secrets and the JWT signing private keys in `auth.jwt_keys`. The worst case: a client holding only the publishable key could insert its own signing key into `auth.jwt_keys` and mint accepted tokens for any user, or overwrite `password_hash`. They could also read `_instancez_migrations`, which stores the resolved config including OAuth client secrets, S3 keys, the Resend API key and function env values. `auth.jwt_keys` is now revoked from all three API roles (`anon`, `authenticated`, `service_role`); `_instancez_migrations` is revoked from those three plus the seed role used by `run_sql`, if one is configured. Existing databases are fixed on the next boot, without a config change, provided the migration (owner) login owns the `auth` tables — Postgres only warns, and does not error, when a non-owner role runs `REVOKE`, so a misconfigured owner DSN silently leaves the old grants in place.
- A retired JWT signing key stops verifying once `jwt_expiry` has passed since its retirement. Before this change it verified forever.
- A transient database error while loading the signing key no longer mints a replacement key, which used to sign every user out.
- Storage uploads now check the caller's RLS policy before spooling the request body to disk, so an RLS-denied or anonymous caller can no longer force up to `max_size` bytes of disk I/O per request.
- Storage downloads through an authenticated route (not the `public/` route) on a public bucket now get `Cache-Control: private`, not `public`, since RLS on that route can still be per-caller.
- Storage downloads with a `multipart/*` content type (e.g. `multipart/x-mixed-replace`) now download as an attachment instead of rendering inline.

### Upgrading

- Keep the mixed-version window short. The first upgraded instance revokes anon's access to `auth.*`. From then on, older instances run their refresh-token, signup-confirmation, and one-time-token (resend) writes as `anon` and lose access, so sign-in, refresh, and signup confirmation/resend routed to them fail until the rollout finishes.
- Rolling back to an older release needs a manual step: re-grant the old privileges yourself. An older binary with an unchanged config never re-runs the grants.

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
