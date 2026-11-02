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
	const body = `[` +
		`{"tokenName":"ci","appIds":["app-1","app-2"],"workspaces":[],"permissions":["application.read"],"createdAt":"2026-07-30T10:00:00Z"},` +
		`{"tokenName":"admin","appIds":[],"workspaces":[],"permissions":["application.write"],"createdAt":"2026-07-30T11:00:00Z"},` +
		`{"tokenName":"stage","appIds":[],"workspaces":["prod","staging"],"permissions":["websocket.connect"],"createdAt":"2026-07-30T12:00:00Z"}]`
	srv, _ := appReadServer(t, "/v2/orgs/local/tokens", body)

	cmd, out := appReadCmd(t, srv.URL)
	if err := runAPIKeyList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "SCOPE", "PERMISSIONS", "application.read"} {
		if !strings.Contains(got, want) {
			t.Errorf("api-key list missing %q:\n%s", want, got)
		}
	}
	// Each key's row names its scope: apps (by id), all apps, or workspaces.
	for key, scope := range map[string]string{
		"ci":    "app ids: app-1, app-2",
		"admin": "all apps",
		"stage": "workspaces: prod, staging",
	} {
		if !rowContains(got, key, scope) {
			t.Errorf("row %q should show scope %q:\n%s", key, scope, got)
		}
	}
}

// rowContains reports whether the table row starting with key contains want.
func rowContains(table, key, want string) bool {
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, key+" ") && strings.Contains(line, want) {
			return true
		}
	}
	return false
}

// newAPIKeyCreateCmd builds a create command with the request flags plus the
// repeatable --app/--workspace/--permission scope flags (arrays and a slice, not
// the operational string --app).
func newAPIKeyCreateCmd(t *testing.T, url string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	c := &cobra.Command{}
	c.Flags().String("url", "", "")
	c.Flags().String("org", "", "")
	c.Flags().String("output", "table", "")
	c.Flags().String("profile", "", "")
	c.Flags().StringArray("app", nil, "")
	c.Flags().StringArray("workspace", nil, "")
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

func TestRunAPIKeyCreateWorkspaceScoped(t *testing.T) {
	isolateConfig(t)
	srv, body := createTokenServer(t, "/v2/orgs/local/tokens/ci", "dbos_secret123")

	cmd, _, _ := newAPIKeyCreateCmd(t, srv.URL)
	_ = cmd.Flags().Set("workspace", "staging")
	_ = cmd.Flags().Set("workspace", "prod")
	_ = cmd.Flags().Set("permission", "websocket.connect")
	if err := runAPIKeyCreate(cmd, []string{"ci"}); err != nil {
		t.Fatal(err)
	}
	if ws, _ := (*body)["workspaces"].([]any); len(ws) != 2 || ws[0] != "staging" || ws[1] != "prod" {
		t.Errorf("workspaces = %v, want [staging prod]", (*body)["workspaces"])
	}
	if _, ok := (*body)["appNames"]; ok {
		t.Errorf("a workspace-scoped create should omit appNames, got %v", (*body)["appNames"])
	}
}

// unreachedServer fails the test on any request: the command must refuse first.
func unreachedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected %s %s: the command should refuse before sending", r.Method, r.URL.Path)
		http.Error(w, "unexpected request", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunAPIKeyCreateWorkspaceWithAppRefused(t *testing.T) {
	isolateConfig(t)
	cmd, out, _ := newAPIKeyCreateCmd(t, unreachedServer(t).URL)
	_ = cmd.Flags().Set("workspace", "staging")
	_ = cmd.Flags().Set("app", "myapp")
	err := runAPIKeyCreate(cmd, []string{"ci"})
	if err == nil || !strings.Contains(err.Error(), "--workspace cannot be combined with --app") {
		t.Fatalf("want a --workspace/--app conflict, got %v", err)
	}
	if code := exitCodeFor(err); code != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", code)
	}
	if out.Len() != 0 {
		t.Errorf("a refused create printed to stdout: %q", out.String())
	}
}

// An empty scope value is refused rather than dropped: dropping the only one
// would leave no scope, and Conductor reads that as every app.
func TestRunAPIKeyCreateEmptyScopeRefused(t *testing.T) {
	for _, tc := range []struct {
		flag   string
		values []string
	}{
		{"workspace", []string{""}},
		{"workspace", []string{"staging,"}},
		{"workspace", []string{" "}},
		{"workspace", []string{"staging", ""}},
		{"app", []string{""}},
		{"app", []string{"myapp,"}},
		{"app", []string{"myapp", ""}},
	} {
		t.Run(tc.flag+"="+strings.Join(tc.values, "|"), func(t *testing.T) {
			isolateConfig(t)
			cmd, _, _ := newAPIKeyCreateCmd(t, unreachedServer(t).URL)
			for _, v := range tc.values {
				_ = cmd.Flags().Set(tc.flag, v)
			}
			err := runAPIKeyCreate(cmd, []string{"ci"})
			if err == nil || !strings.Contains(err.Error(), "--"+tc.flag+" was given an empty name") {
				t.Fatalf("want an empty-name refusal, got %v", err)
			}
			if code := exitCodeFor(err); code != 2 {
				t.Errorf("exit code = %d, want 2 (usage)", code)
			}
		})
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

func TestRunAPIKeyRename(t *testing.T) {
	isolateConfig(t)
	var gotMethod string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/orgs/local/tokens/ci" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		gotMethod = r.Method
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	cmd, out := appReadCmd(t, srv.URL)
	if err := runAPIKeyRename(cmd, []string{"ci", "ci-prod"}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("rename used %s, want PATCH", gotMethod)
	}
	if body["newName"] != "ci-prod" {
		t.Errorf("rename body = %v, want newName ci-prod", body)
	}
	if got := out.String(); !strings.Contains(got, `renamed API key "ci" to "ci-prod"`) {
		t.Errorf("unexpected rename output: %q", got)
	}
}

func TestRunAPIKeyRenameError(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"title":"Conflict","detail":"token already exists","status":409}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := appReadCmd(t, srv.URL)
	err := runAPIKeyRename(cmd, []string{"ci", "taken"})
	if err == nil || !strings.Contains(err.Error(), "Conflict") {
		t.Errorf("want a Conflict error, got %v", err)
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
