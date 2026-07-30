// Package cli implements the dbos command tree. Each subcommand lives in its
// own file and registers itself on rootCmd from that file's init(); there is no
// central registration list. RunE functions return errors rather than printing
// and exiting — Execute owns error printing and the process exit.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "dbos",
	Short: "Command-line client for the DBOS Conductor API",
	Long: `dbos manages DBOS Conductor workflows, queues, schedules, applications,
and access tokens across self-hosted and DBOS Cloud deployments.`,
	// A runtime error should print the error, not a usage dump; and Execute prints
	// errors itself so it can stay quiet on an interrupt.
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command under a context cancelled by SIGINT/SIGTERM, so
// an interrupt aborts the in-flight request cleanly (closing the connection)
// rather than hard-killing mid-request. A second Ctrl-C hard-kills. Execute owns
// error printing and the exit code: 1 general, 2 usage, 3 auth-required, 4
// not-found (see the Exit codes section of AGENTS.md); an interrupt exits 130.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Once the first interrupt cancels ctx, restore default signal handling so a
	// second Ctrl-C terminates immediately instead of being swallowed.
	go func() {
		<-ctx.Done()
		stop()
	}()

	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return
	}
	// If the context was cancelled, an interrupt fired (nothing else cancels it
	// before Execute returns): exit quietly with the conventional 130 rather than
	// printing the request's cancellation error.
	if ctx.Err() != nil {
		os.Exit(130)
	}
	fmt.Fprintln(os.Stderr, "Error:", err)
	os.Exit(exitCodeFor(err))
}

func init() {
	rootCmd.Version = resolveBuildInfo().short()
	// A bad flag is a usage error (exit 2). Execute prints it.
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitError{code: 2, msg: err.Error()}
	})
	// Register --version ourselves, without a shorthand, so Cobra's default init
	// does not claim -v for it. -v is reserved for a future --verbose (see the
	// shorthand-namespace note in AGENTS.md); Cobra still handles this flag.
	rootCmd.Flags().Bool("version", false, "print version information")
}
