package migrations

import (
	"strings"
	"testing"
)

// TestStatementsFromOneBuildsAFreshDatabase proves the printed script for a
// fresh database is self-contained: it creates the schema and the bookkeeping
// table before the first migration, and seeds the version row rather than
// updating a row that does not exist yet.
func TestStatementsFromOneBuildsAFreshDatabase(t *testing.T) {
	got, err := Statements("dbos", 1, false, true)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Join(got, "\n")

	for _, want := range []string{
		`CREATE SCHEMA IF NOT EXISTS "dbos";`,
		`CREATE TABLE IF NOT EXISTS "dbos".dbos_migrations (version BIGINT NOT NULL PRIMARY KEY);`,
		`INSERT INTO "dbos".dbos_migrations (version) VALUES (1);`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("fresh-database script is missing:\n%s", want)
		}
	}
	if i, j := strings.Index(script, "CREATE SCHEMA"), strings.Index(script, "-- Migration 2"); i > j {
		t.Error("the schema is created after the migrations that use it")
	}
	// Exactly one INSERT: every later migration updates the row it seeded.
	if n := strings.Count(script, `INSERT INTO "dbos".dbos_migrations`); n != 1 {
		t.Errorf("fresh-database script seeds the version row %d times, want 1", n)
	}
}

// TestStatementsFromLaterVersionUpdatesTheVersionRow proves an upgrade script
// assumes the bookkeeping already exists: no schema creation, and updates
// rather than inserts.
func TestStatementsFromLaterVersionUpdatesTheVersionRow(t *testing.T) {
	got, err := Statements("dbos", 40, false, true)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Join(got, "\n")

	if strings.Contains(script, "CREATE SCHEMA") {
		t.Error("upgrade script re-creates the schema")
	}
	if strings.Contains(script, `INSERT INTO "dbos".dbos_migrations`) {
		t.Error("upgrade script inserts a version row instead of updating it")
	}
	if strings.Contains(script, "-- Migration 39") {
		t.Error("upgrade script includes a migration below the requested start")
	}
	if !strings.Contains(script, `UPDATE "dbos".dbos_migrations SET version = 40;`) {
		t.Error("upgrade script does not record migration 40")
	}
}

// TestStatementsAcrossTheVersionGap proves the unused range between the Go
// SDK's own history and the cross-SDK base emits bookkeeping for nothing —
// asking for a version inside the gap is legal and produces only the shared
// migrations.
func TestStatementsAcrossTheVersionGap(t *testing.T) {
	got, err := Statements("dbos", SharedMigrationBase-1, false, true)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Join(got, "\n")
	if !strings.Contains(script, "-- Migration 100") {
		t.Error("script from inside the version gap omits the shared migrations")
	}
}

// TestStatementsRejectsAVersionThatDoesNotExist proves the error names the
// valid range, since it is printed verbatim to whoever mistyped the number.
func TestStatementsRejectsAVersionThatDoesNotExist(t *testing.T) {
	for _, from := range []int{0, -1, 1 << 20} {
		_, err := Statements("dbos", from, false, true)
		if err == nil {
			t.Fatalf("Statements(%d) succeeded, want an error", from)
		}
		if !strings.Contains(err.Error(), "valid migrations are 1 through") {
			t.Errorf("Statements(%d) error does not name the valid range: %v", from, err)
		}
	}
}

// TestStatementsDefaultsTheSchema proves an empty schema name is the default
// rather than an unquoted hole in the SQL.
func TestStatementsDefaultsTheSchema(t *testing.T) {
	got, err := Statements("", 1, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[0], `"`+DefaultSchema+`"`) {
		t.Errorf("empty schema did not default to %q: %s", DefaultSchema, got[0])
	}
}

// TestStatementsAreTerminated proves the output is pipeable to psql: every
// statement ends in a semicolon, and everything else is a comment.
func TestStatementsAreTerminated(t *testing.T) {
	got, err := Statements("dbos", 1, false, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if strings.HasPrefix(s, "--") {
			continue
		}
		if !strings.HasSuffix(strings.TrimSpace(s), ";") {
			t.Errorf("statement is not semicolon-terminated:\n%s", s)
		}
	}
}

// TestGrantQueriesCoverFutureTables proves the grants include default
// privileges: without them a migration that adds a table later leaves the
// application role unable to read it.
func TestGrantQueriesCoverFutureTables(t *testing.T) {
	got := GrantQueries("app_role", "dbos")
	joined := strings.Join(got, "\n")

	for _, want := range []string{
		`GRANT USAGE ON SCHEMA "dbos" TO "app_role"`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA "dbos" GRANT ALL ON TABLES TO "app_role"`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA "dbos" GRANT ALL ON SEQUENCES TO "app_role"`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA "dbos" GRANT EXECUTE ON FUNCTIONS TO "app_role"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("grants are missing:\n%s", want)
		}
	}
}

// TestMigration10IsSkippedOnlyForAFreshDatabase pins the one migration whose
// printed form depends on where the script starts.
//
// The skip used to be unconditional, which was right for the case it named and
// wrong for every other. A script starting above version 1 upgrades a database
// that predates the migration-1 fix — nothing else could leave the version row
// below 10 — so it is exactly the database that needs the backfill. Emitting
// the skip comment there while still advancing the version past 10 left
// notifications with no primary key and no later run willing to retry, because
// the version row said the work was done.
func TestMigration10IsSkippedOnlyForAFreshDatabase(t *testing.T) {
	const skip = "-- Migration 10 skipped"

	fresh, err := Statements("dbos", 1, false, true)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Join(fresh, "\n")
	if !strings.Contains(script, skip) {
		t.Error("the fresh-database script does not skip migration 10, which migration 1 already covers")
	}
	if strings.Contains(script, "ADD PRIMARY KEY") {
		t.Error("the fresh-database script backfills a primary key migration 1 just created")
	}

	// Every starting point that implies an existing database must emit the
	// real backfill, including asking for migration 10 itself.
	for _, from := range []int{2, 9, 10} {
		got, err := Statements("dbos", from, false, true)
		if err != nil {
			t.Fatal(err)
		}
		script := strings.Join(got, "\n")
		if strings.Contains(script, skip) {
			t.Errorf("Statements(from=%d) skips migration 10 on a database that predates the fix", from)
		}
		if !strings.Contains(script, `ALTER TABLE "dbos".notifications ADD PRIMARY KEY (message_uuid)`) {
			t.Errorf("Statements(from=%d) does not back the notifications primary key", from)
		}
		// The bookkeeping is what made the old behaviour unrecoverable, so it
		// has to keep pace with the SQL either way.
		if !strings.Contains(script, `UPDATE "dbos".dbos_migrations SET version = 10;`) {
			t.Errorf("Statements(from=%d) does not record migration 10", from)
		}
	}
}

// TestMigration10PrintsTheDialectItCanRun proves each engine is handed the
// only form of this migration it accepts. The DO block is a syntax error on
// CockroachDB v24.1 and reports an unimplemented feature on v26.2; ADD
// CONSTRAINT ... IF NOT EXISTS has no PostgreSQL equivalent. Both were checked
// against live servers, and both are idempotent there.
func TestMigration10PrintsTheDialectItCanRun(t *testing.T) {
	crdb, err := Statements("dbos", 10, true, false)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Join(crdb, "\n")
	if strings.Contains(script, "DO $$") {
		t.Error("the CockroachDB script contains a DO block, which that engine cannot run")
	}
	if !strings.Contains(script, `ADD CONSTRAINT IF NOT EXISTS notifications_pkey PRIMARY KEY (message_uuid)`) {
		t.Error("the CockroachDB script does not add the notifications primary key")
	}

	pg, err := Statements("dbos", 10, false, true)
	if err != nil {
		t.Fatal(err)
	}
	script = strings.Join(pg, "\n")
	if !strings.Contains(script, "DO $$") {
		t.Error("the PostgreSQL script does not use the DO block that makes the backfill conditional")
	}
	if strings.Contains(script, "ADD CONSTRAINT IF NOT EXISTS") {
		t.Error("the PostgreSQL script uses ADD CONSTRAINT IF NOT EXISTS, which PostgreSQL does not support")
	}
}

// TestStatementsRejectsAQuotedSchema matters more from version 2 on, where the
// script now carries migration 10's DO block: that block compares against
// pg_namespace.nspname, so it holds the schema name raw inside a SQL string
// literal, and a quote would close it early.
func TestStatementsRejectsAQuotedSchema(t *testing.T) {
	for _, from := range []int{1, 10} {
		if _, err := Statements(`ha'; DROP TABLE x; --`, from, false, true); err == nil {
			t.Errorf("Statements(from=%d) accepted a schema name containing a quote", from)
		}
	}
}
