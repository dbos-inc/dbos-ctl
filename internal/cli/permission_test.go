package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/config"
)

// bearerPermServer serves the OAuth-only permissions route for a bearer profile.
func bearerPermServer(t *testing.T, org, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/orgs/"+org+"/permissions" {
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// bearerPermCmd sets up a current bearer profile with an explicit org (so
// effectiveOrg resolves without a getCurrentUser call) and an ad-hoc token.
func bearerPermCmd(t *testing.T, url string) *cobra.Command {
	t.Helper()
	t.Setenv("DBOS_TOKEN", "dbos_x")
	f := &config.File{Current: "p", Profiles: map[string]config.Profile{
		"p": {Auth: config.AuthBearer, URL: url, Org: "acme"},
	}}
	if err := config.Save(f); err != nil {
		t.Fatal(err)
	}
	return newCmdWithGlobals()
}

func TestRunPermissionList(t *testing.T) {
	isolateConfig(t)
	srv := bearerPermServer(t, "acme", `["application.read","application.write","websocket.connect"]`)

	cmd := bearerPermCmd(t, srv.URL)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runPermissionList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"PERMISSION", "application.read", "websocket.connect"} {
		if !strings.Contains(got, want) {
			t.Errorf("permission list missing %q:\n%s", want, got)
		}
	}
}

func TestRunPermissionListJSON(t *testing.T) {
	isolateConfig(t)
	srv := bearerPermServer(t, "acme", `["application.read"]`)

	cmd := bearerPermCmd(t, srv.URL)
	_ = cmd.Flags().Set("output", "json")
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runPermissionList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); !strings.HasPrefix(got, "[") || !strings.Contains(got, `"application.read"`) {
		t.Errorf("json output not the raw array:\n%s", got)
	}
}

// TestRunPermissionListNoAuth: a no-auth profile can't reach the OAuth-gated
// route, so the command fails with a mode-aware message before sending.
func TestRunPermissionListNoAuth(t *testing.T) {
	isolateConfig(t)
	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", "http://localhost:8090") // no token → auth none
	cmd.SetOut(&bytes.Buffer{})

	err := runPermissionList(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "requires an OAuth") {
		t.Errorf("want a mode-aware error, got %v", err)
	}
}
