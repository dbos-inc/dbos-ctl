//go:build integration

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/client"
	"github.com/dbos-inc/dbos-cli/internal/conductortest"
	"github.com/dbos-inc/dbos-cli/internal/config"
)

// TestAppListIntegration proves `dbos app list` end to end against a real
// Conductor: it seeds an app via the generated client, then lists it through
// the CLI command and asserts the row appears.
func TestAppListIntegration(t *testing.T) {
	baseURL := conductortest.Start(t)
	ctx := context.Background()

	c, err := api.NewClientWithResponses(baseURL)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	// A fresh no-auth conductor has the local org but no apps; seed one.
	const appName = "test-app"
	reg, err := c.RegisterAppWithResponse(ctx, "local", appName, api.RegisterAppJSONRequestBody{})
	if err != nil {
		t.Fatalf("register app: %v", err)
	}
	if reg.StatusCode() >= 300 {
		t.Fatalf("register app: HTTP %d: %s", reg.StatusCode(), reg.Body)
	}

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", baseURL)
	_ = cmd.Flags().Set("org", "local")
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runAppList(cmd, nil); err != nil {
		t.Fatalf("app list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, appName) {
		t.Errorf("app list output missing seeded app %q:\n%s", appName, got)
	}
}

// TestOAuthRoundTripsIntegration proves both bearer sources work end to end
// against a real OAuth-mode Conductor — the piece the device-flow tests can't
// reach. It stands up one OAuth conductor + issuer, registers a user with a
// minted JWT (Conductor does not auto-provision, so registration is what makes
// the identity exist), then runs two round-trips through the CLI:
//
//	jwt profile      — a user JWT reads its own profile (getCurrentUser, JWT-only)
//	dbos_ key list   — a dbos_ API key minted via the API lists an app
func TestOAuthRoundTripsIntegration(t *testing.T) {
	oc := conductortest.StartOAuth(t)
	ctx := context.Background()

	const (
		subject  = "test|alice"
		email    = "alice@example.com"
		username = "alice"
		appName  = "test-app"
	)
	token := oc.Issuer.Token(t, subject, email)

	jwtClient, err := client.New(client.Config{BaseURL: oc.BaseURL, Token: token})
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	// Register the user with the JWT (POST /v2/users) — creates the user + org.
	reg, err := jwtClient.RegisterUserWithResponse(ctx, api.RegisterUserJSONRequestBody{Name: username})
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	if reg.StatusCode() >= 300 {
		t.Fatalf("register user: HTTP %d: %s", reg.StatusCode(), reg.Body)
	}

	// The org name is generated at registration; the token + app calls need it.
	me, err := jwtClient.GetCurrentUserWithResponse(ctx)
	if err != nil {
		t.Fatalf("get current user: %v", err)
	}
	if me.JSON200 == nil {
		t.Fatalf("get current user: HTTP %d: %s", me.StatusCode(), me.Body)
	}
	orgName := me.JSON200.OrgName

	// Round-trip 1: the JWT reads its own profile back through the CLI.
	t.Run("jwt whoami", func(t *testing.T) {
		isolateConfig(t)
		t.Setenv("DBOS_TOKEN", token)
		saveBearerProfile(t, oc.BaseURL, "")

		cmd := newCmdWithGlobals()
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := runWhoami(cmd, nil); err != nil {
			t.Fatalf("whoami: %v", err)
		}
		if got := out.String(); !strings.Contains(got, username) || !strings.Contains(got, email) {
			t.Errorf("whoami output missing registered identity:\n%s", got)
		}
	})

	// Round-trip 2: a dbos_ API key (minted via the API with application.read)
	// lists an app through the CLI — the passthrough path getCurrentUser can't
	// reach, since API-key callers carry no user identity.
	t.Run("dbos_ key app list", func(t *testing.T) {
		// application.read is what listApps requires; the org creator can grant it.
		perms := []string{"application.read"}
		tok, err := jwtClient.CreateTokenWithResponse(ctx, orgName, "cli-test",
			api.CreateTokenJSONRequestBody{Permissions: &perms})
		if err != nil {
			t.Fatalf("create token: %v", err)
		}
		if tok.JSON201 == nil {
			t.Fatalf("create token: HTTP %d: %s", tok.StatusCode(), tok.Body)
		}
		apiKey := tok.JSON201.Token
		if !strings.HasPrefix(apiKey, "dbos_") {
			t.Fatalf("minted token %q is not a dbos_ key", apiKey)
		}

		// Seed an app (the JWT owner has application.write) for the key to list.
		app, err := jwtClient.RegisterAppWithResponse(ctx, orgName, appName, api.RegisterAppJSONRequestBody{})
		if err != nil {
			t.Fatalf("register app: %v", err)
		}
		if app.StatusCode() >= 300 {
			t.Fatalf("register app: HTTP %d: %s", app.StatusCode(), app.Body)
		}

		isolateConfig(t)
		t.Setenv("DBOS_TOKEN", apiKey)
		saveBearerProfile(t, oc.BaseURL, orgName)

		cmd := newCmdWithGlobals()
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := runAppList(cmd, nil); err != nil {
			t.Fatalf("app list: %v", err)
		}
		if got := out.String(); !strings.Contains(got, appName) {
			t.Errorf("app list (dbos_ key) missing seeded app %q:\n%s", appName, got)
		}
	})
}

// TestAppRegisterDeleteIntegration proves the D1 create/destroy round-trip
// through the CLI against a real no-auth Conductor: register an app, see it in
// the list, delete it (--force, no prompt), and confirm it's gone. This is the
// fixture primitive every later app read/write is exercised against.
func TestAppRegisterDeleteIntegration(t *testing.T) {
	baseURL := conductortest.Start(t)
	const appName = "d1-roundtrip"

	// appCmd builds a command targeting the harness's local org, with a fresh
	// output buffer.
	appCmd := func() (*cobra.Command, *bytes.Buffer) {
		cmd := newCmdWithGlobals()
		_ = cmd.Flags().Set("url", baseURL)
		_ = cmd.Flags().Set("org", "local")
		var out bytes.Buffer
		cmd.SetOut(&out)
		return cmd, &out
	}
	listContains := func() bool {
		cmd, out := appCmd()
		if err := runAppList(cmd, nil); err != nil {
			t.Fatalf("app list: %v", err)
		}
		return strings.Contains(out.String(), appName)
	}

	// register → present in the list.
	reg, _ := appCmd()
	if err := runAppRegister(reg, []string{appName}); err != nil {
		t.Fatalf("app register: %v", err)
	}
	if !listContains() {
		t.Fatalf("registered app %q is not in the list", appName)
	}

	// delete (--force) → gone from the list.
	del, _ := appCmd()
	del.Flags().Bool("force", false, "")
	_ = del.Flags().Set("force", "true")
	if err := runAppDelete(del, []string{appName}); err != nil {
		t.Fatalf("app delete: %v", err)
	}
	if listContains() {
		t.Errorf("app %q still present after delete", appName)
	}
}

// TestAppReadsBareIntegration covers the D2 reads that don't need a live
// executor, against a real no-auth Conductor: `app get` returns the registered
// app's details (exercising the detail renderer end to end), and `app executors`
// succeeds with an empty list (it's DB-backed, so a bare app is valid). The
// versions/metrics reads dispatch to a live executor, so they're covered by the
// executor-fixture test, not here.
func TestAppReadsBareIntegration(t *testing.T) {
	baseURL := conductortest.Start(t)
	ctx := context.Background()
	const appName = "d2-reads"

	seed, err := api.NewClientWithResponses(baseURL)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	reg, err := seed.RegisterAppWithResponse(ctx, "local", appName, api.RegisterAppJSONRequestBody{})
	if err != nil {
		t.Fatalf("register app: %v", err)
	}
	if reg.StatusCode() >= 300 {
		t.Fatalf("register app: HTTP %d: %s", reg.StatusCode(), reg.Body)
	}

	appCmd := func() (*cobra.Command, *bytes.Buffer) {
		cmd := newCmdWithGlobals()
		_ = cmd.Flags().Set("url", baseURL)
		_ = cmd.Flags().Set("org", "local")
		var out bytes.Buffer
		cmd.SetOut(&out)
		return cmd, &out
	}

	// app get → the registered app's details.
	get, out := appCmd()
	if err := runAppGet(get, []string{appName}); err != nil {
		t.Fatalf("app get: %v", err)
	}
	if got := out.String(); !strings.Contains(got, appName) {
		t.Errorf("app get missing the app name:\n%s", got)
	}

	// app executors → empty list, no error (DB-backed; no executor connected).
	execs, _ := appCmd()
	if err := runAppExecutors(execs, []string{appName}); err != nil {
		t.Errorf("app executors on a bare app should succeed (empty), got: %v", err)
	}
}

// saveBearerProfile writes a single current bearer profile at url, optionally
// scoped to org.
func saveBearerProfile(t *testing.T, url, org string) {
	t.Helper()
	f := &config.File{Current: "p", Profiles: map[string]config.Profile{
		"p": {Auth: config.AuthBearer, URL: url, Org: org},
	}}
	if err := config.Save(f); err != nil {
		t.Fatal(err)
	}
}
