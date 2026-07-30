package cli

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/auth"
	"github.com/dbos-inc/dbos-cli/internal/client"
	"github.com/dbos-inc/dbos-cli/internal/creds"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in with the OIDC device flow",
	Args:  cobra.NoArgs,
	RunE:  runLogin,
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored login for the current profile",
	Args:  cobra.NoArgs,
	RunE:  runLogout,
}

func init() {
	// login authenticates a profile (and may target an ad-hoc --url); logout
	// only needs to know which profile's credentials to drop.
	addRequestFlags(loginCmd, "profile", "url")
	addRequestFlags(logoutCmd, "profile")
	rootCmd.AddCommand(loginCmd, logoutCmd)
}

func runLogin(cmd *cobra.Command, _ []string) error {
	s, err := settings(cmd)
	if err != nil {
		return err
	}
	if s.Profile == "" {
		return fmt.Errorf("login needs an active profile — create one with `dbos config set` or pass --profile")
	}
	oidc, err := effectiveOIDC(s)
	if err != nil {
		return err
	}

	tok, err := auth.Login(cmd.Context(), auth.Config{
		Issuer:   oidc.Issuer,
		ClientID: oidc.ClientID,
		Audience: oidc.Audience,
		// offline_access asks for a refresh token; the provider may still decline.
		Scopes: []string{"openid", "offline_access"},
	}, func(da auth.DeviceAuth) {
		w := cmd.ErrOrStderr()
		if da.VerificationURIComplete != "" {
			fmt.Fprintf(w, "To sign in, open:\n\n    %s\n\n", da.VerificationURIComplete)
		} else {
			fmt.Fprintf(w, "To sign in, open %s\n", da.VerificationURI)
		}
		fmt.Fprintf(w, "and confirm the code: %s\n", da.UserCode)
	})
	if err != nil {
		return err
	}

	store, err := creds.NewFileStore()
	if err != nil {
		return err
	}
	c := &creds.Creds{Token: tok.AccessToken, RefreshToken: tok.RefreshToken}
	if tok.ExpiresIn > 0 {
		c.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix()
	}
	// Capture the identity now so org-scoped commands need no extra lookup later.
	// Best-effort: a brand-new user may not be registered yet, so a failure here
	// must not fail the login.
	if cl, err := client.New(client.Config{
		BaseURL:    s.URL,
		Token:      tok.AccessToken,
		HTTPClient: &http.Client{Timeout: 10 * time.Second}, // best-effort; never block login
	}); err == nil {
		if resp, err := cl.GetCurrentUserWithResponse(cmd.Context()); err == nil && resp.JSON200 != nil {
			c.Organization = resp.JSON200.OrgName
			c.UserName = resp.JSON200.Name
		}
	}
	if err := store.Save(s.Profile, c); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Logged in (profile %q).\n", s.Profile)
	return nil
}

func runLogout(cmd *cobra.Command, _ []string) error {
	s, err := settings(cmd)
	if err != nil {
		return err
	}
	if s.Profile == "" {
		return fmt.Errorf("no active profile to log out of")
	}
	store, err := creds.NewFileStore()
	if err != nil {
		return err
	}
	if err := store.Delete(s.Profile); err != nil {
		if errors.Is(err, creds.ErrNotFound) {
			fmt.Fprintf(cmd.OutOrStdout(), "Not logged in (profile %q).\n", s.Profile)
			return nil
		}
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Logged out (profile %q).\n", s.Profile)
	return nil
}
