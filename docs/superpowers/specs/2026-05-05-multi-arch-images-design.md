# Multi-Arch Container Images (amd64 + arm64)

## Goal

Publish both `Dockerfile` (server) and `Dockerfile.lambda` images as multi-arch
manifest lists supporting `linux/amd64` and `linux/arm64`, so:

- Apple Silicon developers get a native-arch image when they pull a published tag.
- Production workloads can run on arm64 (Graviton EC2/ECS, arm64 Lambda) using the
  same tag consumed today by amd64 workloads.

A single tag (e.g. `ultrabase/dev:<sha>`) resolves to the correct arch
transparently — no per-arch tag suffixes.

## Non-goals

- Adding ARM-native CI runners. Cross-compilation in Go makes them unnecessary.
- Per-arch tag suffixes (`-amd64`, `-arm64`). The manifest list is the source of
  truth.
- Migrating any existing function/service to arm64. That is a separate decision;
  this work only makes the option available.

## Design

### Build strategy

Cross-compile in Go, driven by Buildx's `BUILDPLATFORM` / `TARGETARCH` build args.
The Go builder stage runs natively on the amd64 GitHub runner (`$BUILDPLATFORM`)
and produces a binary for `$TARGETARCH`. The runtime stage uses the target arch.

CGO is already disabled, so Go's native cross-compile path is enough — no
emulator is needed for the heavyweight `go build` step. QEMU is still
registered in CI for the runtime stage's `RUN apk add --no-cache ca-certificates`,
which runs target-arch `apk` for ~1–2s per arch and is negligible.

### Dockerfile changes

Both Dockerfiles change identically in the builder stage:

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -o /ultrabase ./cmd/ultrabase
```

Runtime stages (`alpine:3.21` for `Dockerfile`, `alpine:3.21` + lambda-adapter
copy for `Dockerfile.lambda`) need no `--platform` annotation; Buildx selects
the target arch automatically.

The lambda-adapter line is unchanged:

```dockerfile
COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:0.9.1 \
     /lambda-adapter /opt/extensions/lambda-adapter
```

That image is already a multi-arch manifest, so Buildx pulls the matching arch
per target build.

### Workflow changes (`.github/workflows/docker.yml`)

Replace the `docker build` / `docker push` pairs in the `build` job with
`docker/build-push-action@v6` invocations that emit a manifest list per tag:

```yaml
- uses: docker/setup-qemu-action@v3
  with:
    platforms: arm64
- uses: docker/setup-buildx-action@v3

- name: Build and push regular image (multi-arch)
  uses: docker/build-push-action@v6
  with:
    context: .
    file: Dockerfile
    platforms: linux/amd64,linux/arm64
    push: true
    provenance: false
    tags: ${{ steps.login-ecr.outputs.registry }}/ultrabase/${{ steps.env.outputs.env }}:${{ steps.vars.outputs.short_sha }}

- name: Build and push Lambda image (multi-arch)
  uses: docker/build-push-action@v6
  with:
    context: .
    file: Dockerfile.lambda
    platforms: linux/amd64,linux/arm64
    push: true
    provenance: false
    tags: ${{ steps.login-ecr.outputs.registry }}/ultrabase/${{ steps.env.outputs.env }}:${{ steps.vars.outputs.short_sha }}-lambda
```

Notes:

- `setup-qemu-action` is needed only for the runtime stage's `apk add`. The
  Go build stage uses `--platform=$BUILDPLATFORM` and cross-compiles, so it
  never runs under emulation.
- `provenance: false` is preserved to avoid the extra "unknown/unknown" manifest
  entries that ECR's console flags as confusing.
- The `image_uri` / `lambda_image_uri` job outputs are unchanged — they still
  point at a single tag per image.

The pre-existing `check-ci` and `test` jobs and AWS/ECR login steps are not
touched.

## Consumer impact

- **ECR**: no repository config change. Multi-arch manifest lists are supported.
- **Lambda**: function architecture is set at the function level. An existing
  amd64 function continues to pull the amd64 layer from the manifest. Switching
  a function (or creating a new one) on arm64 becomes possible against the same
  tag.
- **ECS / EC2**: `runtimePlatform.cpuArchitecture` (or AMI choice) selects the
  arch out of the manifest.
- **`docker-compose.dev.yaml`**: unchanged. `build: .` builds for the host arch
  locally, so M-series Macs already get arm64 in dev. Only `docker pull` of a
  published tag benefits from the manifest list.

## Risks & mitigations

- **CI time**: arm64 adds a second `go build`. Estimated +30–60s on the build
  job. Acceptable; no QEMU tax.
- **Future CGO need**: if CGO is ever re-enabled, cross-compile becomes
  non-trivial and we'd need to add `setup-qemu-action` or split into per-arch
  jobs with `docker manifest`. Out of scope here; flagged as a constraint.
- **Manifest-aware tooling**: any tool inspecting the image must understand
  manifest lists. ECR, Docker, Lambda, and ECS all do. Custom scripts that
  call `docker inspect` on a single arch should pass `--platform` if they
  rely on a specific arch.

## Test plan

- CI: workflow run produces a manifest list visible via
  `docker buildx imagetools inspect <image>:<sha>` showing both `linux/amd64`
  and `linux/arm64` entries for both the regular and `-lambda` tags.
- Local pull on Apple Silicon: `docker pull <image>:<sha>` followed by
  `docker image inspect --format '{{.Architecture}}' <image>:<sha>` returns
  `arm64`.
- Existing amd64 deployments (Lambda function or whatever currently consumes
  `lambda_image_uri`) deploy and run unchanged against the new manifest tag.
