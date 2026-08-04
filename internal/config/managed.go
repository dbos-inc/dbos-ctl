package config

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

// managedOIDC returns the Auth0 login config for a DBOS-managed domain.
func managedOIDC(domain string) OIDC {
	if domain == ManagedProdDomain {
		return OIDC{Issuer: prodAuth0Issuer, ClientID: prodAuth0ClientID, Audience: managedAudience}
	}
	return OIDC{Issuer: nonprodAuth0Issuer, ClientID: nonprodAuth0ClientID, Audience: managedAudience}
}
