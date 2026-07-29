// Package cli implements the dbos command tree. Each subcommand lives in its
// own file and registers itself on rootCmd from that file's init(); there is no
// central registration list. RunE functions return errors rather than printing
// and exiting — Execute owns the process exit.
package cli

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "dbos",
	Short: "Command-line client for the DBOS Conductor API",
	Long: `dbos manages DBOS Conductor workflows, queues, schedules, applications,
and access tokens across self-hosted and DBOS Cloud deployments.`,
	// A runtime error should print the error, not a usage dump; Cobra still
	// prints usage for genuine flag/arg (usage) errors.
	SilenceUsage: true,
}

// Execute runs the root command, exiting on error (Cobra having already printed
// it) with the code carried by the error: 1 general, 2 usage, 3 auth-required,
// 4 not-found (see the Exit codes section of AGENTS.md).
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(exitCodeFor(err))
	}
}

func init() {
	rootCmd.Version = resolveBuildInfo().short()
	// A bad flag is a usage error (exit 2). Cobra still prints it.
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitError{code: 2, msg: err.Error()}
	})
	// Register --version ourselves, without a shorthand, so Cobra's default init
	// does not claim -v for it. -v is reserved for a future --verbose (see the
	// shorthand-namespace note in AGENTS.md); Cobra still handles this flag.
	rootCmd.Flags().Bool("version", false, "print version information")

	// Global (persistent) flags. Declared here so they show in `dbos --help`;
	// they are consumed once config/profiles and the client land.
	pf := rootCmd.PersistentFlags()
	pf.StringP("output", "o", "table", "output format: table, json, ids")
	pf.StringP("app", "a", "", "application name (overrides $DBOS_APP and the profile)")
	pf.String("org", "", "organization (overrides $DBOS_ORG and the profile)")
	pf.String("url", "", "Conductor base URL (overrides $DBOS_URL and the profile)")
	pf.String("profile", "", "config profile to use (overrides $DBOS_PROFILE)")
}
