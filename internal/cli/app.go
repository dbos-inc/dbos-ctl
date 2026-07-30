package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/output"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Inspect and manage applications",
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List applications",
	Args:  cobra.NoArgs,
	RunE:  runAppList,
}

var appRegisterCmd = &cobra.Command{
	Use:   "register <name>",
	Short: "Register an application",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppRegister,
}

var appDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an application",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppDelete,
}

func init() {
	// app list is org-scoped (it lists every app in the org), so it honors
	// --org but not --app. register/delete name the app positionally.
	addRequestFlags(appListCmd, "profile", "url", "org", "output")
	addRequestFlags(appRegisterCmd, "profile", "url", "org")
	addRequestFlags(appDeleteCmd, "profile", "url", "org")
	appDeleteCmd.Flags().Bool("force", false, "skip the confirmation prompt (required when non-interactive)")
	appCmd.AddCommand(appListCmd, appRegisterCmd, appDeleteCmd)
	rootCmd.AddCommand(appCmd)
}

func runAppList(cmd *cobra.Command, _ []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}

	resp, err := c.ListAppsWithResponse(cmd.Context(), org)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}

	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, appColumns())
}

func runAppRegister(cmd *cobra.Command, args []string) error {
	name := args[0]
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	// An empty body is enough to stand up a bare app record (no executor, no
	// deploy); the tuning fields land with D3 `app update`.
	resp, err := c.RegisterAppWithResponse(cmd.Context(), org, name, api.RegisterAppJSONRequestBody{})
	if err != nil {
		return err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "registered app %q\n", name)
	return nil
}

func runAppDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	force, _ := cmd.Flags().GetBool("force")
	// A destructive delete needs confirmation. Interactively that's the prompt;
	// without a terminal to answer it (a pipe, a file, CI) we refuse rather than
	// delete unattended — --force is required to proceed there.
	if !force {
		if !isInteractive() {
			return fmt.Errorf("refusing to delete app %q without confirmation: re-run with --force (stdin is not a terminal)", name)
		}
		ok, err := confirm(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Delete app %q? This cannot be undone.", name))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(cmd.ErrOrStderr(), "aborted")
			return nil
		}
	}

	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.DeleteAppWithResponse(cmd.Context(), org, name)
	if err != nil {
		return err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deleted app %q\n", name)
	return nil
}

func appColumns() []output.Column[api.Application] {
	return []output.Column[api.Application]{
		{Header: "NAME", Value: func(a api.Application) string { return a.Name }},
		{Header: "STATUS", Value: func(a api.Application) string { return string(a.Status) }},
		{Header: "LANGUAGE", Value: func(a api.Application) string { return deref(a.Language) }},
	}
}
