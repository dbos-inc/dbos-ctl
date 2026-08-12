# dbos-cli Makefile. See AGENTS.md for the design.
#
# `generate` regenerates code from the vendored spec and needs no external
# tooling or network beyond the Go module cache — that is what CI runs.
# `spec` re-vendors the spec from a local conductor checkout and additionally
# needs that checkout plus jq.

CONDUCTOR_DIR ?= $(HOME)/conductor
SPEC          := internal/api/openapi-3.1.json

.PHONY: all generate spec check-spec build test lint tidy

all: generate build

## generate: regenerate the API client + OAuth-gate table from the vendored spec
generate:
	go tool oapi-codegen -config internal/api/oapi-codegen.yaml $(SPEC)
	go run ./internal/gen/oauthgated
	gofmt -w internal/api

## spec: re-vendor the OpenAPI spec from a local conductor checkout (needs conductor + jq)
spec:
	cd $(CONDUCTOR_DIR) && go run . openapi | jq -S . > $(CURDIR)/$(SPEC)

## check-spec: check the vendored spec against the deployed one (needs jq + network)
check-spec:
	./scripts/check-spec-drift.sh

## build: build the dbosctl binary (VCS stamps come from go build; no ldflags needed)
build:
	go build -o dbosctl ./cmd/dbosctl

## test: unit tests only (no Docker)
test:
	go test ./...

## test-integration: container-backed tests (needs Docker; see Testing in AGENTS.md)
test-integration:
	go test -tags integration -timeout 20m ./...

## lint: go vet + gofmt check
lint:
	go vet ./...
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed on:"; echo "$$unformatted"; exit 1; fi

## tidy: sync go.mod/go.sum
tidy:
	go mod tidy
