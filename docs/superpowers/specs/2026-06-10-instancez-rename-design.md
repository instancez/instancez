# Rename ultrabase → instancez (pre-release rebrand)

**Date:** 2026-06-10
**Status:** Approved design, pending implementation plan

## Goal

Fully rebrand the project from **ultrabase** (binary `ultra`) to **instancez**
(binary `inz`) before first release. After this work, no `ultrabase` / `ultra`
reference may remain anywhere in the codebase (docs are explicitly deferred to
a separate pass, except a mechanical find-replace in `README.md`).

Branding (logo + color scheme) is adopted from the sibling project
`../../instancez-coder` (v2 worktree).

## Decisions (settled with user)

- **Binary name:** `inz` (`inz dev`, `inz deploy`, …). `inz` is used for the
  binary **only** — all packages, aliases, file names, env vars, and paths use
  the full `instancez` name.
- **Go module path:** `github.com/saedx1/instancez` (GitHub repo to be renamed
  to match at release; manual step).
- **Env prefixes:** `INSTANCEZ_*` for framework config,
  `INSTANCEZ_ENV_*` for the user-function env passthrough prefix.
- **DB-persisted identifiers:** clean rename, no backward-compat shim
  (pre-release; dev databases get recreated).
- **Dashboard theme:** strict black & white (matching instancez-coder v2's
  neutral Chakra look). The gradient mark is used only for the favicon.

## Naming map

| Current | New |
|---|---|
| module `github.com/saedx1/ultrabase` | `github.com/saedx1/instancez` |
| binary `ultra`, `cmd/ultra/` | `inz`, `cmd/inz/` |
| `ultrabase.yaml` (default config path) | `instancez.yaml` |
| `ULTRABASE_*` env vars (~28) | `INSTANCEZ_*` |
| `ULTRA_ENV_*` passthrough prefix | `INSTANCEZ_ENV_*` |
| `_ultrabase_migrations` table | `_instancez_migrations` |
| `ultrabase_owner` Postgres role | `instancez_owner` |
| `~/.ultra/credentials` | `~/.instancez/credentials` |
| `ultrabase-dashboard` (npm package name) | `instancez-dashboard` |
| `ultrahttp` import alias | `instancezhttp` |
| `internal/config/ultraenv.go` (+ test) | `instancezenv.go` |
| `ULTRA_ENV_*` test fixture names | `INSTANCEZ_ENV_*` |
| `ultra_pat_…` test fixture values | `instancez_pat_…` |
| ECR image path `…/ultrabase/<env>` | `…/instancez/<env>` |
| Docker compose service/image names, Makefile targets, `Dockerfile*` labels | `instancez` equivalents |
| Underscore-reserved prefix docs/strings (`_ultrabase_…`) | `_instancez_…` |

### Invariants — must NOT change

Wire compatibility with `@supabase/supabase-js` is a load-bearing product
promise (`TestSupabaseJSCompat`):

- JWT `role` claim wire tokens: `anon`, `authenticated`, `service_role`.
- URL prefixes: `/auth/v1`, `/rest/v1`, `/storage/v1`, `/functions/v1`.
- All PostgREST operators, headers (`apikey`, `Authorization`, `Prefer`,
  `Range`), and error envelope shapes.
- The two-login design (`INSTANCEZ_OWNER_DATABASE_URL` privileged,
  `INSTANCEZ_AUTH_DATABASE_URL` NOINHERIT authenticator) — names change,
  semantics do not.
- `auth` / `storage` reserved schemas and `auth.uid()` /
  `auth.is_authenticated()` function names. (Correction during planning:
  the session GUCs these read are `app.*` — e.g. `app.user_id`, `app.role` —
  not `ultrabase.*`, so no GUC rename is needed.)

## Branding (dashboard)

- **Logo:** copy `instancez-logo-only.svg` from
  `../../instancez-coder/v2/web/src/assets/` into `dashboard/src/assets/`.
  Replace the programmatic `Logo.tsx` (maroon gradient mark) with a component
  rendering the SVG in white (CSS `invert`/`filter`), since the dashboard is
  dark-only. Wordmark text "instancez" where the sidebar/login show the name.
- **Favicon:** copy the gradient mark
  (`../../instancez-coder/v2/web/public/favicon.svg`, cyan `#04e4f7` → blue
  `#0f03f3`) into `dashboard/public/favicon.svg`; reference it from
  `dashboard/index.html`. Page title → `instancez`.
- **Theme tokens** (`dashboard/src/index.css`) — token *names* unchanged, so
  component churn is near zero; values move from warm maroon to strict
  neutral:

  | Token | New value |
  |---|---|
  | `--color-background` | `#111111` |
  | `--color-foreground` | `#EEEEEE` |
  | `--color-primary` | `#1a1a1a` |
  | `--color-on-primary` | `#EEEEEE` |
  | `--color-secondary` | `#2d2d2d` |
  | `--color-accent` | `#FFFFFF` (white-on-black buttons) |
  | `--color-accent-hover` | `#E0E0E0` |
  | `--color-muted` | `#2d2d2d` |
  | `--color-muted-foreground` | `#A0A0A0` |
  | `--color-border` | `#333333` |
  | `--color-border-hover` | `#555555` |
  | `--color-destructive` | `#D84040` (semantic, kept) |
  | `--color-destructive-hover` | `#C03636` |
  | `--color-warning` | `#E0A030` (semantic, kept) |
  | `--color-info` | `#A0A0A0` |
  | `--color-surface` | `#1a1a1a` |
  | `--color-surface-hover` | `#2d2d2d` |
  | `--color-ring` | `#666666` |
  | `--color-input` | `#1a1a1a` |
  | `--color-input-border` | `#444444` |

  Components hard-coding accent-on-accent assumptions (e.g., light text on
  the accent color) must be checked: with a white accent, "on-accent" content
  must be dark.

## Execution approach

**Layered commits on one branch, single PR.** Each layer compiles and passes
its relevant tests before the next begins:

1. Go module path + all import paths (`go.mod`, every `.go` file,
   `ultrahttp` → `instancezhttp` alias).
2. Env vars, config file default (`instancez.yaml`), CLI binary
   (`cmd/inz`), file renames (`ultraenv.go` → `instancezenv.go`),
   credentials path, CLI help text / command strings.
3. DB-persisted identifiers (`_instancez_migrations`, `instancez_owner`)
   + `internal/testutil/dbboot`.
4. Dashboard branding: logo, favicon, title, theme tokens, npm package name.
5. Infra: `.github/workflows/*`, `Dockerfile`, `Dockerfile.lambda`,
   `docker-compose.dev.yaml`, `Makefile`, `test/integration/supabase-js`
   package names, `ultrabase.yaml` → `instancez.yaml` (repo root example),
   README mechanical find-replace, and `CLAUDE.md` (commands, env vars,
   role/table names must reflect the new names — it is project instructions,
   not deferred docs).
6. Final sweep: grep gate + full verification.

Rejected alternatives: big-bang scripted sed (2,100-reference diff is
unreviewable; `ultra` substring hazards), parallel subagents (layers are
sequential — imports must change before anything compiles).

## Scope boundaries

- **Docs deferred:** `docs/` content (including `docs/logo.svg`,
  `docs/logo.html`, `docs/site/`) is handled in a separate later pass and is
  excluded from the grep gate. `README.md` gets a mechanical find-replace
  only (no rewrite).
- **Manual / external steps (user follow-ups, listed in plan):**
  - Rename the GitHub repo to `instancez` at release.
  - Create/repoint ECR repos `instancez/<env>` (IaC) before the renamed
    `docker.yml` can push.
  - Optionally rename the local checkout dir `~/repos/ultrabase`.
- **Cloud API:** default is already `https://my.instancez.dev/api` — no
  change needed beyond surrounding comments/env-var names.

## Verification

Per CLAUDE.md after each layer, at minimum:

- `go build ./...`
- `go test -race ./...`
- `go test -tags=integration -race ./...` for touched packages (layers 1–3, 5)
- `npm test` in `dashboard/` (layer 4)
- `TestSupabaseJSCompat` must pass after layers 2, 3, and 5.

**Final grep gate** (layer 6):

```sh
grep -riIE 'ultra' --exclude-dir=.git --exclude-dir=node_modules \
  --exclude-dir=dist --exclude-dir=docs . 
```

must return **zero** hits — case-insensitive so it catches `Ultrabase`,
`ULTRA_`, `ultrahttp`, etc. (`docs/` excluded per scope; remove the exclusion
during the later docs pass.)
