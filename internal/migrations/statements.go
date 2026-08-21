package migrations

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Statements returns the SQL the system database migrations execute against a
// PostgreSQL database for the given schema, as an ordered list of
// semicolon-terminated statements and "--" comment lines suitable for execution
// with psql. It never connects to a database.
//
// from is a migration version number: pass 1 for the full fresh-database script
// (including schema creation, the dbos_migrations table, and the initial
// version row), or a later number for migrations from there through the latest
// only. Version numbers are not contiguous: versions between the end of the
// Go-specific history and the cross-SDK shared base are unused and emit
// nothing. Each migration is followed by its version bookkeeping, mirroring the
// runner. The SQL contains CREATE/DROP INDEX CONCURRENTLY, so it must run
// outside a transaction block. An empty schemaName uses the default.
//
// listenNotify decides whether the pg_notify triggers are in the script. Print
// mode never connects, so nothing here can detect CockroachDB or a connection
// pooler: the caller's answer is the only one available, and it is taken at
// face value. The rest of the script is PostgreSQL — a CockroachDB deployment
// wants the live migration, which detects the dialect.
func Statements(schemaName string, from int, listenNotify bool) ([]string, error) {
	if schemaName == "" {
		schemaName = DefaultSchema
	}
	migrations := BuildMigrations(schemaName, false, listenNotify)
	latest := migrations[len(migrations)-1].Version
	if from < 1 || int64(from) > latest {
		// Printed verbatim by the CLI, so worded for the end user.
		return nil, fmt.Errorf("migration %d does not exist: valid migrations are 1 through %d", from, latest)
	}
	sanitizedSchema := pgx.Identifier{schemaName}.Sanitize()

	var statements []string
	if from == 1 {
		statements = append(statements,
			fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", sanitizedSchema),
			fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (version BIGINT NOT NULL PRIMARY KEY);", sanitizedSchema, MigrationTable),
		)
	}

	versionRowExists := from > 1
	for _, migration := range migrations {
		if migration.Version < int64(from) {
			continue
		}
		if migration.Version == 10 {
			// Migration 10 backfills the notifications primary key, which
			// migration 1 already creates on a fresh database.
			statements = append(statements, "-- Migration 10 skipped: not applicable on fresh databases")
		} else if sql := strings.TrimSpace(migration.SQL); sql != "" {
			if !strings.HasSuffix(sql, ";") {
				sql += ";"
			}
			statements = append(statements, fmt.Sprintf("-- Migration %d", migration.Version), sql)
		}
		// Mirror the runner's per-migration version bookkeeping.
		if versionRowExists {
			statements = append(statements, fmt.Sprintf("UPDATE %s.%s SET version = %d;", sanitizedSchema, MigrationTable, migration.Version))
		} else {
			statements = append(statements, fmt.Sprintf("INSERT INTO %s.%s (version) VALUES (%d);", sanitizedSchema, MigrationTable, migration.Version))
			versionRowExists = true
		}
	}
	return statements, nil
}
