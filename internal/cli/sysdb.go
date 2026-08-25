package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dbos-inc/dbos-ctl/internal/migrations"
)

// dbURLEnv is the system database URL's environment variable, spelled the same
// as every DBOS SDK spells it, so a shell already set up to run an application
// can run these without repeating the URL.
const dbURLEnv = "DBOS_SYSTEM_DATABASE_URL"

// sysdb groups the commands that open a database instead of calling Conductor.
//
// Every other command in this CLI works through a profile: it names a Conductor
// and an application, and Conductor answers or forwards. These do not. They
// connect to PostgreSQL directly, take a database URL, and honour none of the
// common flags — so they are their own noun rather than three bare verbs mixed
// in among login and whoami.
//
// The grouping is dbosctl's grammar, not a renaming: the subcommand names are
// the ones Python, TypeScript, and Go use, so what a runbook calls this
// operation does not depend on which SDK wrote the runbook.
var sysdbCmd = &cobra.Command{
	Use:   "sysdb",
	Short: "Manage the DBOS system database",
	Long: `Manage the DBOS system database.

These commands connect to PostgreSQL (or CockroachDB) directly rather than
through Conductor, so they take a database URL rather than a profile, and none
of the common flags apply to them.

The system schema is shared by every DBOS SDK and the migrations are built into
this binary, so provisioning and maintaining a system database does not mean
picking an SDK and installing its toolchain.`,
}

func init() {
	addSysdbFlags(sysdbCmd.PersistentFlags())
	rootCmd.AddCommand(sysdbCmd)
}

// addSysdbFlags installs the flags every sysdb command shares. On the parent
// these are persistent flags, so each subcommand inherits one definition rather
// than repeating it and drifting; tests install them directly on a standalone
// command to exercise the same definitions.
func addSysdbFlags(f *pflag.FlagSet) {
	f.StringP("db-url", "D", "", "system database URL (overrides $"+dbURLEnv+")")
	f.String("schema", migrations.DefaultSchema, "schema holding the DBOS system tables")
}
