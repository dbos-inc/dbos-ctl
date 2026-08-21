package migrations

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
)

// grantTimeout bounds the whole grant sequence. Grants are catalog updates
// against a schema the application is not yet using, so they are fast or they
// are blocked — and a blocked one should report rather than hang a CLI run.
const grantTimeout = 30 * time.Second

// GrantQueries returns the statements that give roleName full access to the
// DBOS system tables in schemaName, including default privileges so that tables
// a later migration adds are covered too. Neither name may contain a quote;
// callers validate that before rendering, since these are identifiers and not
// bound parameters.
func GrantQueries(roleName, schemaName string) []string {
	schemaSQL := pgx.Identifier{schemaName}.Sanitize()
	roleSQL := pgx.Identifier{roleName}.Sanitize()

	return []string{
		fmt.Sprintf(`GRANT USAGE ON SCHEMA %s TO %s`, schemaSQL, roleSQL),
		fmt.Sprintf(`GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA %s TO %s`, schemaSQL, roleSQL),
		fmt.Sprintf(`GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA %s TO %s`, schemaSQL, roleSQL),
		fmt.Sprintf(`GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA %s TO %s`, schemaSQL, roleSQL),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT ALL ON TABLES TO %s`, schemaSQL, roleSQL),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT ALL ON SEQUENCES TO %s`, schemaSQL, roleSQL),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT EXECUTE ON FUNCTIONS TO %s`, schemaSQL, roleSQL),
	}
}

// Grant runs GrantQueries against the system database. It is separate from
// Apply because the application role is optional: a database whose owner also
// runs the application needs no grants.
func Grant(ctx context.Context, databaseURL, roleName, schemaName string, progress io.Writer) error {
	if schemaName == "" {
		schemaName = DefaultSchema
	}
	logf(progress, "Granting privileges on schema %s to %s", schemaName, roleName)

	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to the system database: %w", err)
	}
	// Closing is not part of the timed sequence: it should still run when the
	// deadline is what ended the work.
	defer conn.Close(context.WithoutCancel(ctx))

	execCtx, cancel := context.WithTimeout(ctx, grantTimeout)
	defer cancel()

	for _, query := range GrantQueries(roleName, schemaName) {
		if _, err := conn.Exec(execCtx, query); err != nil {
			return fmt.Errorf("failed to execute %q: %w", query, err)
		}
	}
	return nil
}
