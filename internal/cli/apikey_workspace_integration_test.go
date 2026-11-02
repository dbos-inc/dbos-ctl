//go:build integration

package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dbos-inc/dbos-ctl/internal/api"
	"github.com/dbos-inc/dbos-ctl/internal/client"
	"github.com/dbos-inc/dbos-ctl/internal/conductortest"
)

// TestAPIKeyWorkspaceIntegration drives Workspace-scoped `api-key create` and
// the scope column of `api-key list` against a real no-auth Conductor: a key
// scoped to two workspaces next to an app-scoped and an all-apps key, the
// --app conflict, empty names, and Conductor's refusal of organization-wide
// permissions on a workspace key. None of the refusals may leave a key behind.
func TestAPIKeyWorkspaceIntegration(t *testing.T) {
	isolateConfig(t)
	baseURL := conductortest.Start(t)
	ctx := context.Background()

	seed, err := newSeedClient(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	// Registered while `default` is the only workspace, so it needs none named.
	registerAppOrFail(t, seed, "local", "key-app")
	ws, err := seed.CreateWorkspaceWithResponse(ctx, "local", api.CreateWorkspaceJSONRequestBody{Name: "staging"})
	if err != nil || ws.JSON201 == nil {
		t.Fatalf("create workspace: %v %s", err, ws.Body)
	}

	create := func(name string, flags map[string][]string) (string, error) {
		t.Helper()
		cmd, out, _ := newAPIKeyCreateCmd(t, baseURL)
		_ = cmd.Flags().Set("org", "local")
		for flag, values := range flags {
			for _, v := range values {
				_ = cmd.Flags().Set(flag, v)
			}
		}
		err := runAPIKeyCreate(cmd, []string{name})
		return strings.TrimSpace(out.String()), err
	}
	list := func(format string) string {
		t.Helper()
		cmd, out := appReadCmd(t, baseURL)
		_ = cmd.Flags().Set("org", "local")
		_ = cmd.Flags().Set("output", format)
		if err := runAPIKeyList(cmd, nil); err != nil {
			t.Fatalf("api-key list: %v", err)
		}
		return out.String()
	}

	secret, err := create("ws-key", map[string][]string{
		"workspace":  {"staging", "default"},
		"permission": {"websocket.connect", "application.read"},
	})
	if err != nil {
		t.Fatalf("workspace-scoped create: %v", err)
	}
	if !strings.HasPrefix(secret, "dbos_") {
		t.Fatalf("minted secret %q is not a dbos_ key", secret)
	}
	if _, err := create("app-key", map[string][]string{"app": {"key-app"}}); err != nil {
		t.Fatalf("app-scoped create: %v", err)
	}
	if _, err := create("all-key", nil); err != nil {
		t.Fatalf("unscoped create: %v", err)
	}

	table := list("table")
	for key, scope := range map[string]string{
		"ws-key":  "workspaces: default, staging",
		"app-key": "app ids: ",
		"all-key": "all apps",
	} {
		if !rowContains(table, key, scope) {
			t.Errorf("row %q should show scope %q:\n%s", key, scope, table)
		}
	}
	if !rowContains(table, "ws-key", "application.read, websocket.connect") &&
		!rowContains(table, "ws-key", "websocket.connect, application.read") {
		t.Errorf("ws-key should keep both requested permissions:\n%s", table)
	}
	if js := list("json"); !strings.Contains(js, `"workspaces": [`) || !strings.Contains(js, `"staging"`) {
		t.Errorf("json list missing the workspace scope:\n%s", js)
	}

	// Refusals, each named so the list can show none of them was created.
	refusals := []struct {
		name  string
		flags map[string][]string
		want  []string
	}{
		{"conflict", map[string][]string{"workspace": {"staging"}, "app": {"key-app"}},
			[]string{"--workspace cannot be combined with --app"}},
		{"empty", map[string][]string{"workspace": {""}},
			[]string{"--workspace was given an empty name"}},
		{"trailing", map[string][]string{"workspace": {"staging,"}},
			[]string{"--workspace was given an empty name"}},
		{"later-empty", map[string][]string{"workspace": {"staging", ""}},
			[]string{"--workspace was given an empty name"}},
		{"org-read", map[string][]string{"workspace": {"staging"}, "permission": {"organization.read"}},
			[]string{"[1014]", `"organization.read" is organization-wide`, "application.read, application.write, metric.read, websocket.connect"}},
		// token.read, not token.write: no key may hold token.write at all, so
		// that one never reaches the workspace check.
		{"token-read", map[string][]string{"workspace": {"staging"}, "permission": {"application.read", "token.read"}},
			[]string{"[1014]", `"token.read" is organization-wide`}},
	}
	for _, r := range refusals {
		_, err := create(r.name, r.flags)
		if err == nil {
			t.Errorf("%s: create succeeded, want a refusal", r.name)
			continue
		}
		for _, want := range r.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error missing %q: %v", r.name, want, err)
			}
		}
	}
	table = list("table")
	for _, r := range refusals {
		if hasRow(table, r.name) {
			t.Errorf("refused key %q was created:\n%s", r.name, table)
		}
	}
}

// TestAPIKeyWorkspaceEscalationIntegration proves a refused escalation reaches
// the CLI user readably: against an OAuth Conductor (self-hosted orgs are
// Enterprise, so custom roles work), a member whose role holds token.write but
// only reads in `staging` can mint a read key for `staging`, not a write key.
func TestAPIKeyWorkspaceEscalationIntegration(t *testing.T) {
	oc := conductortest.StartOAuth(t)
	ctx := context.Background()

	alice := oc.Issuer.Token(t, "test|alice", "alice@example.com")
	admin, err := client.New(client.Config{BaseURL: oc.BaseURL, Token: alice})
	if err != nil {
		t.Fatal(err)
	}
	mustSucceed := func(desc string, status int, body []byte, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", desc, err)
		}
		if status >= 300 {
			t.Fatalf("%s: HTTP %d: %s", desc, status, body)
		}
	}

	reg, err := admin.RegisterUserWithResponse(ctx, api.RegisterUserJSONRequestBody{Name: "alice"})
	mustSucceed("register alice", reg.StatusCode(), reg.Body, err)
	me, err := admin.GetCurrentUserWithResponse(ctx)
	mustSucceed("get current user", me.StatusCode(), me.Body, err)
	if me.JSON200 == nil {
		t.Fatalf("get current user: HTTP %d: %s", me.StatusCode(), me.Body)
	}
	org := me.JSON200.OrgName

	ws, err := admin.CreateWorkspaceWithResponse(ctx, org, api.CreateWorkspaceJSONRequestBody{Name: "staging"})
	mustSucceed("create workspace", ws.StatusCode(), ws.Body, err)
	staging := "staging"
	orgPerms := []string{"token.read", "token.write"}
	grants := []api.Grant{{Permission: "application.read", Workspace: &staging}}
	role, err := admin.CreateRoleWithResponse(ctx, org, api.CreateRoleJSONRequestBody{
		Name: "keylead", Permissions: &orgPerms, Grants: &grants,
	})
	mustSucceed("create role", role.StatusCode(), role.Body, err)

	sec, err := admin.GenerateSecretWithResponse(ctx, org)
	mustSucceed("generate invite secret", sec.StatusCode(), sec.Body, err)
	if sec.JSON201 == nil {
		t.Fatalf("generate invite secret: HTTP %d: %s", sec.StatusCode(), sec.Body)
	}
	bobToken := oc.Issuer.Token(t, "test|bob", "bob@example.com")
	bob, err := client.New(client.Config{BaseURL: oc.BaseURL, Token: bobToken})
	if err != nil {
		t.Fatal(err)
	}
	join, err := bob.RegisterUserWithResponse(ctx, api.RegisterUserJSONRequestBody{Name: "bob", Secret: &sec.JSON201.Secret})
	mustSucceed("register bob", join.StatusCode(), join.Body, err)
	grant, err := admin.GrantRoleWithResponse(ctx, org, "bob", "keylead")
	mustSucceed("grant role", grant.StatusCode(), grant.Body, err)

	isolateConfig(t)
	t.Setenv("DBOS_TOKEN", bobToken)
	saveBearerProfile(t, oc.BaseURL, org)
	create := func(name, permission string) error {
		t.Helper()
		cmd, _, _ := newAPIKeyCreateCmd(t, oc.BaseURL)
		_ = cmd.Flags().Set("workspace", "staging")
		_ = cmd.Flags().Set("permission", permission)
		return runAPIKeyCreate(cmd, []string{name})
	}

	if err := create("bob-read", "application.read"); err != nil {
		t.Fatalf("a key within bob's grants was refused: %v", err)
	}
	err = create("bob-write", "application.write")
	if err == nil {
		t.Fatal("a key beyond bob's grants was created")
	}
	for _, want := range []string{"[1005]", "cannot grant permissions you do not hold", `application.write in workspace "staging"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("escalation error missing %q: %v", want, err)
		}
	}
	if code := exitCodeFor(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

// hasRow reports whether the table has a row starting with key.
func hasRow(table, key string) bool { return rowContains(table, key, "") }
