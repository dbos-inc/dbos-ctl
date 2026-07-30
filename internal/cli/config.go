package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage connection profiles",
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List profiles",
	Args:  cobra.NoArgs,
	RunE:  runConfigList,
}

var configShowCmd = &cobra.Command{
	Use:   "show [profile]",
	Short: "Show a profile (defaults to the current one)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runConfigShow,
}

var configUseCmd = &cobra.Command{
	Use:   "use <profile>",
	Short: "Set the current profile",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigUse,
}

var configSetCmd = &cobra.Command{
	Use:   "set <profile>",
	Short: "Create or update a profile",
	Long: `Create or update a profile. Only the flags you pass change; unnamed
fields are left as-is.

Target exactly one of:
  --cloud                DBOS Cloud (production domain cloud.dbos.dev)
  --url <conductor url>  a self-hosted Conductor

For a self-hosted Conductor with OIDC login, add --issuer and --client-id (and
--audience if the deployment requires it); that implies bearer auth. Bearer auth
without OIDC (a dbos_ API key only) is requested with --auth bearer; since an API
key carries no user identity, set --org too so the CLI knows your organization.`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigSet,
}

func init() {
	// config set writes the local config file, so these name the values to
	// store — not the request-shaping overrides the operational commands take.
	configSetCmd.Flags().String("url", "", "self-hosted Conductor base URL")
	configSetCmd.Flags().String("org", "", "organization (only needed when it can't be derived from a login)")
	configSetCmd.Flags().String("app", "", "default application for this profile")
	configSetCmd.Flags().String("auth", "", "force bearer auth without OIDC (a dbos_ API key)")
	configSetCmd.Flags().String("issuer", "", "OIDC issuer URL (implies bearer auth)")
	configSetCmd.Flags().String("audience", "", "OIDC audience (bearer profiles)")
	configSetCmd.Flags().String("client-id", "", "OIDC client ID (implies bearer auth)")
	// A DBOS Cloud profile: derives url + bearer auth + the Auth0 tenant.
	configSetCmd.Flags().Bool("cloud", false, "make this a DBOS Cloud profile")
	// --domain overrides the production cloud domain for non-production
	// clusters; it implies --cloud. Hidden — internal use only.
	configSetCmd.Flags().String("domain", "", "DBOS Cloud domain")
	_ = configSetCmd.Flags().MarkHidden("domain")

	configCmd.AddCommand(configListCmd, configShowCmd, configUseCmd, configSetCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigList(cmd *cobra.Command, _ []string) error {
	f, err := config.Load()
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if len(f.Profiles) == 0 {
		fmt.Fprintln(w, "no profiles configured (create one with `dbos config set`)")
		return nil
	}
	names := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	// A leading "*" marks the current profile.
	for _, n := range names {
		marker := "  "
		if n == f.Current {
			marker = "* "
		}
		fmt.Fprintf(w, "%s%s\n", marker, n)
	}
	return nil
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	f, err := config.Load()
	if err != nil {
		return err
	}
	name := f.Current
	if len(args) == 1 {
		name = args[0]
	}
	if name == "" {
		return fmt.Errorf("no profile given and no current profile set")
	}
	p, ok := f.Profiles[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "name      %s\n", name)
	if p.Domain != "" {
		// A cloud profile derives url + bearer auth from the domain.
		fmt.Fprintf(w, "domain    %s\n", p.Domain)
		fmt.Fprintf(w, "auth      bearer\n")
	} else {
		auth := p.Auth
		if auth == "" {
			auth = config.AuthNone
		}
		fmt.Fprintf(w, "auth      %s\n", auth)
		fmt.Fprintf(w, "url       %s\n", p.URL)
	}
	if p.Org != "" {
		fmt.Fprintf(w, "org       %s\n", p.Org)
	}
	if p.App != "" {
		fmt.Fprintf(w, "app       %s\n", p.App)
	}
	if p.OIDC != nil {
		fmt.Fprintf(w, "issuer    %s\n", p.OIDC.Issuer)
		fmt.Fprintf(w, "audience  %s\n", p.OIDC.Audience)
		fmt.Fprintf(w, "clientID  %s\n", p.OIDC.ClientID)
	}
	return nil
}

func runConfigUse(cmd *cobra.Command, args []string) error {
	name := args[0]
	f, err := config.Load()
	if err != nil {
		return err
	}
	if _, ok := f.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found (create it with `dbos config set %s ...`)", name, name)
	}
	f.Current = name
	if err := config.Save(f); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "current profile is now %q\n", name)
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	name := args[0]
	f, err := config.Load()
	if err != nil {
		return err
	}

	p := f.Profiles[name] // zero Profile if new
	if cmd.Flags().Changed("url") {
		p.URL, _ = cmd.Flags().GetString("url")
	}
	if cmd.Flags().Changed("org") {
		p.Org, _ = cmd.Flags().GetString("org")
	}
	if cmd.Flags().Changed("app") {
		p.App, _ = cmd.Flags().GetString("app")
	}
	if cmd.Flags().Changed("auth") {
		v, _ := cmd.Flags().GetString("auth")
		a := config.Auth(v)
		if a != config.AuthNone && a != config.AuthBearer {
			return fmt.Errorf("invalid --auth %q (want: none, bearer)", v)
		}
		p.Auth = a
	}
	if cmd.Flags().Changed("issuer") || cmd.Flags().Changed("audience") || cmd.Flags().Changed("client-id") {
		if p.OIDC == nil {
			p.OIDC = &config.OIDC{}
		}
		if cmd.Flags().Changed("issuer") {
			p.OIDC.Issuer, _ = cmd.Flags().GetString("issuer")
		}
		if cmd.Flags().Changed("audience") {
			p.OIDC.Audience, _ = cmd.Flags().GetString("audience")
		}
		if cmd.Flags().Changed("client-id") {
			p.OIDC.ClientID, _ = cmd.Flags().GetString("client-id")
		}
		// OIDC login implies bearer auth, so don't make the user also pass
		// --auth. An explicit --auth still wins (checked above).
		if !cmd.Flags().Changed("auth") {
			p.Auth = config.AuthBearer
		}
	}
	// --cloud (or --domain, which implies it) makes a DBOS Cloud profile:
	// the domain derives url + bearer auth + the Auth0 tenant, so a self-hosted
	// --url alongside them would contradict. --domain overrides the production
	// domain.
	cloudFlags := cmd.Flags().Changed("cloud") || cmd.Flags().Changed("domain")
	if cloudFlags && cmd.Flags().Changed("url") {
		return fmt.Errorf("--cloud and --url are mutually exclusive (cloud derives its url from the domain)")
	}
	if cloudFlags {
		domain := config.CloudProdDomain
		if cmd.Flags().Changed("domain") {
			d, _ := cmd.Flags().GetString("domain")
			if strings.Contains(d, "/") {
				return fmt.Errorf("--domain should be a bare host like cloud.dbos.dev, not a URL")
			}
			domain = d
		}
		p.Domain = domain
		p.URL = "" // cloud derives its url from the domain
	}

	// A profile must target either DBOS Cloud (--cloud) or a self-hosted
	// conductor (--url). This only bites a genuinely unconfigured profile: an
	// upsert that edits other fields keeps whichever was already set.
	if p.URL == "" && p.Domain == "" {
		return fmt.Errorf("specify --cloud for a DBOS Cloud profile, or --url for a self-hosted conductor")
	}

	if f.Profiles == nil {
		f.Profiles = map[string]config.Profile{}
	}
	f.Profiles[name] = p
	// The first profile created becomes current automatically.
	if f.Current == "" {
		f.Current = name
	}
	if err := config.Save(f); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "saved profile %q\n", name)
	return nil
}
