package cli

import (
	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/config"
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
	// listPermissions is OAuth-gated: the route only exists on an OAuth-enabled
	// or DBOS-managed Conductor, so a no-auth profile can't reach it. Fail with a
	// mode-aware message rather than a raw 404. (The general spec-derived
	// pre-request gate lands in F4; this is the first gated command.)
	if s.Auth != config.AuthBearer {
		return &exitError{code: 1, msg: "listing permissions requires an OAuth-enabled or DBOS-managed Conductor; the current profile uses no auth"}
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
