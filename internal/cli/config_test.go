package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/config"
)

func newConfigSetCmd() *cobra.Command {
	c := &cobra.Command{}
	c.Flags().String("url", "", "")
	c.Flags().String("org", "", "")
	c.Flags().String("app", "", "")
	c.Flags().String("auth", "", "")
	c.Flags().String("issuer", "", "")
	c.Flags().String("audience", "", "")
	c.Flags().String("client-id", "", "")
	c.Flags().Bool("managed", false, "")
	c.Flags().String("domain", "", "")
	c.SetContext(context.Background())
	return c
}

func TestConfigSetDomain(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	set := newConfigSetCmd()
	_ = set.Flags().Set("domain", "staging.dev.dbos.dev")
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"staging"}); err != nil {
		t.Fatal(err)
	}
	f, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Profiles["staging"].Domain; got != "staging.dev.dbos.dev" {
		t.Errorf("domain = %q, not saved", got)
	}

	// A URL passed as --domain is rejected.
	set2 := newConfigSetCmd()
	_ = set2.Flags().Set("domain", "https://staging.dev.dbos.dev")
	set2.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set2, []string{"bad"}); err == nil {
		t.Error("a URL as --domain should error")
	}
}

func TestConfigSetUseListShow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// set the first profile — it should auto-become current.
	set := newConfigSetCmd()
	_ = set.Flags().Set("url", "http://localhost:8090")
	_ = set.Flags().Set("org", "local")
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"local"}); err != nil {
		t.Fatal(err)
	}
	f, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Current != "local" {
		t.Errorf("current = %q, want local (first profile)", f.Current)
	}
	if f.Profiles["local"].URL != "http://localhost:8090" {
		t.Errorf("url not saved: %+v", f.Profiles["local"])
	}

	// set a second profile.
	set2 := newConfigSetCmd()
	_ = set2.Flags().Set("url", "https://cloud.dbos.dev/conductor")
	_ = set2.Flags().Set("auth", "bearer")
	set2.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set2, []string{"managed"}); err != nil {
		t.Fatal(err)
	}

	// list shows both, current marked.
	var listOut bytes.Buffer
	list := &cobra.Command{}
	list.SetOut(&listOut)
	if err := runConfigList(list, nil); err != nil {
		t.Fatal(err)
	}
	if ls := listOut.String(); !strings.Contains(ls, "* local") || !strings.Contains(ls, "managed") {
		t.Errorf("list output wrong:\n%s", ls)
	}

	// use the managed profile.
	use := &cobra.Command{}
	use.SetOut(&bytes.Buffer{})
	if err := runConfigUse(use, []string{"managed"}); err != nil {
		t.Fatal(err)
	}
	if f, _ = config.Load(); f.Current != "managed" {
		t.Errorf("after use: current = %q, want managed", f.Current)
	}
	// use unknown profile fails.
	if err := runConfigUse(use, []string{"nope"}); err == nil {
		t.Error("use unknown profile = nil error, want error")
	}

	// show reflects the bearer auth.
	var showOut bytes.Buffer
	show := &cobra.Command{}
	show.SetOut(&showOut)
	if err := runConfigShow(show, []string{"managed"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showOut.String(), "auth      bearer") {
		t.Errorf("show missing bearer auth:\n%s", showOut.String())
	}
}

func TestConfigSetManaged(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// --managed → the DBOS-managed production domain.
	set := newConfigSetCmd()
	_ = set.Flags().Set("managed", "true")
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"managed"}); err != nil {
		t.Fatal(err)
	}
	f, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Profiles["managed"].Domain; got != config.ManagedProdDomain {
		t.Errorf("domain = %q, want %q", got, config.ManagedProdDomain)
	}

	// --managed on a profile that previously had a url clears the url.
	set2 := newConfigSetCmd()
	_ = set2.Flags().Set("url", "http://localhost:8090")
	set2.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set2, []string{"switch"}); err != nil {
		t.Fatal(err)
	}
	set3 := newConfigSetCmd()
	_ = set3.Flags().Set("managed", "true")
	set3.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set3, []string{"switch"}); err != nil {
		t.Fatal(err)
	}
	if f, _ = config.Load(); f.Profiles["switch"].URL != "" {
		t.Errorf("--managed left a stale url %q", f.Profiles["switch"].URL)
	}
	if f.Profiles["switch"].Domain != config.ManagedProdDomain {
		t.Errorf("--managed domain = %q, want prod", f.Profiles["switch"].Domain)
	}
}

func TestConfigSetManagedURLConflict(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := newConfigSetCmd()
	_ = set.Flags().Set("managed", "true")
	_ = set.Flags().Set("url", "http://localhost:8090")
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"x"}); err == nil {
		t.Error("--managed with --url should error")
	}
}

func TestConfigSetRequiresTarget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Neither --managed nor --url on a new profile is an error, not a silent
	// managed default.
	set := newConfigSetCmd()
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"nowhere"}); err == nil {
		t.Error("a profile with neither --managed nor --url should error")
	}

	// Editing an existing profile's other fields does not require re-stating the
	// target — the upsert keeps the url that was already set.
	seed := newConfigSetCmd()
	_ = seed.Flags().Set("url", "http://localhost:8090")
	seed.SetOut(&bytes.Buffer{})
	if err := runConfigSet(seed, []string{"local"}); err != nil {
		t.Fatal(err)
	}
	edit := newConfigSetCmd()
	_ = edit.Flags().Set("org", "acme")
	edit.SetOut(&bytes.Buffer{})
	if err := runConfigSet(edit, []string{"local"}); err != nil {
		t.Fatalf("editing an existing profile should not require a target flag: %v", err)
	}
	f, _ := config.Load()
	if f.Profiles["local"].URL != "http://localhost:8090" || f.Profiles["local"].Org != "acme" {
		t.Errorf("upsert did not preserve url / apply org: %+v", f.Profiles["local"])
	}
}

func TestConfigSetOIDCImpliesBearer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// A self-hosted profile with OIDC flags but no --auth infers bearer.
	set := newConfigSetCmd()
	_ = set.Flags().Set("url", "http://host:8090")
	_ = set.Flags().Set("issuer", "https://idp.example/realm")
	_ = set.Flags().Set("client-id", "dbos-cli")
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"corp"}); err != nil {
		t.Fatal(err)
	}
	f, _ := config.Load()
	if got := f.Profiles["corp"].Auth; got != config.AuthBearer {
		t.Errorf("OIDC flags should imply bearer auth, got %q", got)
	}

	// An explicit --auth still wins over the inference.
	set2 := newConfigSetCmd()
	_ = set2.Flags().Set("url", "http://host:8090")
	_ = set2.Flags().Set("issuer", "https://idp.example/realm")
	_ = set2.Flags().Set("auth", "none")
	set2.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set2, []string{"weird"}); err != nil {
		t.Fatal(err)
	}
	if f, _ = config.Load(); f.Profiles["weird"].Auth != config.AuthNone {
		t.Errorf("explicit --auth none should win over the OIDC inference, got %q", f.Profiles["weird"].Auth)
	}

	// A bare --url with no OIDC and no --auth stays no-auth (resolve defaults it).
	set3 := newConfigSetCmd()
	_ = set3.Flags().Set("url", "http://host:8090")
	set3.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set3, []string{"plain"}); err != nil {
		t.Fatal(err)
	}
	if f, _ = config.Load(); f.Profiles["plain"].Auth != "" {
		t.Errorf("a bare --url should not set auth, got %q", f.Profiles["plain"].Auth)
	}
}

func TestConfigSetInvalidAuth(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := newConfigSetCmd()
	_ = set.Flags().Set("auth", "banana")
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"x"}); err == nil {
		t.Error("set --auth banana = nil error, want error")
	}
}
