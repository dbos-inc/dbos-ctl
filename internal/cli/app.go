package cli

import (
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

func init() {
	// app list is org-scoped (it lists every app in the org), so it honors
	// --org but not --app.
	addRequestFlags(appListCmd, "profile", "url", "org", "output")
	appCmd.AddCommand(appListCmd)
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

func appColumns() []output.Column[api.Application] {
	return []output.Column[api.Application]{
		{Header: "NAME", Value: func(a api.Application) string { return a.Name }},
		{Header: "STATUS", Value: func(a api.Application) string { return string(a.Status) }},
		{Header: "LANGUAGE", Value: func(a api.Application) string { return deref(a.Language) }},
	}
}
