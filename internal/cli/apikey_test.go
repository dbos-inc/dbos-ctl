package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunAPIKeyList(t *testing.T) {
	isolateConfig(t)
	const body = `[{"tokenName":"ci","appIds":["app-1"],"permissions":["application.read"],"createdAt":"2026-07-30T10:00:00Z"},{"tokenName":"admin","appIds":[],"permissions":["application.write"],"createdAt":"2026-07-30T11:00:00Z"}]`
	srv, _ := appReadServer(t, "/v2/orgs/local/tokens", body)

	cmd, out := appReadCmd(t, srv.URL)
	if err := runAPIKeyList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "APPS", "PERMISSIONS", "ci", "app-1", "application.read", "admin"} {
		if !strings.Contains(got, want) {
			t.Errorf("api-key list missing %q:\n%s", want, got)
		}
	}
	// An unscoped key (no appIds) reads as "(all)".
	if !strings.Contains(got, "(all)") {
		t.Errorf("api-key list should show (all) for an unscoped key:\n%s", got)
	}
}

// newAPIKeyCreateCmd builds a create command with the request flags plus the
// repeatable --app/--permission scope flags (StringSlice, not the operational
// string --app).
func newAPIKeyCreateCmd(t *testing.T, url string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	c := &cobra.Command{}
	c.Flags().String("url", "", "")
	c.Flags().String("org", "", "")
	c.Flags().String("output", "table", "")
	c.Flags().String("profile", "", "")
	c.Flags().StringSlice("app", nil, "")
	c.Flags().StringSlice("permission", nil, "")
	c.SetContext(context.Background())
	_ = c.Flags().Set("url", url)
	var out, errBuf bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errBuf)
	return c, &out, &errBuf
}

// createTokenServer captures the POST body so a test can assert the scope, and
// replies with a minted token.
func createTokenServer(t *testing.T, wantPath, token string) (*httptest.Server, *map[string]any) {
	t.Helper()
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + token + `","tokenName":"ci"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotBody
}

func TestRunAPIKeyCreate(t *testing.T) {
	isolateConfig(t)
	srv, body := createTokenServer(t, "/v2/orgs/local/tokens/ci", "dbos_secret123")

	cmd, out, errBuf := newAPIKeyCreateCmd(t, srv.URL)
	_ = cmd.Flags().Set("app", "myapp")
	_ = cmd.Flags().Set("permission", "application.read")
	if err := runAPIKeyCreate(cmd, []string{"ci"}); err != nil {
		t.Fatal(err)
	}
	// The bare secret goes to stdout (capturable), nothing else.
	if got := strings.TrimSpace(out.String()); got != "dbos_secret123" {
		t.Errorf("stdout = %q, want the bare token", got)
	}
	// The shown-once warning goes to stderr.
	if !strings.Contains(errBuf.String(), "not shown again") {
		t.Errorf("stderr missing the shown-once warning: %q", errBuf.String())
	}
	// The scope was sent in the request body.
	if apps, _ := (*body)["appNames"].([]any); len(apps) != 1 || apps[0] != "myapp" {
		t.Errorf("appNames not sent: %v", (*body)["appNames"])
	}
	if perms, _ := (*body)["permissions"].([]any); len(perms) != 1 || perms[0] != "application.read" {
		t.Errorf("permissions not sent: %v", (*body)["permissions"])
	}
}

func TestRunAPIKeyCreateJSON(t *testing.T) {
	isolateConfig(t)
	srv, _ := createTokenServer(t, "/v2/orgs/local/tokens/ci", "dbos_secret123")

	cmd, out, _ := newAPIKeyCreateCmd(t, srv.URL)
	_ = cmd.Flags().Set("output", "json")
	if err := runAPIKeyCreate(cmd, []string{"ci"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `"token": "dbos_secret123"`) || !strings.Contains(got, `"tokenName": "ci"`) {
		t.Errorf("json output not the raw TokenCreated shape:\n%s", got)
	}
}

func TestRunAPIKeyCreateUnscoped(t *testing.T) {
	isolateConfig(t)
	srv, body := createTokenServer(t, "/v2/orgs/local/tokens/ci", "dbos_x")

	cmd, _, _ := newAPIKeyCreateCmd(t, srv.URL)
	if err := runAPIKeyCreate(cmd, []string{"ci"}); err != nil {
		t.Fatal(err)
	}
	// With no --app/--permission, the body omits them (nil, not empty arrays).
	if _, ok := (*body)["appNames"]; ok {
		t.Errorf("unscoped create should omit appNames, got %v", (*body)["appNames"])
	}
	if _, ok := (*body)["permissions"]; ok {
		t.Errorf("unscoped create should omit permissions, got %v", (*body)["permissions"])
	}
}

func TestRunAPIKeyDelete(t *testing.T) {
	isolateConfig(t)
	var gotMethod string
	del := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/orgs/local/tokens/ci" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(del.Close)

	cmd, out := appReadCmd(t, del.URL)
	if err := runAPIKeyDelete(cmd, []string{"ci"}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("delete used %s, want DELETE", gotMethod)
	}
	if got := out.String(); !strings.Contains(got, `deleted API key "ci"`) {
		t.Errorf("unexpected delete output: %q", got)
	}
}

func TestRunAPIKeyDeleteError(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"Not Found","detail":"no such key","status":404}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := appReadCmd(t, srv.URL)
	err := runAPIKeyDelete(cmd, []string{"ghost"})
	if err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("want a Not Found error, got %v", err)
	}
}
