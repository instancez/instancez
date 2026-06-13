# Docs Site Design

**Date:** 2026-06-13
**Status:** Approved

## Summary

Build a docs site for instancez at `docs/site/` using Starlight (Astro), deployed to GitHub Pages, with a custom marketing landing page and four top-level content sections: Quick Start, Build, Deploy, and API Reference.

---

## Decisions

| Topic | Decision |
|---|---|
| Framework | Starlight (Astro) |
| Location | `docs/site/` in the main repo |
| Deployment | GitHub Pages, GitHub Actions on push to `main` |
| Custom domain | Supported — configurable in Starlight + GitHub Pages |
| Primary audience | Developers building apps; deployment is part of the journey |
| Root (`/`) | Custom marketing landing page (not Starlight's default index) |

---

## Visual Design

The landing page mockup is the approved reference. Key rules:

- **Page chrome:** black (`#080808`) background, white text — no accent colors in the UI
- **Code blocks:** full syntax highlighting (Shiki dark theme) — the code is where color lives
- **Contrast:** all secondary text at `#bbb` or above against `#080808`; `#999` minimum for body copy
- **Typography:** system sans-serif, tight letter-spacing, heavy weights for headings
- **CTA buttons:** white fill with black text (primary), border-only (secondary)

The Starlight dark theme is used as the base and overridden with instancez brand colors via `custom.css`.

---

## Site Structure

```
docs/site/
├── astro.config.mjs
├── package.json
├── src/
│   ├── pages/
│   │   └── index.astro           # custom landing page (replaces Starlight's default)
│   ├── content/
│   │   └── docs/
│   │       ├── quick-start.md    # single page — see below
│   │       ├── build/
│   │       │   ├── schema.md
│   │       │   ├── auth.md
│   │       │   ├── querying.md
│   │       │   ├── rls.md
│   │       │   ├── functions.md
│   │       │   └── storage.md
│   │       ├── deploy/
│   │       │   ├── docker.md
│   │       │   ├── lambda.md
│   │       │   ├── self-hosted.md
│   │       │   └── env-vars.md
│   │       └── api-reference/
│   │           ├── rest.md
│   │           ├── auth.md
│   │           ├── rpc.md
│   │           ├── functions.md
│   │           ├── storage.md
│   │           ├── cli.md
│   │           └── config.md
│   └── styles/
│       └── custom.css            # brand color overrides
└── public/
    └── favicon.svg
```

---

## Navigation

Starlight sidebar, auto-generated from the file tree. Top-level groups:

1. **Quick Start** — single page
2. **Build** — schema, auth, querying, RLS, functions, storage
3. **Deploy** — docker, lambda, self-hosted, env vars
4. **API Reference** — REST, auth, RPC, functions, storage, CLI, config

The nav bar mirrors these four sections with a "Get Started →" CTA linking to Quick Start.

---

## Landing Page (`/`)

Sections in order:

1. **Nav bar** — logo, four section links, "Get Started →" CTA
2. **Hero** — headline ("Your backend. Your infrastructure."), one-line description, two CTAs (Quick Start, GitHub), install snippet
3. **Feature grid** — 3×2 grid: YAML-driven schema, Auth, PostgREST API, Code functions, Storage, Deploy anywhere
4. **Code demo** — split view: `instancez.yaml` (left) / "Your Frontend" supabase-js code (right), both syntax-highlighted
5. **CTA strip** — "Ready to build?" → Quick Start

Install command shown in the hero:
```
curl -fsSL https://get.instancez.io | sh
```

> **Confirm before shipping:** The domain `get.instancez.io` is a placeholder. The actual install script URL must be confirmed and the script must exist at that URL before the landing page and Quick Start go live. Use a GitHub Releases fallback (e.g. `github.com/instancez/instancez/releases/latest/download/install.sh`) if a vanity domain is not ready.

---

## Quick Start (`/quick-start`)

A single, tight page. No tutorials, no hand-holding. Three beats:

1. **Install** — one command (the `curl | sh` above), with platform callouts (macOS, Linux, Windows) if install methods differ
2. **Wow** — 1–2 commands that result in a running Supabase-compatible API (e.g. `inz init my-app` + `cd my-app && docker compose up` or equivalent). Show the terminal output. Show a working supabase-js query against it.
3. **What's next** — links into Build sections

The exact commands must be verified against the current `inz init` and `inz dev` behavior before the page is written.

---

## Build Section

Task-oriented guides. Each page covers one capability end-to-end: the concept, the YAML to write, and how to use it from supabase-js. Pages:

- **schema.md** — tables, fields, types, constraints, foreign keys, auto-migrations on save
- **auth.md** — all auth methods (password, magic link, email OTP, Google, GitHub, anonymous, TOTP MFA), session lifecycle, `auth.users`
- **querying.md** — filtering, operators, embeds, aggregates, pagination, count, range, text search
- **rls.md** — policy syntax, `auth.uid()`, `auth.is_authenticated()`, patterns (owner-only, public read, service-role bypass)
- **functions.md** — JS ESM handlers, `req`/`ctx` API, secrets, npm deps, `inz dev` vs `inz deploy` vs `inz serve`
- **storage.md** — buckets, upload/download, bucket policies, local vs S3

---

## Deploy Section

- **docker.md** — `docker compose up`, env vars, the two DB URL requirement
- **lambda.md** — AWS Lambda ARM64, ECR image (not manifest list), env var wiring
- **self-hosted.md** — bare metal / VPS, `inz serve`, systemd, reverse proxy
- **env-vars.md** — complete environment variable reference (`INSTANCEZ_DATABASE_URL`, `INSTANCEZ_OWNER_DATABASE_URL`, `INSTANCEZ_AUTH_DATABASE_URL`, `INSTANCEZ_ENV_*`, JWT config, etc.)

---

## API Reference Section

Exhaustive reference, not guides. Each page is a full surface map:

- **rest.md** — CRUD endpoints, all PostgREST query operators (`eq`, `gt`, `like`, `contains`, `order`, `limit`, `range`, `select`, embeds, aggregates, `Prefer: return=…`, error envelope shape)
- **auth.md** — `/auth/v1/*` endpoint listing, request/response shapes, JWT claims structure
- **rpc.md** — `/rest/v1/rpc/<name>` calling convention, YAML declaration under `rpc:`, argument passing
- **functions.md** — `/functions/v1/<name>`, `req` properties, `ctx` properties, response shape, timeout/error codes
- **storage.md** — `/storage/v1/*` endpoints, bucket operations, object upload/download
- **cli.md** — all `inz` subcommands with flags and examples (`init`, `dev`, `serve`, `validate`, `deploy`, `doctor`, `status`, `login`, `logout`, `whoami`, `bootstrap`, `bundle`, `providers`)
- **config.md** — complete `instancez.yaml` schema, every key with type, default, and example

---

## Existing Docs Migration

Two existing docs files exist and must be **audited against the current codebase** before any content is reused:

| File | Target | Notes |
|---|---|---|
| `docs/functions.md` | `build/functions.md` + `api-reference/functions.md` | Verify `req`/`ctx` surface, YAML keys, lifecycle sections match current implementation |
| `docs/examples/gearstore/README.md` | Potentially linked from Quick Start or a future Examples section | Verify all supabase-js calls, auth flows, and function examples still work |

Do not copy content verbatim — read the source files and verify each claim against the Go implementation before writing the doc page.

---

## Deployment Pipeline

GitHub Actions workflow at `.github/workflows/docs.yml`:

```
trigger: push to main (paths: docs/site/**)
jobs:
  build: npm ci + npm run build (inside docs/site/)
  deploy: actions/deploy-pages → GitHub Pages
```

Starlight outputs a static site to `docs/site/dist/`. No adapter needed for GitHub Pages (static output is the default).

If a custom domain is configured, add a `CNAME` file to `docs/site/public/` and set the domain in the GitHub Pages settings.

---

## Syntax Highlighting

Starlight uses Shiki. The theme used in code blocks throughout the site should match the landing page palette:

- Keywords: `#c792ea` (purple)
- Method calls / functions: `#82aaff` (blue)
- Strings: `#c3e88d` (green)
- Booleans / numbers: `#f78c6c` (orange)
- Comments: `#546e7a` (dimmed italic)
- Default text: `#cdd3de`

Configure via `shikiConfig` in `astro.config.mjs` using a custom theme or the closest built-in dark theme (e.g. `material-theme-darker`).

---

## Out of Scope (v1)

- Versioned docs (no goreleaser-style version switcher)
- i18n / translations
- Blog
- Algolia / DocSearch (Pagefind covers v1 search needs)
- Examples section (gearstore can be linked from Quick Start; a full Examples section is a v2 addition)
