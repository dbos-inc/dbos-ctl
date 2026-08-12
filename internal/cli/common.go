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

// addRequestFlags installs the request-shaping flags a command honors, by name.
// The definitions live here so every command spells a flag identically, and so
// each command's --help lists only the subset it actually uses (whoami has no
// --org; config makes no request and declares none of these). An unknown name
// is a programmer error.
func addRequestFlags(cmd *cobra.Command, names ...string) {
	f := cmd.Flags()
	for _, n := range names {
		switch n {
		case "profile":
			f.String("profile", "", "config profile to use (overrides $DBOS_PROFILE)")
		case "url":
			f.String("url", "", "Conductor base URL (overrides $DBOS_URL and the profile)")
		case "org":
			f.String("org", "", "organization (overrides $DBOS_ORG and the profile)")
		case "app":
			f.StringP("app", "a", "", "application name (overrides $DBOS_APP and the profile)")
		case "output":
			f.StringP("output", "o", "table", "output format: table, json")
		default:
			panic("addRequestFlags: unknown flag " + n)
		}
	}
}

// field reads one setting's flag value (with whether the flag was set) and its
// environment value. A command only declares the request flags it honors, so a
// flag this command does not define contributes no flag value — env and profile
// still apply. Only a string flag participates: a command may legitimately give
// a same-named flag another type (e.g. `api-key create`'s repeatable `--app`
// scope), which contributes no string value here — env/profile still resolve it.
func field(cmd *cobra.Command, flag, env string) (config.FieldSource, error) {
	var src config.FieldSource
	if cmd.Flags().Lookup(flag) != nil {
		if v, err := cmd.Flags().GetString(flag); err == nil {
			src.Flag = v
			src.FlagSet = cmd.Flags().Changed(flag)
		}
	}
	src.Env = os.Getenv(env)
	return src, nil
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

// effectiveOrg resolves the organization for an org-scoped request. An
// explicitly configured org (flag > env > profile) always wins. Otherwise a
// no-auth self-hosted target is the hardcoded "local" org (Resolve fills this in
// too), while an authenticated target — managed or self-hosted OIDC — uses the org
// captured at login, so the user need not know their org name and no extra
// request is made. c is only used for the live-lookup fallback (an ad-hoc token
// with no stored login).
func effectiveOrg(ctx context.Context, c *api.ClientWithResponses, s config.Settings) (string, error) {
	if s.Org != "" {
		return s.Org, nil
	}
	if s.Auth != config.AuthBearer {
		return "local", nil
	}
	// `dbosctl login` captured the org, so prefer it — no extra request. An ad-hoc
	// $DBOS_TOKEN may be a different identity than any stored login, so for it we
	// derive live rather than trust the stored org.
	if os.Getenv("DBOS_TOKEN") == "" {
		if org := storedOrg(s.Profile); org != "" {
			return org, nil
		}
	}
	resp, err := c.GetCurrentUserWithResponse(ctx)
	if err != nil {
		return "", err
	}
	if resp.JSON200 == nil {
		return "", apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return resp.JSON200.OrgName, nil
}

// effectiveApp resolves the application name for an app-scoped request. Its
// value is already flag > env > profile (Resolve fills s.App); this only turns
// an empty result into a clear error naming how to set it.
func effectiveApp(s config.Settings) (string, error) {
	if s.App == "" {
		return "", fmt.Errorf("no application set; pass -a/--app, set $DBOS_APP, or add an app to the profile")
	}
	return s.App, nil
}

// storedOrg returns the organization captured at login for the profile, or ""
// if there is no stored login (or it predates org capture — e.g. a TS-CLI login
// without an organization).
func storedOrg(profile string) string {
	store, err := creds.NewFileStore()
	if err != nil {
		return ""
	}
	c, err := store.Load(profile)
	if err != nil {
		return ""
	}
	return c.Organization
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
		return "", fmt.Errorf("session for profile %q has expired; run `dbosctl login`", s.Profile)
	}
	oidc, err := effectiveOIDC(s)
	if err != nil {
		return "", err
	}
	tok, err := auth.Refresh(ctx, auth.Config{Issuer: oidc.Issuer, ClientID: oidc.ClientID, Audience: oidc.Audience}, c.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refreshing session for profile %q (try `dbosctl login`): %w", s.Profile, err)
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
// for managed profiles (from the domain) and self-hosted OIDC profiles (from the
// oidc block); a target with neither can't run the device flow.
func effectiveOIDC(s config.Settings) (config.OIDC, error) {
	if s.OIDC == nil || s.OIDC.Issuer == "" {
		return config.OIDC{}, fmt.Errorf("profile %q has no login config; set --issuer and --client-id with `dbosctl config set`", s.Profile)
	}
	return *s.OIDC, nil
}

func notLoggedIn(profile string) error {
	if profile == "" {
		return fmt.Errorf("not logged in (run `dbosctl login`)")
	}
	return fmt.Errorf("not logged in for profile %q (run `dbosctl login`)", profile)
}

// deref returns the pointed-to string, or "" for a nil pointer.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
