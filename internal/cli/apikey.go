package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dbos-inc/dbos-ctl/internal/api"
	"github.com/dbos-inc/dbos-ctl/internal/output"
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

var apiKeyRenameCmd = &cobra.Command{
	Use:   "rename <name> <new-name>",
	Short: "Rename an API key (its secret is unchanged)",
	Args:  cobra.ExactArgs(2),
	RunE:  runAPIKeyRename,
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
	addRequestFlags(apiKeyRenameCmd, "profile", "url", "org")
	addRequestFlags(apiKeyDeleteCmd, "profile", "url", "org")
	// create's --app scopes the key to app names (repeatable), distinct from the
	// operational -a/--app, so it's declared here rather than via addRequestFlags.
	// The scope flags are string arrays split by scopeNames rather than string
	// slices: a slice parses an empty value to nothing, so a mistyped
	// `--workspace ""` could not be told apart from leaving the flag out.
	apiKeyCreateCmd.Flags().StringArray("app", nil, "scope the key to these `apps` (repeatable or comma-separated; not with --workspace; default: all apps)")
	apiKeyCreateCmd.Flags().StringArray("workspace", nil, "scope the key to the apps in these `workspaces`, including ones moved in later (repeatable or comma-separated; not with --app)")
	apiKeyCreateCmd.Flags().StringSlice("permission", nil, "grant these permissions, e.g. application.read (repeatable)")

	apiKeyCmd.AddCommand(apiKeyListCmd, apiKeyCreateCmd, apiKeyRenameCmd, apiKeyDeleteCmd)
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
	perms, _ := cmd.Flags().GetStringSlice("permission")
	if cmd.Flags().Changed("workspace") && cmd.Flags().Changed("app") {
		return &exitError{code: 2, msg: "--workspace cannot be combined with --app: a key is scoped to either workspaces or apps"}
	}
	apps, err := scopeNames(cmd, "app")
	if err != nil {
		return err
	}
	workspaces, err := scopeNames(cmd, "workspace")
	if err != nil {
		return err
	}
	body := api.CreateTokenJSONRequestBody{}
	if len(apps) > 0 {
		body.AppNames = &apps
	}
	if len(workspaces) > 0 {
		body.Workspaces = &workspaces
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

// scopeNames reads a scope flag's names, splitting comma-separated values, and
// refuses an empty one. Sending it would fail at best; dropping it could leave
// no scope at all, which Conductor reads as every app.
func scopeNames(cmd *cobra.Command, flag string) ([]string, error) {
	// GetSlice, not GetStringArray: the getter round-trips through String(),
	// which reads a lone empty value back as no value.
	f := cmd.Flags().Lookup(flag)
	if f == nil {
		return nil, nil
	}
	values, _ := f.Value.(pflag.SliceValue)
	if values == nil {
		return nil, nil
	}
	var names []string
	for _, v := range values.GetSlice() {
		for _, n := range strings.Split(v, ",") {
			if strings.TrimSpace(n) == "" {
				return nil, &exitError{code: 2, msg: fmt.Sprintf("--%s was given an empty name: name each one, or omit --%s to scope the key to all apps", flag, flag)}
			}
			names = append(names, n)
		}
	}
	return names, nil
}

func runAPIKeyRename(cmd *cobra.Command, args []string) error {
	name, newName := args[0], args[1]
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.UpdateTokenWithResponse(cmd.Context(), org, name, api.UpdateTokenJSONRequestBody{NewName: &newName})
	if err != nil {
		return err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "renamed API key %q to %q\n", name, newName)
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
		{Header: "SCOPE", Value: apiKeyScope},
		{Header: "PERMISSIONS", Value: func(t api.Token) string { return strings.Join(t.Permissions, ", ") }},
		{Header: "CREATED", Value: func(t api.Token) string { return fmtTime(t.CreatedAt) }},
	}
}

// apiKeyScope renders what a key reaches: its workspaces, its apps (Conductor
// lists them by id), or all apps when it names neither.
func apiKeyScope(t api.Token) string {
	switch {
	case len(t.Workspaces) > 0:
		return "workspaces: " + strings.Join(t.Workspaces, ", ")
	case len(t.AppIds) > 0:
		return "app ids: " + strings.Join(t.AppIds, ", ")
	default:
		return "all apps"
	}
}
