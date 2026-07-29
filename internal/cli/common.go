package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/client"
	"github.com/dbos-inc/dbos-cli/internal/config"
	"github.com/dbos-inc/dbos-cli/internal/output"
)

// settings loads the config and resolves effective settings from the global
// flags, the environment, and the active profile (flag > env > profile).
func settings(cmd *cobra.Command) (config.Settings, error) {
	f, err := config.Load()
	if err != nil {
		return config.Settings{}, err
	}
	profile, err := field(cmd, "profile", "DBOS_PROFILE")
	if err != nil {
		return config.Settings{}, err
	}
	url, err := field(cmd, "url", "DBOS_URL")
	if err != nil {
		return config.Settings{}, err
	}
	org, err := field(cmd, "org", "DBOS_ORG")
	if err != nil {
		return config.Settings{}, err
	}
	app, err := field(cmd, "app", "DBOS_APP")
	if err != nil {
		return config.Settings{}, err
	}
	return f.Resolve(config.Inputs{Profile: profile, URL: url, Org: org, App: app})
}

// field reads one setting's flag value (with whether the flag was set) and its
// environment value. GetString only errors if the flag is undefined or
// non-string — a bug, not user input — but we surface it rather than silently
// resolving to "".
func field(cmd *cobra.Command, flag, env string) (config.FieldSource, error) {
	v, err := cmd.Flags().GetString(flag)
	if err != nil {
		return config.FieldSource{}, fmt.Errorf("reading --%s: %w", flag, err)
	}
	return config.FieldSource{
		Flag:    v,
		FlagSet: cmd.Flags().Changed(flag),
		Env:     os.Getenv(env),
	}, nil
}

// resolvedFormat returns the validated -o/--output format. Output is not a
// profile field (there is no DBOS_OUTPUT), so it comes straight from the flag.
func resolvedFormat(cmd *cobra.Command) (output.Format, error) {
	v, _ := cmd.Flags().GetString("output")
	return output.ParseFormat(v)
}

// newClient builds a no-auth Conductor client for the resolved base URL. Bearer
// injection is added by the auth milestone.
func newClient(s config.Settings) (*api.ClientWithResponses, error) {
	return client.New(client.Config{BaseURL: s.URL})
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
