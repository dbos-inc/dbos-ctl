package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/migrations"
	"github.com/dbos-inc/dbos-ctl/internal/output"
)

// rename-application re-owns rows after an application is renamed. The name and
// every flag match the Python, TypeScript, and Go CLIs, which all three agree
// on: a runbook that names this command should not have to change because the
// binary running it did.
var renameApplicationCmd = &cobra.Command{
	Use: "rename-application",
	// The full name is the one the SDK CLIs use, so it stays the name; typing
	// it is another matter.
	Aliases: []string{"rename-app"},
	Short:   "Re-own a system database's rows after an application is renamed",
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
	// Counts render as a table or as JSON, the same -o every other command that
	// prints data honors.
	addRequestFlags(cmd, "output")
	// --to is required, but not via MarkFlagRequired: cobra rejects that before
	// RunE and returns a plain error, which exits 1. Every other usage mistake
	// here exits 2, and scripts branch on that, so the check lives in RunE.
}

func runRenameApplication(cmd *cobra.Command, _ []string) error {
	// Resolved first: an unusable -o should fail before the prompt, not after
	// the rows have moved and there is nothing left to render.
	format, err := resolvedFormat(cmd)
	if err != nil {
		return &exitError{code: 2, msg: err.Error()}
	}
	// -o ids names nothing here: these print counts, not identifiers.
	// output.List and output.Detail refuse it too, but only when they come to
	// render -- which is after the rows are gone. For a destructive command the
	// refusal has to come first, so it is spelled out here.
	if format == output.FormatIDs {
		return &exitError{code: 2, msg: `output format "ids" is not supported by this command (try table or json)`}
	}
	schema, _ := cmd.Flags().GetString("schema")
	oldName, _ := cmd.Flags().GetString("from")
	newName, _ := cmd.Flags().GetString("to")
	adopt, _ := cmd.Flags().GetBool("adopt-unclaimed-rows")
	batchSize, _ := cmd.Flags().GetInt("batch-size")
	force, _ := cmd.Flags().GetBool("force")

	if newName == "" {
		return &exitError{code: 2, msg: "no application to re-own the rows to: pass --to"}
	}
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

	counts, renameErr := migrations.RenameApplication(cmd.Context(), dbURL, migrations.RenameInput{
		OldName:            oldName,
		NewName:            newName,
		Schema:             schema,
		BatchSize:          batchSize,
		AdoptUnclaimedRows: adopt,
	}, cmd.ErrOrStderr())

	// Counts go to stdout, with the progress on stderr, so a scripted rename can
	// read what moved without parsing log lines, and through the same -o as
	// every other command that prints data: a table by default, JSON on request.
	//
	// Printed on failure too, and that is the case it exists for: the batched
	// tail commits as it goes, so a rename that dies partway has durably moved
	// what these report, and that is where a re-run picks up. Dropping them
	// would leave the operator to guess. RenameApplication zeroes them when the
	// opening transaction rolls back, so a failure there still reports nothing.
	if err := output.Detail(cmd.OutOrStdout(), format, counts, renameCountFields()); err != nil && renameErr == nil {
		return err
	}
	return renameErr
}

// renameCountFields renders what a rename moved, by kind. Labelled with the
// json names, as the other detail views are, so the table and the JSON name the
// same things.
func renameCountFields() []output.Field[migrations.ApplicationRowCounts] {
	return []output.Field[migrations.ApplicationRowCounts]{
		{Label: "workflows", Value: func(c migrations.ApplicationRowCounts) string { return strconv.FormatInt(c.Workflows, 10) }},
		{Label: "steps", Value: func(c migrations.ApplicationRowCounts) string { return strconv.FormatInt(c.Steps, 10) }},
		{Label: "queues", Value: func(c migrations.ApplicationRowCounts) string { return strconv.FormatInt(c.Queues, 10) }},
		{Label: "schedules", Value: func(c migrations.ApplicationRowCounts) string { return strconv.FormatInt(c.Schedules, 10) }},
		{Label: "versions", Value: func(c migrations.ApplicationRowCounts) string { return strconv.FormatInt(c.Versions, 10) }},
	}
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
