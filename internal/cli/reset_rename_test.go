package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newResetCmd and newRenameCmd build commands carrying the real flag
// definitions, so a test exercises the flags the binary actually has.
func newResetCmd(t *testing.T, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{Use: "reset", RunE: runReset, SilenceUsage: true, SilenceErrors: true}
	addSysdbFlags(cmd.Flags())
	addResetFlags(cmd)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return cmd, &out
}

func newRenameCmd(t *testing.T, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{Use: "rename-application", RunE: runRenameApplication, SilenceUsage: true, SilenceErrors: true}
	addSysdbFlags(cmd.Flags())
	addRenameApplicationFlags(cmd)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return cmd, &out
}

// notATerminal forces the non-interactive path for one test, restoring the real
// check afterwards. Every destructive command consults it.
func notATerminal(t *testing.T) {
	t.Helper()
	original := isInteractive
	isInteractive = func() bool { return false }
	t.Cleanup(func() { isInteractive = original })
}

// wantUsageError asserts the error is a usage error (exit 2) mentioning want.
// Exit 2 is what separates "you asked for something contradictory" from "the
// database refused", and scripts branch on it.
func wantUsageError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a usage error mentioning %q, got nil", want)
	}
	if code := exitCodeFor(err); code != 2 {
		t.Errorf("exit code %d, want 2 (usage): %v", code, err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not mention %q", err.Error(), want)
	}
}

func TestResetRejectsApplicationWithDropDatabase(t *testing.T) {
	// Scoping to one application and dropping the database ask for opposite
	// blast radii; guessing which one was meant would destroy the difference.
	cmd, _ := newResetCmd(t, "--drop-database", "--application", "billing", "--db-url", unreachableURL)
	wantUsageError(t, cmd.Execute(), "--application cannot be combined with --drop-database")
}

func TestResetRejectsSchemaWithDropDatabase(t *testing.T) {
	cmd, _ := newResetCmd(t, "--drop-database", "--schema", "dbos_alt", "--db-url", unreachableURL)
	wantUsageError(t, cmd.Execute(), "--schema cannot be combined with --drop-database")
}

// The default schema is not "passed", so it must not trip the check above.
func TestResetAllowsDropDatabaseWithoutExplicitSchema(t *testing.T) {
	notATerminal(t)
	cmd, _ := newResetCmd(t, "--drop-database", "--db-url", unreachableURL)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected the missing-confirmation refusal")
	}
	if strings.Contains(err.Error(), "--schema cannot be combined") {
		t.Errorf("the schema default was treated as an explicit flag: %v", err)
	}
}

// An empty --application is a shell variable that did not expand. Reading it as
// "no --application" would turn a request to empty one application's rows into
// emptying every application's, and --force would skip the prompt that is the
// only other thing standing in the way.
func TestResetRejectsAnEmptyApplication(t *testing.T) {
	cmd, _ := newResetCmd(t, "--application", "", "--force", "--db-url", unreachableURL)
	wantUsageError(t, cmd.Execute(), "--application was given an empty name")
}

// The same mistake must not slip past the --drop-database guard either.
func TestResetRejectsEmptyApplicationWithDropDatabase(t *testing.T) {
	cmd, _ := newResetCmd(t, "--drop-database", "--application", "", "--force", "--db-url", unreachableURL)
	wantUsageError(t, cmd.Execute(), "--application cannot be combined with --drop-database")
}

func TestResetRefusesWithoutATerminal(t *testing.T) {
	notATerminal(t)
	cmd, _ := newResetCmd(t, "--db-url", unreachableURL)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("reset proceeded unattended with no terminal to confirm on")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal does not say how to proceed: %v", err)
	}
	// The refusal names the database, so it must not carry the password there
	// either: refusals get pasted into tickets like any other output.
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("refusal leaked the password: %v", err)
	}
}

func TestResetRejectsQuotedSchema(t *testing.T) {
	cmd, _ := newResetCmd(t, "--schema", `db"os`, "--db-url", unreachableURL)
	wantUsageError(t, cmd.Execute(), "quotes")
}

func TestRenameRequiresASource(t *testing.T) {
	// Without --from or --adopt-unclaimed-rows there is nothing to move, which
	// would otherwise be a rename that cheerfully reports moving no rows.
	cmd, _ := newRenameCmd(t, "--to", "new-app", "--db-url", unreachableURL)
	wantUsageError(t, cmd.Execute(), "nothing to re-own")
}

func TestRenameRejectsNonPositiveBatchSize(t *testing.T) {
	cmd, _ := newRenameCmd(t, "--from", "old", "--to", "new", "--batch-size", "0", "--db-url", unreachableURL)
	wantUsageError(t, cmd.Execute(), "--batch-size")
}

func TestRenameRejectsSameFromAndTo(t *testing.T) {
	// Not merely a no-op: the moved rows would go on matching the predicate, so
	// the batch loop would never advance its watermark.
	cmd, _ := newRenameCmd(t, "--from", "same", "--to", "same", "--db-url", unreachableURL)
	wantUsageError(t, cmd.Execute(), "--from and --to are both")
}

func TestRenameRequiresTo(t *testing.T) {
	// Exit 2 like every other usage mistake here, which is why the check is in
	// RunE rather than MarkFlagRequired: cobra's own refusal exits 1.
	cmd, _ := newRenameCmd(t, "--from", "old", "--db-url", unreachableURL)
	wantUsageError(t, cmd.Execute(), "--to")
}

func TestRenameRefusesWithoutATerminal(t *testing.T) {
	notATerminal(t)
	cmd, _ := newRenameCmd(t, "--from", "old", "--to", "new", "--db-url", unreachableURL)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("rename proceeded unattended with no terminal to confirm on")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal does not say how to proceed: %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("refusal leaked the password: %v", err)
	}
}

// The prompt has to name what is about to move, since that is the only thing
// distinguishing the two sources an operator can ask for.
func TestRenameSourcesDescribeWhatMoves(t *testing.T) {
	for _, tc := range []struct {
		name    string
		oldName string
		adopt   bool
		want    []string
	}{
		{name: "from only", oldName: "old", want: []string{`"old"'s rows`}},
		{name: "adopt only", adopt: true, want: []string{"rows no application owns"}},
		{name: "both", oldName: "old", adopt: true, want: []string{`"old"'s rows`, "rows no application owns"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(renameSources(tc.oldName, tc.adopt), " and ")
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("description %q is missing %q", got, want)
				}
			}
		})
	}
	if len(renameSources("", false)) != 0 {
		t.Error("naming no source described something anyway")
	}
}

// The standalone commands the tests above build cannot catch a regression in
// how the real tree is wired, so this checks the tree itself: the three
// database commands live under sysdb, and each one inherits the connection
// flags rather than redeclaring them.
func TestSysdbOwnsTheDatabaseCommands(t *testing.T) {
	var sysdb *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "sysdb" {
			sysdb = c
		}
		// A database command loose at the top level would work, and would be
		// the wrong shape; nothing else would fail if one were added there.
		switch c.Name() {
		case "migrate", "reset", "rename-application":
			t.Errorf("%q is registered at the top level, not under sysdb", c.Name())
		}
	}
	if sysdb == nil {
		t.Fatal("rootCmd has no sysdb command")
	}

	want := map[string]bool{"migrate": false, "reset": false, "rename-application": false}
	for _, c := range sysdb.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("sysdb has no %q subcommand", name)
		}
	}

	// rename-application is a mouthful, and the short form is what people will
	// actually type; the full name stays canonical because the SDKs use it.
	var rename *cobra.Command
	for _, c := range sysdb.Commands() {
		if c.Name() == "rename-application" {
			rename = c
		}
	}
	if rename == nil {
		t.Fatal("sysdb has no rename-application command")
	}
	if !rename.HasAlias("rename-app") {
		t.Errorf("rename-application does not accept the rename-app alias, aliases: %v", rename.Aliases)
	}

	for _, flag := range []string{"db-url", "schema"} {
		if sysdb.PersistentFlags().Lookup(flag) == nil {
			t.Errorf("sysdb does not define --%s persistently, so its subcommands cannot share one definition", flag)
		}
		for _, c := range sysdb.Commands() {
			if c.Flags().Lookup(flag) != nil {
				t.Errorf("%s redeclares --%s instead of inheriting it", c.Name(), flag)
			}
		}
	}
}
