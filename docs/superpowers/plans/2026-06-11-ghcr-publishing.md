# ghcr Publishing + Platform ECR Backend Build Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** instancez publishes public multi-arch images to ghcr.io (and `inz` binaries to GitHub Releases on tags); instancez-platform builds the private ECR lambda-arm64 image from instancez source inside its `docker-services.yml`.

**Architecture:** instancez's `docker.yml` becomes `publish.yml` (ghcr login via `GITHUB_TOKEN`, no AWS). The platform gains a `build-backend` job that clones `instancez/instancez@main` with the existing deploy key, keys its skip-if-exists check on the *instancez* commit SHA, and pushes `instancez/<env>:<sha>-lambda-arm64` — the only variant the deployer consumes (Lambda is hardcoded arm64 and rejects manifest lists). `deploy-infrastructure.yml` wires the resulting URI into `pulumi config set images:backend_uri`.

**Tech Stack:** GitHub Actions, docker buildx (+ QEMU for multi-arch on ghcr), Go ldflags injection, `gh release create`, AWS ECR.

**Repos:**
- instancez: `/home/saedx1/repos/instancez/main` (branch `main`) — Tasks 1–4
- instancez-platform: `/home/saedx1/repos/instancez-platform/v2` (branch `v2`) — Tasks 5–6

Spec: `docs/superpowers/specs/2026-06-11-ghcr-publishing-design.md`

**Known facts (verified):**
- Go module path: `github.com/instancez/instancez`; version var: `internal/cli/root.go:12` `var version = "dev"`.
- `dashboard/embed.go` embeds `dashboard/dist` (`//go:embed all:dist`) — binaries need a dashboard build first; dist is arch-independent.
- Platform v2 worktree has **uncommitted user WIP** in `.github/workflows/deploy-infrastructure.yml` and `destroy-infrastructure.yml` (pulumi state-bucket rename `instancez-coder-…` → `instancez-platform-…`). Do NOT commit or revert that WIP; Task 6 uses a stash dance.
- CI check names gated on: `Go unit tests`, `Go integration tests`, `Dashboard tests` (unchanged).

---

### Task 1: `commit` var + `inz version` output (instancez)

**Files:**
- Modify: `internal/cli/root.go`
- Create: `internal/cli/version_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/version_test.go`:

```go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCmd(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute version: %v", err)
	}
	got := out.String()
	// Defaults: version="dev", commit="unknown"; release builds override via ldflags.
	if !strings.Contains(got, "instancez vdev (unknown)") {
		t.Errorf("version output = %q, want it to contain %q", got, "instancez vdev (unknown)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestVersionCmd ./internal/cli/`
Expected: FAIL — output is empty because the current `Run` uses `fmt.Printf` (writes to os.Stdout, not the cobra out buffer) and prints no commit.

- [ ] **Step 3: Implement**

In `internal/cli/root.go`, change line 12:

```go
// version/commit are injected at build time via
// -ldflags "-X github.com/instancez/instancez/internal/cli.version=… -X github.com/instancez/instancez/internal/cli.commit=…"
var (
	version = "dev"
	commit  = "unknown"
)
```

And change `newVersionCmd`:

```go
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("instancez v%s (%s)\n", version, commit)
		},
	}
}
```

(`cmd.Printf` writes to `OutOrStdout()` — same behavior for users, capturable in tests. `fmt` may become an unused import; remove it from the import block only if the compiler says so — `fmt` is still used elsewhere in the file by `Execute`.)

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/cli/` then `go build ./...`
Expected: PASS, build OK.

- [ ] **Step 5: Verify ldflags injection works end-to-end**

```sh
go build -ldflags "-X github.com/instancez/instancez/internal/cli.version=9.9.9 -X github.com/instancez/instancez/internal/cli.commit=abc1234" -o /tmp/inz-ldflags-test ./cmd/inz
/tmp/inz-ldflags-test version
rm /tmp/inz-ldflags-test
```

Expected output: `instancez v9.9.9 (abc1234)`

- [ ] **Step 6: Commit**

```bash
git add internal/cli/root.go internal/cli/version_test.go
git commit -m "feat(cli): inject commit alongside version via ldflags"
```

---

### Task 2: `VERSION`/`COMMIT` build args in both Dockerfiles (instancez)

**Files:**
- Modify: `Dockerfile:23-35`
- Modify: `Dockerfile.lambda:21-31`

- [ ] **Step 1: Edit `Dockerfile` builder stage**

Replace lines 23–35 (the `builder` stage) so the ARGs and ldflags are added:

```dockerfile
# --- go builder ---
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overlay the (possibly real, possibly stub) dist into the embed path
# before `go build` reads the //go:embed directive.
COPY --from=dashboard /out/dist ./dashboard/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags "-X github.com/instancez/instancez/internal/cli.version=${VERSION} -X github.com/instancez/instancez/internal/cli.commit=${COMMIT}" \
    -o /inz ./cmd/inz
```

- [ ] **Step 2: Edit `Dockerfile.lambda` builder stage**

Replace lines 21–31 (the `builder` stage) identically:

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=dashboard /out/dist ./dashboard/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags "-X github.com/instancez/instancez/internal/cli.version=${VERSION} -X github.com/instancez/instancez/internal/cli.commit=${COMMIT}" \
    -o /inz ./cmd/inz
```

Also update the `Dockerfile.lambda` header comment (lines 3–6) since the per-arch builds now happen in instancez-platform:

```dockerfile
# Per-arch Lambda image (Lambda rejects multi-arch manifest lists). The
# per-arch ECR tags Lambda actually runs are built by instancez-platform's
# docker-services.yml; the ghcr `-lambda` tag built from publish.yml is a
# multi-arch manifest list (never pulled by Lambda directly). The
# WITH_DASHBOARD flag mirrors the main Dockerfile: true (default) embeds
# the SPA, false produces a slim binary that 404s on /dashboard.
```

- [ ] **Step 3: Verify with a local build (no dashboard, fast)**

```sh
docker build --build-arg WITH_DASHBOARD=false --build-arg VERSION=9.9.9 --build-arg COMMIT=abc1234 -t inz-args-test -f Dockerfile .
docker run --rm inz-args-test inz version
docker rmi inz-args-test
```

Expected: `instancez v9.9.9 (abc1234)`

Also confirm the default still builds: `docker build --build-arg WITH_DASHBOARD=false -t inz-args-default -f Dockerfile.lambda . && docker run --rm --entrypoint inz inz-args-default version && docker rmi inz-args-default`
Expected: `instancez vdev (unknown)`

- [ ] **Step 4: Commit**

```bash
git add Dockerfile Dockerfile.lambda
git commit -m "feat(docker): VERSION/COMMIT build args injected via ldflags"
```

---

### Task 3: `docker.yml` → `publish.yml` (ghcr images + release binaries) (instancez)

**Files:**
- Delete: `.github/workflows/docker.yml`
- Create: `.github/workflows/publish.yml`

- [ ] **Step 1: Write `.github/workflows/publish.yml`**

The `check-ci` job is carried over from `docker.yml` verbatim. Full file:

```yaml
name: Publish

on:
  push:
    branches: [main]
    tags: ["v*"]
  workflow_dispatch:

env:
  IMAGE: ghcr.io/${{ github.repository }}

jobs:
  check-ci:
    name: Check CI Status
    runs-on: ubuntu-latest
    permissions:
      checks: read
    outputs:
      tests_passed: ${{ steps.check.outputs.passed }}
    steps:
      - name: Check if CI already passed for this commit
        id: check
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          NAMES=("Go unit tests" "Go integration tests" "Dashboard tests")
          for i in $(seq 1 60); do
            JSON=$(gh api repos/${{ github.repository }}/commits/${{ github.sha }}/check-runs \
              --jq '[.check_runs[] | select(.name == "Go unit tests" or .name == "Go integration tests" or .name == "Dashboard tests")]')
            COUNT=$(echo "$JSON" | jq 'length')
            if [ "$COUNT" -lt 3 ]; then
              echo "Found $COUNT/3 check runs, no CI run yet"
              echo "passed=false" >> $GITHUB_OUTPUT
              exit 0
            fi
            IN_PROGRESS=$(echo "$JSON" | jq '[.[] | select(.status != "completed")] | length')
            if [ "$IN_PROGRESS" -gt 0 ]; then
              echo "CI still running ($IN_PROGRESS jobs in progress), waiting 30s... ($i/60)"
              sleep 30
              continue
            fi
            ALL_SUCCESS=$(echo "$JSON" | jq 'all(.conclusion == "success")')
            if [ "$ALL_SUCCESS" = "true" ]; then
              echo "All CI checks passed"
              echo "passed=true" >> $GITHUB_OUTPUT
            else
              echo "CI checks completed but not all succeeded"
              echo "passed=false" >> $GITHUB_OUTPUT
            fi
            exit 0
          done
          echo "Timed out waiting for CI"
          exit 1

  test:
    name: Run Tests
    needs: check-ci
    if: needs.check-ci.outputs.tests_passed != 'true'
    uses: ./.github/workflows/ci.yml

  images:
    needs: [check-ci, test]
    if: always() && (needs.test.result == 'success' || needs.test.result == 'skipped')
    name: Publish Images
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4

      - name: Resolve version
        id: version
        run: |
          if [[ "$GITHUB_REF" == refs/tags/v* ]]; then
            echo "version=${GITHUB_REF_NAME#v}" >> $GITHUB_OUTPUT
          else
            echo "version=dev-${GITHUB_SHA::7}" >> $GITHUB_OUTPUT
          fi

      - name: Login to ghcr.io
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Docker metadata (OCI labels)
        id: meta
        uses: docker/metadata-action@v5
        with:
          images: ${{ env.IMAGE }}
          tags: |
            type=raw,value=${{ steps.version.outputs.version }}

      - name: Set up QEMU
        uses: docker/setup-qemu-action@v3
        with:
          platforms: arm64

      - name: Set up Buildx
        uses: docker/setup-buildx-action@v3

      # Unsuffixed tag == standalone (the default flavor).
      - name: Build and push standalone image (multi-arch)
        uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile
          platforms: linux/amd64,linux/arm64
          push: true
          provenance: false
          labels: ${{ steps.meta.outputs.labels }}
          build-args: |
            VERSION=${{ steps.version.outputs.version }}
            COMMIT=${{ github.sha }}
          tags: |
            ${{ env.IMAGE }}:${{ steps.version.outputs.version }}
            ${{ env.IMAGE }}:${{ steps.version.outputs.version }}-standalone

      # Multi-arch is fine on ghcr: Lambda never pulls this tag. The per-arch
      # ECR images Lambda runs are built by instancez-platform.
      - name: Build and push Lambda image (multi-arch)
        uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile.lambda
          platforms: linux/amd64,linux/arm64
          push: true
          provenance: false
          labels: ${{ steps.meta.outputs.labels }}
          build-args: |
            VERSION=${{ steps.version.outputs.version }}
            COMMIT=${{ github.sha }}
          tags: ${{ env.IMAGE }}:${{ steps.version.outputs.version }}-lambda

  binaries:
    needs: [check-ci, test]
    if: always() && startsWith(github.ref, 'refs/tags/v') && (needs.test.result == 'success' || needs.test.result == 'skipped')
    name: Release Binaries
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-node@v4
        with:
          node-version: "22"
          cache: npm
          cache-dependency-path: dashboard/package-lock.json

      # The binary embeds dashboard/dist (//go:embed); one build serves all
      # target platforms since the dist is arch-independent.
      - name: Build dashboard
        run: |
          cd dashboard
          npm ci
          npm run build

      - uses: actions/setup-go@v5
        with:
          go-version: "1.25"
          cache: true

      - name: Build binaries
        run: |
          VERSION=${GITHUB_REF_NAME#v}
          LDFLAGS="-s -w -X github.com/instancez/instancez/internal/cli.version=${VERSION} -X github.com/instancez/instancez/internal/cli.commit=${GITHUB_SHA}"
          mkdir -p dist
          for os in linux darwin windows; do
            for arch in amd64 arm64; do
              bin="inz"
              [ "$os" = "windows" ] && bin="inz.exe"
              CGO_ENABLED=0 GOOS=$os GOARCH=$arch \
                go build -ldflags "$LDFLAGS" -o "$bin" ./cmd/inz
              if [ "$os" = "windows" ]; then
                zip -q "dist/inz_${VERSION}_${os}_${arch}.zip" "$bin"
              else
                tar -czf "dist/inz_${VERSION}_${os}_${arch}.tar.gz" "$bin"
              fi
              rm "$bin"
            done
          done
          (cd dist && sha256sum -- * > checksums.txt)

      - name: Create GitHub Release
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh release create "$GITHUB_REF_NAME" --generate-notes dist/*
```

- [ ] **Step 2: Delete the old workflow**

```bash
git rm .github/workflows/docker.yml
```

- [ ] **Step 3: Validate the workflow file**

Run: `actionlint .github/workflows/publish.yml` if `command -v actionlint` succeeds; otherwise at minimum `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/publish.yml'))" && echo OK`.
Expected: no errors / `OK`.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/publish.yml
git commit -m "feat(ci)!: publish public images to ghcr + release binaries, drop ECR push"
```

---

### Task 4: CLAUDE.md gotcha + feedback-loop wording (instancez)

**Files:**
- Modify: `CLAUDE.md` (gotchas bullet "The Lambda image is per-arch", feedback_loop sentence about the Docker build job)

- [ ] **Step 1: Update the Lambda gotcha**

Replace the bullet:

> - **The Lambda image is per-arch.** `Dockerfile.lambda` is built once per platform (`-lambda-amd64`, `-lambda-arm64`) because Lambda rejects multi-arch manifest lists. Don't "fix" this back to a manifest list.

with:

> - **Lambda images and registries.** AWS Lambda only pulls single-arch images from private ECR — never manifest lists, never ghcr. The public `ghcr.io/instancez/instancez:<ver>-lambda` tag built by `publish.yml` is intentionally a multi-arch manifest list (nothing feeds it to Lambda). The per-arch ECR image Lambda actually runs (`instancez/<env>:<sha>-lambda-arm64`) is built from instancez source by instancez-platform's `docker-services.yml`. Don't "simplify" that ECR build into a manifest list.

- [ ] **Step 2: Update the feedback_loop sentence**

In `<feedback_loop>`, replace:

> CI runs the same three jobs (`Go unit tests`, `Go integration tests`, `Dashboard tests`) in `.github/workflows/ci.yml` and gates the Docker build job on them.

with:

> CI runs the same three jobs (`Go unit tests`, `Go integration tests`, `Dashboard tests`) in `.github/workflows/ci.yml`; `.github/workflows/publish.yml` (ghcr images + release binaries) gates on them.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md for ghcr publishing and platform-side ECR build"
```

---

### Task 5: `build-backend` job in docker-services.yml (instancez-platform v2)

**Repo:** `/home/saedx1/repos/instancez-platform/v2` — work and commit on branch `v2`. Do not touch the uncommitted WIP in `deploy-infrastructure.yml` / `destroy-infrastructure.yml`.

**Files:**
- Modify: `.github/workflows/docker-services.yml` (outputs block ~line 39-41, new job after `build-data` ~line 265)

- [ ] **Step 1: Add the workflow_call output**

In the `on.workflow_call.outputs` block, after `lifecycle_image_uri` (lines 39–41), add:

```yaml
      backend_image_uri:
        description: "Instancez backend Lambda image URI (private ECR, arm64)"
        value: ${{ jobs.build-backend.outputs.backend_uri }}
```

- [ ] **Step 2: Add the `build-backend` job**

Insert after the `build-data` job (after line 265, before `build-ai`). Unlike the other jobs it has no paths-filter entry — the source lives in the instancez repo, so "did it change" is answered by the ECR tag-exists check keyed on the instancez commit SHA:

```yaml
  build-backend:
    name: Build Instancez Backend
    runs-on: ubuntu-24.04-arm
    environment: ${{ inputs.environment }}
    permissions:
      contents: read
      id-token: write
    outputs:
      backend_uri: ${{ steps.set-uri.outputs.uri }}
    steps:
      - name: Checkout instancez backend
        uses: actions/checkout@v4
        with:
          repository: instancez/instancez
          path: instancez-src
          ref: main
          # instancez/instancez is private; read-only deploy key (registered on
          # instancez) stored as the INSTANCEZ_DEPLOY_KEY secret on this repo.
          # Drop the ssh-key once the repo goes public.
          ssh-key: ${{ secrets.INSTANCEZ_DEPLOY_KEY }}
      - name: Resolve instancez SHA
        id: vars
        run: |
          FULL_SHA=$(git -C instancez-src rev-parse HEAD)
          echo "sha=${FULL_SHA}" >> $GITHUB_OUTPUT
          echo "short_sha=${FULL_SHA:0:7}" >> $GITHUB_OUTPUT
      - name: Configure AWS credentials
        uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: ${{ secrets.AWS_ROLE_ARN }}
          aws-region: ${{ env.AWS_REGION }}
      - name: Login to Amazon ECR
        id: login-ecr
        uses: aws-actions/amazon-ecr-login@v2
      - name: Determine environment
        id: env
        run: |
          echo "env=${{ inputs.environment }}" >> $GITHUB_OUTPUT
      # Skip when this instancez commit was already pushed (the tag is keyed
      # on the instancez SHA, not this repo's SHA).
      - name: Check if tag exists
        id: check-tag
        run: |
          if aws ecr describe-images \
            --repository-name instancez/${{ steps.env.outputs.env }} \
            --image-ids imageTag=${{ steps.vars.outputs.short_sha }}-lambda-arm64 \
            --region ${{ env.AWS_REGION }} \
            --query 'imageDetails[0].imageTags[0]' \
            --output text 2>/dev/null; then
            echo "exists=true" >> $GITHUB_OUTPUT
            echo "Tag ${{ steps.vars.outputs.short_sha }}-lambda-arm64 already exists in ECR"
          else
            echo "exists=false" >> $GITHUB_OUTPUT
            echo "Tag ${{ steps.vars.outputs.short_sha }}-lambda-arm64 does not exist, will build and push"
          fi
      # Only the arm64 Lambda image: the deployer hardcodes ArchitectureArm64
      # (deployer/pkg/lambdamanager/manager.go) and Lambda rejects manifest
      # lists, so this stays single-arch. Standalone/amd64 variants live on
      # ghcr.io/instancez/instancez.
      - name: Build and push Docker image
        if: steps.check-tag.outputs.exists == 'false'
        run: |
          URI="${{ steps.login-ecr.outputs.registry }}/instancez/${{ steps.env.outputs.env }}:${{ steps.vars.outputs.short_sha }}-lambda-arm64"
          docker build \
            --provenance=false \
            --platform linux/arm64 \
            --build-arg VERSION=dev-${{ steps.vars.outputs.short_sha }} \
            --build-arg COMMIT=${{ steps.vars.outputs.sha }} \
            -f instancez-src/Dockerfile.lambda \
            -t "$URI" \
            instancez-src
          docker push "$URI"
      - name: Set service URI
        id: set-uri
        run: |
          echo "uri=${{ steps.login-ecr.outputs.registry }}/instancez/${{ steps.env.outputs.env }}:${{ steps.vars.outputs.short_sha }}-lambda-arm64" >> $GITHUB_OUTPUT
```

- [ ] **Step 3: Validate**

Run: `actionlint .github/workflows/docker-services.yml` if available; otherwise `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/docker-services.yml'))" && echo OK`.
Expected: no errors / `OK`.

- [ ] **Step 4: Commit (on v2, this file only)**

```bash
git add .github/workflows/docker-services.yml
git commit -m "feat(ci): build instancez backend lambda image in docker-services"
```

---

### Task 6: Wire `images:backend_uri` in deploy-infrastructure.yml (instancez-platform v2)

**Files:**
- Modify: `.github/workflows/deploy-infrastructure.yml:83-91` ("Set Pulumi config for image URIs" step)

This file has **uncommitted user WIP** (state-bucket rename on line 72). Stash it, make our edit, commit, then restore the WIP.

- [ ] **Step 1: Stash the WIP**

```bash
git stash push -m "wip: pulumi state bucket rename" -- .github/workflows/deploy-infrastructure.yml
```

- [ ] **Step 2: Add the config line**

In the "Set Pulumi config for image URIs" step, after the `images:lifecycle_uri` line, add:

```yaml
          pulumi config set --stack ${{ github.event.inputs.environment }} images:backend_uri "${{ needs.build-services.outputs.backend_image_uri }}"
```

(Same indentation as the surrounding `pulumi config set` lines. This replaces hand-editing `images:backend_uri` in `Pulumi.<env>.yaml`.)

- [ ] **Step 3: Validate and commit**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/deploy-infrastructure.yml'))" && echo OK
git add .github/workflows/deploy-infrastructure.yml
git commit -m "feat(ci): set images:backend_uri from docker-services output"
```

- [ ] **Step 4: Restore the WIP**

```bash
git stash pop
git status --short   # expect: M .github/workflows/deploy-infrastructure.yml (bucket rename), M destroy-infrastructure.yml
git diff .github/workflows/deploy-infrastructure.yml   # expect: only the instancez-coder→instancez-platform bucket line
```

The hunks are ~15 lines apart so the pop should apply cleanly; if it conflicts, resolve by keeping both the bucket rename (line 72) and the new `backend_uri` line.

---

### Task 7: Final verification (both repos)

- [ ] **Step 1: instancez full loop**

```sh
cd /home/saedx1/repos/instancez/main
go build ./...
go test -race ./...
```

Expected: PASS. (No integration-tagged or dashboard code was touched; CLAUDE.md's integration/dashboard gates don't apply, but run them if anything unexpected was modified.)

- [ ] **Step 2: Confirm no stray ECR/AWS references remain in instancez workflows**

Run: `grep -rn "ecr\|AWS_" .github/workflows/` — expected: no matches (ci.yml has none; docker.yml is gone).

- [ ] **Step 3: Platform worktree sanity**

```sh
cd /home/saedx1/repos/instancez-platform/v2
git log --oneline -3        # the two new commits on v2
git status --short          # only the pre-existing WIP files remain modified
```

- [ ] **Step 4: Report the manual follow-ups to the user**

Not automatable — list verbatim in the final summary:
1. After the first `publish.yml` push: set `ghcr.io/instancez/instancez` package visibility to **public** (GitHub → org packages → instancez → Package settings → Change visibility).
2. When the instancez repo goes public: remove `ssh-key: ${{ secrets.INSTANCEZ_DEPLOY_KEY }}` from the instancez checkouts in `docker-services.yml` (build-data + build-backend) and `pr-validation.yml`, then delete the deploy key + secret.
3. Push of instancez `main` and the `v2` branch of instancez-platform is the user's call (per CLAUDE.md the local loop is green before push).
