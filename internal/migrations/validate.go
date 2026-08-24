package migrations

import (
	"fmt"
	"strings"
)

// Schema and role names reach the SQL as quoted identifiers, and the schema
// reaches migration 10 as a string literal besides. A quote inside one would
// escape that quoting, and no legitimate name needs it.
//
// The check lives here rather than in the CLI because every path that renders
// these names into SQL runs through this package: a live migration, a grant,
// and the printed SQL alike. Guarding only the printing path — which is where
// this started — leaves the one that actually reaches a database unguarded.
const quoteChars = `"'`

// ValidateSchemaName rejects a schema name that cannot be safely rendered.
func ValidateSchemaName(schema string) error {
	if strings.ContainsAny(schema, quoteChars) {
		return fmt.Errorf("schema names containing quotes are not supported")
	}
	return nil
}

// ValidateRoleName rejects a role name that cannot be safely rendered.
func ValidateRoleName(role string) error {
	if strings.ContainsAny(role, quoteChars) {
		return fmt.Errorf("role names containing quotes are not supported")
	}
	return nil
}
