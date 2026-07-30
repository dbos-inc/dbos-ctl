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
	Long: `Create or update a profile. Only the flags you pass are changed, so a
field is left untouched unless named. --url/--org/--app are the global flags;
--auth and the OIDC flags are specific to this command.`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigSet,
}

func init() {
	configSetCmd.Flags().String("auth", "", "authentication: none or bearer")
	configSetCmd.Flags().String("issuer", "", "OIDC issuer URL (bearer profiles)")
	configSetCmd.Flags().String("audience", "", "OIDC audience (bearer profiles)")
	configSetCmd.Flags().String("client-id", "", "OIDC client ID (bearer profiles)")
	// A DBOS Cloud profile: derives url + bearer auth + the Auth0 tenant.
	// Hidden — non-production clusters are for internal use.
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
	}
	if cmd.Flags().Changed("domain") {
		d, _ := cmd.Flags().GetString("domain")
		if strings.Contains(d, "/") {
			return fmt.Errorf("--domain should be a bare host like cloud.dbos.dev, not a URL")
		}
		p.Domain = d
	}

	// A profile with neither a self-hosted URL nor a domain targets DBOS Cloud:
	// default it to the production domain (`dbos config set cloud` just works).
	// The hidden --domain overrides this for non-production clusters.
	if p.URL == "" && p.Domain == "" {
		p.Domain = config.CloudProdDomain
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
