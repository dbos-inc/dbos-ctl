// Code generated from internal/api/openapi-3.1.json by internal/gen/oauthgated; DO NOT EDIT.

package api

// OAuthGated reports, by operationId, which operations carry the spec's
// x-dbos-requires-oauth extension and therefore do not exist in a no-auth
// (auth: none) deployment. The CLI consults it to fail such a command before
// the request with a mode-aware message rather than on a raw 404.
var OAuthGated = map[string]bool{
	"createRole":      true,
	"deleteRole":      true,
	"generateSecret":  true,
	"getCurrentUser":  true,
	"getOrg":          true,
	"grantRole":       true,
	"joinOrg":         true,
	"listAuditLogs":   true,
	"listMembers":     true,
	"listPermissions": true,
	"listRoles":       true,
	"registerUser":    true,
	"removeMember":    true,
	"updateOrg":       true,
}
