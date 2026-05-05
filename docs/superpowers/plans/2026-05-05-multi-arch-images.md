# Multi-Arch Container Images Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish `Dockerfile` and `Dockerfile.lambda` images as multi-arch manifest lists supporting `linux/amd64` and `linux/arm64`, so a single tag works on Apple Silicon developer machines and on arm64 production targets (Graviton, arm64 Lambda).

**Architecture:** Cross-compile the Go binary on the amd64 builder using Buildx's `$BUILDPLATFORM` / `$TARGETARCH` build args (no QEMU for the heavy step). The runtime stage runs target-arch `apk add ca-certificates` under QEMU emulation, which is small enough to be negligible. CI publishes a single manifest list per tag via `docker/build-push-action`.

**Tech Stack:** Docker Buildx, GitHub Actions (`docker/setup-qemu-action@v3`, `docker/setup-buildx-action@v3`, `docker/build-push-action@v6`), AWS ECR, Go cross-compilation.

---

## File Structure

Files modified by this plan:

- `Dockerfile` — server image; builder stage gets cross-compile args, runtime stage unchanged.
- `Dockerfile.lambda` — Lambda image; same change as `Dockerfile`. Runtime stage already pulls a multi-arch lambda-adapter image, no change needed there.
- `.github/workflows/docker.yml` — replace per-image `docker build`/`docker push` pairs with `docker/build-push-action@v6` invocations using `platforms: linux/amd64,linux/arm64`. Add `setup-qemu-action` and `setup-buildx-action` steps.

No new files. The spec at `docs/superpowers/specs/2026-05-05-multi-arch-images-design.md` is the design reference.

---

## Task 1: Cross-compile builder stage in `Dockerfile`

**Files:**
- Modify: `Dockerfile`

- [ ] **Step 1: Update `Dockerfile` to cross-compile via Buildx args**

Replace the current contents of `Dockerfile` with:

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETOS TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -o /ultrabase ./cmd/ultrabase

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /ultrabase /usr/local/bin/ultrabase
WORKDIR /app
CMD ["ultrabase", "serve"]
```

The two changes vs. today: pin the builder to `--platform=$BUILDPLATFORM` so the `go build` always runs natively on the host arch, and pass `GOOS=$TARGETOS GOARCH=$TARGETARCH` to `go build` so it emits binaries for the target arch.

- [ ] **Step 2: Confirm the existing single-arch build still works**

The new Dockerfile must remain a drop-in replacement for the current single-arch CI build. Verify by building for the host (amd64) only — this exercises the new `ARG`s with empty values, which should still produce a working amd64 binary because `go build` with empty `GOOS`/`GOARCH` defaults to the host.

Run:

```bash
docker buildx build --platform linux/amd64 --load -t ultrabase:test-amd64 -f Dockerfile .
docker run --rm ultrabase:test-amd64 ultrabase --help
```

Expected: build succeeds; `ultrabase --help` prints the CLI help text.

- [ ] **Step 3: Cross-build for arm64 to confirm the cross-compile path**

Run:

```bash
docker buildx build --platform linux/arm64 -f Dockerfile -o type=cacheonly .
```

Expected: build succeeds. The `-o type=cacheonly` avoids needing a registry or a loadable single-arch image; the build just has to complete. If the host has no QEMU registered, this step's runtime stage (`RUN apk add ...`) may fail — if so, run `docker run --privileged --rm tonistiigi/binfmt --install arm64` once and retry. CI will register QEMU via `setup-qemu-action`.

- [ ] **Step 4: Build both platforms together to confirm manifest assembly**

Run:

```bash
docker buildx build --platform linux/amd64,linux/arm64 -f Dockerfile -o type=cacheonly .
```

Expected: build succeeds for both platforms. This is the same invocation `build-push-action` will perform in CI (minus `--push`).

- [ ] **Step 5: Commit**

```bash
git add Dockerfile
git commit -m "Cross-compile server Dockerfile via Buildx TARGETARCH"
```

---

## Task 2: Cross-compile builder stage in `Dockerfile.lambda`

**Files:**
- Modify: `Dockerfile.lambda`

- [ ] **Step 1: Update `Dockerfile.lambda` to cross-compile via Buildx args**

Replace the current contents of `Dockerfile.lambda` with:

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETOS TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -o /ultrabase ./cmd/ultrabase

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /ultrabase /usr/local/bin/ultrabase
COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:0.9.1 /lambda-adapter /opt/extensions/lambda-adapter
WORKDIR /app
EXPOSE 8080
CMD ["ultrabase", "serve", "--data", "--migrate"]
```

The lambda-adapter image is already a multi-arch manifest at the registry, so Buildx pulls the matching arch automatically per target build — `COPY --from=` needs no `--platform` annotation.

- [ ] **Step 2: Confirm amd64 build still works**

Run:

```bash
docker buildx build --platform linux/amd64 --load -t ultrabase:test-amd64-lambda -f Dockerfile.lambda .
docker run --rm --entrypoint /bin/sh ultrabase:test-amd64-lambda -c \
  'test -x /opt/extensions/lambda-adapter && /usr/local/bin/ultrabase --help'
```

Expected: build succeeds; the adapter binary exists and is executable, and `ultrabase --help` prints help text.

- [ ] **Step 3: Cross-build for arm64**

Run:

```bash
docker buildx build --platform linux/arm64 -f Dockerfile.lambda -o type=cacheonly .
```

Expected: build succeeds.

- [ ] **Step 4: Build both platforms together**

Run:

```bash
docker buildx build --platform linux/amd64,linux/arm64 -f Dockerfile.lambda -o type=cacheonly .
```

Expected: build succeeds for both platforms.

- [ ] **Step 5: Commit**

```bash
git add Dockerfile.lambda
git commit -m "Cross-compile Lambda Dockerfile via Buildx TARGETARCH"
```

---

## Task 3: Switch CI workflow to multi-arch Buildx push

**Files:**
- Modify: `.github/workflows/docker.yml`

- [ ] **Step 1: Replace the build steps in the `build` job**

In `.github/workflows/docker.yml`, locate the `build` job's `steps:` section. The current sequence after `Set environment` is:

```yaml
      - name: Build and push regular image
        run: |
          IMAGE=${{ steps.login-ecr.outputs.registry }}/ultrabase/${{ steps.env.outputs.env }}:${{ steps.vars.outputs.short_sha }}
          docker build --provenance=false -t $IMAGE .
          docker push $IMAGE

      - name: Build and push Lambda image
        run: |
          IMAGE=${{ steps.login-ecr.outputs.registry }}/ultrabase/${{ steps.env.outputs.env }}:${{ steps.vars.outputs.short_sha }}-lambda
          docker build --provenance=false -f Dockerfile.lambda -t $IMAGE .
          docker push $IMAGE
```

Replace those two steps with:

```yaml
      - name: Set up QEMU
        uses: docker/setup-qemu-action@v3
        with:
          platforms: arm64

      - name: Set up Buildx
        uses: docker/setup-buildx-action@v3

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

The setup steps must come *after* `Set environment` and *before* the new build steps — they need ECR login (already done above) and the `short_sha` / `env` outputs (set above).

The `Set image URIs` step at the end of the job is unchanged: it still emits a single tag per image, which is now a manifest list rather than a single-arch image. Consumers (Lambda functions, ECS task definitions) read the tag the same way and select the arch from the manifest.

- [ ] **Step 2: Validate the workflow YAML parses**

Run:

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/docker.yml'))" && echo OK
```

Expected: `OK`.

- [ ] **Step 3: Sanity-check with `actionlint` if available**

Run:

```bash
command -v actionlint >/dev/null && actionlint .github/workflows/docker.yml || echo "actionlint not installed; skipping"
```

Expected: either `actionlint` reports no issues, or the "skipping" message. Don't install actionlint solely for this step.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/docker.yml
git commit -m "Publish multi-arch manifest lists for both images"
```

---

## Task 4: Verify on first workflow run

This task runs *after* the changes are merged or the workflow is otherwise dispatched. It confirms the published artifacts are correct manifest lists. Do not mark complete until verified.

**Files:** none.

- [ ] **Step 1: Trigger a workflow run for the `dev` environment**

If the change is on a branch that doesn't auto-trigger `docker.yml`, dispatch it manually:

```bash
gh workflow run docker.yml -f environment=dev
gh run watch
```

Expected: the `build` job completes successfully.

- [ ] **Step 2: Inspect the resulting manifest for the regular image**

From the workflow run logs, copy the regular `image_uri` output (e.g.
`<acct>.dkr.ecr.us-east-1.amazonaws.com/ultrabase/dev:<sha>`). Run:

```bash
docker buildx imagetools inspect <image_uri>
```

Expected: output lists two manifests under `Manifests:` with `Platform: linux/amd64` and `Platform: linux/arm64`. There should be no `unknown/unknown` entries (that's why `provenance: false` is set).

- [ ] **Step 3: Inspect the Lambda image manifest**

Same check for the lambda variant:

```bash
docker buildx imagetools inspect <lambda_image_uri>
```

Expected: same `linux/amd64` and `linux/arm64` entries.

- [ ] **Step 4: Confirm an existing amd64 deployment is unaffected**

Whatever currently consumes `lambda_image_uri` (Lambda function or other consumer of the Lambda image) should redeploy against the new tag without changes. Confirm it pulls the amd64 layer and runs:

```bash
aws lambda update-function-code --function-name <fn> --image-uri <lambda_image_uri>
aws lambda invoke --function-name <fn> /tmp/out.json && cat /tmp/out.json
```

Or whatever the equivalent invocation is for the consumer. Expected: function works as before.

If there is no existing arm64 consumer to test, that's fine — the spec explicitly states migrating any function to arm64 is out of scope. The verification here is only that the new manifest list does not regress the existing amd64 path.

---

## Notes for the implementer

- **Why `--platform=$BUILDPLATFORM` only on the builder stage?** The runtime stage must be the target arch — that's what gets shipped. The builder stage is throwaway, so we lock it to the runner's arch and let `go build` cross-compile.
- **Why pass `TARGETOS` and not just `TARGETARCH`?** `TARGETOS` is `linux` in every case for these images, but using both args matches the canonical Go cross-compile pattern from the upstream Buildx docs and makes the intent obvious.
- **Why `setup-qemu-action` if Go cross-compiles?** Only the runtime stage's `RUN apk add --no-cache ca-certificates` runs target-arch binaries. That's a few seconds of emulated work, not the multi-minute compile a fully-emulated Go build would be.
- **Why `-o type=cacheonly` for arm64 verification?** Multi-platform builds can't `--load` into the local Docker daemon (it doesn't support manifest lists); `cacheonly` lets us confirm the build succeeds without a registry round-trip.
