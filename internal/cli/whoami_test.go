package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dbos-inc/dbos-ctl/internal/config"
)

func TestRunWhoamiLocal(t *testing.T) {
	isolateConfig(t)
	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", "http://localhost:8090") // selfhosted, auth none
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runWhoami(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "name   local") || !strings.Contains(got, "org    local") {
		t.Errorf("selfhosted whoami should report local identity:\n%s", got)
	}
}

// userMe serves GET /v2/users/me with a UserProfile.
func userMe(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/users/me" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"alice","email":"a@example.com","orgName":"acme","orgId":"o1","createdAt":"2026-01-01T00:00:00Z","subscriptionPlan":"team"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func bearerProfileCmd(t *testing.T, url string) *bytes.Buffer {
	t.Helper()
	f := &config.File{Current: "p", Profiles: map[string]config.Profile{
		"p": {Auth: config.AuthBearer, URL: url},
	}}
	if err := config.Save(f); err != nil {
		t.Fatal(err)
	}
	return &bytes.Buffer{}
}

func TestRunWhoamiBearer(t *testing.T) {
	isolateConfig(t)
	t.Setenv("DBOS_TOKEN", "dbos_x")
	srv := userMe(t)
	out := bearerProfileCmd(t, srv.URL)

	cmd := newCmdWithGlobals()
	cmd.SetOut(out)
	if err := runWhoami(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"alice", "a@example.com", "acme", "team"} {
		if !strings.Contains(got, want) {
			t.Errorf("whoami output missing %q:\n%s", want, got)
		}
	}
}

func TestRunWhoamiBearerJSON(t *testing.T) {
	isolateConfig(t)
	t.Setenv("DBOS_TOKEN", "dbos_x")
	srv := userMe(t)
	out := bearerProfileCmd(t, srv.URL)

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("output", "json")
	cmd.SetOut(out)
	if err := runWhoami(cmd, nil); err != nil {
		t.Fatal(err)
	}
	// json is the raw API shape, so fields the table view drops (orgId) appear.
	if got := out.String(); !strings.Contains(got, `"orgId"`) {
		t.Errorf("json should be the raw UserProfile shape:\n%s", got)
	}
}
