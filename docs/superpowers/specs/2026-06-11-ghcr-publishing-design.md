# Design: Public ghcr publishing for instancez + ECR backend build in instancez-platform

Date: 2026-06-11
Repos affected: `instancez/instancez` (this repo), `instancez-platform` (v2 branch only)

## Goal

instancez stops pushing images to the private AWS ECR registry. Instead it
publishes public images to GitHub Container Registry (ghcr.io) and, on release
tags, cross-platform `inz` binaries to GitHub Releases. instancez-platform
takes over producing the private ECR image that its deployer needs for Lambda,
building it from instancez source as part of its existing
`docker-services.yml` service-build workflow.

Context that shapes the design:

- The platform deployer consumes exactly one backend image:
  `instancez/<env>:<sha>-lambda-arm64` in private ECR (Lambda requires private
  ECR and the deployer hardcodes `ArchitectureArm64`;
  `deployer/pkg/lambdamanager/manager.go:79`). Nothing in the platform
  consumes the standalone or amd64-lambda variants.
- Lambda rejects multi-arch manifest lists, so the ECR lambda image stays
  single-arch. ghcr images have no such constraint and are multi-platform.
- `instancez/instancez` is currently private; the platform clones it with the
  `INSTANCEZ_DEPLOY_KEY` deploy key. The repo will go public later — keep the
  key until then.
- The `inz` binary embeds the dashboard SPA (`dashboard/embed.go`,
  `//go:embed all:dist`), so binary builds need `npm run build` first. The
  dist is arch-independent; one dashboard build serves all binary targets.

## instancez changes

### 1. Version/commit injection

- `internal/cli/root.go`: add `var commit = "unknown"` next to the existing
  `var version = "dev"`. `inz version` prints both, e.g.
  `instancez v1.2.3 (abc1234)`.
- `Dockerfile` and `Dockerfile.lambda`: add `ARG VERSION=dev` and
  `ARG COMMIT=unknown`, threaded into `go build` as
  `-ldflags "-X github.com/instancez/instancez/internal/cli.version=$VERSION -X github.com/instancez/instancez/internal/cli.commit=$COMMIT"`.

### 2. `.github/workflows/docker.yml` → `.github/workflows/publish.yml`

**Triggers:**

- `push` to `main` → dev images
- `push` of tags `v*` → release images + binaries + GitHub Release
- `workflow_dispatch` from any ref → dev images

**Kept:** the `check-ci` job (waits for the three CI checks on the commit) and
the fallback `test` job that runs `ci.yml` when no checks exist. Images and
binaries publish only after tests pass.

**Removed:** all AWS/ECR steps, the `environment` input, `workflow_call`
trigger and outputs (nothing consumes them).

**Images job:** login to ghcr.io via `docker/login-action` with
`GITHUB_TOKEN` (`permissions: packages: write`). QEMU + buildx. Version
resolution:

- tag `v1.2.3` → `VERSION=1.2.3`
- otherwise → `VERSION=dev-<short-sha>`

Pushed tags on `ghcr.io/instancez/instancez` (no `latest`, no env split):

| Image | Dockerfile | Platforms | Tags |
|---|---|---|---|
| standalone | `Dockerfile` | linux/amd64, linux/arm64 | `<VERSION>`, `<VERSION>-standalone` |
| lambda | `Dockerfile.lambda` | linux/amd64, linux/arm64 | `<VERSION>-lambda` |

The unsuffixed tag is the standalone image (standalone is the default
flavor). OCI labels (`org.opencontainers.image.source/revision/version`) via
`docker/metadata-action`; `VERSION`/`COMMIT` passed as build args.

**Binaries job (tag pushes only):** `permissions: contents: write`.

1. Build dashboard once: node 22, `npm ci && npm run build` in `dashboard/`
   (populates `dashboard/dist` for `//go:embed`).
2. Loop GOOS ∈ {linux, darwin, windows} × GOARCH ∈ {amd64, arm64},
   `CGO_ENABLED=0`, same ldflags injection. Output name `inz` (`inz.exe` on
   windows).
3. Package `inz_<VERSION>_<os>_<arch>.tar.gz` (`.zip` for windows), generate
   `checksums.txt` (sha256).
4. `gh release create <tag> --generate-notes` + upload archives and
   checksums.

### 3. CLAUDE.md

Update the "Lambda image is per-arch" gotcha: the per-arch constraint applies
to the ECR image consumed by Lambda, which is now built in
instancez-platform's `docker-services.yml`. The ghcr `-lambda` tag is a
multi-arch manifest list by design (nothing pulls it into Lambda directly).
Update the architecture/commands sections if they mention ECR publishing.

## instancez-platform changes (v2 branch only)

### 4. `docker-services.yml`: new `build-backend` job

Follows the shape of the existing service jobs (`ubuntu-24.04-arm`, AWS OIDC,
ECR login) with these differences:

- **No paths-filter entry.** The source lives in the instancez repo, so
  change detection is the ECR tag-exists check keyed on the instancez commit:
  checkout `instancez/instancez@main` (deploy key, as `build-data` already
  does), resolve its short SHA, and skip the build when
  `instancez/<env>:<sha>-lambda-arm64` already exists.
- **Build only the lambda-arm64 image** — native arm64 build, no QEMU:
  `docker build -f Dockerfile.lambda --platform linux/arm64` from the
  instancez checkout, with `VERSION=dev-<sha>` / `COMMIT=<full sha>` build
  args, pushed to `instancez/<env>:<sha>-lambda-arm64` (the existing ECR repo
  and tag scheme that `images:backend_uri` already points at).
- **Output** `backend_uri` (the lambda-arm64 URI) whether built or skipped;
  surface it as a `workflow_call` output `backend_image_uri`.

### 5. `deploy-infrastructure.yml`

Add to the "Set Pulumi config for image URIs" step:

```sh
pulumi config set --stack <env> images:backend_uri "${{ needs.build-services.outputs.backend_image_uri }}"
```

This replaces the current hand-edited `images:backend_uri` value in
`Pulumi.<env>.yaml`.

## Out of scope / follow-ups

- **ghcr package visibility:** the first push creates a *private* ghcr
  package; flipping `ghcr.io/instancez/instancez` to public is a one-time
  manual step in GitHub package settings.
- **Deploy key removal:** when the instancez repo goes public, drop
  `ssh-key: ${{ secrets.INSTANCEZ_DEPLOY_KEY }}` from the instancez checkouts
  in `docker-services.yml` and `pr-validation.yml`.
- Pinning the platform's backend build to a release tag instead of `main`
  (e.g. a workflow input) can be added later if prod needs it.

## Testing

- instancez: `go build ./...`, `go test -race ./...` (the `internal/cli`
  change), local `docker build` of both Dockerfiles with and without
  `VERSION`/`COMMIT` args, `actionlint` on the workflow if available.
- instancez-platform: `actionlint`; the workflow itself is validated by the
  next dev deploy run.
