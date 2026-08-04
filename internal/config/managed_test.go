package config

import "testing"

func TestResolveManagedDomainProd(t *testing.T) {
	f := &File{Profiles: map[string]Profile{"prod": {Domain: "cloud.dbos.dev"}}}
	s, err := f.Resolve(Inputs{Profile: src("prod", true, "")})
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://cloud.dbos.dev/conductor" {
		t.Errorf("URL = %q, want the derived managed URL", s.URL)
	}
	if s.Auth != AuthBearer {
		t.Errorf("Auth = %q, want bearer", s.Auth)
	}
	if s.OIDC == nil || s.OIDC.Issuer != prodAuth0Issuer || s.OIDC.ClientID != prodAuth0ClientID {
		t.Errorf("OIDC = %+v, want the production tenant", s.OIDC)
	}
}

func TestResolveManagedDomainNonProd(t *testing.T) {
	f := &File{Profiles: map[string]Profile{"staging": {Domain: "staging.dev.dbos.dev"}}}
	s, err := f.Resolve(Inputs{Profile: src("staging", true, "")})
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://staging.dev.dbos.dev/conductor" {
		t.Errorf("URL = %q", s.URL)
	}
	if s.Auth != AuthBearer {
		t.Errorf("Auth = %q, want bearer", s.Auth)
	}
	if s.OIDC == nil || s.OIDC.Issuer != nonprodAuth0Issuer || s.OIDC.ClientID != nonprodAuth0ClientID {
		t.Errorf("OIDC = %+v, want the non-production tenant", s.OIDC)
	}
}

func TestResolveProdRecognizedFromURL(t *testing.T) {
	// A production URL with no explicit Domain is still recognized as managed —
	// across every equivalent spelling of the host. Any miss here falls through
	// to no-auth (a baffling 401) or, worse, to the non-production tenant.
	for _, u := range []string{
		"https://cloud.dbos.dev/conductor",
		"https://cloud.dbos.dev:443/conductor",  // explicit default port
		"https://CLOUD.DBOS.DEV/conductor",      // uppercase
		"https://Cloud.DBOS.dev/conductor",      // mixed case
		"https://cloud.dbos.dev./conductor",     // root-absolute trailing dot
		"https://cloud.dbos.dev.:443/conductor", // trailing dot + port
		"https://CLOUD.DBOS.DEV./conductor",     // trailing dot + uppercase
		"https://cloud.dbos.dev",                // no path
		"https://cloud.dbos.dev/",               // bare root path
	} {
		f := &File{Profiles: map[string]Profile{"p": {URL: u}}}
		s, err := f.Resolve(Inputs{Profile: src("p", true, "")})
		if err != nil {
			t.Fatal(err)
		}
		if s.Domain != ManagedProdDomain || s.Auth != AuthBearer {
			t.Errorf("%s should resolve as managed+bearer: domain=%q auth=%q", u, s.Domain, s.Auth)
		}
		if s.OIDC == nil || s.OIDC.Issuer != prodAuth0Issuer {
			t.Errorf("%s: OIDC = %+v, want the production tenant", u, s.OIDC)
		}
	}
}

func TestResolveProdRequiresHTTPS(t *testing.T) {
	// The production host over anything but https is an error, not a silent
	// fall-through to no-auth: the bearer token must never ride a plaintext
	// request, and a scheme typo should say so rather than surface as a 401.
	for _, u := range []string{
		"http://cloud.dbos.dev/conductor",
		"//cloud.dbos.dev/conductor",
		"http://cloud.dbos.dev:443/conductor",
		"http://CLOUD.DBOS.DEV/conductor",
	} {
		f := &File{Profiles: map[string]Profile{"p": {URL: u}}}
		if _, err := f.Resolve(Inputs{Profile: src("p", true, "")}); err == nil {
			t.Errorf("Resolve(%q) = nil error, want an https requirement", u)
		}
	}

	// A non-production host over http is untouched — self-hosted conductors are
	// routinely plaintext on a private network.
	f := &File{Profiles: map[string]Profile{"p": {URL: "http://localhost:8090"}}}
	s, err := f.Resolve(Inputs{Profile: src("p", true, "")})
	if err != nil {
		t.Fatalf("a plaintext self-hosted URL should resolve fine: %v", err)
	}
	if s.Domain != "" || s.Auth != AuthNone {
		t.Errorf("self-hosted http resolved as managed: domain=%q auth=%q", s.Domain, s.Auth)
	}
}

func TestManagedOIDCProdHostVariants(t *testing.T) {
	// A stored domain in any equivalent spelling still selects production. The
	// failure this guards is silent: the wrong branch hands out staging Auth0
	// credentials for a production host.
	for _, d := range []string{
		"cloud.dbos.dev",
		"cloud.dbos.dev:443",
		"CLOUD.DBOS.DEV",
		"Cloud.DBOS.dev",
		"cloud.dbos.dev.",
		"cloud.dbos.dev.:443",
		"CLOUD.DBOS.DEV.",
	} {
		if o := managedOIDC(d); o.Issuer != prodAuth0Issuer {
			t.Errorf("managedOIDC(%q) issuer = %q, want the production tenant", d, o.Issuer)
		}
	}
	// Genuinely non-production hosts still get the shared staging tenant — the
	// normalization must not over-match onto a different host.
	for _, d := range []string{
		"staging.dev.dbos.dev",
		"cloud.dbos.dev.evil.com",
		"notcloud.dbos.dev",
		"cloud.dbos.de",
	} {
		if o := managedOIDC(d); o.Issuer != nonprodAuth0Issuer {
			t.Errorf("managedOIDC(%q) issuer = %q, want the non-production tenant", d, o.Issuer)
		}
	}
}

func TestResolveExplicitOIDCWinsOverDomain(t *testing.T) {
	f := &File{Profiles: map[string]Profile{
		"p": {Domain: "cloud.dbos.dev", OIDC: &OIDC{Issuer: "https://custom/", ClientID: "cid"}},
	}}
	s, err := f.Resolve(Inputs{Profile: src("p", true, "")})
	if err != nil {
		t.Fatal(err)
	}
	if s.OIDC.Issuer != "https://custom/" {
		t.Errorf("explicit oidc should win over the domain tenant, got %+v", s.OIDC)
	}
}
