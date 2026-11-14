package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/client"
	"github.com/dbos-inc/dbos-ctl/internal/cloud"
	"github.com/dbos-inc/dbos-ctl/internal/config"
	"github.com/dbos-inc/dbos-ctl/internal/output"
)

// A license key is what a self-hosted Conductor needs to start: it reads one
// from DBOS_CONDUCTOR_LICENSE_KEY and checks it with DBOS Cloud. Cloud issues
// them, not Conductor, so this is the one command that calls cloud's own API
// rather than the Conductor API, which is why it needs a DBOS-managed profile
// even though the key is for a self-hosted deployment.
var licenseKeyCmd = &cobra.Command{
	Use:   "license-key",
	Short: "Manage self-hosted Conductor license keys",
}

var licenseKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a limited-time trial license key for self-hosting Conductor",
	Long: `Create a license key for self-hosted Conductor, issued by DBOS to
your organization. The key is printed once, along with when it expires. Pass it
to self-hosted Conductor as DBOS_CONDUCTOR_LICENSE_KEY.

This needs a DBOS-managed profile and a user login (` + "`dbosctl login`" + `), not an
API key. Your plan may limit how many keys your organization can create.`,
	Args: cobra.NoArgs,
	RunE: runLicenseKeyCreate,
}

func init() {
	// No --url: the request goes to DBOS Cloud, whose address comes from the
	// managed profile, not to the Conductor URL the flag would override.
	addRequestFlags(licenseKeyCreateCmd, "profile", "org", "output")
	licenseKeyCmd.AddCommand(licenseKeyCreateCmd)
	rootCmd.AddCommand(licenseKeyCmd)
}

// cloudBaseURL maps a managed domain to the cloud API origin. A variable so
// tests can point it at a plain-http server.
var cloudBaseURL = config.ManagedCloudURL

func runLicenseKeyCreate(cmd *cobra.Command, _ []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	s, err := settings(cmd)
	if err != nil {
		return err
	}
	if err := requireManaged(s); err != nil {
		return err
	}
	token, err := bearerToken(cmd, s)
	if err != nil {
		return err
	}
	// Cloud's own routes authenticate users only. Refuse here rather than send
	// the key and relay a 401 that reads as an expired login.
	if strings.HasPrefix(token, "dbos_") {
		return apiKeyNotAccepted(s.Profile)
	}
	// The Conductor client is only for effectiveOrg's live lookup, when the
	// login stored no org.
	c, err := client.New(client.Config{BaseURL: s.URL, Token: token})
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}

	cc := cloud.New(cloud.Config{BaseURL: cloudBaseURL(s.Domain), Token: token})
	key, raw, err := cc.CreateLicenseKey(cmd.Context(), org)
	if err != nil {
		return cloudError(err)
	}

	if format == output.FormatJSON {
		return output.JSON(cmd.OutOrStdout(), raw)
	}
	// Scalar convention, as in `api-key create`: the bare key to stdout so
	// `$(dbosctl license-key create)` captures it; the rest goes to stderr.
	expires := "It does not expire."
	if key.ExpirationDate != nil {
		expires = "It expires " + fmtTime(*key.ExpirationDate) + "."
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "License key created for organization %q. Store it now. It will not be shown again.\n", org)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s Pass it to self-hosted Conductor as DBOS_CONDUCTOR_LICENSE_KEY.\n", expires)
	fmt.Fprintln(cmd.OutOrStdout(), key.Key)
	return nil
}

// requireManaged refuses a target that is not DBOS-managed: only DBOS Cloud
// issues license keys, and a self-hosted profile has no cloud to ask.
func requireManaged(s config.Settings) error {
	if s.Domain != "" {
		return nil
	}
	what := "no profile is selected"
	if s.Profile != "" {
		what = fmt.Sprintf("profile %q is not DBOS-managed", s.Profile)
	}
	return fmt.Errorf("license keys are issued by DBOS Cloud, and %s; pass --profile with a managed profile (create one with `dbosctl config set <name> --managed`)", what)
}

// apiKeyNotAccepted explains why a dbos_ API key cannot create a license key,
// naming where the key came from so the user knows what to change.
func apiKeyNotAccepted(profile string) error {
	if os.Getenv("DBOS_TOKEN") != "" {
		return &exitError{code: 3, msg: "license keys can only be created by a logged-in user, not an API key; unset $DBOS_TOKEN and run `dbosctl login`"}
	}
	return &exitError{code: 3, msg: fmt.Sprintf("license keys can only be created by a logged-in user, and profile %q holds an API key; run `dbosctl login`", profile)}
}
