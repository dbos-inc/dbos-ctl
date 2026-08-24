package migrations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultSchema is where DBOS puts its system tables unless told otherwise.
const DefaultSchema = "dbos"

// Apply brings the system database at databaseURL up to the latest migration:
// it creates the database if it does not exist, detects whether the server is
// CockroachDB (which changes what some migrations render), and applies whatever
// is pending. Progress lines go to progress, which may be nil.
//
// listenNotify asks for the triggers that fire pg_notify. CockroachDB overrides
// it: it has no LISTEN/NOTIFY, so those migrations cannot be applied there at
// all, and asking for them is a mistake worth correcting rather than failing
// over. Nothing can detect the other reason to turn them off — a connection
// pooler in transaction mode, where the notifications are delivered to a
// session the application does not keep — so that one has to be asked for.
//
// The order matters and mirrors the SDK's startup path: the database has to
// exist before a connection can report the server type, and the server type
// decides which SQL the pending migrations are made of.
func Apply(ctx context.Context, databaseURL, schema string, listenNotify bool, progress io.Writer) error {
	if schema == "" {
		schema = DefaultSchema
	}
	if err := ValidateSchemaName(schema); err != nil {
		return err
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse database URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create connection pool: %w", err)
	}
	defer pool.Close()

	if err := EnsureDatabase(ctx, pool, progress); err != nil {
		return err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection to detect database type: %w", err)
	}
	isCockroach := IsCockroachDB(conn.Conn())
	// Release before anything that closes the pool: Close blocks until every
	// acquired connection is returned, so a deferred Release would deadlock.
	conn.Release()
	if isCockroach {
		logf(progress, "Detected CockroachDB")
		if listenNotify {
			listenNotify = false
			logf(progress, "CockroachDB has no LISTEN/NOTIFY: migrating without the notification triggers")
		}
	}

	pending, err := ShouldMigrate(ctx, pool, schema, isCockroach, listenNotify)
	if err != nil {
		return fmt.Errorf("failed to determine migration status: %w", err)
	}
	if !pending {
		logf(progress, "System database is already up to date (migration %d)", LatestVersion())
		return nil
	}
	return RunMigrations(ctx, pool, schema, isCockroach, listenNotify, progress)
}

// LatestVersion is the version a fully migrated database reports. It takes no
// arguments because it depends on neither the dialect nor LISTEN/NOTIFY: a
// migration that renders empty still occupies its version, so every deployment
// counts to the same number.
func LatestVersion() int64 {
	m := BuildMigrations(DefaultSchema, false, true)
	return m[len(m)-1].Version
}

// IsCockroachDB reports whether the connection is to CockroachDB, which
// announces itself with a crdb_version parameter.
func IsCockroachDB(conn *pgx.Conn) bool {
	return conn.PgConn().ParameterStatus("crdb_version") != ""
}

// EnsureDatabase creates the database named in the pool's connection string if
// it does not exist yet, by connecting to the server's "postgres" database.
func EnsureDatabase(ctx context.Context, pool *pgxpool.Pool, progress io.Writer) error {
	poolConfig := pool.Config()
	dbName := poolConfig.ConnConfig.Database
	if dbName == "" {
		return errors.New("no database name in the connection string")
	}

	serverConfig := poolConfig.ConnConfig.Copy()
	serverConfig.Database = "postgres"
	conn, err := pgx.ConnectConfig(ctx, serverConfig)
	if err != nil {
		return fmt.Errorf("failed to connect to the PostgreSQL server: %w", err)
	}
	defer conn.Close(ctx)

	var exists bool
	if err := conn.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", dbName).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check whether database %s exists: %w", dbName, err)
	}
	if exists {
		return nil
	}
	createSQL := fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{dbName}.Sanitize())
	if _, err := conn.Exec(ctx, createSQL); err != nil {
		return fmt.Errorf("failed to create database %s: %w", dbName, err)
	}
	logf(progress, "Created database %s", dbName)
	return nil
}

// ShouldMigrate reports whether any migration work remains for the schema.
// Returns true if the schema is missing, the dbos_migrations table is missing,
// or the recorded version is behind the latest.
func ShouldMigrate(ctx context.Context, pool *pgxpool.Pool, schema string, isCockroach, listenNotify bool) (bool, error) {
	var schemaExists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`,
		schema).Scan(&schemaExists)
	if err != nil {
		return false, fmt.Errorf("failed to check if schema %s exists: %w", schema, err)
	}
	if !schemaExists {
		return true, nil
	}

	var tableExists bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)`,
		schema, MigrationTable).Scan(&tableExists)
	if err != nil {
		return false, fmt.Errorf("failed to check if migration table exists: %w", err)
	}
	if !tableExists {
		return true, nil
	}

	var currentVersion int64
	q := fmt.Sprintf("SELECT version FROM %s.%s LIMIT 1", pgx.Identifier{schema}.Sanitize(), MigrationTable)
	err = pool.QueryRow(ctx, q).Scan(&currentVersion)
	if err != nil && err != pgx.ErrNoRows {
		return false, fmt.Errorf("failed to get current migration version: %w", err)
	}
	migrations := BuildMigrations(schema, isCockroach, listenNotify)
	return currentVersion < migrations[len(migrations)-1].Version, nil
}

// CleanupInvalidIndexes drops indexes left in an INVALID state by a prior
// failed CREATE INDEX CONCURRENTLY. Such indexes are not used by the planner
// but block recreating an index of the same name. Must be called before
// retrying an online migration.
func CleanupInvalidIndexes(ctx context.Context, pool *pgxpool.Pool, schema string, progress io.Writer) error {
	q := `SELECT i.relname FROM pg_index ix
	      JOIN pg_class i ON i.oid = ix.indexrelid
	      JOIN pg_class t ON t.oid = ix.indrelid
	      JOIN pg_namespace n ON n.oid = t.relnamespace
	      WHERE NOT ix.indisvalid AND n.nspname = $1`
	rows, err := pool.Query(ctx, q, schema)
	if err != nil {
		return fmt.Errorf("failed to list invalid indexes: %w", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan invalid index name: %w", err)
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate invalid indexes: %w", err)
	}
	sanitizedSchema := pgx.Identifier{schema}.Sanitize()
	for _, name := range names {
		logf(progress, "Dropping index %s.%s, left invalid by a prior failed migration", schema, name)
		dropQ := fmt.Sprintf(`DROP INDEX CONCURRENTLY IF EXISTS %s.%s`, sanitizedSchema, pgx.Identifier{name}.Sanitize())
		if _, err := pool.Exec(ctx, dropQ); err != nil {
			return fmt.Errorf("failed to drop invalid index %s.%s: %w", schema, name, err)
		}
	}
	return nil
}

// RunMigrations applies every migration above the schema's recorded version.
//
// Unlike the SDK's copy this does not retry: the SDK migrates during
// application startup, where a transient database error should not fail a
// deploy, while a CLI invocation can simply report the error and be run again.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, schema string, isCockroach, listenNotify bool, progress io.Writer) error {
	migrations := BuildMigrations(schema, isCockroach, listenNotify)
	sanitizedSchema := pgx.Identifier{schema}.Sanitize()

	// Schema + migrations table setup in a single short transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	var schemaExists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`,
		schema).Scan(&schemaExists); err != nil {
		return fmt.Errorf("failed to check if schema %s exists: %w", schema, err)
	}
	if !schemaExists {
		createSchemaQuery := fmt.Sprintf("CREATE SCHEMA %s", sanitizedSchema)
		if _, err := tx.Exec(ctx, createSchemaQuery); err != nil {
			return fmt.Errorf("failed to create schema %s: %w", schema, err)
		}
		logf(progress, "Created schema %s", schema)
	}
	var migrationTableExists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)`,
		schema, MigrationTable).Scan(&migrationTableExists); err != nil {
		return fmt.Errorf("failed to check if migration table exists: %w", err)
	}
	if !migrationTableExists {
		createTableQuery := fmt.Sprintf(`CREATE TABLE %s.%s (version BIGINT NOT NULL PRIMARY KEY)`,
			sanitizedSchema, MigrationTable)
		if _, err := tx.Exec(ctx, createTableQuery); err != nil {
			return fmt.Errorf("failed to create migrations table: %w", err)
		}
	}
	var currentVersion int64
	q := fmt.Sprintf("SELECT version FROM %s.%s LIMIT 1", sanitizedSchema, MigrationTable)
	if err := tx.QueryRow(ctx, q).Scan(&currentVersion); err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit migration setup transaction: %w", err)
	}

	// Apply pending migrations one at a time.
	invalidIndexesCleaned := false
	applied := 0
	for _, migration := range migrations {
		if migration.Version <= currentVersion {
			continue
		}
		logf(progress, "Applying migration %d", migration.Version)

		if migration.Online {
			// Online migrations must run outside a transaction so PostgreSQL will accept CREATE/DROP INDEX CONCURRENTLY.
			// Before the first online migration, sweep up any indexes left INVALID by a prior crashed run.
			// The version bump is necessarily a second, non-atomic round-trip. If it fails and must re-run, re-executing the migration has to be safe.
			if !invalidIndexesCleaned {
				if err := CleanupInvalidIndexes(ctx, pool, schema, progress); err != nil {
					return err
				}
				invalidIndexesCleaned = true
			}
			if _, err := pool.Exec(ctx, migration.SQL); err != nil {
				return fmt.Errorf("failed to execute migration %d: %w", migration.Version, err)
			}
			if err := writeMigrationVersion(ctx, pool, schema, migration.Version, currentVersion); err != nil {
				return err
			}
			currentVersion = migration.Version
			applied++
			continue
		}

		if len(migration.Steps) > 0 {
			// A migration that cannot be one transaction: each step commits on
			// its own, and the version row only moves once all of them have.
			// A failure part-way through therefore replays the whole migration
			// on the next run, which is safe because every step is idempotent.
			if err := applySteppedMigration(ctx, pool, schema, migration, currentVersion); err != nil {
				return err
			}
			currentVersion = migration.Version
			applied++
			continue
		}

		if err := applyCatalogMigration(ctx, pool, schema, migration, currentVersion); err != nil {
			return err
		}
		currentVersion = migration.Version
		applied++
	}

	logf(progress, "Applied %d migration(s); system database is at migration %d", applied, currentVersion)
	return nil
}

// applySteppedMigration applies a migration whose statements must not share a
// transaction, then advances the version row.
//
// Each Exec on the pool is its own implicit transaction, which is the whole
// point: CockroachDB before v25 will not build a partial index on a column that
// became visible in the transaction still running, and batching the statements
// any other way — one multi-statement query, or two queries inside one explicit
// transaction — keeps them in one transaction and fails the same way.
//
// The version bump is deliberately last and separate. Advancing it between
// steps would strand the migration half-applied with nothing willing to finish
// it, which is the failure this arrangement exists to avoid.
func applySteppedMigration(
	ctx context.Context,
	pool *pgxpool.Pool,
	schema string,
	migration MigrationFile,
	currentVersion int64,
) error {
	for i, step := range migration.Steps {
		if strings.TrimSpace(step) == "" {
			continue
		}
		if _, err := pool.Exec(ctx, step); err != nil {
			return fmt.Errorf("failed to execute migration %d (step %d of %d): %w",
				migration.Version, i+1, len(migration.Steps), err)
		}
	}
	return writeMigrationVersion(ctx, pool, schema, migration.Version, currentVersion)
}

// applyCatalogMigration runs a single non-online migration and its version bump in one transaction.
func applyCatalogMigration(
	ctx context.Context,
	pool *pgxpool.Pool,
	schema string,
	migration MigrationFile,
	currentVersion int64,
) error {
	mtx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction for migration %d: %w", migration.Version, err)
	}
	defer mtx.Rollback(ctx)

	// A migration that renders empty (migration 20 on CockroachDB, say) still
	// advances the version row, so it is not re-evaluated next time.
	if strings.TrimSpace(migration.SQL) != "" {
		if _, err := mtx.Exec(ctx, migration.SQL); err != nil {
			return fmt.Errorf("failed to execute migration %d: %w", migration.Version, err)
		}
	}

	if err := writeMigrationVersion(ctx, mtx, schema, migration.Version, currentVersion); err != nil {
		return err
	}
	if err := mtx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit migration %d: %w", migration.Version, err)
	}
	return nil
}

func writeMigrationVersion(ctx context.Context, exec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, schema string, version int64, lastApplied int64) error {
	sanitizedSchema := pgx.Identifier{schema}.Sanitize()
	if lastApplied == 0 {
		insertQuery := fmt.Sprintf("INSERT INTO %s.%s (version) VALUES ($1)", sanitizedSchema, MigrationTable)
		if _, err := exec.Exec(ctx, insertQuery, version); err != nil {
			return fmt.Errorf("failed to insert migration version %d: %w", version, err)
		}
	} else {
		updateQuery := fmt.Sprintf("UPDATE %s.%s SET version = $1", sanitizedSchema, MigrationTable)
		if _, err := exec.Exec(ctx, updateQuery, version); err != nil {
			return fmt.Errorf("failed to update migration version to %d: %w", version, err)
		}
	}
	return nil
}

// logf writes one progress line. A nil writer discards, so callers that have
// nothing to show need no branch.
func logf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format+"\n", args...)
}
