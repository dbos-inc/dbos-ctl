package migrations

import (
	"errors"
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
// from also changes what migration 10 prints, which is the only migration that
// varies that way: see migration10Statements.
//
// isCockroach and listenNotify are the same two switches BuildMigrations takes.
// Print mode never connects, so neither can be detected here: the caller's
// answers are the only ones available and are taken at face value. A caller
// asking for CockroachDB must also pass listenNotify false — that engine has no
// LISTEN/NOTIFY on any version — and Apply enforces the same pairing where it
// can detect the dialect for itself.
func Statements(schemaName string, from int, isCockroach, listenNotify bool) ([]string, error) {
	if schemaName == "" {
		schemaName = DefaultSchema
	}
	// Not only belt and braces alongside the CLI's own check: from version 2 on
	// this prints migration 10, whose DO block carries the raw schema name
	// inside a SQL string literal.
	if err := ValidateSchemaName(schemaName); err != nil {
		return nil, err
	}
	if isCockroach && listenNotify {
		return nil, errors.New("CockroachDB has no LISTEN/NOTIFY: render it with listenNotify false")
	}
	migrations := BuildMigrations(schemaName, isCockroach, listenNotify)
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
			statements = append(statements, migration10Statements(from, isCockroach, sanitizedSchema, migration.SQL)...)
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

// migration10Statements renders migration 10 for a printed script. It is the
// one migration whose printed form depends on where the script starts, and the
// one whose PostgreSQL SQL a CockroachDB script cannot use.
//
// From version 1 the script builds a fresh database, where migration 1 already
// creates the notifications primary key. The backfill would find nothing to do,
// so it is skipped and only its version bookkeeping is emitted.
//
// From any later version the script upgrades a database that predates that fix
// — the version row could not sit below 10 otherwise — which is precisely the
// case this migration exists for. Skipping it there while still advancing the
// version past 10 would leave notifications without a primary key and no later
// run willing to retry, since the version row would claim the work was done.
// That is what this function exists to prevent.
//
// The dialect picks the form, because idempotence is spelled differently on
// each. PostgreSQL gets the DO block, which consults pg_constraint itself.
// CockroachDB cannot run that block — it parses, then fails as unimplemented —
// and takes ADD CONSTRAINT ... IF NOT EXISTS instead, which PostgreSQL has no
// equivalent of. The runner reaches the same two behaviours by other means:
// there it can check from Go, so it does.
func migration10Statements(from int, isCockroach bool, sanitizedSchema, postgresSQL string) []string {
	if from == 1 {
		return []string{"-- Migration 10 skipped: not applicable on fresh databases"}
	}
	sql := strings.TrimSpace(postgresSQL)
	if isCockroach {
		sql = strings.TrimSpace(fmt.Sprintf(migration10AddCockroachSQL, sanitizedSchema))
	}
	if !strings.HasSuffix(sql, ";") {
		sql += ";"
	}
	return []string{"-- Migration 10", sql}
}
