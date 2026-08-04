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
		// Hostname() (not Host) so an explicit :443 or IPv6 brackets still match,
		// and isManagedProd folds case.
		if u, err := url.Parse(resolvedURL); err == nil && isManagedProd(u.Hostname()) {
			// Production is https-only — managedURL emits nothing else. Say so
			// rather than fall through: silently dropping to no-auth surfaces as a
			// 401 that sends the user to `dbos login` for a scheme typo, and
			// attaching the bearer token anyway would put it on the wire in
			// cleartext (the host's 301 to https comes too late).
			if u.Scheme != "https" {
				return Settings{}, fmt.Errorf("%s must be reached over https (got %q)", ManagedProdDomain, resolvedURL)
			}
			domain = ManagedProdDomain // store canonical, not whatever was typed
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
