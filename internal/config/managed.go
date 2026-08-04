package config

import (
	"net"
	"strings"
)

// Auth0 tenants for DBOS-managed Conductor. A managed profile — one with a
// Domain — derives its conductor URL and login endpoints from these: the
// production tenant for cloud.dbos.dev, else the single shared non-production
// tenant (staging and dev clusters like <name>.dev.dbos.dev all use it). The
// audience is shared.
//
// Targeting a non-production deployment is intentionally undocumented — it
// exists for DBOS-internal clusters, and the `dbos config set --domain` flag is
// hidden.
const (
	ManagedProdDomain    = "cloud.dbos.dev"
	managedConductorPath = "/conductor" // the managed reverse-proxy prefix

	prodAuth0Issuer   = "https://login.dbos.dev/"
	prodAuth0ClientID = "6p7Sjxf13cyLMkdwn14MxlH7JdhILled"

	nonprodAuth0Issuer   = "https://dbos-inc.us.auth0.com/"
	nonprodAuth0ClientID = "G38fLmVErczEo9ioCFjVIHea6yd0qMZu"

	// The Auth0 API identifier, fixed server-side — not CLI terminology.
	managedAudience = "dbos-cloud-api"
)

// managedURL is the conductor base URL for a DBOS-managed domain.
func managedURL(domain string) string {
	return "https://" + domain + managedConductorPath
}

// isManagedProd reports whether a domain — a bare host, optionally with a port —
// is the production one. Port, case, and a root-absolute trailing dot are all
// ignored: an exact comparison would send "cloud.dbos.dev:443", "CLOUD.DBOS.DEV",
// or "cloud.dbos.dev." down the non-production branch, silently logging the user
// into the staging tenant against production.
func isManagedProd(domain string) bool {
	host := domain
	if h, _, err := net.SplitHostPort(domain); err == nil {
		host = h
	}
	// Exactly one trailing dot is legal (the DNS root), so trim at most one.
	host = strings.TrimSuffix(host, ".")
	return strings.EqualFold(host, ManagedProdDomain)
}

// managedOIDC returns the Auth0 login config for a DBOS-managed domain.
func managedOIDC(domain string) OIDC {
	if isManagedProd(domain) {
		return OIDC{Issuer: prodAuth0Issuer, ClientID: prodAuth0ClientID, Audience: managedAudience}
	}
	return OIDC{Issuer: nonprodAuth0Issuer, ClientID: nonprodAuth0ClientID, Audience: managedAudience}
}
