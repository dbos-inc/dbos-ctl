package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/config"
	"github.com/dbos-inc/dbos-cli/internal/creds"
)

// newCmdWithGlobals returns a throwaway command carrying the same global flags
// rootCmd declares, so a RunE can be exercised in isolation.
func newCmdWithGlobals() *cobra.Command {
	c := &cobra.Command{}
	c.Flags().String("url", "", "")
	c.Flags().String("org", "", "")
	c.Flags().String("app", "", "")
	c.Flags().String("output", "table", "")
	c.Flags().String("profile", "", "")
	c.SetContext(context.Background())
	return c
}

// isolateConfig points config at an empty temp dir and clears DBOS_* env, so a
// test resolves settings from flags alone, independent of any real user config.
func isolateConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DBOS_PROFILE", "")
	t.Setenv("DBOS_URL", "")
	t.Setenv("DBOS_ORG", "")
	t.Setenv("DBOS_APP", "")
	t.Setenv("DBOS_TOKEN", "")
}

const oneAppJSON = `[{"name":"myapp","status":"ACTIVE","language":"python","id":"a1","orgId":"local","dbosCloud":false,"executorTimeoutSecs":0,"privateMode":false}]`

func appServer(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/orgs/local/apps" {
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunAppListJSON(t *testing.T) {
	isolateConfig(t)
	srv := appServer(t, http.StatusOK, "application/json", oneAppJSON)

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", srv.URL)
	_ = cmd.Flags().Set("output", "json")
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runAppList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `"name": "myapp"`) {
		t.Errorf("json output missing app:\n%s", got)
	}
}

func TestRunAppListTable(t *testing.T) {
	isolateConfig(t)
	srv := appServer(t, http.StatusOK, "application/json", oneAppJSON)

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", srv.URL) // output defaults to table
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runAppList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "STATUS", "LANGUAGE", "myapp", "ACTIVE", "python"} {
		if !strings.Contains(got, want) {
			t.Errorf("table output missing %q:\n%s", want, got)
		}
	}
}

func TestEffectiveOrg(t *testing.T) {
	ctx := context.Background()
	// Explicit org wins — no client call (nil client proves it).
	if got, err := effectiveOrg(ctx, nil, config.Settings{Auth: config.AuthBearer, Org: "myorg"}); err != nil || got != "myorg" {
		t.Errorf("explicit org: got %q, %v; want myorg", got, err)
	}
	// A no-auth target is "local" without a client call.
	if got, err := effectiveOrg(ctx, nil, config.Settings{Auth: config.AuthNone}); err != nil || got != "local" {
		t.Errorf("no-auth org: got %q, %v; want local", got, err)
	}
}

// TestRunAppListDerivesOrgLive proves the fallback: a bearer target with an
// ad-hoc $DBOS_TOKEN (no stored login) and no configured org derives the org
// live from getCurrentUser rather than sending an empty org segment (404).
func TestRunAppListDerivesOrgLive(t *testing.T) {
	isolateConfig(t)
	t.Setenv("DBOS_TOKEN", "jwt")
	const org = "acme"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/users/me":
			_, _ = w.Write([]byte(`{"name":"alice","email":"a@x","orgName":"` + org + `","orgId":"o1","createdAt":"2026-01-01T00:00:00Z","subscriptionPlan":"free"}`))
		case "/v2/orgs/" + org + "/apps":
			_, _ = w.Write([]byte(oneAppJSON))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	f := &config.File{Current: "p", Profiles: map[string]config.Profile{
		"p": {Auth: config.AuthBearer, URL: srv.URL}, // bearer, no org
	}}
	if err := config.Save(f); err != nil {
		t.Fatal(err)
	}

	cmd := newCmdWithGlobals()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runAppList(cmd, nil); err != nil {
		t.Fatalf("app list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "myapp") {
		t.Errorf("app list did not derive org from identity:\n%s", got)
	}
}

// TestRunAppListUsesStoredOrg proves the common path: with an org captured at
// login, app list uses it directly and makes no getCurrentUser request.
func TestRunAppListUsesStoredOrg(t *testing.T) {
	isolateConfig(t)
	const org = "acme"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/users/me" {
			t.Errorf("getCurrentUser should not be called when the org is stored")
		}
		if r.URL.Path != "/v2/orgs/"+org+"/apps" {
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(oneAppJSON))
	}))
	t.Cleanup(srv.Close)

	f := &config.File{Current: "p", Profiles: map[string]config.Profile{
		"p": {Auth: config.AuthBearer, URL: srv.URL},
	}}
	if err := config.Save(f); err != nil {
		t.Fatal(err)
	}
	store, err := creds.NewFileStore()
	if err != nil {
		t.Fatal(err)
	}
	// A stored login that captured the org (as `dbos login` now does).
	if err := store.Save("p", &creds.Creds{Token: "jwt", Organization: org}); err != nil {
		t.Fatal(err)
	}

	cmd := newCmdWithGlobals()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runAppList(cmd, nil); err != nil {
		t.Fatalf("app list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "myapp") {
		t.Errorf("app list did not use the stored org:\n%s", got)
	}
}

func TestRunAppListError(t *testing.T) {
	isolateConfig(t)
	srv := appServer(t, http.StatusForbidden, "application/problem+json",
		`{"title":"Forbidden","detail":"past limit","status":403}`)

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", srv.URL)
	cmd.SetOut(&bytes.Buffer{})

	err := runAppList(cmd, nil)
	if err == nil {
		t.Fatal("expected an error for a 403 response, got nil")
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("error missing problem title: %v", err)
	}
}
