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
	c.Flags().Bool("cloud", false, "")
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
	if err := runConfigSet(set2, []string{"cloud"}); err != nil {
		t.Fatal(err)
	}

	// list shows both, current marked.
	var listOut bytes.Buffer
	list := &cobra.Command{}
	list.SetOut(&listOut)
	if err := runConfigList(list, nil); err != nil {
		t.Fatal(err)
	}
	if ls := listOut.String(); !strings.Contains(ls, "* local") || !strings.Contains(ls, "cloud") {
		t.Errorf("list output wrong:\n%s", ls)
	}

	// use cloud.
	use := &cobra.Command{}
	use.SetOut(&bytes.Buffer{})
	if err := runConfigUse(use, []string{"cloud"}); err != nil {
		t.Fatal(err)
	}
	if f, _ = config.Load(); f.Current != "cloud" {
		t.Errorf("after use: current = %q, want cloud", f.Current)
	}
	// use unknown profile fails.
	if err := runConfigUse(use, []string{"nope"}); err == nil {
		t.Error("use unknown profile = nil error, want error")
	}

	// show cloud reflects the bearer auth.
	var showOut bytes.Buffer
	show := &cobra.Command{}
	show.SetOut(&showOut)
	if err := runConfigShow(show, []string{"cloud"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showOut.String(), "auth      bearer") {
		t.Errorf("show missing bearer auth:\n%s", showOut.String())
	}
}

func TestConfigSetCloud(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// --cloud → the DBOS Cloud production domain.
	set := newConfigSetCmd()
	_ = set.Flags().Set("cloud", "true")
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"cloud"}); err != nil {
		t.Fatal(err)
	}
	f, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Profiles["cloud"].Domain; got != config.CloudProdDomain {
		t.Errorf("domain = %q, want %q", got, config.CloudProdDomain)
	}

	// --cloud on a profile that previously had a url clears the url.
	set2 := newConfigSetCmd()
	_ = set2.Flags().Set("url", "http://localhost:8090")
	set2.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set2, []string{"switch"}); err != nil {
		t.Fatal(err)
	}
	set3 := newConfigSetCmd()
	_ = set3.Flags().Set("cloud", "true")
	set3.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set3, []string{"switch"}); err != nil {
		t.Fatal(err)
	}
	if f, _ = config.Load(); f.Profiles["switch"].URL != "" {
		t.Errorf("--cloud left a stale url %q", f.Profiles["switch"].URL)
	}
	if f.Profiles["switch"].Domain != config.CloudProdDomain {
		t.Errorf("--cloud domain = %q, want prod", f.Profiles["switch"].Domain)
	}
}

func TestConfigSetCloudURLConflict(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := newConfigSetCmd()
	_ = set.Flags().Set("cloud", "true")
	_ = set.Flags().Set("url", "http://localhost:8090")
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"x"}); err == nil {
		t.Error("--cloud with --url should error")
	}
}

func TestConfigSetRequiresTarget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Neither --cloud nor --url on a new profile is an error, not a silent
	// cloud default.
	set := newConfigSetCmd()
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"nowhere"}); err == nil {
		t.Error("a profile with neither --cloud nor --url should error")
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

func TestConfigSetInvalidAuth(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := newConfigSetCmd()
	_ = set.Flags().Set("auth", "banana")
	set.SetOut(&bytes.Buffer{})
	if err := runConfigSet(set, []string{"x"}); err == nil {
		t.Error("set --auth banana = nil error, want error")
	}
}
