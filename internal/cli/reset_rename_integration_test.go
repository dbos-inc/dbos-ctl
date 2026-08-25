//go:build integration

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/dbos-inc/dbos-ctl/internal/migrations"
)

func runResetOrFail(t *testing.T, args ...string) string {
	t.Helper()
	cmd, out := newResetCmd(t, append(args, "--force")...)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dbosctl reset %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out.String()
}

func runRenameOrFail(t *testing.T, args ...string) string {
	t.Helper()
	cmd, out := newRenameCmd(t, append(args, "--force")...)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dbosctl rename-application %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out.String()
}

// seedWorkflow inserts a workflow and one step for it, owned by application
// (empty inserts NULL, i.e. a row no application owns).
func seedWorkflow(t *testing.T, conn *pgx.Conn, id, status, application string) {
	t.Helper()
	var owner any
	if application != "" {
		owner = application
	}
	exec(t, conn, `INSERT INTO dbos.workflow_status (workflow_uuid, status, name, application_name)
	               VALUES ($1, $2, 'test.workflow', $3)`, id, status, owner)
	exec(t, conn, `INSERT INTO dbos.operation_outputs (workflow_uuid, function_id, function_name, application_name)
	               VALUES ($1, 0, 'step', $2)`, id, owner)
}

func seedOwnedRows(t *testing.T, conn *pgx.Conn, suffix, application string) {
	t.Helper()
	var owner any
	if application != "" {
		owner = application
	}
	exec(t, conn, `INSERT INTO dbos.queues (queue_id, name, application_name) VALUES ($1, $2, $3)`,
		"q-"+suffix, "queue-"+suffix, owner)
	exec(t, conn, `INSERT INTO dbos.workflow_schedules (schedule_id, schedule_name, workflow_name, schedule, context, application_name)
	               VALUES ($1, $2, 'test.scheduled', '* * * * *', '{}', $3)`,
		"s-"+suffix, "schedule-"+suffix, owner)
	exec(t, conn, `INSERT INTO dbos.application_versions (version_id, version_name, application_name) VALUES ($1, $2, $3)`,
		"v-"+suffix, "version-"+suffix, owner)
}

func exec(t *testing.T, conn *pgx.Conn, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func countRows(t *testing.T, conn *pgx.Conn, table string) int64 {
	t.Helper()
	return scalar[int64](t, conn, "SELECT count(*) FROM dbos."+table)
}

// TestResetEmptiesWithoutUnmigratingIntegration is the whole argument for the
// default: after a reset the database is empty *and still migrated*, so an
// application can start against it without anyone running migrate again.
func TestResetEmptiesWithoutUnmigratingIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")
	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	seedWorkflow(t, conn, "wf-1", "SUCCESS", "app")
	seedWorkflow(t, conn, "wf-2", "PENDING", "app")
	seedOwnedRows(t, conn, "1", "app")

	runResetOrFail(t, "--db-url", dbURL)

	for _, table := range []string{"workflow_status", "operation_outputs", "queues", "workflow_schedules", "application_versions"} {
		if n := countRows(t, conn, table); n != 0 {
			t.Errorf("%s still holds %d row(s) after reset", table, n)
		}
	}
	// The two things that make this a reset rather than a teardown.
	if got := scalar[int64](t, conn, `SELECT version FROM dbos.dbos_migrations`); got != migrations.LatestVersion() {
		t.Errorf("migration version is %d after reset, want %d: the schema was un-migrated", got, migrations.LatestVersion())
	}
	if !scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='dbos' AND table_name='workflow_status')`) {
		t.Error("workflow_status no longer exists after reset")
	}
}

// TestResetReportsWhatEachTableLostIntegration pins the ordering rule. Every
// foreign key in the schema cascades from workflow_status, so emptying it first
// would clear the child tables and leave their own DELETE reporting zero rows
// for a table it had just emptied. The counts are the only thing that notices,
// which is exactly why it needs a test rather than a comment.
func TestResetReportsWhatEachTableLostIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")
	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	seedWorkflow(t, conn, "wf-1", "SUCCESS", "app")
	seedWorkflow(t, conn, "wf-2", "SUCCESS", "app")

	out := runResetOrFail(t, "--db-url", dbURL)

	// Two workflows and their two steps: both lines must show 2, not the 0 a
	// cascade would leave behind.
	for _, want := range []string{"Emptied workflow_status (2 rows)", "Emptied operation_outputs (2 rows)"} {
		if !strings.Contains(out, want) {
			t.Errorf("reset did not report %q; a cascade emptied the table before its own delete:\n%s", want, out)
		}
	}
	// The child has to be reported before the parent, since that ordering is
	// what makes the number above true.
	if strings.Index(out, "Emptied operation_outputs") > strings.Index(out, "Emptied workflow_status") {
		t.Errorf("workflow_status was emptied before the tables that reference it:\n%s", out)
	}
	// The same counts go to stdout, as a table by default: the log lines above
	// are progress on stderr, the table is the result.
	if !strings.Contains(out, "TABLE") || !strings.Contains(out, "ROWS") {
		t.Errorf("reset did not write its per-table counts as a table:\n%s", out)
	}

	// And as JSON on request, so a scripted reset reads them without parsing
	// anything. Re-run against the now-empty schema: the shape is what is under
	// test, not the numbers.
	jsonOut := runResetOrFail(t, "--db-url", dbURL, "-o", "json")
	if !strings.Contains(jsonOut, `"table": "workflow_status"`) || !strings.Contains(jsonOut, `"rows":`) {
		t.Errorf("reset -o json did not write its per-table counts as JSON:\n%s", jsonOut)
	}
}

// TestSystemTablesMatchTheMigratedSchemaIntegration is what makes naming the
// tables safe instead of merely convenient. The list is compiled in so a reset
// can never touch a table DBOS did not create; the risk it takes on is going
// stale, and a migration that added a table would then leave it full while the
// command reported success. Compare the list against a real migrated schema so
// that adding a migration without adding its table fails here.
func TestSystemTablesMatchTheMigratedSchemaIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")
	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	rows, err := conn.Query(context.Background(),
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = 'dbos' AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	actual := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		// The migration table is the one a reset spares, so it is not in the list.
		if name != migrations.MigrationTable {
			actual[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	named := map[string]bool{}
	for _, table := range migrations.SystemTables {
		named[table] = true
		if !actual[table] {
			t.Errorf("SystemTables names %q, which a migrated schema does not have", table)
		}
	}
	for table := range actual {
		if !named[table] {
			t.Errorf("a migrated schema has %q, which SystemTables does not name: a reset would leave it full", table)
		}
	}
}

// A second reset must be as safe as the first: nothing to delete is not an
// error, which is what makes this usable between test runs.
func TestResetIsRepeatableIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")
	runMigrateOrFail(t, "--db-url", dbURL)

	runResetOrFail(t, "--db-url", dbURL)
	runResetOrFail(t, "--db-url", dbURL)
}

// A schema migrated past what this build knows may carry tables neither command
// names, and both work from compiled-in lists. Reset would leave such a table
// full and report success; rename would leave its rows under the old name and
// report success, which is the half-renamed application the whole design avoids.
// Both must refuse, and refuse before doing anything.
func TestSysdbCommandsRefuseASchemaAheadOfTheBinaryIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")
	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	seedWorkflow(t, conn, "wf-1", "SUCCESS", "old-app")
	seedOwnedRows(t, conn, "old", "old-app")
	// Stand in for a newer SDK having migrated this schema.
	exec(t, conn, `UPDATE dbos.dbos_migrations SET version = $1`, migrations.LatestVersion()+1)

	resetCmd, resetOut := newResetCmd(t, "--db-url", dbURL, "--force")
	if err := resetCmd.Execute(); err == nil {
		t.Errorf("reset emptied a schema ahead of the binary:\n%s", resetOut)
	} else if !strings.Contains(err.Error(), "upgrade dbosctl") {
		t.Errorf("reset refusal does not name the fix: %v", err)
	}

	renameCmd, renameOut := newRenameCmd(t, "--db-url", dbURL, "--from", "old-app", "--to", "new-app", "--force")
	if err := renameCmd.Execute(); err == nil {
		t.Errorf("rename re-owned rows in a schema ahead of the binary:\n%s", renameOut)
	} else if !strings.Contains(err.Error(), "upgrade dbosctl") {
		t.Errorf("rename refusal does not name the fix: %v", err)
	}

	// Refusing has to mean nothing moved. A rename that renamed the tables it
	// knows and then reported a version error would leave exactly the split
	// ownership the refusal exists to prevent.
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name = 'old-app'`); n != 1 {
		t.Errorf("the refused rename moved workflow rows anyway: %d left under the old name", n)
	}
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.queues WHERE application_name = 'old-app'`); n != 1 {
		t.Errorf("the refused rename moved queue rows anyway: %d left under the old name", n)
	}
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name = 'new-app'`); n != 0 {
		t.Errorf("the refused rename created %d row(s) under the new name", n)
	}
}

// TestResetScopedToApplicationIntegration covers the shared system database:
// one application's history goes, its neighbour's stays, and the steps of the
// deleted workflows go with them by foreign key rather than by a second pass.
func TestResetScopedToApplicationIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")
	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	seedWorkflow(t, conn, "billing-1", "SUCCESS", "billing")
	seedWorkflow(t, conn, "orders-1", "SUCCESS", "orders")
	seedWorkflow(t, conn, "unclaimed-1", "SUCCESS", "")
	seedOwnedRows(t, conn, "billing", "billing")
	seedOwnedRows(t, conn, "orders", "orders")

	runResetOrFail(t, "--db-url", dbURL, "--app", "billing")

	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name = 'billing'`); n != 0 {
		t.Errorf("billing still has %d workflow(s)", n)
	}
	// The cascade, not a second delete: operation_outputs rows go because their
	// workflow_status parent went.
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.operation_outputs WHERE workflow_uuid = 'billing-1'`); n != 0 {
		t.Errorf("billing's steps survived its workflows: %d row(s)", n)
	}
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name = 'orders'`); n != 1 {
		t.Errorf("orders lost workflows to billing's reset: %d row(s), want 1", n)
	}
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.operation_outputs WHERE workflow_uuid = 'orders-1'`); n != 1 {
		t.Errorf("orders lost steps to billing's reset: %d row(s), want 1", n)
	}
	// Unclaimed rows belong to no application, so a scoped reset is not the
	// thing that should decide their fate.
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name IS NULL`); n != 1 {
		t.Errorf("unclaimed workflows were taken by a scoped reset: %d row(s), want 1", n)
	}
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.queues WHERE application_name = 'orders'`); n != 1 {
		t.Error("orders lost its queue to billing's reset")
	}
}

func TestResetDropDatabaseIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_drop_me")
	runMigrateOrFail(t, "--db-url", dbURL)

	runResetOrFail(t, "--db-url", dbURL, "--drop-database")

	admin := connect(t, e.url("postgres"))
	if scalar[bool](t, admin, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = 'dbos_drop_me')`) {
		t.Error("the database still exists after --drop-database")
	}
}

// TestRenameMovesEveryOwnedKindIntegration checks the five row kinds the counts
// report, and that a rename leaves nothing behind under the old name.
func TestRenameMovesEveryOwnedKindIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")
	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	seedWorkflow(t, conn, "wf-terminal", "SUCCESS", "old-app")
	seedWorkflow(t, conn, "wf-pending", "PENDING", "old-app")
	seedWorkflow(t, conn, "wf-enqueued", "ENQUEUED", "old-app")
	// DELAYED counts as in-flight: it is scheduled to run, not finished, so it
	// must move with the version rows rather than in the batched tail.
	seedWorkflow(t, conn, "wf-delayed", "DELAYED", "old-app")
	seedOwnedRows(t, conn, "old", "old-app")
	seedWorkflow(t, conn, "wf-other", "SUCCESS", "other-app")

	out := runRenameOrFail(t, "--db-url", dbURL, "--from", "old-app", "--to", "new-app")

	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name = 'old-app'`); n != 0 {
		t.Errorf("%d workflow(s) left under the old name", n)
	}
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name = 'new-app'`); n != 4 {
		t.Errorf("new-app owns %d workflow(s), want 4", n)
	}
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.operation_outputs WHERE application_name = 'new-app'`); n != 4 {
		t.Errorf("new-app owns %d step(s), want 4", n)
	}
	for _, table := range []string{"queues", "workflow_schedules", "application_versions"} {
		n := scalar[int64](t, conn, fmt.Sprintf(`SELECT count(*) FROM dbos.%s WHERE application_name = 'new-app'`, table))
		if n != 1 {
			t.Errorf("new-app owns %d row(s) in %s, want 1", n, table)
		}
	}
	// A rename is scoped to the application named, not to everything present.
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name = 'other-app'`); n != 1 {
		t.Errorf("another application's workflows were re-owned: %d row(s), want 1", n)
	}
	for _, want := range []string{`"workflows": 4`, `"steps": 4`, `"queues": 1`, `"schedules": 1`, `"versions": 1`} {
		if !strings.Contains(out, want) {
			t.Errorf("reported counts are missing %s:\n%s", want, out)
		}
	}
}

// TestRenameReportsNothingWhenTheFirstTransactionRollsBackIntegration forces a
// failure partway through the opening transaction. Queues and schedules have
// already been updated at that point, so the counts would happily report them —
// but the rollback takes them back, and a count of rows that did not move is
// worse than no count at all.
func TestRenameReportsNothingWhenTheFirstTransactionRollsBackIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")
	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	seedWorkflow(t, conn, "wf-1", "SUCCESS", "old-app")
	seedOwnedRows(t, conn, "old", "old-app")
	// Drop the third table the transaction touches, so the first two succeed
	// and then it fails with work already done. Nothing references it.
	exec(t, conn, `DROP TABLE dbos.application_versions`)

	// Called directly rather than through the command: the CLI discards the
	// counts when it gets an error, so the contract this is about — what the
	// counts say alongside a failure — is only visible here.
	counts, err := migrations.RenameApplication(context.Background(), dbURL, migrations.RenameInput{
		OldName: "old-app",
		NewName: "new-app",
	}, io.Discard)
	if err == nil {
		t.Fatal("rename succeeded against a schema missing application_versions")
	}

	// The rollback: the queue and schedule updates that did run must be gone.
	for _, table := range []string{"queues", "workflow_schedules"} {
		n := scalar[int64](t, conn, fmt.Sprintf(`SELECT count(*) FROM dbos.%s WHERE application_name = 'new-app'`, table))
		if n != 0 {
			t.Errorf("%s kept %d row(s) re-owned by a transaction that rolled back", table, n)
		}
	}
	// And nothing may be reported as moved, since nothing was.
	if counts != (migrations.ApplicationRowCounts{}) {
		t.Errorf("counts report %+v after a rollback that moved nothing", counts)
	}
}

// Unclaimed rows are never implied. They move only when asked for, because
// claiming rows that predate system-database sharing is a decision.
func TestRenameAdoptsUnclaimedRowsOnlyWhenAskedIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")
	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	seedWorkflow(t, conn, "wf-owned", "SUCCESS", "old-app")
	seedWorkflow(t, conn, "wf-unclaimed", "SUCCESS", "")

	runRenameOrFail(t, "--db-url", dbURL, "--from", "old-app", "--to", "new-app")
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name IS NULL`); n != 1 {
		t.Fatalf("unclaimed rows were adopted without --adopt-unclaimed-rows: %d left, want 1", n)
	}

	runRenameOrFail(t, "--db-url", dbURL, "--to", "new-app", "--adopt-unclaimed-rows")
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name IS NULL`); n != 0 {
		t.Errorf("--adopt-unclaimed-rows left %d unclaimed row(s)", n)
	}
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name = 'new-app'`); n != 2 {
		t.Errorf("new-app owns %d workflow(s), want 2", n)
	}
}

// A batch size below the row count drives the watermark loop through several
// passes. If ranges were mis-bounded this either loops forever or leaves rows,
// and both show up here.
func TestRenameBatchesTerminalWorkflowsIntegration(t *testing.T) {
	e := startEngine(t)
	dbURL := e.url("dbos_sys")
	runMigrateOrFail(t, "--db-url", dbURL)

	conn := connect(t, dbURL)
	const total = 7
	for i := 0; i < total; i++ {
		seedWorkflow(t, conn, fmt.Sprintf("wf-%02d", i), "SUCCESS", "old-app")
	}

	out := runRenameOrFail(t, "--db-url", dbURL, "--from", "old-app", "--to", "new-app", "--batch-size", "2")

	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.workflow_status WHERE application_name = 'new-app'`); n != total {
		t.Errorf("batched rename moved %d workflow(s), want %d", n, total)
	}
	if n := scalar[int64](t, conn, `SELECT count(*) FROM dbos.operation_outputs WHERE application_name = 'new-app'`); n != total {
		t.Errorf("batched rename moved %d step(s), want %d", n, total)
	}
	if !strings.Contains(out, fmt.Sprintf(`"workflows": %d`, total)) {
		t.Errorf("counts do not add up across batches:\n%s", out)
	}
}
