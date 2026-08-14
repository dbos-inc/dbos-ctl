package cli

import (
	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/output"
)

var permissionCmd = &cobra.Command{
	Use:   "permission",
	Short: "Inspect grantable permissions",
}

var permissionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the permissions that can be granted (to an API key or a role)",
	Args:  cobra.NoArgs,
	RunE:  runPermissionList,
}

func init() {
	addRequestFlags(permissionListCmd, "profile", "url", "org", "output")
	permissionCmd.AddCommand(permissionListCmd)
	rootCmd.AddCommand(permissionCmd)
}

func runPermissionList(cmd *cobra.Command, _ []string) error {
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
	resp, err := c.ListPermissionsWithResponse(cmd.Context(), org)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, permissionColumns())
}

func permissionColumns() []output.Column[string] {
	return []output.Column[string]{
		{Header: "PERMISSION", Value: func(p string) string { return p }},
	}
}
