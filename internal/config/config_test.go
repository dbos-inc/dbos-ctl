package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func src(flag string, set bool, env string) FieldSource {
	return FieldSource{Flag: flag, FlagSet: set, Env: env}
}

func TestResolvePrecedence(t *testing.T) {
	f := &File{
		Current: "prod",
		Profiles: map[string]Profile{
			"prod": {URL: "https://prof", Org: "acme", App: "billing"},
		},
	}
	// URL from flag, Org from env, App from profile — flag > env > profile.
	s, err := f.Resolve(Inputs{
		URL: src("https://flag", true, "https://env"),
		Org: src("", false, "envorg"),
		App: src("", false, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://flag" {
		t.Errorf("URL = %q, want the flag value", s.URL)
	}
	if s.Org != "envorg" {
		t.Errorf("Org = %q, want the env value", s.Org)
	}
	if s.App != "billing" {
		t.Errorf("App = %q, want the profile value", s.App)
	}
	if s.Profile != "prod" {
		t.Errorf("Profile = %q, want prod", s.Profile)
	}
}

func TestResolveProfileSelection(t *testing.T) {
	f := &File{
		Current:  "a",
		Profiles: map[string]Profile{"a": {URL: "urla"}, "b": {URL: "urlb"}},
	}
	// --profile overrides Current.
	s, err := f.Resolve(Inputs{Profile: src("b", true, "")})
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "urlb" {
		t.Errorf("URL = %q, want urlb (from profile b)", s.URL)
	}
	// An unknown profile is an error.
	if _, err := f.Resolve(Inputs{Profile: src("nope", true, "")}); err == nil {
		t.Error("Resolve with unknown profile = nil error, want error")
	}
}

func TestResolveOrgDefaultsLocal(t *testing.T) {
	f := &File{Profiles: map[string]Profile{}}
	s, err := f.Resolve(Inputs{URL: src("http://localhost:8090", true, "")})
	if err != nil {
		t.Fatal(err)
	}
	if s.Org != "local" {
		t.Errorf("Org = %q, want local (no-auth default)", s.Org)
	}
	if s.Auth != AuthNone {
		t.Errorf("Auth = %q, want none", s.Auth)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	// A nested path exercises MkdirAll.
	path := filepath.Join(t.TempDir(), "dbos", "config.yaml")
	in := &File{
		Current: "prod",
		Profiles: map[string]Profile{
			"prod": {
				Auth: AuthBearer, URL: "https://cloud.dbos.dev/conductor", Org: "acme",
				OIDC: &OIDC{Issuer: "https://login", Audience: "aud", ClientID: "cid"},
			},
			"local": {Auth: AuthNone, URL: "http://localhost:8090"},
		},
	}
	if err := saveTo(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := loadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n in: %+v\nout: %+v", in, out)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	f, err := loadFrom(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Profiles) != 0 || f.Current != "" {
		t.Errorf("missing config should be empty, got %+v", f)
	}
}
