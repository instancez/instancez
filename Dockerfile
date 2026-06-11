# syntax=docker/dockerfile:1.7

# WITH_DASHBOARD=true (default) builds the dashboard SPA in a node stage
# and embeds it into the Go binary via //go:embed. WITH_DASHBOARD=false
# uses a stub dist/ so the binary builds without Node and serves no SPA
# (dashboard route returns 404; auth/REST/storage are unaffected).
ARG WITH_DASHBOARD=true

# --- dashboard build stages — one is selected by WITH_DASHBOARD ---
FROM --platform=$BUILDPLATFORM node:22-alpine AS dashboard-true
WORKDIR /src/dashboard
COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm ci
COPY dashboard/ ./
RUN npm run build && mkdir -p /out && mv dist /out/dist

FROM --platform=$BUILDPLATFORM alpine:3.21 AS dashboard-false
RUN mkdir -p /out/dist && touch /out/dist/.gitkeep

FROM dashboard-${WITH_DASHBOARD} AS dashboard

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

FROM alpine:3.21
# nodejs runs code-function workers (serve + dev). npm is needed by `inz dev`,
# which builds functions on the fly; serve consumes a pre-built bundle and
# doesn't use npm.
RUN apk add --no-cache ca-certificates nodejs npm
COPY --from=builder /inz /usr/local/bin/inz
WORKDIR /app
CMD ["inz", "serve"]
