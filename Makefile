APP_NAME ?= wetpaint

# try getconf (linux / macos), getconf (BSD), nproc, then fallback to 1
NPROCS := $(shell getconf _NPROCESSORS_ONLN 2>/dev/null || getconf NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo 1)
MAKEFLAGS += --jobs=$(NPROCS)

# The Go module lives in server/, the TypeScript client in client/.
GO_DIR := server
CLIENT_DIR := client

.PHONY: all build generate test test-server test-client lint lint-fix mod-tidy vet golangci-lint \
	staticcheck modernize govulncheck typos typecheck gorelease-check client-deps update clean help

all: build

build:
	GITHUB_REPOSITORY=local goreleaser build --clean --snapshot --single-target

generate:
	cd $(GO_DIR) && go generate ./...

test: test-server test-client

test-server:
	cd $(GO_DIR) && go test -race ./...

test-client: client-deps
	cd $(CLIENT_DIR) && bun run test

lint: mod-tidy vet staticcheck golangci-lint modernize govulncheck typos typecheck gorelease-check

lint-fix:
	cd $(GO_DIR) && go mod tidy
	cd $(GO_DIR) && golangci-lint run --fix
	cd $(GO_DIR) && go fix ./...
	typos -w
	$(MAKE) lint

mod-tidy:
	cd $(GO_DIR) && go mod tidy -diff

vet:
	cd $(GO_DIR) && go vet ./...

golangci-lint:
	cd $(GO_DIR) && golangci-lint run

staticcheck:
	cd $(GO_DIR) && go run honnef.co/go/tools/cmd/staticcheck@latest ./...

modernize:
	cd $(GO_DIR) && go fix -diff ./...

govulncheck:
	cd $(GO_DIR) && go run golang.org/x/vuln/cmd/govulncheck@latest ./...

typos:
	typos

typecheck: client-deps
	cd $(CLIENT_DIR) && bun run typecheck

gorelease-check:
	goreleaser check

client-deps:
	cd $(CLIENT_DIR) && bun install --frozen-lockfile

update:
	cd $(GO_DIR) && go get -v -u ./...
	cd $(GO_DIR) && go mod tidy
	cd $(CLIENT_DIR) && bun update
	$(MAKE) test

clean:
	rm -rf dist/ $(CLIENT_DIR)/dist/ $(GO_DIR)/cmd/server/kodata/

help:
	@echo "make           # Build $(APP_NAME) (server binary with the client in kodata)"
	@echo "make test      # Run server and client tests"
	@echo "make generate  # Generate code"
	@echo "make lint-fix  # Run linters and try fix issues"
	@echo "make lint      # Run linters"
	@echo "make update    # Update dependencies"
	@echo "make clean     # Remove built app"
