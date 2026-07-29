package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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
