# Changelog

All notable changes to instancez are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/), and the project aims to follow [semantic versioning](https://semver.org/).

## [Unreleased]

<!-- Add entries here as you merge changes. Move them under a version heading when you cut a release. -->

### Added

### Changed

### Fixed

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
