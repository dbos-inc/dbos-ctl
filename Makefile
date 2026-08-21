# dbos-cli Makefile. See AGENTS.md for the design.
#
# `generate` regenerates code from the vendored spec and needs no external
# tooling or network beyond the Go module cache — that is what CI runs.
# `spec` re-vendors the spec from a local conductor checkout and additionally
# needs that checkout plus jq. `migrations` does the same for the system
# database migrations, from a local dbos-transact-golang checkout.

CONDUCTOR_DIR ?= $(HOME)/conductor
TRANSACT_DIR  ?= $(HOME)/dbos-transact-golang
SPEC          := internal/api/openapi-3.1.json
MIGRATIONS    := internal/migrations/sql

# Set JUNIT to a path to also write a JUnit XML report (CI publishes it as a
# summary and an artifact). Plain `go test` otherwise, so a local run needs
# nothing built and its output is unchanged.
JUNIT ?=
GOTEST := go test
ifneq ($(JUNIT),)
GOTEST := go tool gotestsum --junitfile $(JUNIT) --format testname --
endif

.PHONY: all generate spec migrations build snapshot test lint tidy

all: generate build

## generate: regenerate the API client + OAuth-gate table from the vendored spec
generate:
	go tool oapi-codegen -config internal/api/oapi-codegen.yaml $(SPEC)
	go run ./internal/gen/oauthgated
	gofmt -w internal/api

## spec: re-vendor the OpenAPI spec from a local conductor checkout (needs conductor + jq)
spec:
	cd $(CONDUCTOR_DIR) && go run . openapi | jq -S . > $(CURDIR)/$(SPEC)

## migrations: re-vendor the Postgres system-database migrations from a local transact checkout
##   The Go runner beside them is NOT regenerated: diff it against the upstream
##   sysdb package by hand and update the commit named in internal/migrations/doc.go.
migrations:
	rm -f $(MIGRATIONS)/*.sql
	cp $(TRANSACT_DIR)/dbos/internal/sysdb/migrations/*.sql $(MIGRATIONS)/
	$(GOTEST) ./internal/migrations/

## build: build the dbosctl binary (VCS stamps come from go build; no ldflags needed)
build:
	go build -o dbosctl ./cmd/dbosctl

## snapshot: build release artifacts locally (all platforms, no tag, no publish)
snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest build --snapshot --clean

## test: unit tests only (no Docker)
test:
	$(GOTEST) ./...

## test-integration: container-backed tests (needs Docker; see Testing in AGENTS.md)
test-integration:
	$(GOTEST) -tags integration -timeout 20m ./...

## lint: go vet + gofmt check
lint:
	go vet ./...
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed on:"; echo "$$unformatted"; exit 1; fi

## tidy: sync go.mod/go.sum
tidy:
	go mod tidy
