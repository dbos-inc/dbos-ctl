package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newMigrateCmd builds a command carrying the real flag definitions, so a test
// exercises the flags the binary actually has.
func newMigrateCmd(t *testing.T, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{Use: "migrate", RunE: runMigrate, SilenceUsage: true, SilenceErrors: true}
	addMigrateFlags(cmd)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return cmd, &out
}

// unreachableURL points at a closed port. Print modes must succeed against it:
// that is what proves they never open a connection.
const unreachableURL = "postgres://nobody:secret@127.0.0.1:1/nowhere"

func TestPrintMigrationsNeverConnects(t *testing.T) {
	cmd, out := newMigrateCmd(t, "--print-migrations", "all", "--db-url", unreachableURL)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("print mode failed against an unreachable database: %v", err)
	}
	script := out.String()

	if !strings.HasPrefix(script, "-- DBOS system database migrations for postgres://nobody:***@127.0.0.1:1/nowhere\n") {
		t.Errorf("header is missing or leaks the password:\n%s", firstLine(script))
	}
	for _, want := range []string{
		"-- This script is for FRESH databases only.",
		"CREATE SCHEMA IF NOT EXISTS \"dbos\";",
		"-- Migration 1",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("printed script is missing %q", want)
		}
	}
}

func TestPrintMigrationsFromAVersionOmitsTheFreshWarning(t *testing.T) {
	cmd, out := newMigrateCmd(t, "--print-migrations", "40")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	script := out.String()
	if strings.Contains(script, "FRESH databases") {
		t.Error("an upgrade script is labelled as fresh-database only")
	}
	if strings.Contains(script, "-- Migration 39") {
		t.Error("upgrade script includes a migration below the requested start")
	}
	if !strings.Contains(script, "-- Migration 40") {
		t.Error("upgrade script omits the requested migration")
	}
}

// TestPrintMigrationsWithoutListenNotify proves the flag is the sole arbiter in
// print mode — nothing here can detect a pooler — and that the resulting script
// says which of the two it is, since no reader could otherwise tell.
func TestPrintMigrationsWithoutListenNotify(t *testing.T) {
	cmd, out := newMigrateCmd(t, "--print-migrations", "all", "--no-listen-notify")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	script := out.String()

	// Matched on the statements, not the word: the header comment below names
	// pg_notify itself.
	for _, unwanted := range []string{"PERFORM pg_notify", "CREATE TRIGGER"} {
		if strings.Contains(script, unwanted) {
			t.Errorf("script generated with --no-listen-notify contains %q", unwanted)
		}
	}
	if !strings.Contains(script, "-- Generated with --no-listen-notify") {
		t.Error("script does not record that the triggers were left out")
	}
	// The drops still run: they are IF EXISTS no-ops here, and skipping them
	// would strand the triggers on a database migrated the other way.
	if !strings.Contains(script, "DROP TRIGGER IF EXISTS dbos_streams_trigger") {
		t.Error("script omits migration 43's drop")
	}
	if !strings.Contains(script, "DROP TRIGGER IF EXISTS dbos_workflow_events_trigger") {
		t.Error("script omits migration 44's drop")
	}

	with, _ := newMigrateCmd(t, "--print-migrations", "all")
	var withOut strings.Builder
	with.SetOut(&withOut)
	if err := with.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PERFORM pg_notify", "CREATE TRIGGER dbos_notifications_trigger"} {
		if !strings.Contains(withOut.String(), want) {
			t.Errorf("the default script is missing %q", want)
		}
	}
	if strings.Contains(withOut.String(), "-- Generated with --no-listen-notify") {
		t.Error("the default script claims the triggers were left out")
	}
}

func TestPrintUserRole(t *testing.T) {
	cmd, out := newMigrateCmd(t, "--print-user-role", "-r", "app_role", "--schema", "myapp")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	script := out.String()
	if !strings.HasPrefix(script, "-- Permissions on DBOS schema myapp for role app_role\n") {
		t.Errorf("header is missing:\n%s", firstLine(script))
	}
	if !strings.Contains(script, `GRANT USAGE ON SCHEMA "myapp" TO "app_role";`) {
		t.Errorf("grants are missing from:\n%s", script)
	}
}

// TestMigrateUsageErrors covers the flag combinations that cannot be honored.
// Each is a usage error (exit 2), not a runtime failure, and each says which
// flag to fix.
func TestMigrateUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "both print modes",
			args: []string{"--print-migrations", "all", "--print-user-role", "-r", "app"},
			want: "cannot be combined",
		},
		{
			name: "role print without a role",
			args: []string{"--print-user-role"},
			want: "requires --app-role",
		},
		{
			name: "unparseable version",
			args: []string{"--print-migrations", "later"},
			want: `invalid --print-migrations value "later"`,
		},
		{
			name: "quoted schema",
			args: []string{"--print-migrations", "all", "--schema", `db"os`},
			want: "schema names containing quotes",
		},
		{
			name: "quoted role",
			args: []string{"--print-user-role", "-r", `ap"p`},
			want: "role names containing quotes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _ := newMigrateCmd(t, tc.args...)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("command succeeded, want a usage error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
			if got := exitCodeFor(err); got != 2 {
				t.Errorf("exit code %d, want 2 for a usage error", got)
			}
		})
	}
}

// TestMigrateVersionOutOfRange is not a usage error: the flag parsed fine and
// the number is simply not a migration, so the message names the valid range.
func TestMigrateVersionOutOfRange(t *testing.T) {
	cmd, _ := newMigrateCmd(t, "--print-migrations", "9999")
	err := cmd.Execute()
	if err == nil {
		t.Fatal("command succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "valid migrations are 1 through") {
		t.Errorf("error does not name the valid range: %v", err)
	}
}

func TestMigrateRequiresADatabaseURL(t *testing.T) {
	t.Setenv(dbURLEnv, "")
	cmd, _ := newMigrateCmd(t)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("command succeeded with no database URL")
	}
	if !strings.Contains(err.Error(), "--db-url") || !strings.Contains(err.Error(), dbURLEnv) {
		t.Errorf("error names neither way to supply a URL: %v", err)
	}
}

// TestMigrateRejectsSQLite proves the SQLite gap is reported rather than
// attempted: only the Postgres migration set is vendored.
func TestMigrateRejectsSQLite(t *testing.T) {
	cmd, _ := newMigrateCmd(t, "--db-url", "sqlite:app.db")
	err := cmd.Execute()
	if err == nil {
		t.Fatal("command accepted a SQLite URL")
	}
	if !strings.Contains(err.Error(), "PostgreSQL") {
		t.Errorf("error does not explain what dbosctl migrates: %v", err)
	}
}

// TestMigrateReadsTheEnvironment proves the env var is a real fallback: with it
// set, resolution gets far enough to reject the SQLite scheme it names.
func TestMigrateReadsTheEnvironment(t *testing.T) {
	t.Setenv(dbURLEnv, "sqlite:from-env.db")
	cmd, _ := newMigrateCmd(t)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "from-env.db") {
		t.Errorf("environment URL was not used: %v", err)
	}
}

func TestMaskPassword(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"postgres://user:hunter2@localhost:5432/dbos", "postgres://user:***@localhost:5432/dbos"},
		{"postgres://user:hunter2@localhost:5432/dbos?sslmode=disable", "postgres://user:***@localhost:5432/dbos?sslmode=disable"},
		{"postgres://user@localhost:5432/dbos", "postgres://user@localhost:5432/dbos"},
		{"postgres://localhost:5432/dbos", "postgres://localhost:5432/dbos"},
		{"host=localhost password=hunter2 dbname=dbos", "host=localhost password=*** dbname=dbos"},
		{"host=localhost PASSWORD = hunter2 dbname=dbos", "host=localhost password=*** dbname=dbos"},
	}
	for _, tc := range cases {
		if got := maskPassword(tc.in); got != tc.want {
			t.Errorf("maskPassword(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.Contains(maskPassword(tc.in), "hunter2") {
			t.Errorf("maskPassword(%q) leaked the password", tc.in)
		}
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
