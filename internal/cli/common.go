package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/client"
	"github.com/dbos-inc/dbos-cli/internal/output"
)

// flagOrEnv returns the flag value if the user set it, else $env when non-empty,
// else the flag's default. This is the flag > env half of the precedence chain;
// the profile layer (config milestone) slots in below env.
func flagOrEnv(cmd *cobra.Command, flag, env string) string {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetString(flag)
		return v
	}
	if env != "" {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	v, _ := cmd.Flags().GetString(flag)
	return v
}

// resolvedOrg returns the organization: --org > $DBOS_ORG > "local". A no-auth
// deployment is always org "local".
func resolvedOrg(cmd *cobra.Command) string {
	if v := flagOrEnv(cmd, "org", "DBOS_ORG"); v != "" {
		return v
	}
	return "local"
}

// resolvedFormat returns the validated -o/--output format.
func resolvedFormat(cmd *cobra.Command) (output.Format, error) {
	v, _ := cmd.Flags().GetString("output")
	return output.ParseFormat(v)
}

// newClient builds a no-auth Conductor client from --url > $DBOS_URL.
func newClient(cmd *cobra.Command) (*api.ClientWithResponses, error) {
	return client.New(client.Config{BaseURL: flagOrEnv(cmd, "url", "DBOS_URL")})
}

// apiError formats a non-2xx Conductor response. This is the minimal surface;
// the error-mapping milestone extends it (401 -> "run dbos login", past-limit,
// mode gating).
func apiError(status int, problem *api.ErrorModel, body []byte) error {
	if problem != nil {
		var msg string
		if problem.Title != nil {
			msg = *problem.Title
		}
		if problem.Detail != nil && *problem.Detail != "" {
			if msg != "" {
				msg += ": "
			}
			msg += *problem.Detail
		}
		if msg != "" {
			return fmt.Errorf("%s (HTTP %d)", msg, status)
		}
	}
	if b := strings.TrimSpace(string(body)); b != "" {
		return fmt.Errorf("HTTP %d: %s", status, b)
	}
	return fmt.Errorf("HTTP %d", status)
}

// deref returns the pointed-to string, or "" for a nil pointer.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
