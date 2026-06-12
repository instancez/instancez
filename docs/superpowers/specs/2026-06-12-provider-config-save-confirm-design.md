# Provider config model + save confirmation design

Date: 2026-06-12. Approved by Saed in-session.

## Problem

1. The dashboard presented provider secrets as bare "environment variables".
   They are provider **configs**: some are credentials, the rest are ordinary
   settings. Credentials should always render first, and every config must be
   settable through env vars or through the UI (which writes `.env`).
2. Saving applied changes immediately. Saves must first show a summary — a
   diff per file being changed (`instancez.yaml`, `.env`) — and ask for
   confirmation.

## Decisions (user-confirmed)

- **Storage model:** credentials are only ever `${VAR}` references in
  `instancez.yaml`; their values go to `.env` (or are exported). Non-credential
  settings are written as literal YAML values, but a hand-edited `${VAR}` ref
  in any setting is respected and rendered as env-managed.
- **Confirmation scope:** every dashboard save, on all pages.
- **Secret display in the summary:** masked with a last-4 tail
  (`••••abcd`).

## Design

### Backend

`POST /api/_admin/config/preview` (gated like `PUT /config`): accepts the same
JSON config body, validates it, and returns
`{"current": "<yaml>", "proposed": "<yaml>"}`. `current` is the raw source
bytes (env refs preserved — no secret values transit the API); `proposed` is
`yaml.Marshal` of the parsed body — the same call `handlePutConfig` uses, so
the preview is byte-accurate. No migration, no write. Validation failures
return the same `{errors: [...]}` shape as PUT.

### Frontend

- **Provider schemas** (`Providers.tsx`, `Auth.tsx`): each provider declares
  fields as `credential` (label, env var) or `setting` (label, config key).
  Credentials render first as masked inputs that stage `.env` writes, with
  set/unset badges and an `env NAME` caption. Settings render as plain inputs
  bound to literal YAML values; a value matching `^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`
  renders env-managed (badge + staged `.env` input) instead.
  Credentials: email `api_key`; S3 `access_key_id`/`secret_access_key`;
  GCS `credentials`; MinIO access/secret keys; OAuth `client_secret`.
- **Save confirmation** lives in `useConfigState.save()` (the choke point all
  pages call): fetch the preview, open a `ConfirmSaveDialog` showing an
  `instancez.yaml` unified diff (rendered client-side with the `diff` npm
  package) and, when present, the staged `.env` changes as
  `NAME=••••tail (added|updated)`. Confirm proceeds with the existing
  `PUT /config` (+ the caller's `putDotenv`); cancel aborts and keeps the page
  dirty. Pages pass staged dotenv metadata via
  `save(config, { dotenvChanges })`. Preview validation errors surface in the
  existing SaveBar error area.

### Testing

Go: preview handler returns both YAMLs, performs no write, surfaces
validation errors, honors dashboard mode gates. Frontend: creds-first
ordering, env-ref respect, dialog content (diff lines, masked tails),
confirm/cancel wiring through `save()`. All written before implementation.
