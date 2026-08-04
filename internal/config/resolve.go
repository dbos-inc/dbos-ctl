package config

import (
	"fmt"
	"net/url"
)

// FieldSource carries one setting's flag value (with whether the flag was
// explicitly set) and its environment value, for the flag > env > profile
// precedence.
type FieldSource struct {
	Flag    string
	FlagSet bool
	Env     string
}

func (fs FieldSource) pick(profileVal string) string {
	if fs.FlagSet {
		return fs.Flag
	}
	if fs.Env != "" {
		return fs.Env
	}
	return profileVal
}

// Inputs are the flag/env sources for the resolvable settings.
type Inputs struct {
	Profile FieldSource
	URL     FieldSource
	Org     FieldSource
	App     FieldSource
}

// Settings are the effective settings after applying precedence.
type Settings struct {
	Profile string // active profile name, or "" for ad-hoc use
	Domain  string // DBOS-managed domain, or "" when not a managed target
	URL     string
	Org     string
	App     string
	Auth    Auth
	OIDC    *OIDC
}

// Resolve computes effective settings: for each field, flag > env > active
// profile. The active profile is the Profile source picked against File.Current;
// naming a profile that does not exist is an error.
func (f *File) Resolve(in Inputs) (Settings, error) {
	name := in.Profile.pick(f.Current)
	var p Profile
	if name != "" {
		var ok bool
		p, ok = f.Profiles[name]
		if !ok {
			return Settings{}, fmt.Errorf("profile %q not found (see `dbos config list`)", name)
		}
	}

	// A managed profile is identified by an explicit Domain (which derives the
	// conductor URL), or — for production only — by a URL that points at
	// cloud.dbos.dev.
	domain := p.Domain
	profileURL := p.URL
	if domain != "" {
		profileURL = managedURL(domain)
	}
	resolvedURL := in.URL.pick(profileURL)
	if domain == "" {
		if u, err := url.Parse(resolvedURL); err == nil && u.Host == ManagedProdDomain {
			domain = ManagedProdDomain
		}
	}

	s := Settings{
		Profile: name,
		Domain:  domain,
		URL:     resolvedURL,
		Org:     in.Org.pick(p.Org),
		App:     in.App.pick(p.App),
		Auth:    p.Auth,
		OIDC:    p.OIDC,
	}
	// A managed target always authenticates and, absent an explicit oidc block,
	// uses the Auth0 tenant for its domain.
	if domain != "" {
		s.Auth = AuthBearer
		if s.OIDC == nil {
			o := managedOIDC(domain)
			s.OIDC = &o
		}
	}
	if s.Auth == "" {
		s.Auth = AuthNone
	}
	// A no-auth deployment is always org "local".
	if s.Org == "" && s.Auth == AuthNone {
		s.Org = "local"
	}
	return s, nil
}
