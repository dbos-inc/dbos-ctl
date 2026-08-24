package migrations

import (
	"context"
	"io"
	"strings"
	"testing"
)

// unreachableURL parses fine and connects to nothing. A test using it proves a
// check ran before the connection attempt: reaching the network would report a
// dial failure instead.
const unreachableURL = "postgres://user@127.0.0.1:1/dbos"

// TestApplyRejectsAQuotedSchemaBeforeConnecting is the regression test for a
// guard that lived only in the CLI's print path. Migration 10 interpolates the
// raw, unsanitized schema into a SQL string literal — it compares against
// pg_namespace.nspname, which holds the bare name, so the sanitized identifier
// would never match — and a schema name carrying a quote closes that literal
// early. Print mode skips migration 10 entirely, so the only path that ever
// executes it was the one with no check.
func TestApplyRejectsAQuotedSchemaBeforeConnecting(t *testing.T) {
	for _, schema := range []string{`db"os`, `ha'; DROP TABLE x; --`} {
		err := Apply(context.Background(), unreachableURL, schema, true, io.Discard)
		if err == nil {
			t.Fatalf("Apply(%q) succeeded, want a rejection", schema)
		}
		if !strings.Contains(err.Error(), "schema names containing quotes") {
			t.Errorf("Apply(%q) failed with %q, want the quote rejection", schema, err)
		}
	}
}

func TestGrantRejectsQuotedNamesBeforeConnecting(t *testing.T) {
	cases := []struct {
		name, role, schema, want string
	}{
		{"quoted schema", "app", `db"os`, "schema names containing quotes"},
		{"quoted role", `ap"p`, "dbos", "role names containing quotes"},
		{"role closing a literal", `ha'; DROP TABLE x; --`, "dbos", "role names containing quotes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Grant(context.Background(), unreachableURL, tc.role, tc.schema, io.Discard)
			if err == nil {
				t.Fatal("Grant succeeded, want a rejection")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Grant failed with %q, want %q", err, tc.want)
			}
		})
	}
}

// TestValidatorsAcceptNamesThatMerelyNeedQuoting guards against tightening the
// check into a rejection of anything unusual. A name with a space or an
// uppercase letter is legal; it just has to reach the SQL sanitized.
func TestValidatorsAcceptNamesThatMerelyNeedQuoting(t *testing.T) {
	for _, name := range []string{"dbos", "Odd Schema", "select", "dbos-2", "schéma"} {
		if err := ValidateSchemaName(name); err != nil {
			t.Errorf("ValidateSchemaName(%q) = %v, want nil", name, err)
		}
		if err := ValidateRoleName(name); err != nil {
			t.Errorf("ValidateRoleName(%q) = %v, want nil", name, err)
		}
	}
}
