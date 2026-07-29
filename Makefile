# dbos-cli Makefile. See AGENTS.md for the design.
#
# `generate` regenerates code from the vendored spec and needs no external
# tooling or network beyond the Go module cache — that is what CI runs.
# `spec` re-vendors the spec from a local conductor checkout and additionally
# needs that checkout plus jq.

CONDUCTOR_DIR ?= $(HOME)/conductor
SPEC          := internal/api/openapi-3.1.json

.PHONY: all generate spec build test lint tidy

all: generate build

## generate: regenerate the API client + OAuth-gate table from the vendored spec
generate:
	go tool oapi-codegen -config internal/api/oapi-codegen.yaml $(SPEC)
	go run ./internal/gen/oauthgated
	gofmt -w internal/api

## spec: re-vendor the OpenAPI spec from a local conductor checkout (needs conductor + jq)
spec:
	cd $(CONDUCTOR_DIR) && go run . openapi | jq -S . > $(CURDIR)/$(SPEC)

## build: build the dbos binary (A2 relocates the entrypoint to ./cmd/dbos)
build:
	go build -o dbos .

## test: unit tests only (no Docker)
test:
	go test ./...

## lint: go vet + gofmt check
lint:
	go vet ./...
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed on:"; echo "$$unformatted"; exit 1; fi

## tidy: sync go.mod/go.sum
tidy:
	go mod tidy
