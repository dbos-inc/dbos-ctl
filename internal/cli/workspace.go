package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/api"
	"github.com/dbos-inc/dbos-ctl/internal/output"
)

// A Workspace is a named grouping of applications inside an org, and the unit
// permissions and API keys can be scoped to. Every org keeps at least one.
var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage workspaces",
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspaces",
	Args:  cobra.NoArgs,
	RunE:  runWorkspaceList,
}

var workspaceCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an empty workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkspaceCreate,
}

var workspaceRenameCmd = &cobra.Command{
	Use:   "rename <name> <new-name>",
	Short: "Rename a workspace (its applications stay in it)",
	Args:  cobra.ExactArgs(2),
	RunE:  runWorkspaceRename,
}

// Deletion is empty-only: Conductor refuses a workspace that still holds
// applications (or is the org's last), and its refusal says what to do, so
// there is no option to move applications along with it.
// The not-empty refusal also names the command that moves them.
var workspaceDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an empty workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkspaceDelete,
}

func init() {
	addRequestFlags(workspaceListCmd, "profile", "url", "org", "output")
	addRequestFlags(workspaceCreateCmd, "profile", "url", "org")
	addRequestFlags(workspaceRenameCmd, "profile", "url", "org")
	addRequestFlags(workspaceDeleteCmd, "profile", "url", "org")

	workspaceCmd.AddCommand(workspaceListCmd, workspaceCreateCmd, workspaceRenameCmd, workspaceDeleteCmd)
	rootCmd.AddCommand(workspaceCmd)
}

func runWorkspaceList(cmd *cobra.Command, _ []string) error {
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
	resp, err := c.ListWorkspacesWithResponse(cmd.Context(), org)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, workspaceColumns())
}

func runWorkspaceCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.CreateWorkspaceWithResponse(cmd.Context(), org, api.CreateWorkspaceJSONRequestBody{Name: name})
	if err != nil {
		return err
	}
	if resp.JSON201 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "created workspace %q\n", name)
	return nil
}

func runWorkspaceRename(cmd *cobra.Command, args []string) error {
	name, newName := args[0], args[1]
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.RenameWorkspaceWithResponse(cmd.Context(), org, name, api.RenameWorkspaceJSONRequestBody{Name: newName})
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "renamed workspace %q to %q\n", name, newName)
	return nil
}

func runWorkspaceDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.DeleteWorkspaceWithResponse(cmd.Context(), org, name)
	if err != nil {
		return err
	}
	if err := checkStatus(resp.StatusCode(), resp.HTTPResponse, resp.ApplicationproblemJSONDefault, resp.Body); err != nil {
		if conductorCode(resp.ApplicationproblemJSONDefault) == codeWorkspaceNotEmpty {
			err = withHint(err, "move them with `dbosctl app move <app> <workspace>`")
		}
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deleted workspace %q\n", name)
	return nil
}

func workspaceColumns() []output.Column[api.Workspace] {
	return []output.Column[api.Workspace]{
		{Header: "NAME", Value: func(w api.Workspace) string { return w.Name }},
		{Header: "CREATED", Value: func(w api.Workspace) string { return fmtTime(w.CreatedAt) }},
	}
}
