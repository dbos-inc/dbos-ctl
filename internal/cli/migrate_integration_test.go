//go:build integration

package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dbos-inc/dbos-ctl/internal/migrations"
)

// TestMigrateCreatesSystemDatabaseIntegration proves the whole first run: the
// database does not exist, and afterwards it holds a fully migrated schema. The
// second run proves re-running is safe, which is what makes this usable from a
// deploy script.
//
// On CockroachDB the same assertions carry more: the dialect differences are
// spread across a dozen migrations — no CONCURRENTLY, a different migration 28,
// migration 10 applied in Go, no ALTER FUNCTION, no DROP TRIGGER — and reaching
// the latest version is what says every one of them was rendered for the engine
// that is actually there.
func TestMigrateCreatesSystemDatabaseIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")

	out := runMigrateOrFail(t, "--db-url", dbURL)

	if e.cockroach {
		// Detection, not configuration: nobody passed a flag.
		if !strings.Contains(out, "Detected CockroachDB") {
			t.Errorf("CockroachDB was not detected:\n%s", out)
		}
		if !strings.Contains(out, "no LISTEN/NOTIFY") {
			t.Errorf("LISTEN/NOTIFY was not turned off for CockroachDB:\n%s", out)
		}
	} else if strings.Contains(out, "Detected CockroachDB") {
		t.Errorf("PostgreSQL was detected as CockroachDB:\n%s", out)
	}

	conn := connect(t, dbURL)
	latest := migrations.LatestVersion()
	if got := scalar[int64](t, conn, `SELECT version FROM dbos.dbos_migrations`); got != latest {
		t.Errorf("recorded migration version %d, want %d", got, latest)
	}
	if !scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='dbos' AND table_name='workflow_status')`) {
		t.Error("workflow_status was not created")
	}
	// A column from the shared cross-SDK range: its absence would mean the
	// migration set stopped short of what other SDKs assume.
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

// TestMigrateNotificationTriggersIntegration is the one place the two engines
// are expected to differ. PostgreSQL gets the notifications trigger migration 1
// installs; CockroachDB gets nothing, because it has no LISTEN/NOTIFY on any
// version. Either way the triggers migrations 43 and 44 drop must be gone.
func TestMigrateNotificationTriggersIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")

	runMigrateOrFail(t, "--db-url", dbURL)
	conn := connect(t, dbURL)

	if e.listenNotify {
		if n := userTriggers(t, conn, "dbos"); n != 1 {
			t.Errorf("found %d triggers, want exactly the notifications trigger", n)
		}
		if !scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgname = 'dbos_notifications_trigger' AND NOT tgisinternal)`) {
			t.Error("dbos_notifications_trigger was not installed")
		}
	} else if n := userTriggers(t, conn, "dbos"); n != 0 {
		t.Errorf("found %d triggers on an engine with no LISTEN/NOTIFY, want 0", n)
	}

	// Migrations 43 and 44 remove these two wherever they were created.
	for _, tg := range []string{"dbos_streams_trigger", "dbos_workflow_events_trigger"} {
		if scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgname = $1 AND NOT tgisinternal)`, tg) {
			t.Errorf("%s survived the migrations that drop it", tg)
		}
	}
	for _, fn := range []string{"workflow_events_function", "streams_function"} {
		if functionExists(t, conn, "dbos", fn) {
			t.Errorf("%s survived the migrations that drop it", fn)
		}
	}
}

// TestMigrateWithoutListenNotifyIntegration proves the flag reaches a real
// database: the schema comes out complete and at the latest version, but with
// none of the triggers that would fire pg_notify inside a write transaction.
// This is the shape a deployment behind a transaction-mode pooler needs, which
// nothing can detect and so has to be asked for. On CockroachDB the flag is
// redundant — that is what the engine gets anyway — and asking for it must not
// change anything else.
func TestMigrateWithoutListenNotifyIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")

	runMigrateOrFail(t, "--db-url", dbURL, "--no-listen-notify")

	conn := connect(t, dbURL)
	if got := scalar[int64](t, conn, `SELECT version FROM dbos.dbos_migrations`); got != migrations.LatestVersion() {
		t.Errorf("recorded migration version %d, want %d", got, migrations.LatestVersion())
	}
	if n := userTriggers(t, conn, "dbos"); n != 0 {
		t.Errorf("%d trigger(s) installed with --no-listen-notify, want 0", n)
	}
	for _, fn := range []string{"notifications_function", "workflow_events_function", "streams_function"} {
		if functionExists(t, conn, "dbos", fn) {
			t.Errorf("%s exists with --no-listen-notify", fn)
		}
	}
	// The tables and functions that are not about notification are still there.
	if !scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='dbos' AND table_name='streams')`) {
		t.Error("streams table is missing: --no-listen-notify removed more than the triggers")
	}
	if !functionExists(t, conn, "dbos", "enqueue_workflow") {
		t.Error("enqueue_workflow is missing: --no-listen-notify removed more than the triggers")
	}
}

// TestMigrateCockroachIgnoresListenNotifyRequestIntegration proves detection
// wins over the flag. Asking for LISTEN/NOTIFY on CockroachDB is a mistake the
// command corrects rather than an instruction it follows — the migrations that
// install those triggers cannot be applied there at all.
func TestMigrateCockroachIgnoresListenNotifyRequestIntegration(t *testing.T) {
	e := startEngine(t)
	e.onlyOn(t, engineCockroach)
	dbURL := e.url("dbos_sys")

	// No --no-listen-notify: the default asks for the triggers.
	out := runMigrateOrFail(t, "--db-url", dbURL)
	if !strings.Contains(out, "migrating without the notification triggers") {
		t.Errorf("the request for LISTEN/NOTIFY was not corrected:\n%s", out)
	}
	if n := userTriggers(t, connect(t, dbURL), "dbos"); n != 0 {
		t.Errorf("%d trigger(s) installed on CockroachDB, want 0", n)
	}
}

// TestMigrateCustomSchemaIntegration proves --schema is honored everywhere, not
// just in the printed SQL: a second schema in the same database migrates
// independently of the default one.
func TestMigrateCustomSchemaIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")

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
	e := startEngine(t)
	dbURL := e.url("dbos_sys")

	// No password: nothing here logs in as the role, and CockroachDB's insecure
	// mode — which is how the test container runs — refuses to set one.
	admin := connect(t, e.url("postgres"))
	if _, err := admin.Exec(context.Background(), `CREATE ROLE app_role`); err != nil {
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
// owning the migrations here rests on: a database dbosctl migrated is one a DBOS
// SDK will connect to and leave alone. The SDK pinned in go.mod ships the same
// migration set, so what this pins down is that two copies of one set do not
// fight over a database — the SDK finds nothing pending and does not touch the
// version row.
func TestMigratedDatabaseIsUsableBySDKIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")

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
