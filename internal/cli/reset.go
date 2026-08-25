package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/migrations"
)

// reset empties the DBOS system tables. Like migrate it opens a database
// rather than calling Conductor, so it takes a URL rather than a profile.
//
// The default is deliberately narrower than the SDK CLIs' `dbos reset`, which
// drop the database outright. dbosctl is schema-granular — --schema exists so
// the DBOS tables can share a database with application tables — and the
// system database is documented as shareable between applications. Dropping it
// therefore reaches past what the operator asked about. Emptying the tables
// leaves the schema migrated and immediately usable, and needs only the
// privileges --app-role grants. --drop-database is still there for the blunt
// version.
var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Empty the DBOS system database",
	Long: `Empty the DBOS system database.

Deletes the DBOS rows, leaving the schema itself migrated and ready to use, so
the database does not have to be provisioned again. The migration history is
kept: clearing it would re-run applied migrations over tables that exist.

Pass --application to empty one application's rows in a system database shared
by several. Everything else in the schema is left alone.

Pass --drop-database to drop the whole database instead. That reaches past
--schema: it takes any application tables sharing the database, and every
application sharing the system schema, and leaves nothing to migrate into.

This command reaches the database directly, so it takes a database URL rather
than a profile.`,
	Args: cobra.NoArgs,
	RunE: runReset,
}

func init() {
	addResetFlags(resetCmd)
	sysdbCmd.AddCommand(resetCmd)
}

// addResetFlags installs the command's flags. Separate from init so that tests
// can build a command carrying exactly these definitions.
func addResetFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("application", "", "empty only this application's rows, for a shared system database")
	f.Bool("drop-database", false, "drop the whole database instead of emptying the DBOS tables")
	f.Bool("force", false, "skip the confirmation prompt (required when non-interactive)")
}

func runReset(cmd *cobra.Command, _ []string) error {
	schema, _ := cmd.Flags().GetString("schema")
	application, _ := cmd.Flags().GetString("application")
	dropDatabase, _ := cmd.Flags().GetBool("drop-database")
	force, _ := cmd.Flags().GetBool("force")

	if dropDatabase {
		// Both of these describe work inside the database, so pairing them with
		// dropping it asks for two different things at once. Reporting that
		// beats picking one and destroying more than the operator described.
		//
		// Changed rather than a non-empty value, as for --schema: `--application
		// "$APP"` with APP unset is a mistake, and reading it as "no
		// --application" would wave it past this guard into a wider reset than
		// anything on the command line asked for.
		if cmd.Flags().Changed("application") {
			return &exitError{code: 2, msg: "--application cannot be combined with --drop-database: dropping the database takes every application's rows"}
		}
		if cmd.Flags().Changed("schema") {
			return &exitError{code: 2, msg: "--schema cannot be combined with --drop-database: dropping the database takes every schema in it"}
		}
	}
	// Same mistake, without --drop-database: the flag asks to scope the reset to
	// one application and names none. Widening that to every application's rows
	// — no prompt to catch it under --force — is the worst reading available.
	if cmd.Flags().Changed("application") && application == "" {
		return &exitError{code: 2, msg: "--application was given an empty name: omit it entirely to empty every application's rows"}
	}
	if err := migrations.ValidateSchemaName(schema); err != nil {
		return &exitError{code: 2, msg: err.Error()}
	}

	dbURL, err := resolveDBURL(cmd)
	if err != nil {
		return err
	}

	proceed, err := confirmReset(cmd, force, dbURL, schema, application, dropDatabase)
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	progress := cmd.ErrOrStderr()
	if dropDatabase {
		return migrations.DropDatabase(cmd.Context(), dbURL, progress)
	}
	fmt.Fprintf(progress, "Emptying %s (schema %s)\n", maskPassword(dbURL), schema)
	_, err = migrations.Empty(cmd.Context(), dbURL, schema, application, progress)
	return err
}

// confirmReset asks before destroying anything, unless --force was passed. It
// reports whether to proceed; declining is not an error.
func confirmReset(cmd *cobra.Command, force bool, dbURL, schema, application string, dropDatabase bool) (bool, error) {
	if force {
		return true, nil
	}
	// Without a terminal to answer the prompt (a pipe, a file, CI) we refuse
	// rather than destroy unattended: --force is how that is asked for.
	if !isInteractive() {
		return false, fmt.Errorf("refusing to reset %s without confirmation: re-run with --force (stdin is not a terminal)", maskPassword(dbURL))
	}

	// Each prompt names what is actually at stake, which is not the same in the
	// three modes: one application's rows, one schema's, or a whole database's.
	var prompt string
	switch {
	case dropDatabase:
		prompt = fmt.Sprintf("Drop the database at %s? This destroys every schema in it, including any application tables, and cannot be undone.", maskPassword(dbURL))
	case application != "":
		prompt = fmt.Sprintf("Empty %q's rows in schema %s of %s? Its workflow history is deleted; other applications sharing the schema are left alone.", application, schema, maskPassword(dbURL))
	default:
		prompt = fmt.Sprintf("Empty the DBOS tables in schema %s of %s? Every application sharing this schema loses its workflow history.", schema, maskPassword(dbURL))
	}

	ok, err := confirm(cmd.InOrStdin(), cmd.ErrOrStderr(), prompt)
	if err != nil {
		return false, err
	}
	if !ok {
		fmt.Fprintln(cmd.ErrOrStderr(), "aborted")
		return false, nil
	}
	return true, nil
}
