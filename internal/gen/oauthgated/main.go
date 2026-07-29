// Command oauthgated generates internal/api/oauth_gated.go from the vendored
// OpenAPI spec. oapi-codegen discards vendor extensions, so the
// x-dbos-requires-oauth markers never reach the generated client; this reads
// the same spec and emits the operationId -> true gate table the CLI consults
// to refuse OAuth-only operations before sending them in a no-auth deployment.
//
// Run via `make generate`; do not run by hand. Inputs and outputs are fixed
// relative to the repo root.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"sort"
)

const (
	specPath = "internal/api/openapi-3.1.json"
	outPath  = "internal/api/oauth_gated.go"
	// extension is the vendor property conductor stamps on operations that only
	// exist when OAuth is enabled.
	extension = "x-dbos-requires-oauth"
)

// operation captures just the two fields we need off each spec operation. A
// path item also holds non-operation keys ("parameters", "summary", ...); those
// values either fail to decode into this struct or carry no operationId, so
// they fall out naturally.
type operation struct {
	OperationID   string `json:"operationId"`
	RequiresOAuth bool   `json:"x-dbos-requires-oauth"`
}

type spec struct {
	Paths map[string]map[string]json.RawMessage `json:"paths"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "oauthgated:", err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	var s spec
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("parsing %s: %w", specPath, err)
	}

	var gated []string
	for _, item := range s.Paths {
		for _, methodValue := range item {
			var op operation
			if err := json.Unmarshal(methodValue, &op); err != nil {
				// Not an operation object (e.g. a "parameters" array) — skip.
				continue
			}
			if op.OperationID != "" && op.RequiresOAuth {
				gated = append(gated, op.OperationID)
			}
		}
	}
	sort.Strings(gated)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "// Code generated from %s by internal/gen/oauthgated; DO NOT EDIT.\n\n", specPath)
	buf.WriteString("package api\n\n")
	fmt.Fprintf(&buf, "// OAuthGated reports, by operationId, which operations carry the spec's\n")
	fmt.Fprintf(&buf, "// %s extension and therefore do not exist in a no-auth\n", extension)
	buf.WriteString("// (auth: none) deployment. The CLI consults it to fail such a command before\n")
	buf.WriteString("// the request with a mode-aware message rather than on a raw 404.\n")
	buf.WriteString("var OAuthGated = map[string]bool{\n")
	for _, id := range gated {
		fmt.Fprintf(&buf, "\t%q: true,\n", id)
	}
	buf.WriteString("}\n")

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("gofmt: %w", err)
	}
	if err := os.WriteFile(outPath, formatted, 0o644); err != nil {
		return err
	}
	fmt.Printf("oauthgated: wrote %d gated operations to %s\n", len(gated), outPath)
	return nil
}
