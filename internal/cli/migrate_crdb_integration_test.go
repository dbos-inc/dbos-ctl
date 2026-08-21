//go:build integration

package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/joho/godotenv"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"

	"github.com/dbos-inc/dbos-ctl/internal/migrations"
)

// crdbImage is the CockroachDB the migrations are tested against. Pinned to a
// minor stream rather than :latest so a CockroachDB release cannot turn this
// job red with no change in this repo — the same reasoning as CONDUCTOR_IMAGE.
// Override with DBOS_TEST_COCKROACH_IMAGE to check a different one.
const crdbImage = "cockroachdb/cockroach:latest-v26.2"

// crdbEnableEnv gates this tier. The image is ~500MB and these tests are the
// slowest in the suite, so they are opt-in locally and always on in CI.
const crdbEnableEnv = "DBOS_TEST_COCKROACH"

// startCockroach brings up a single-node CockroachDB and returns a URL template
// with one %s for the database name, matching startPostgres.
func startCockroach(t *testing.T) string {
	t.Helper()
	// Tests run in the package directory, two levels below the module root.
	_ = godotenv.Load("../../.env")
	if os.Getenv(crdbEnableEnv) == "" {
		t.Skipf("integration: %s not set (see .env.example)", crdbEnableEnv)
	}

	image := crdbImage
	if override := os.Getenv("DBOS_TEST_COCKROACH_IMAGE"); override != "" {
		image = override
	}

	ctx := context.Background()
	// Insecure: the container's secure mode issues client certificates, and the
	// module hands those back as a registered driver name rather than a URL,
	// which is not something the command under test can be given.
	crdb, err := cockroachdb.Run(ctx, image, cockroachdb.WithInsecure())
	if err != nil {
		t.Fatalf("start cockroachdb: %v", err)
	}
	t.Cleanup(func() { _ = crdb.Terminate(ctx) })

	host, err := crdb.Host(ctx)
	if err != nil {
		t.Fatalf("cockroachdb host: %v", err)
	}
	port, err := crdb.MappedPort(ctx, "26257/tcp")
	if err != nil {
		t.Fatalf("cockroachdb port: %v", err)
	}
	return fmt.Sprintf("postgres://root@%s:%s/%%s?sslmode=disable", host, port.Port())
}

// TestMigrateCockroachIntegration proves the whole set applies to CockroachDB.
// The dialect differences are spread across a dozen migrations — no
// CONCURRENTLY, a different migration 28, migration 10 applied in Go, no
// ALTER FUNCTION — and reaching the latest version is what says every one of
// them was rendered correctly for this engine.
//
// Migrations 43 and 44 are skipped here — CockroachDB before v25 cannot parse
// DROP TRIGGER, and there is nothing to drop — so reaching the latest version
// also proves that skip still happens. Sending those statements to CockroachDB
// would stop the run at migration 43 rather than reaching the end.
func TestMigrateCockroachIntegration(t *testing.T) {
	urlFor := startCockroach(t)
	dbURL := fmt.Sprintf(urlFor, "dbos_sys")

	out := runMigrateOrFail(t, "--db-url", dbURL)

	// Detection, not configuration: nobody passed --no-listen-notify.
	if !strings.Contains(out, "Detected CockroachDB") {
		t.Errorf("CockroachDB was not detected:\n%s", out)
	}
	if !strings.Contains(out, "no LISTEN/NOTIFY") {
		t.Errorf("LISTEN/NOTIFY was not turned off for CockroachDB:\n%s", out)
	}

	conn := connect(t, dbURL)
	if got := scalar[int64](t, conn, `SELECT version FROM dbos.dbos_migrations`); got != migrations.LatestVersion() {
		t.Errorf("recorded migration version %d, want %d", got, migrations.LatestVersion())
	}
	if !scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='dbos' AND table_name='workflow_status')`) {
		t.Error("workflow_status was not created")
	}
	if !scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='dbos' AND table_name='workflow_status' AND column_name='application_name')`) {
		t.Error("workflow_status.application_name is missing: the shared migrations did not run")
	}

	// Nothing notification-shaped should exist: migration 1's LISTEN/NOTIFY half
	// and migration 39 never ran, so there was never anything for 43 and 44 to
	// drop.
	for _, fn := range []string{"notifications_function", "workflow_events_function", "streams_function"} {
		if scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'dbos' AND p.proname = $1)`, fn) {
			t.Errorf("%s exists on CockroachDB", fn)
		}
	}

	// A second run has nothing to do, which is what makes this safe in a deploy
	// script that runs on every release.
	second := runMigrateOrFail(t, "--db-url", dbURL)
	if !strings.Contains(second, "already up to date") {
		t.Errorf("re-running did not report an up-to-date database:\n%s", second)
	}
}

// TestMigrateCockroachIgnoresListenNotifyRequestIntegration proves detection
// wins over the flag. Asking for LISTEN/NOTIFY on CockroachDB is a mistake the
// command corrects rather than an instruction it follows — the migrations that
// install those triggers cannot be applied there at all.
func TestMigrateCockroachIgnoresListenNotifyRequestIntegration(t *testing.T) {
	urlFor := startCockroach(t)
	dbURL := fmt.Sprintf(urlFor, "dbos_sys")

	// No --no-listen-notify: the default asks for the triggers.
	out := runMigrateOrFail(t, "--db-url", dbURL)
	if !strings.Contains(out, "migrating without the notification triggers") {
		t.Errorf("the request for LISTEN/NOTIFY was not corrected:\n%s", out)
	}

	conn := connect(t, dbURL)
	if n := scalar[int64](t, conn, `SELECT count(*) FROM pg_trigger tg
		JOIN pg_class c ON c.oid = tg.tgrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'dbos' AND NOT tg.tgisinternal`); n != 0 {
		t.Errorf("%d trigger(s) installed on CockroachDB, want 0", n)
	}
}
