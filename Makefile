# dbos-cli Makefile. See AGENTS.md for the design.
#
# `generate` regenerates code from the vendored spec and needs no external
# tooling or network beyond the Go module cache — that is what CI runs.
# `spec` re-vendors the spec from a local conductor checkout and additionally
# needs that checkout plus jq.

CONDUCTOR_DIR ?= $(HOME)/conductor
SPEC          := internal/api/openapi-3.1.json

# Set JUNIT to a path to also write a JUnit XML report (CI publishes it as a
# summary and an artifact). Plain `go test` otherwise, so a local run needs
# nothing built and its output is unchanged.
JUNIT ?=
GOTEST := go test
ifneq ($(JUNIT),)
GOTEST := go tool gotestsum --junitfile $(JUNIT) --format testname --
endif

.PHONY: all generate spec build snapshot test test-integration test-sysdb test-images lint tidy

all: generate build

## generate: regenerate the API client + OAuth-gate table from the vendored spec
generate:
	go tool oapi-codegen -config internal/api/oapi-codegen.yaml $(SPEC)
	go run ./internal/gen/oauthgated
	gofmt -w internal/api

## spec: re-vendor the OpenAPI spec from a local conductor checkout (needs conductor + jq)
spec:
	cd $(CONDUCTOR_DIR) && go run . openapi | jq -S . > $(CURDIR)/$(SPEC)

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
##   The sysdb tests skip here: they are per-engine, and `test-sysdb` runs them.
test-integration:
	$(GOTEST) -tags integration -timeout 20m ./...

## test-sysdb: sysdb tests against one engine (ENGINE=postgres|cockroach)
##   Covers every `dbosctl sysdb` command -- migrate, reset, rename-application --
##   since all three run real SQL that the two engines do not always agree on.
##   The name pattern has to match every sysdb test: one that names neither a
##   command nor Sysdb would be skipped here and never run anywhere.
##   One engine per run because a process migrates one database; CI matrixes over both.
ENGINE ?= postgres
test-sysdb:
	DBOS_TEST_ENGINE=$(ENGINE) $(GOTEST) -tags integration -timeout 20m -run 'Migrate|Reset|Rename|Sysdb' ./internal/cli/

## lint: go vet + gofmt check
lint:
	go vet ./...
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed on:"; echo "$$unformatted"; exit 1; fi

## tidy: sync go.mod/go.sum
tidy:
	go mod tidy

## test-images: build the prebaked test database images (needs Docker)
##   Images whose DBOS schema is already migrated, for the SDK suites. The win is
##   CockroachDB's: it prices every DDL as an online schema change, so migrating
##   one database costs ~60s there against ~0.75s on PostgreSQL. The PostgreSQL
##   image earns its place by letting a suite treat the two engines identically,
##   not by being faster.
##
##   The schema is generated from this tree, never checked in, so an image cannot
##   ship migrations older than the source it was built from. The tag carries the
##   migration version, read back from the generated script -- statements.go
##   always ends it by recording the version, and that same row is what lets an
##   SDK skip migrating.
PG_BASE_IMAGE     ?= postgres:16
CRDB_BASE_IMAGE   ?= cockroachdb/cockroach:latest-v25.2
TEST_IMAGE_REPO   ?= ghcr.io/dbos-inc
TEST_DB_COUNT     ?= 4

.PHONY: test-images
test-images:
	go run ./cmd/dbosctl sysdb migrate --print-migrations all > docker/schema.postgres.sql
	go run ./cmd/dbosctl sysdb migrate --print-migrations all --cockroach > docker/schema.cockroach.sql
	@version=$$(sed -n 's/.*SET version = \([0-9]*\).*/\1/p' docker/schema.postgres.sql | tail -1); \
	pgtag="$$(echo $(PG_BASE_IMAGE) | tr ':/' '--')"; \
	crdbtag="$$(echo $(CRDB_BASE_IMAGE) | sed 's/.*://; s/^latest-v//')"; \
	set -x; \
	docker build -f docker/Dockerfile.postgres docker \
	  --build-arg BASE_IMAGE=$(PG_BASE_IMAGE) --build-arg DB_COUNT=$(TEST_DB_COUNT) \
	  -t $(TEST_IMAGE_REPO)/dbos-test-postgres:$${pgtag#postgres-}-m$$version; \
	docker build -f docker/Dockerfile.cockroach docker \
	  --build-arg BASE_IMAGE=$(CRDB_BASE_IMAGE) --build-arg DB_COUNT=$(TEST_DB_COUNT) \
	  -t $(TEST_IMAGE_REPO)/dbos-test-cockroach:$$crdbtag-m$$version
