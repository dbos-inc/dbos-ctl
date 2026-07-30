package config

// DBOS Cloud Auth0 tenants. A cloud profile — one with a Domain — derives its
// conductor URL and login endpoints from these: the production tenant for
// cloud.dbos.dev, else the single shared non-production tenant (staging and
// dev clusters like <name>.dev.dbos.dev all use it). The audience is shared.
//
// Targeting a non-production cloud is intentionally undocumented — it exists for
// DBOS-internal clusters, and the `dbos config set --domain` flag is hidden.
const (
	CloudProdDomain    = "cloud.dbos.dev"
	cloudConductorPath = "/conductor" // the cloud reverse-proxy prefix

	prodAuth0Issuer   = "https://login.dbos.dev/"
	prodAuth0ClientID = "6p7Sjxf13cyLMkdwn14MxlH7JdhILled"

	nonprodAuth0Issuer   = "https://dbos-inc.us.auth0.com/"
	nonprodAuth0ClientID = "G38fLmVErczEo9ioCFjVIHea6yd0qMZu"

	cloudAudience = "dbos-cloud-api"
)

// cloudURL is the conductor base URL for a DBOS Cloud domain.
func cloudURL(domain string) string {
	return "https://" + domain + cloudConductorPath
}

// cloudOIDC returns the Auth0 login config for a DBOS Cloud domain.
func cloudOIDC(domain string) OIDC {
	if domain == CloudProdDomain {
		return OIDC{Issuer: prodAuth0Issuer, ClientID: prodAuth0ClientID, Audience: cloudAudience}
	}
	return OIDC{Issuer: nonprodAuth0Issuer, ClientID: nonprodAuth0ClientID, Audience: cloudAudience}
}
