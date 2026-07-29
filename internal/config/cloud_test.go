package config

import "testing"

func TestResolveCloudDomainProd(t *testing.T) {
	f := &File{Profiles: map[string]Profile{"prod": {Domain: "cloud.dbos.dev"}}}
	s, err := f.Resolve(Inputs{Profile: src("prod", true, "")})
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://cloud.dbos.dev/conductor" {
		t.Errorf("URL = %q, want the derived cloud URL", s.URL)
	}
	if s.Auth != AuthBearer {
		t.Errorf("Auth = %q, want bearer", s.Auth)
	}
	if s.OIDC == nil || s.OIDC.Issuer != prodAuth0Issuer || s.OIDC.ClientID != prodAuth0ClientID {
		t.Errorf("OIDC = %+v, want the production tenant", s.OIDC)
	}
}

func TestResolveCloudDomainNonProd(t *testing.T) {
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
	// A production URL with no explicit Domain is still recognized as cloud.
	f := &File{Profiles: map[string]Profile{"p": {URL: "https://cloud.dbos.dev/conductor"}}}
	s, err := f.Resolve(Inputs{Profile: src("p", true, "")})
	if err != nil {
		t.Fatal(err)
	}
	if s.Domain != "cloud.dbos.dev" || s.Auth != AuthBearer {
		t.Errorf("prod URL should resolve as cloud+bearer: domain=%q auth=%q", s.Domain, s.Auth)
	}
	if s.OIDC == nil || s.OIDC.Issuer != prodAuth0Issuer {
		t.Errorf("OIDC = %+v, want the production tenant", s.OIDC)
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
