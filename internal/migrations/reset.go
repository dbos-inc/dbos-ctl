package migrations

import (
	"context"
	"fmt"
	"io"
	"sort"
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

	tables, err := resetTargets(execCtx, conn, schema, applicationName)
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

// resetTargets lists the tables a reset should delete from.
//
// Scoped to an application, that is the fixed set carrying application_name.
// Unscoped, it is read from the catalog rather than hard-coded: which tables
// exist depends on how far the schema is migrated, so a list compiled into the
// binary would silently leave rows behind in a database newer than it.
func resetTargets(ctx context.Context, conn *pgx.Conn, schema, applicationName string) ([]string, error) {
	present, err := schemaTables(ctx, conn, schema)
	if err != nil {
		return nil, err
	}
	if applicationName == "" {
		out := make([]string, 0, len(present))
		for table := range present {
			if table == MigrationTable {
				continue
			}
			out = append(out, table)
		}
		// Deterministic order, so a dry run and the real one report the same
		// way. Any order is correct: every foreign key here cascades on delete.
		sort.Strings(out)
		return out, nil
	}

	out := make([]string, 0, len(applicationOwnedTables))
	for _, table := range applicationOwnedTables {
		if _, ok := present[table]; ok {
			out = append(out, table)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("schema %s has no application_name columns: it predates migration 100, so it cannot be reset per application", schema)
	}
	return out, nil
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
	defer conn.Close(context.WithoutCancel(ctx))

	sanitized := pgx.Identifier{dbName}.Sanitize()
	if IsCockroachDB(conn) {
		// CockroachDB has no DROP DATABASE ... WITH (FORCE), so sessions are
		// evicted by hand first. Best effort: a role may not be allowed to
		// signal other backends, and the drop is worth attempting regardless.
		_, _ = conn.Exec(ctx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			 WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+sanitized); err != nil {
			return fmt.Errorf("failed to drop database %s: %w", dbName, err)
		}
	} else if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+sanitized+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("failed to drop database %s: %w", dbName, err)
	}

	logf(progress, "Dropped database %s", dbName)
	return nil
}
