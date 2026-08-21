//go:build integration

package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dbos-inc/dbos-ctl/internal/migrations"
)

// startPostgres brings up a bare Postgres and returns a URL template with one
// %s for the database name. The conductor fixture is deliberately not reused:
// these tests want a server nobody has migrated yet.
func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	pg, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("dbos"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	host, err := pg.Host(ctx)
	if err != nil {
		t.Fatalf("postgres host: %v", err)
	}
	port, err := pg.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("postgres port: %v", err)
	}
	return fmt.Sprintf("postgres://postgres:dbos@%s:%s/%%s?sslmode=disable", host, port.Port())
}

// runMigrateOrFail runs the command the way the binary does, returning what the
// operator would have seen on stderr.
func runMigrateOrFail(t *testing.T, args ...string) string {
	t.Helper()
	cmd, out := newMigrateCmd(t, args...)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dbosctl migrate %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out.String()
}

func connect(t *testing.T, url string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect %s: %v", url, err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func scalar[T any](t *testing.T, conn *pgx.Conn, query string, args ...any) T {
	t.Helper()
	var v T
	if err := conn.QueryRow(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return v
}

// TestMigrateCreatesSystemDatabaseIntegration proves the whole first run: the
// database does not exist, and afterwards it holds a fully migrated schema. The
// second run proves re-running is safe, which is what makes this usable from a
// deploy script.
func TestMigrateCreatesSystemDatabaseIntegration(t *testing.T) {
	urlFor := startPostgres(t)
	dbURL := fmt.Sprintf(urlFor, "dbos_sys")

	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	latest := migrations.LatestVersion()
	if got := scalar[int64](t, conn, `SELECT version FROM dbos.dbos_migrations`); got != latest {
		t.Errorf("recorded migration version %d, want %d", got, latest)
	}
	if !scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='dbos' AND table_name='workflow_status')`) {
		t.Error("workflow_status was not created")
	}
	// A column from the shared cross-SDK range: its absence would mean the
	// vendored set stopped short of the migrations other SDKs assume.
	if !scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='dbos' AND table_name='workflow_status' AND column_name='application_name')`) {
		t.Error("workflow_status.application_name is missing: the shared migrations did not run")
	}

	// Second run: nothing pending, and it says so rather than reapplying.
	second := runMigrateOrFail(t, "--db-url", dbURL)
	if !strings.Contains(second, "already up to date") {
		t.Errorf("re-running did not report an up-to-date database:\n%s", second)
	}
	if got := scalar[int64](t, conn, `SELECT version FROM dbos.dbos_migrations`); got != latest {
		t.Errorf("version moved to %d on a no-op run, want %d", got, latest)
	}
}

// TestMigrateWithoutListenNotifyIntegration proves the flag reaches a real
// database: the schema comes out complete and at the latest version, but with
// none of the triggers that would fire pg_notify inside a write transaction.
// This is the shape a deployment behind a transaction-mode pooler needs, which
// nothing can detect and so has to be asked for.
func TestMigrateWithoutListenNotifyIntegration(t *testing.T) {
	urlFor := startPostgres(t)
	dbURL := fmt.Sprintf(urlFor, "dbos_sys")

	runMigrateOrFail(t, "--db-url", dbURL, "--no-listen-notify")

	conn := connect(t, dbURL)
	if got := scalar[int64](t, conn, `SELECT version FROM dbos.dbos_migrations`); got != migrations.LatestVersion() {
		t.Errorf("recorded migration version %d, want %d", got, migrations.LatestVersion())
	}
	if n := scalar[int64](t, conn, `SELECT count(*) FROM pg_trigger tg
		JOIN pg_class c ON c.oid = tg.tgrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'dbos' AND NOT tg.tgisinternal`); n != 0 {
		t.Errorf("%d trigger(s) installed with --no-listen-notify, want 0", n)
	}
	for _, fn := range []string{"notifications_function", "workflow_events_function", "streams_function"} {
		if scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'dbos' AND p.proname = $1)`, fn) {
			t.Errorf("%s exists with --no-listen-notify", fn)
		}
	}
	// The tables and functions that are not about notification are still there.
	if !scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='dbos' AND table_name='streams')`) {
		t.Error("streams table is missing: --no-listen-notify removed more than the triggers")
	}
	if !scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'dbos' AND p.proname = 'enqueue_workflow')`) {
		t.Error("enqueue_workflow is missing: --no-listen-notify removed more than the triggers")
	}
}

// TestMigrateDefaultInstallsTheNotificationTriggerIntegration is the other half
// of the pair: by default a PostgreSQL database gets the notifications trigger
// that migration 1 installs and migrations 43 and 44 do not drop.
func TestMigrateDefaultInstallsTheNotificationTriggerIntegration(t *testing.T) {
	urlFor := startPostgres(t)
	dbURL := fmt.Sprintf(urlFor, "dbos_sys")

	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	triggers := scalar[int64](t, conn, `SELECT count(*) FROM pg_trigger tg
		JOIN pg_class c ON c.oid = tg.tgrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'dbos' AND NOT tg.tgisinternal AND tg.tgname = 'dbos_notifications_trigger'`)
	if triggers != 1 {
		t.Errorf("found %d dbos_notifications_trigger, want 1", triggers)
	}
	// Migrations 43 and 44 dropped these two, so they must not survive.
	for _, tg := range []string{"dbos_streams_trigger", "dbos_workflow_events_trigger"} {
		if scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgname = $1 AND NOT tgisinternal)`, tg) {
			t.Errorf("%s survived the migrations that drop it", tg)
		}
	}
}

// TestMigrateCustomSchemaIntegration proves --schema is honored everywhere, not
// just in the printed SQL: a second schema in the same database migrates
// independently of the default one.
func TestMigrateCustomSchemaIntegration(t *testing.T) {
	urlFor := startPostgres(t)
	dbURL := fmt.Sprintf(urlFor, "dbos_sys")

	runMigrateOrFail(t, "--db-url", dbURL, "--schema", "tenant_a")

	conn := connect(t, dbURL)
	if got := scalar[int64](t, conn, `SELECT version FROM tenant_a.dbos_migrations`); got != migrations.LatestVersion() {
		t.Errorf("tenant_a is at migration %d, want the latest", got)
	}
	if scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name='dbos')`) {
		t.Error("migrating tenant_a also created the default schema")
	}
}

// TestMigrateGrantsApplicationRoleIntegration proves --app-role produces a role
// that can actually use the system tables — the point of the flag is that the
// application need not own the database or hold DDL rights.
func TestMigrateGrantsApplicationRoleIntegration(t *testing.T) {
	urlFor := startPostgres(t)
	dbURL := fmt.Sprintf(urlFor, "dbos_sys")

	admin := connect(t, fmt.Sprintf(urlFor, "postgres"))
	if _, err := admin.Exec(context.Background(), `CREATE ROLE app_role LOGIN PASSWORD 'app'`); err != nil {
		t.Fatalf("create role: %v", err)
	}

	runMigrateOrFail(t, "--db-url", dbURL, "--app-role", "app_role")

	conn := connect(t, dbURL)
	if !scalar[bool](t, conn, `SELECT has_schema_privilege('app_role', 'dbos', 'USAGE')`) {
		t.Error("app_role cannot use the dbos schema")
	}
	for _, priv := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		if !scalar[bool](t, conn, `SELECT has_table_privilege('app_role', 'dbos.workflow_status', $1)`, priv) {
			t.Errorf("app_role lacks %s on workflow_status", priv)
		}
	}
}

// TestMigratedDatabaseIsUsableBySDKIntegration is the compatibility claim that
// justifies vendoring the migrations at all: a database dbosctl migrated is one
// a DBOS SDK will connect to and leave alone. The SDK pinned in go.mod ships the
// same migration set that is vendored here, so what this pins down is that two
// copies of one set do not fight over a database — the SDK finds nothing pending
// and does not touch the version row.
func TestMigratedDatabaseIsUsableBySDKIntegration(t *testing.T) {
	urlFor := startPostgres(t)
	dbURL := fmt.Sprintf(urlFor, "dbos_sys")

	runMigrateOrFail(t, "--db-url", dbURL)
	conn := connect(t, dbURL)
	before := scalar[int64](t, conn, `SELECT version FROM dbos.dbos_migrations`)

	client, err := dbos.NewClient(context.Background(), dbos.ClientConfig{DatabaseURL: dbURL})
	if err != nil {
		t.Fatalf("SDK client rejected a dbosctl-migrated database: %v", err)
	}
	// The SDK's Client methods take the client as their first argument.
	defer client.Shutdown(client, 5*time.Second)

	if _, err := client.ListWorkflows(client); err != nil {
		t.Errorf("SDK could not read the migrated schema: %v", err)
	}
	if after := scalar[int64](t, conn, `SELECT version FROM dbos.dbos_migrations`); after != before {
		t.Errorf("SDK moved the migration version from %d to %d", before, after)
	}
}
