package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/client"
	"github.com/dbos-inc/dbos-cli/internal/config"
	"github.com/dbos-inc/dbos-cli/internal/output"
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Show the logged-in identity",
	Args:  cobra.NoArgs,
	RunE:  runProfile,
}

func init() {
	rootCmd.AddCommand(profileCmd)
}

func runProfile(cmd *cobra.Command, _ []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	s, err := settings(cmd)
	if err != nil {
		return err
	}

	// Selfhosted (no auth): /v2/users/me does not exist, so report the static
	// local identity rather than calling the API.
	if s.Auth != config.AuthBearer {
		return renderProfile(cmd, format, nil)
	}

	token, err := bearerToken(cmd, s)
	if err != nil {
		return err
	}
	c, err := client.New(client.Config{BaseURL: s.URL, Token: token})
	if err != nil {
		return err
	}
	resp, err := c.GetCurrentUserWithResponse(cmd.Context())
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return renderProfile(cmd, format, resp.JSON200)
}

// renderProfile prints a user profile; u is nil for the selfhosted local
// identity. json emits the raw API shape (List's single-object counterpart).
func renderProfile(cmd *cobra.Command, format output.Format, u *api.UserProfile) error {
	w := cmd.OutOrStdout()

	if format == output.FormatJSON {
		if u != nil {
			return output.JSON(w, u)
		}
		return output.JSON(w, map[string]string{"name": "local", "orgName": "local"})
	}

	name, email, org, plan := "local", "", "local", ""
	admin := false
	if u != nil {
		name, email, org = u.Name, u.Email, u.OrgName
		plan = string(u.SubscriptionPlan)
		if u.IsDbosAdmin != nil {
			admin = *u.IsDbosAdmin
		}
	}
	fmt.Fprintf(w, "name   %s\n", name)
	if email != "" {
		fmt.Fprintf(w, "email  %s\n", email)
	}
	fmt.Fprintf(w, "org    %s\n", org)
	if plan != "" {
		fmt.Fprintf(w, "plan   %s\n", plan)
	}
	if admin {
		fmt.Fprintln(w, "admin  true")
	}
	return nil
}
