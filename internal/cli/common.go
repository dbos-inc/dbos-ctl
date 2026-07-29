package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/auth"
	"github.com/dbos-inc/dbos-cli/internal/client"
	"github.com/dbos-inc/dbos-cli/internal/config"
	"github.com/dbos-inc/dbos-cli/internal/creds"
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
	urlSrc, err := field(cmd, "url", "DBOS_URL")
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
	return f.Resolve(config.Inputs{Profile: profile, URL: urlSrc, Org: org, App: app})
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

// clientFor resolves settings and builds a Conductor client with the bearer
// token attached (empty for a no-auth request). It returns the settings too, so
// callers get the resolved org/app.
func clientFor(cmd *cobra.Command) (*api.ClientWithResponses, config.Settings, error) {
	s, err := settings(cmd)
	if err != nil {
		return nil, s, err
	}
	token, err := bearerToken(cmd, s)
	if err != nil {
		return nil, s, err
	}
	c, err := client.New(client.Config{BaseURL: s.URL, Token: token})
	return c, s, err
}

// bearerToken resolves the bearer token for a request, or "" for a no-auth
// request. Precedence: $DBOS_TOKEN (which implies bearer) > the profile's stored
// login. A dbos_ API key is sent as-is; an OIDC access token is refreshed when
// it has expired.
func bearerToken(cmd *cobra.Command, s config.Settings) (string, error) {
	if t := os.Getenv("DBOS_TOKEN"); t != "" {
		return t, nil
	}
	if s.Auth != config.AuthBearer {
		return "", nil
	}
	store, err := creds.NewFileStore()
	if err != nil {
		return "", err
	}
	c, err := store.Load(s.Profile)
	if errors.Is(err, creds.ErrNotFound) {
		return "", notLoggedIn(s.Profile)
	}
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(c.Token, "dbos_") {
		return c.Token, nil // static API key: never refreshed
	}
	if c.ExpiresAt > 0 && time.Now().Unix() >= c.ExpiresAt {
		return refreshStored(cmd.Context(), s, store, c)
	}
	return c.Token, nil
}

// refreshStored exchanges the stored refresh token for a fresh access token and
// persists it, returning the new access token.
func refreshStored(ctx context.Context, s config.Settings, store creds.Store, c *creds.Creds) (string, error) {
	if c.RefreshToken == "" {
		return "", fmt.Errorf("session for profile %q has expired; run `dbos login`", s.Profile)
	}
	oidc, err := effectiveOIDC(s)
	if err != nil {
		return "", err
	}
	tok, err := auth.Refresh(ctx, auth.Config{Issuer: oidc.Issuer, ClientID: oidc.ClientID, Audience: oidc.Audience}, c.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refreshing session for profile %q (try `dbos login`): %w", s.Profile, err)
	}
	updated := *c
	updated.Token = tok.AccessToken
	if tok.RefreshToken != "" {
		updated.RefreshToken = tok.RefreshToken
	}
	if tok.ExpiresIn > 0 {
		updated.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix()
	}
	if err := store.Save(s.Profile, &updated); err != nil {
		return "", err
	}
	return updated.Token, nil
}

// effectiveOIDC returns the OIDC config for login/refresh. Resolve populates it
// for cloud profiles (from the domain) and self-hosted OIDC profiles (from the
// oidc block); a target with neither can't run the device flow.
func effectiveOIDC(s config.Settings) (config.OIDC, error) {
	if s.OIDC == nil || s.OIDC.Issuer == "" {
		return config.OIDC{}, fmt.Errorf("profile %q has no login config; set --issuer and --client-id with `dbos config set`", s.Profile)
	}
	return *s.OIDC, nil
}

func notLoggedIn(profile string) error {
	if profile == "" {
		return fmt.Errorf("not logged in (run `dbos login`)")
	}
	return fmt.Errorf("not logged in for profile %q (run `dbos login`)", profile)
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
