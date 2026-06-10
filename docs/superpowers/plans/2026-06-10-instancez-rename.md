# ultrabase → instancez Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fully rebrand ultrabase (binary `ultra`) to instancez (binary `inz`) — module path, env vars, config file, DB identifiers, CLI, infra, and dashboard branding — leaving zero `ultra`/`ultrabase` references in the repo (docs/ excluded, deferred to a later pass).

**Architecture:** Layered commits on one branch; each task is one pattern-scoped rename that leaves the tree compiling and tests green. Mechanical sed for high-volume patterns, manual edits for branding code. The existing test suite (unit + integration + `TestSupabaseJSCompat`) is the safety net — no new tests are written; a final case-insensitive grep gate proves completeness.

**Tech Stack:** Go 1.25, React/Vite dashboard (vitest), Docker/testcontainers for integration tests.

**Spec:** `docs/superpowers/specs/2026-06-10-instancez-rename-design.md`

**Conventions used throughout:**
- Brand casing: `instancez` (user-visible strings, lowercase like instancez-coder), `Instancez` (sentence-start in comments, exported Go identifiers), `INSTANCEZ` (env vars).
- All sed scopes use `git grep -l` so only tracked files are touched (node_modules/dist are never tracked). Always exclude `:!docs/` — the docs pass is deferred and the spec/plan files intentionally contain old names.
- After every task: the listed verify commands MUST pass before committing (CLAUDE.md feedback loop).
- Integration tests need a running Docker daemon; the supabase-js suite also needs node+npm.

**Wire-compat invariants — NEVER rename these (TestSupabaseJSCompat guards them):**
`anon` / `authenticated` / `service_role` JWT tokens; `/auth/v1`, `/rest/v1`, `/storage/v1`, `/functions/v1` prefixes; PostgREST operators/headers/error shapes; `auth.uid()` / `auth.is_authenticated()`; the `app.*` session GUCs (`app.user_id`, `app.role`, `app.email`, `app.jwt`).

---

### Task 0: Branch

**Files:** none

- [ ] **Step 1: Create the working branch**

```bash
git checkout -b rename-instancez
```

- [ ] **Step 2: Baseline check — record the starting reference count**

```bash
git grep -iIc 'ultra' -- ':!docs/' | awk -F: '{s+=$2} END {print s}'
```

Expected: a number around 2000. This is the count we drive to zero.

---

### Task 1: Go module path + `ultrahttp` import alias

**Files:**
- Modify: `go.mod` (module line)
- Modify: every `.go` file importing `github.com/saedx1/ultrabase/...` (~100 files)
- Modify: `Makefile` (LDFLAGS `-X github.com/saedx1/ultrabase/internal/cli.version=...`)
- Modify: `internal/cli/serve.go`, `internal/cli/dev.go`, `internal/cli/flags.go`, `internal/app/funcbundle_integration_test.go`, `internal/adapter/http/pgrupstream/integration_test.go`, `internal/adapter/http/supabase_integration_test.go`, `internal/adapter/funcs/clients_integration_test.go` (the 7 files using the `ultrahttp` alias)

- [ ] **Step 1: Rewrite the module path everywhere (repo-wide — also catches Makefile LDFLAGS)**

```bash
git grep -l 'github.com/saedx1/ultrabase' -- ':!docs/' | xargs sed -i 's#github.com/saedx1/ultrabase#github.com/saedx1/instancez#g'
```

- [ ] **Step 2: Rename the `ultrahttp` import alias**

```bash
git grep -l 'ultrahttp' -- ':!docs/' | xargs sed -i 's/ultrahttp/instancezhttp/g'
```

- [ ] **Step 3: Verify no module-path or alias references remain**

```bash
git grep -n 'saedx1/ultrabase\|ultrahttp' -- ':!docs/'
```

Expected: no output (exit code 1).

- [ ] **Step 4: Build and unit-test**

```bash
go build ./... && go vet ./... && go test -race ./...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor: rename Go module to github.com/saedx1/instancez"
```

---

### Task 2: Env var prefixes + `ultraenv` identifiers

**Files:**
- Modify: every file mentioning `ULTRABASE_` (~28 vars; Go, `docker-compose.dev.yaml`, `.github/workflows/ci.yml`, `Dockerfile*`, `README.md`, `CLAUDE.md`)
- Modify: every file mentioning `ULTRA_ENV_` (function env passthrough + tests)
- Rename: `internal/config/ultraenv.go` → `internal/config/instancezenv.go`
- Rename: `internal/config/ultraenv_test.go` → `internal/config/instancezenv_test.go`
- Modify: Go identifiers `LoadUltraEnv`, `asUltraEnvRef`, `ultraEnvPrefix`, `ultraEnvRefPattern` + their tests

- [ ] **Step 1: Rename env var prefixes (order matters — `ULTRA_ENV_` first, since `ULTRABASE_` sed cannot touch it but keep them distinct anyway)**

```bash
git grep -l 'ULTRA_ENV_' -- ':!docs/' | xargs sed -i 's/ULTRA_ENV_/INSTANCEZ_ENV_/g'
git grep -l 'ULTRABASE_' -- ':!docs/' | xargs sed -i 's/ULTRABASE_/INSTANCEZ_/g'
```

- [ ] **Step 2: Rename the ultraenv Go identifiers (case-aware, both exported and unexported)**

```bash
git grep -l 'UltraEnv\|ultraEnv' -- ':!docs/' | xargs sed -i 's/UltraEnv/InstancezEnv/g; s/ultraEnv/instancezEnv/g'
```

- [ ] **Step 3: Rename the files**

```bash
git mv internal/config/ultraenv.go internal/config/instancezenv.go
git mv internal/config/ultraenv_test.go internal/config/instancezenv_test.go
```

(Both files exist under exactly these names.)

- [ ] **Step 4: Verify the patterns are gone**

```bash
git grep -in 'ULTRABASE_\|ULTRA_ENV_\|ultraenv' -- ':!docs/'
```

Expected: no output.

- [ ] **Step 5: Build and unit-test**

```bash
go build ./... && go test -race ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "refactor: rename env var prefixes to INSTANCEZ_* / INSTANCEZ_ENV_*"
```

---

### Task 3: Config file `ultrabase.yaml` → `instancez.yaml`

**Files:**
- Rename: `ultrabase.yaml` → `instancez.yaml` (repo-root dev config)
- Modify: every file containing the string `ultrabase.yaml` (CLI flag defaults in `internal/cli/flags.go`, loader, `docker-compose.dev.yaml` volume mount, `dashboard/src/lib/downloadYaml.ts`, tests, `CLAUDE.md`, `README.md`, …)

- [ ] **Step 1: Replace the string everywhere, then move the file**

```bash
git grep -l 'ultrabase\.yaml' -- ':!docs/' | xargs sed -i 's/ultrabase\.yaml/instancez.yaml/g'
git mv ultrabase.yaml instancez.yaml
```

- [ ] **Step 2: Verify**

```bash
git grep -n 'ultrabase.yaml' -- ':!docs/'
```

Expected: no output.

- [ ] **Step 3: Build, unit-test, and dashboard test (downloadYaml.test.ts asserts the filename)**

```bash
go build ./... && go test -race ./...
cd dashboard && npm test; cd ..
```

Expected: PASS (both).

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "refactor: rename config file to instancez.yaml"
```

---

### Task 4: Binary `ultra` → `inz` + credentials dir

**Files:**
- Rename: `cmd/ultra/` → `cmd/inz/`
- Modify: `Makefile` (build target, install, clean, help text)
- Modify: `Dockerfile` (build path, COPY, CMD), `Dockerfile.lambda` (same)
- Modify: `.gitignore` (built-binary entry)
- Modify: `internal/cli/root.go` (cobra `Use:` and help/example strings), any CLI help text using `ultra <cmd>` (e.g. `internal/cli/dev.go`, `internal/cli/init.go`, `internal/cli/login.go`, `internal/cli/doctor.go`)
- Modify: `internal/cloud/credentials.go` (`~/.ultra` → `~/.instancez`) + `internal/cloud/credentials_test.go`
- Modify: `CLAUDE.md`, `README.md` command examples (`./ultra dev` → `./inz dev`)

- [ ] **Step 1: Move the command directory**

```bash
git mv cmd/ultra cmd/inz
git grep -l 'cmd/ultra' -- ':!docs/' | xargs sed -i 's#cmd/ultra#cmd/inz#g'
```

- [ ] **Step 2: Update the credentials path**

In `internal/cloud/credentials.go`, change:

```go
// credentialsPath returns the absolute path to ~/.instancez/credentials.
// Honors HOME for testability.
func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".instancez", "credentials"), nil
}
```

Then sweep any other `.ultra` path references (tests assert this path):

```bash
git grep -n '"\.ultra"\|/\.ultra\b' -- ':!docs/'
```

Replace each hit's `.ultra` with `.instancez`.

- [ ] **Step 3: Rename the binary in build/runtime references**

These are word-boundary `ultra` occurrences — do them with a careful sed, then audit:

```bash
git grep -l '\bultra\b' -- ':!docs/' | xargs sed -i 's/\bultra\b/inz/g'
git grep -in '\bultra\b' -- ':!docs/'
```

Expected after the audit grep: no output. Spot-check the diff for false positives before committing — in particular `Makefile` (binary name, `mv ultra` → `mv inz`, help text), `Dockerfile`/`Dockerfile.lambda` (`go build -o /inz ./cmd/inz`, `CMD ["inz", "serve"]`), `.gitignore`, and cobra `Use:`/help strings. The fixture `ultra_pat_abc123` in `internal/cloud/credentials_test.go` does NOT match `\bultra\b` (underscore is a word char) — it is handled in Task 7.

- [ ] **Step 4: Build, unit-test, build the binary under its new name**

```bash
go build ./... && go test -race ./...
go build -o inz ./cmd/inz && ./inz --help && rm inz
```

Expected: tests PASS; help output shows `inz` as the command name with no `ultra` strings.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor: rename CLI binary to inz, credentials dir to ~/.instancez"
```

---

### Task 5: DB-persisted identifiers

**Files:**
- Modify: `internal/cli/bootstrap.go` (`ownerRole = "ultrabase_owner"`)
- Modify: `internal/testutil/dbboot/dbboot.go` (`OwnerRole = "ultrabase_owner"`) + `container.go`
- Modify: `internal/cli/preflight/preflight.go` (role list) + tests
- Modify: every file containing `_ultrabase_migrations` (`internal/app/engine.go`, `internal/app/migrate*.go`, `internal/adapter/postgres/pool.go`, `internal/adapter/http/admin_handler.go`, `internal/cli/validate.go`, tests, `CLAUDE.md`)
- Modify: `docker-compose.dev.yaml` + `.github/workflows/ci.yml` (postgres user/password/db `ultrabase` → `instancez`, DSNs)

- [ ] **Step 1: Rename the migrations table and owner role**

```bash
git grep -l '_ultrabase_migrations' -- ':!docs/' | xargs sed -i 's/_ultrabase_migrations/_instancez_migrations/g'
git grep -l 'ultrabase_owner' -- ':!docs/' | xargs sed -i 's/ultrabase_owner/instancez_owner/g'
```

- [ ] **Step 2: Rename the dev/CI postgres credentials (compose + CI use `ultrabase` as user/password/dbname)**

```bash
sed -i 's/\bultrabase\b/instancez/g' docker-compose.dev.yaml .github/workflows/ci.yml
```

Check the result — DSNs must be internally consistent (`postgres://instancez:instancez@…/instancez`, owner DSN `postgres://instancez_owner:…`):

```bash
grep -n 'instancez\|ultra' docker-compose.dev.yaml .github/workflows/ci.yml
```

Expected: only `instancez*` hits, no `ultra*` hits.

- [ ] **Step 3: Unit tests, then integration tests (these provision the renamed role and migrations table in fresh containers)**

```bash
go build ./... && go test -race ./...
go test -tags=integration -race ./internal/app/... ./internal/cli/... ./internal/adapter/...
```

Expected: PASS. (Integration needs Docker. This exercises dbboot's `instancez_owner` provisioning and the `_instancez_migrations` table end-to-end.)

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "refactor!: rename DB identifiers (_instancez_migrations, instancez_owner)

Pre-release breaking change: existing dev databases must be recreated."
```

---

### Task 6: Dashboard branding (logo, favicon, theme, names)

**Files:**
- Create: `dashboard/src/assets/instancez-logo-only.svg` (copied from instancez-coder, background rect stripped)
- Create: `dashboard/public/favicon.svg` (gradient mark, copied verbatim)
- Rewrite: `dashboard/src/components/Logo.tsx`
- Modify: `dashboard/src/index.css` (theme tokens)
- Modify: `dashboard/index.html` (title + favicon link)
- Modify: `dashboard/src/components/Sidebar.tsx`, `dashboard/src/pages/Login.tsx` (brand text, GitHub link)
- Modify: `dashboard/package.json` + `dashboard/package-lock.json` (`ultrabase-dashboard` → `instancez-dashboard`)
- Modify: `dashboard/src/api/client.ts`, `App.tsx`, `Login.tsx`, `RpcDetail.tsx` + tests (`ultrabase_admin_key` sessionStorage key)

- [ ] **Step 1: Copy the logo assets**

```bash
mkdir -p dashboard/src/assets
cp ../../instancez-coder/v2/web/src/assets/instancez-logo-only.svg dashboard/src/assets/instancez-logo-only.svg
cp ../../instancez-coder/v2/web/public/favicon.svg dashboard/public/favicon.svg
```

Then edit `dashboard/src/assets/instancez-logo-only.svg`: **delete the opaque background rect** (the line `<rect x="367.736" y="280.8939" width="344.5282" height="382.8968" fill="white"/>`) so the mark is transparent — the dashboard renders it inverted on a dark background and the white rect would become a black box.

- [ ] **Step 2: Rewrite `dashboard/src/components/Logo.tsx`** (the dashboard is dark-only, so render the mark white via CSS invert; keep the existing props so `Sidebar.tsx`/`Login.tsx` callers don't change):

```tsx
import logoUrl from "../assets/instancez-logo-only.svg";

interface LogoProps {
  size?: number;
  className?: string;
}

export function Logo({ size = 36, className }: LogoProps) {
  return (
    <img
      src={logoUrl}
      width={size}
      height={size}
      alt="instancez"
      className={className}
      style={{ filter: "invert(1)" }}
    />
  );
}
```

If `tsc` complains about the `.svg` import in Step 5's build, ensure `dashboard/src/vite-env.d.ts` exists with `/// <reference types="vite/client" />`.

- [ ] **Step 3: Retheme `dashboard/src/index.css`** — replace the token values (names unchanged) per the spec:

```css
  --color-background: #111111;
  --color-foreground: #EEEEEE;
  --color-primary: #1a1a1a;
  --color-on-primary: #EEEEEE;
  --color-secondary: #2d2d2d;
  --color-accent: #FFFFFF;
  --color-accent-hover: #E0E0E0;
  --color-muted: #2d2d2d;
  --color-muted-foreground: #A0A0A0;
  --color-border: #333333;
  --color-border-hover: #555555;
  --color-destructive: #D84040;
  --color-destructive-hover: #C03636;
  --color-warning: #E0A030;
  --color-info: #A0A0A0;
  --color-surface: #1a1a1a;
  --color-surface-hover: #2d2d2d;
  --color-ring: #666666;
  --color-input: #1a1a1a;
  --color-input-border: #444444;
```

Then audit for accent-contrast bugs: the accent changed from dark red to **white**, so any component putting light text on `accent` is now broken.

```bash
grep -rn 'on-accent\|text-white.*bg-accent\|bg-accent.*text-white' dashboard/src
```

For each hit, switch the foreground to a dark value (e.g. `text-background` / `#111111`).

- [ ] **Step 4: Brand strings, favicon link, names, sessionStorage key**

```bash
cd dashboard
grep -rl 'ultrabase_admin_key' src | xargs sed -i 's/ultrabase_admin_key/instancez_admin_key/g'
sed -i 's/ultrabase-dashboard/instancez-dashboard/g' package.json package-lock.json
cd ..
```

Manual edits:
- `dashboard/index.html`: `<title>instancez</title>` and ensure `<link rel="icon" type="image/svg+xml" href="/favicon.svg" />`.
- `dashboard/src/components/Sidebar.tsx:32`: `Ultrabase` → `instancez`; line 83: `href="https://github.com/ultrabase/ultrabase"` → `href="https://github.com/saedx1/instancez"`.
- `dashboard/src/pages/Login.tsx:39`: `Ultrabase Dashboard` → `instancez dashboard`.
- Update matching assertions in `dashboard/src/pages/Login.test.tsx` (and any other test asserting the old strings — find them with `grep -rin 'ultra' dashboard/src`).

- [ ] **Step 5: Verify no dashboard references remain, run tests, eyeball it**

```bash
grep -rin 'ultra' dashboard/src dashboard/index.html dashboard/package.json dashboard/public
cd dashboard && npm test && npm run build; cd ..
```

Expected: grep silent; vitest + tsc/vite build PASS. Then `cd dashboard && npm run dev` and visually check sidebar logo (white mark on dark), login page, favicon, button contrast.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat(dashboard): instancez branding — logo, favicon, black & white theme"
```

---

### Task 7: Catch-all brand sweep (everything that's left)

**Files:** whatever still matches — known stragglers: `instancez.yaml` project name (`name: Ultrabase Devx`), `internal/cli/init.go` templates, `.github/workflows/docker.yml` ECR path `…/ultrabase/<env>`, `test/integration/supabase-js/package.json` + `package-lock.json` (`ultrabase-supabase-js-harness`), `internal/cloud/credentials_test.go` (`ultra_pat_abc123`), comments saying "Ultrabase" across `internal/`, `README.md`, `CLAUDE.md`, `.dockerignore`, `pkg/configvalidate/`, `internal/csvutil/`.

- [ ] **Step 1: Case-aware catch-all**

```bash
git grep -l 'ultrabase' -- ':!docs/' | xargs sed -i 's/ultrabase/instancez/g'
git grep -l 'Ultrabase' -- ':!docs/' | xargs sed -i 's/Ultrabase/Instancez/g'
git grep -l 'ULTRABASE' -- ':!docs/' | xargs sed -i 's/ULTRABASE/INSTANCEZ/g'
git grep -l 'ultra_pat_' -- ':!docs/' | xargs sed -i 's/ultra_pat_/instancez_pat_/g'
```

- [ ] **Step 2: Audit every remaining case-insensitive hit**

```bash
git grep -in 'ultra' -- ':!docs/'
```

Expected: no output. If anything appears (e.g. `Ultra` mid-identifier, an `ULTRA` in a workflow), fix it by hand with the same brand-casing conventions.

- [ ] **Step 3: User-visible string check** — the catch-all turns comments into "Instancez" but user-visible output should be lowercase "instancez" per brand. Check CLI output strings:

```bash
git grep -n '"[^"]*Instancez[^"]*"' -- '*.go'
```

For hits inside `fmt.Print*/Println` user-facing strings (doctor output, init templates, login prompts), prefer `instancez` unless it starts a sentence. Comments may keep `Instancez`. Use judgment; don't churn non-visible strings.

- [ ] **Step 4: Full unit + dashboard verification**

```bash
go build ./... && go vet ./... && go test -race ./...
cd dashboard && npm test; cd ..
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor: final ultrabase -> instancez brand sweep"
```

---

### Task 8: Grep gate + full verification

**Files:** none (verification only)

- [ ] **Step 1: The grep gate (case-insensitive, binary-file-skipping)**

```bash
git grep -iIn 'ultra' -- ':!docs/'
```

Expected: **zero hits**. Also check untracked-but-shippable files:

```bash
grep -riIn 'ultra' --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=dist --exclude-dir=docs .
```

Expected: zero hits.

- [ ] **Step 2: Full feedback loop per CLAUDE.md**

```bash
go build ./...
go test -race ./...
go test -tags=integration -race ./...
cd dashboard && npm test && npm run build; cd ..
```

Expected: all PASS. The integration run includes `TestSupabaseJSCompat` (wire-compat proof) and the renamed role/table provisioning.

- [ ] **Step 3: Smoke the dev flow end-to-end with the new names**

```bash
go build -o inz ./cmd/inz
docker compose -f docker-compose.dev.yaml up -d postgres
INSTANCEZ_DATABASE_URL='postgres://instancez:instancez@localhost:5432/instancez?sslmode=disable' ./inz dev &
sleep 5; curl -s localhost:8080/rest/v1/ -H 'apikey: invalid' | head -c 200; kill %1
docker compose -f docker-compose.dev.yaml down -v
rm inz
```

Expected: dev boots, provisions `instancez_owner` + `authenticator`, serves; curl returns a Supabase-shaped error envelope. (Adjust the compose port if postgres is mapped differently — check `docker-compose.dev.yaml`.)

- [ ] **Step 4: Commit anything outstanding, push, open PR**

```bash
git status --short   # should be clean
git push -u origin rename-instancez
gh pr create --title "Rename ultrabase -> instancez" --body "$(cat <<'EOF'
Full pre-release rebrand per docs/superpowers/specs/2026-06-10-instancez-rename-design.md.

- Module: github.com/saedx1/instancez; binary: inz
- Env: INSTANCEZ_* / INSTANCEZ_ENV_*; config: instancez.yaml
- DB (breaking, pre-release): _instancez_migrations, instancez_owner
- Dashboard: instancez logo/favicon, black & white theme
- docs/ deferred to a separate pass

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Manual follow-ups (user, external — not part of this plan)

1. Rename the GitHub repo `saedx1/ultrabase` → `saedx1/instancez` (module path in `go.mod` assumes this at release).
2. Create/repoint ECR repositories `instancez/<env>` before `.github/workflows/docker.yml` can push (the workflow's image path was renamed in Task 7).
3. Optionally rename the local checkout dir `~/repos/ultrabase` → `~/repos/instancez`.
4. Later docs pass: rewrite `README.md` properly, do `docs/` (including `docs/logo.svg`, `docs/logo.html`, `docs/site/`), then remove the `:!docs/` exclusion from the grep gate.
