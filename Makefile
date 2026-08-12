SQLC_VERSION := v1.31.1

# Stamped into the binary so a running instance can identify itself (see
# internal/buildinfo). Release builds override this with the Git tag.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/shawn-bluce/renderbin/backend/internal/buildinfo.Version=$(VERSION)

.PHONY: dev dev-api dev-web build-web build-api build check sqlc test tidy

dev:
	@echo "backend on :8080, frontend on :5173 (first run: create the admin account on the welcome page) — Ctrl+C to stop both"
	@trap 'kill 0' EXIT INT TERM; \
	$(MAKE) dev-api & \
	$(MAKE) dev-web & \
	wait

dev-api:
	cd backend && go run ./cmd/server

dev-web:
	cd web && pnpm run dev

build-web:
	cd web && pnpm run build
	find backend/internal/web/dist -mindepth 1 -not -name '.gitignore' -delete
	cp -r web/build/. backend/internal/web/dist/

build-api:
	cd backend && go build -ldflags "$(LDFLAGS)" -o bin/server ./cmd/server

build: build-web build-api

# Must stay a superset of what .github/workflows/ci.yml runs on a PR --
# `pnpm run lint` (prettier --check + eslint) belongs here for that reason. It
# was missing, so a contributor could get `make check` green, be told by the
# README that this is the bar, and still fail CI on formatting alone.
check:
	cd web && pnpm run lint
	cd web && pnpm run check
	cd backend && gofmt -l . | tee /dev/stderr | (! read)
	cd backend && go vet ./...

sqlc:
	cd backend && go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate

test:
	cd web && pnpm run test
	cd backend && go test ./... -count=1

tidy:
	cd backend && go mod tidy
