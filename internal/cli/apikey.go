package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/output"
)

// The CLI noun is api-key (the console/SDK term); the conductor v2 API calls it
// a token. `token`/`apikey` are aliases. See AGENTS.md "API keys, org, roles".
var apiKeyCmd = &cobra.Command{
	Use:     "api-key",
	Aliases: []string{"token", "apikey"},
	Short:   "Manage API keys",
}

var apiKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List API keys",
	Args:  cobra.NoArgs,
	RunE:  runAPIKeyList,
}

var apiKeyCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an API key (its secret is shown once)",
	Args:  cobra.ExactArgs(1),
	RunE:  runAPIKeyCreate,
}

var apiKeyDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an API key",
	Args:  cobra.ExactArgs(1),
	RunE:  runAPIKeyDelete,
}

func init() {
	addRequestFlags(apiKeyListCmd, "profile", "url", "org", "output")
	addRequestFlags(apiKeyCreateCmd, "profile", "url", "org", "output")
	addRequestFlags(apiKeyDeleteCmd, "profile", "url", "org")
	// create's --app scopes the key to app names (repeatable), distinct from the
	// operational -a/--app, so it's declared here rather than via addRequestFlags.
	apiKeyCreateCmd.Flags().StringSlice("app", nil, "scope the key to these apps (repeatable; default: all apps)")
	apiKeyCreateCmd.Flags().StringSlice("permission", nil, "grant these permissions, e.g. application.read (repeatable)")

	apiKeyCmd.AddCommand(apiKeyListCmd, apiKeyCreateCmd, apiKeyDeleteCmd)
	rootCmd.AddCommand(apiKeyCmd)
}

func runAPIKeyList(cmd *cobra.Command, _ []string) error {
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
	resp, err := c.ListTokensWithResponse(cmd.Context(), org)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, apiKeyColumns())
}

func runAPIKeyCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	apps, _ := cmd.Flags().GetStringSlice("app")
	perms, _ := cmd.Flags().GetStringSlice("permission")
	body := api.CreateTokenJSONRequestBody{}
	if len(apps) > 0 {
		body.AppNames = &apps
	}
	if len(perms) > 0 {
		body.Permissions = &perms
	}

	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.CreateTokenWithResponse(cmd.Context(), org, name, body)
	if err != nil {
		return err
	}
	if resp.JSON201 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}

	if format == output.FormatJSON {
		return output.JSON(cmd.OutOrStdout(), resp.JSON201)
	}
	// Scalar convention: the bare secret to stdout so `$(dbosctl api-key create ci)`
	// captures it; the shown-once warning goes to stderr to keep stdout clean.
	fmt.Fprintf(cmd.ErrOrStderr(), "API key %q created — store this secret now, it is not shown again:\n", resp.JSON201.TokenName)
	fmt.Fprintln(cmd.OutOrStdout(), resp.JSON201.Token)
	return nil
}

func runAPIKeyDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.DeleteTokenWithResponse(cmd.Context(), org, name)
	if err != nil {
		return err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deleted API key %q\n", name)
	return nil
}

func apiKeyColumns() []output.Column[api.Token] {
	return []output.Column[api.Token]{
		{Header: "NAME", Value: func(t api.Token) string { return t.TokenName }},
		{Header: "APPS", Value: func(t api.Token) string { return joinOrAll(t.AppIds) }},
		{Header: "PERMISSIONS", Value: func(t api.Token) string { return strings.Join(t.Permissions, ", ") }},
		{Header: "CREATED", Value: func(t api.Token) string { return fmtTime(t.CreatedAt) }},
	}
}

// joinOrAll renders a key's app scope: the app list, or "(all)" when unscoped.
func joinOrAll(apps []string) string {
	if len(apps) == 0 {
		return "(all)"
	}
	return strings.Join(apps, ", ")
}
