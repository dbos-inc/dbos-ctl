package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/migrations"
)

// rename-application re-owns rows after an application is renamed. The name and
// every flag match the Python, TypeScript, and Go CLIs, which all three agree
// on: a runbook that names this command should not have to change because the
// binary running it did.
var renameApplicationCmd = &cobra.Command{
	Use:   "rename-application",
	Short: "Re-own a system database's rows after an application is renamed",
	Long: `Re-own a system database's rows after an application is renamed.

An application owns the workflows, steps, queues, schedules, and versions it
creates, keyed by its configured name. Renaming the application leaves those
rows behind under the old name, where it will not find them. This moves them.

Stop the application being renamed before running this. Nothing here locks it
out, and a running one keeps dequeuing under its old name.

Rows no application owns are not moved unless --adopt-unclaimed-rows is passed:
they predate system-database sharing, and claiming them is a decision rather
than a default.

This command reaches the database directly, so it takes a database URL rather
than a profile.`,
	Args: cobra.NoArgs,
	RunE: runRenameApplication,
}

func init() {
	addRenameApplicationFlags(renameApplicationCmd)
	sysdbCmd.AddCommand(renameApplicationCmd)
}

// addRenameApplicationFlags installs the command's flags. Separate from init so
// that tests can build a command carrying exactly these definitions.
func addRenameApplicationFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringP("from", "f", "", "the application's previous name; omit to only adopt unclaimed rows")
	f.StringP("to", "t", "", "the application that ends up owning the rows")
	f.Bool("adopt-unclaimed-rows", false, "also take rows no application owns (application_name is null)")
	f.Int("batch-size", migrations.DefaultRenameBatchSize, "workflows and steps re-owned per transaction")
	f.Bool("force", false, "skip the confirmation prompt (required when non-interactive)")
	_ = cmd.MarkFlagRequired("to")
}

func runRenameApplication(cmd *cobra.Command, _ []string) error {
	schema, _ := cmd.Flags().GetString("schema")
	oldName, _ := cmd.Flags().GetString("from")
	newName, _ := cmd.Flags().GetString("to")
	adopt, _ := cmd.Flags().GetBool("adopt-unclaimed-rows")
	batchSize, _ := cmd.Flags().GetInt("batch-size")
	force, _ := cmd.Flags().GetBool("force")

	// Naming no source is the easy mistake to make, and it would otherwise be a
	// rename that reports moving nothing rather than an error.
	sources := renameSources(oldName, adopt)
	if len(sources) == 0 {
		return &exitError{code: 2, msg: "nothing to re-own: pass --from, --adopt-unclaimed-rows, or both"}
	}
	// Rejected here rather than let it reach SQL, where a nonsensical batch
	// would only surface after the first transaction had already committed.
	if batchSize < 1 {
		return &exitError{code: 2, msg: fmt.Sprintf("invalid --batch-size %d: expected a positive integer", batchSize)}
	}
	if err := migrations.ValidateSchemaName(schema); err != nil {
		return &exitError{code: 2, msg: err.Error()}
	}
	if oldName != "" && oldName == newName {
		return &exitError{code: 2, msg: fmt.Sprintf("--from and --to are both %q: to adopt unclaimed rows into it, pass --to without --from", newName)}
	}

	dbURL, err := resolveDBURL(cmd)
	if err != nil {
		return err
	}

	if !force {
		if !isInteractive() {
			return fmt.Errorf("refusing to re-own rows in %s without confirmation: re-run with --force (stdin is not a terminal)", maskPassword(dbURL))
		}
		prompt := fmt.Sprintf(
			"Re-own %s in %s as %q? Stop the application being renamed before running this.",
			strings.Join(sources, " and "), maskPassword(dbURL), newName)
		ok, err := confirm(cmd.InOrStdin(), cmd.ErrOrStderr(), prompt)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(cmd.ErrOrStderr(), "aborted")
			return nil
		}
	}

	counts, err := migrations.RenameApplication(cmd.Context(), dbURL, migrations.RenameInput{
		OldName:            oldName,
		NewName:            newName,
		Schema:             schema,
		BatchSize:          batchSize,
		AdoptUnclaimedRows: adopt,
	}, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	// Counts go to stdout as JSON, with the progress on stderr, so a scripted
	// rename can read what moved without parsing log lines.
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(counts)
}

// renameSources describes the rows a rename will move, for the prompt. Empty
// means the operator named none, which is a usage error rather than a no-op.
func renameSources(oldName string, adopt bool) []string {
	var sources []string
	if oldName != "" {
		sources = append(sources, fmt.Sprintf("%q's rows", oldName))
	}
	if adopt {
		sources = append(sources, "rows no application owns")
	}
	return sources
}
