PNPM ?= npx --yes pnpm@12.11.2
GO ?= go
GOFMT ?= gofmt
E2E_ARGS ?= --project=chromium
E2E_INSTALL_ARGS ?= chromium

.PHONY: help install dev build build-dashboard build-go test test-go test-go-race test-js check-docs typecheck format format-go format-check format-check-go lint lint-go lint-fix lint-fix-format generate generate-check test-e2e smoke-production clean

help:
	@echo "make install | install-dsh | install-project-runtime | install-document-runtime | install-layerfs | package-linux | dev | build | test | test-dsh | test-layerfs | test-project-env | test-go-race | check-docs | typecheck | format | format-check | lint | lint-fix | lint-fix-format | generate | generate-check | test-e2e | smoke-production"

install:
	$(PNPM) install --frozen-lockfile
	$(GO) mod download

.PHONY: install-dsh install-project-runtime install-document-runtime install-layerfs build-sandbox test-layerfs test-dsh test-project-env
.PHONY: package-linux smoke-bundle
package-linux: package-daemon

smoke-bundle:
	node scripts/smoke-bundle.mjs

install-dsh:
	node tools/dsh-runtime/install.mjs

install-project-runtime:
	$(PNPM) --dir tools/project-runtime install --frozen-lockfile --ignore-scripts

install-document-runtime:
	node tools/document-runtime/install.mjs

install-layerfs:
	node tools/layerfs-runtime/install.mjs

build-sandbox:
	CGO_ENABLED=0 $(GO) build -trimpath -o dist/camp-sandbox ./apps/server/cmd/camp-sandbox

test-layerfs: build-sandbox
	node --test tools/layerfs-runtime/test/*.test.mjs tools/dsh-runtime/camp-tools/*.test.mjs
	CAMP_TEST_LAYERFS_RUNTIME="$(CURDIR)/tools/layerfs-runtime" CAMP_TEST_DSH_RUNTIME="$(CURDIR)/tools/dsh-runtime" $(GO) test -race ./apps/server/internal/layerfs ./apps/server/internal/runnertools ./apps/server/internal/sandboxexec -count=1

test-project-env:
	CAMP_PROJECT_ENV_TEST=1 $(GO) test -race ./apps/server/internal/projectenv ./apps/server/internal/execution ./apps/server/internal/resources

test-dsh: build-sandbox
	CAMP_TEST_LAYERFS_RUNTIME="$(CURDIR)/tools/layerfs-runtime" CAMP_TEST_SANDBOX_HELPER="$(CURDIR)/dist/camp-sandbox" CAMP_TEST_DSH_RUNTIME="$(CURDIR)/tools/dsh-runtime" $(GO) test -race ./apps/server/internal/runs -run Integration -count=1 -v

dev:
	$(PNPM) run dev

build:
	$(PNPM) run build:production

build-dashboard:
	$(PNPM) run build

build-go:
	$(GO) build -o dist/server-dev ./apps/server/cmd/server

test: test-go test-js

test-js:
	$(PNPM) run test

test-go: generate-check
	$(GO) test ./...

test-go-race: generate-check
	$(GO) test -race ./...

check-docs:
	$(PNPM) run check:docs

generate:
	$(GO) run ./tools/sqlgen -go "$(GO)" generate

generate-check:
	$(GO) run ./tools/sqlgen -go "$(GO)" check

typecheck:
	$(PNPM) run typecheck

format: format-go
	$(PNPM) run format

format-go:
	$(GOFMT) -w *.go internal apps/server tools

format-check: format-check-go
	$(PNPM) run format:check

format-check-go:
	@set -eu; files="$$($(GOFMT) -l *.go internal apps/server tools)"; \
		if [ -n "$$files" ]; then printf '%s\n' "$$files"; exit 1; fi

lint: lint-go
	$(PNPM) run lint

lint-go:
	$(GO) vet ./...

lint-fix: format-go
	$(PNPM) run lint:fix

lint-fix-format: format-go
	$(PNPM) run lint:fix:format

test-e2e:
	$(PNPM) exec playwright install $(E2E_INSTALL_ARGS)
	$(PNPM) run test:e2e $(E2E_ARGS)

smoke-production:
	$(PNPM) run smoke:production

clean:
	rm -rf dist apps/dashboard/dist apps/server/internal/webui/dist coverage test-results playwright-report

.PHONY: remote-dev test-remote test-remote-native package-daemon build-server-image
remote-dev:
	node scripts/remote.mjs up

test-remote:
	node scripts/remote.mjs test

test-remote-native:
	node scripts/remote.mjs native

package-daemon: build-dashboard build-sandbox
	node scripts/package-daemon.mjs

build-server-image:
	docker build --target server -t camp-server:local .
