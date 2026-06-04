# Top-level build wrapper. Raw `go build` stays fast for backend-only
# iteration; `make build` chains the npm dashboard build so the resulting
# binary serves the SPA at /dashboard out of //go:embed.

DASHBOARD_DIR := dashboard
DASHBOARD_DIST := $(DASHBOARD_DIR)/dist
NODE_MODULES_STAMP := $(DASHBOARD_DIR)/node_modules/.package-lock.json

PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -X github.com/saedx1/ultrabase/internal/cli.version=$(VERSION)

.PHONY: build build-go build-dashboard test test-go test-integration test-dashboard clean help

help:
	@echo "make build              — dashboard SPA + go binary, installed to $(BINDIR)"
	@echo "make build-go           — go binary only (no dashboard rebuild)"
	@echo "make build-dashboard    — npm build for dashboard/dist"
	@echo "make test               — go unit + integration + dashboard vitest"
	@echo "make test-go            — go unit tests"
	@echo "make test-integration   — go integration tests (requires Docker)"
	@echo "make test-dashboard     — dashboard vitest"
	@echo "make clean              — remove ultra binary + dashboard/dist contents"

build: build-dashboard build-go
	@mkdir -p $(BINDIR)
	mv ultra $(BINDIR)/ultra
	@echo "Installed ultra → $(BINDIR)/ultra"

build-go:
	go build -ldflags "$(LDFLAGS)" -o ultra ./cmd/ultra

build-dashboard: $(NODE_MODULES_STAMP)
	cd $(DASHBOARD_DIR) && npm run build

$(NODE_MODULES_STAMP): $(DASHBOARD_DIR)/package.json $(DASHBOARD_DIR)/package-lock.json
	cd $(DASHBOARD_DIR) && npm ci
	@touch $(NODE_MODULES_STAMP)

test: test-go test-integration test-dashboard

test-go:
	go test -race ./...

test-integration:
	go test -tags=integration -race ./...

test-dashboard: $(NODE_MODULES_STAMP)
	cd $(DASHBOARD_DIR) && npm test

clean:
	rm -f ultra
	find $(DASHBOARD_DIST) -mindepth 1 ! -name .gitkeep -delete 2>/dev/null || true
	@touch $(DASHBOARD_DIST)/.gitkeep
