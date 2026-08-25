package migrations

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resetTimeout bounds emptying the tables. Unlike a grant this can be real
// work — a long history is a lot of rows — so the budget is minutes rather
// than seconds, and still finite so a lock held by a running application
// reports instead of hanging the CLI.
const resetTimeout = 10 * time.Minute

// dropTimeout bounds dropping the database. A drop is quick when it can run at
// all; what makes it slow is a session still holding the database open. Postgres
// clears those with WITH (FORCE), but CockroachDB has no such thing and the
// eviction below is best effort, so the blocked case is reachable — and there it
// waits with nothing on stderr to say why. Finite, so it reports instead.
const dropTimeout = 2 * time.Minute

// applicationOwnedTables are the tables carrying an application_name column,
// added by migrations 100-104. An application-scoped reset deletes from these
// and lets the foreign keys take the rest: every workflow-keyed table
// (operation_outputs, notifications, workflow_events, workflow_events_history,
// streams) references workflow_status ON DELETE CASCADE, so removing a
// workflow removes its steps, messages, events, and streams with it.
//
// Ordered so operation_outputs goes first. Its own application_name is what
// migration 104 added, and a step row could in principle name an application
// its workflow does not; deleting by that column first honours the ownership
// model rather than depending on the cascade to agree with it.
var applicationOwnedTables = []string{
	"operation_outputs",
	"workflow_status",
	"queues",
	"workflow_schedules",
	"application_versions",
}

// SystemTables is every table the DBOS system schema holds, apart from
// MigrationTable, which a reset spares.
//
// Named rather than read from the catalog. This binary owns the migrations that
// create these tables, so it knows the set exactly — and a reset that emptied
// whatever the catalog reported would empty an application's own tables in a
// schema that holds both. --schema makes that reachable: point it at a schema
// carrying application tables, and enumeration turns a reset into data loss the
// operator never asked for. Leaving a row behind is recoverable; that is not.
//
// The order is the order a reset empties them in. workflow_status goes last
// because every foreign key in the schema points at it and cascades on delete:
// emptying it first would clear the tables that reference it, and each of their
// own DELETEs would then report zero rows for a table it had just cleared.
// Correctness does not depend on this — a cascade and an explicit delete reach
// the same empty table — but the reported counts do.
//
// A migration that adds a table adds it here too. The tests fail otherwise:
// TestSystemTablesMatchTheMigratedSchemaIntegration compares this list against
// a freshly migrated database.
var SystemTables = []string{
	"application_versions",
	"event_dispatch_kv",
	"notifications",
	"operation_outputs",
	"queues",
	"streams",
	"workflow_events",
	"workflow_events_history",
	"workflow_schedules",
	"workflow_status",
}

// TableCount is the number of rows a reset removed from one table.
type TableCount struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// Empty deletes the DBOS rows from schema, leaving the schema itself migrated
// and immediately usable. dbos_migrations is spared: clearing it would re-run
// applied migrations against tables that already exist.
//
// applicationName scopes the reset to one application's rows, for a system
// database shared by several. Empty means every application's.
//
// Rows go by DELETE rather than TRUNCATE. TRUNCATE would need CASCADE to cross
// the foreign keys and takes an ACCESS EXCLUSIVE lock on every table it names;
// on CockroachDB it is priced as a schema change. DELETE needs only the
// privileges --app-role already grants, which is the point of provisioning the
// schema out of band in the first place.
func Empty(ctx context.Context, databaseURL, schema, applicationName string, progress io.Writer) ([]TableCount, error) {
	if schema == "" {
		schema = DefaultSchema
	}
	if err := ValidateSchemaName(schema); err != nil {
		return nil, err
	}

	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the system database: %w", err)
	}
	// Closing is not part of the timed sequence: it should still run when the
	// deadline is what ended the work.
	defer conn.Close(context.WithoutCancel(ctx))

	execCtx, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()

	present, err := schemaTables(execCtx, conn, schema)
	if err != nil {
		return nil, err
	}
	if err := checkNotAheadOfBinary(execCtx, conn, schema, present); err != nil {
		return nil, err
	}
	var owned map[string]struct{}
	if applicationName != "" {
		if owned, err = applicationNameTables(execCtx, conn, schema); err != nil {
			return nil, err
		}
	}
	tables, err := resetTargets(schema, applicationName, present, owned)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("no DBOS tables found in schema %s: nothing to reset (is the schema migrated?)", schema)
	}

	// One transaction: a half-emptied system database is a worse state than
	// either end of the operation, and the whole point is to reach a known one.
	tx, err := conn.Begin(execCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin the reset transaction: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(execCtx))

	counts := make([]TableCount, 0, len(tables))
	for _, table := range tables {
		qualified := pgx.Identifier{schema, table}.Sanitize()
		var tag pgconn.CommandTag
		var execErr error
		if applicationName == "" {
			tag, execErr = tx.Exec(execCtx, "DELETE FROM "+qualified)
		} else {
			tag, execErr = tx.Exec(execCtx, "DELETE FROM "+qualified+" WHERE application_name = $1", applicationName)
		}
		if execErr != nil {
			return nil, fmt.Errorf("failed to empty %s: %w", qualified, execErr)
		}
		counts = append(counts, TableCount{Table: table, Rows: tag.RowsAffected()})
	}

	if err := tx.Commit(execCtx); err != nil {
		return nil, fmt.Errorf("failed to commit the reset: %w", err)
	}
	for _, c := range counts {
		logf(progress, "Emptied %s (%d rows)", c.Table, c.Rows)
	}
	return counts, nil
}

// resetTargets lists the tables a reset should delete from: the ones this
// binary knows about that the schema actually has.
//
// Intersecting with what is present rather than assuming the full set, because
// a schema may be migrated only part of the way — a table added by a migration
// it has not reached yet is not there to empty.
//
// An application-scoped reset intersects against owned rather than present.
// Presence is the wrong question there: workflow_status, queues, and
// operation_outputs all predate migration 100 by a long way, so a schema that
// never reached 100 still has every table in applicationOwnedTables and would
// pass a presence check, only to fail on the first DELETE with a bare
// SQLSTATE 42703. What decides whether a table can be scoped to an application
// is the application_name column, so that is what is checked.
func resetTargets(schema, applicationName string, present, owned map[string]struct{}) ([]string, error) {
	known, have := SystemTables, present
	if applicationName != "" {
		known, have = applicationOwnedTables, owned
	}

	out := make([]string, 0, len(known))
	for _, table := range known {
		if _, ok := have[table]; ok {
			out = append(out, table)
		}
	}
	if len(out) == 0 && applicationName != "" {
		return nil, fmt.Errorf("schema %s has no application_name columns: it predates migration 100, so it cannot be reset per application", schema)
	}
	return out, nil
}

// checkNotAheadOfBinary refuses to work on a schema migrated past what this
// build knows. Both Empty and RenameApplication call it.
//
// Their table lists are compiled in, which is what keeps them off tables DBOS
// did not create — but it also means a migration this build has never seen
// could have added a table they would silently skip. A reset would leave that
// table full and report success. A rename would leave its rows under the old
// name and report success, which is the half-owned application the opening
// transaction exists to prevent, arrived at the long way round.
//
// The system schema is shared by every SDK and they release on their own
// schedules, so a database ahead of this dbosctl is a normal thing to meet
// rather than a corrupt one. Say so, and name the fix.
func checkNotAheadOfBinary(ctx context.Context, conn *pgx.Conn, schema string, present map[string]struct{}) error {
	if _, ok := present[MigrationTable]; !ok {
		// Nothing has migrated this schema, so there is no version to be ahead of.
		return nil
	}
	var version int64
	q := fmt.Sprintf("SELECT version FROM %s LIMIT 1", pgx.Identifier{schema, MigrationTable}.Sanitize())
	if err := conn.QueryRow(ctx, q).Scan(&version); err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return fmt.Errorf("failed to read the migration version of schema %s: %w", schema, err)
	}
	if latest := LatestVersion(); version > latest {
		return fmt.Errorf("schema %s is at migration %d but this dbosctl knows up to %d: a newer migration may have added tables this build would leave behind; upgrade dbosctl", schema, version, latest)
	}
	return nil
}

// schemaTables returns the base tables in a schema, as a set.
func schemaTables(ctx context.Context, conn *pgx.Conn, schema string) (map[string]struct{}, error) {
	rows, err := conn.Query(ctx,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = $1 AND table_type = 'BASE TABLE'`, schema)
	if err != nil {
		return nil, fmt.Errorf("failed to list the tables in schema %s: %w", schema, err)
	}
	defer rows.Close()

	present := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to read the table list for schema %s: %w", schema, err)
		}
		present[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read the table list for schema %s: %w", schema, err)
	}
	return present, nil
}

// DropDatabase drops the system database named by databaseURL, connecting to
// the server's own postgres database to do it.
//
// This is the blunt instrument, and reaches past everything --schema means: it
// takes the whole database, including any application tables sharing it and
// every other application sharing the system schema. Empty is the default for
// that reason.
func DropDatabase(ctx context.Context, databaseURL string, progress io.Writer) error {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse the database URL: %w", err)
	}
	dbName := poolConfig.ConnConfig.Database
	if dbName == "" {
		return fmt.Errorf("no database name in the connection string")
	}

	serverConfig := poolConfig.ConnConfig.Copy()
	serverConfig.Database = "postgres"
	conn, err := pgx.ConnectConfig(ctx, serverConfig)
	if err != nil {
		return fmt.Errorf("failed to connect to the PostgreSQL server: %w", err)
	}
	// Closing is not part of the timed sequence: it should still run when the
	// deadline is what ended the work.
	defer conn.Close(context.WithoutCancel(ctx))

	execCtx, cancel := context.WithTimeout(ctx, dropTimeout)
	defer cancel()

	sanitized := pgx.Identifier{dbName}.Sanitize()
	if IsCockroachDB(conn) {
		// CockroachDB has no DROP DATABASE ... WITH (FORCE), so sessions are
		// evicted by hand first. Best effort: a role may not be allowed to
		// signal other backends, and the drop is worth attempting regardless.
		_, _ = conn.Exec(execCtx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			 WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		if _, err := conn.Exec(execCtx, "DROP DATABASE IF EXISTS "+sanitized); err != nil {
			return fmt.Errorf("failed to drop database %s: %w", dbName, err)
		}
	} else if _, err := conn.Exec(execCtx, "DROP DATABASE IF EXISTS "+sanitized+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("failed to drop database %s: %w", dbName, err)
	}

	logf(progress, "Dropped database %s", dbName)
	return nil
}

// applicationNameTables returns the base tables in a schema that carry an
// application_name column, as a set.
//
// Separate from schemaTables because the two answer different questions and an
// application-scoped reset needs both: which tables exist, and which of those
// migrations 100-104 taught to name their owner. A schema can be migrated to
// any point in that range, so the answers genuinely differ.
func applicationNameTables(ctx context.Context, conn *pgx.Conn, schema string) (map[string]struct{}, error) {
	rows, err := conn.Query(ctx,
		`SELECT table_name FROM information_schema.columns
		 WHERE table_schema = $1 AND column_name = 'application_name'`, schema)
	if err != nil {
		return nil, fmt.Errorf("failed to list the application_name columns in schema %s: %w", schema, err)
	}
	defer rows.Close()

	owned := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to read the application_name column list for schema %s: %w", schema, err)
		}
		owned[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read the application_name column list for schema %s: %w", schema, err)
	}
	return owned, nil
}
