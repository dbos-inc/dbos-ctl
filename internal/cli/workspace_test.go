package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dbos-inc/dbos-ctl/internal/config"
)

func TestRunWorkspaceList(t *testing.T) {
	isolateConfig(t)
	const body = `[{"name":"default","createdAt":"2026-10-20T10:00:00Z"},{"name":"staging","createdAt":"2026-10-22T09:30:00Z"}]`
	srv, _ := appReadServer(t, "/v2/orgs/local/workspaces", body)

	cmd, out := appReadCmd(t, srv.URL)
	if err := runWorkspaceList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "CREATED", "default", "2026-10-20T10:00:00Z", "staging", "2026-10-22T09:30:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("workspace list missing %q:\n%s", want, got)
		}
	}
}

// The org follows the usual flag > env > profile precedence.
func TestRunWorkspaceListOrgResolution(t *testing.T) {
	const body = `[]`
	saveProfile := func(t *testing.T, url string) {
		t.Helper()
		t.Setenv("DBOS_TOKEN", "dbos_x")
		f := &config.File{Current: "p", Profiles: map[string]config.Profile{
			"p": {Auth: config.AuthBearer, URL: url, Org: "fromprofile"},
		}}
		if err := config.Save(f); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("profile", func(t *testing.T) {
		isolateConfig(t)
		srv, _ := appReadServer(t, "/v2/orgs/fromprofile/workspaces", body)
		saveProfile(t, srv.URL)
		cmd := newCmdWithGlobals()
		cmd.SetOut(&bytes.Buffer{})
		if err := runWorkspaceList(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("env over profile", func(t *testing.T) {
		isolateConfig(t)
		srv, _ := appReadServer(t, "/v2/orgs/fromenv/workspaces", body)
		saveProfile(t, srv.URL)
		t.Setenv("DBOS_ORG", "fromenv")
		cmd := newCmdWithGlobals()
		cmd.SetOut(&bytes.Buffer{})
		if err := runWorkspaceList(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("flag over env", func(t *testing.T) {
		isolateConfig(t)
		srv, _ := appReadServer(t, "/v2/orgs/fromflag/workspaces", body)
		saveProfile(t, srv.URL)
		t.Setenv("DBOS_ORG", "fromenv")
		cmd := newCmdWithGlobals()
		_ = cmd.Flags().Set("org", "fromflag")
		cmd.SetOut(&bytes.Buffer{})
		if err := runWorkspaceList(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
}

// workspaceWriteServer expects one method+path, captures the JSON body, and
// replies with status and body.
func workspaceWriteServer(t *testing.T, method, path string, status int, reply string) (*httptest.Server, *map[string]any) {
	t.Helper()
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method || r.URL.Path != path {
			t.Errorf("unexpected %s %s, want %s %s", r.Method, r.URL.Path, method, path)
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		if reply != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotBody
}

func TestRunWorkspaceCreate(t *testing.T) {
	isolateConfig(t)
	srv, body := workspaceWriteServer(t, http.MethodPost, "/v2/orgs/local/workspaces",
		http.StatusCreated, `{"name":"staging","createdAt":"2026-10-23T10:00:00Z"}`)

	cmd, out := appReadCmd(t, srv.URL)
	if err := runWorkspaceCreate(cmd, []string{"staging"}); err != nil {
		t.Fatal(err)
	}
	if (*body)["name"] != "staging" {
		t.Errorf("create body = %v, want name staging", *body)
	}
	if got := out.String(); got != "created workspace \"staging\"\n" {
		t.Errorf("unexpected create output: %q", got)
	}
}

// problemServer answers every request with a Conductor problem+json refusal.
func problemServer(t *testing.T, status int, title, detail string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"title": title, "detail": detail, "status": status})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Only DBOS-managed Conductor enforces the Workspace Limit, so the integration
// harness (self-hosted) can't reach this refusal.
func TestRunWorkspaceCreateLimitReached(t *testing.T) {
	isolateConfig(t)
	srv := problemServer(t, http.StatusConflict, "Conflict",
		"[2024] workspace limit reached: the organization may have at most 2 workspaces; upgrade to DBOS Teams or Enterprise for more")

	cmd, out := appReadCmd(t, srv.URL)
	err := runWorkspaceCreate(cmd, []string{"third"})
	if err == nil {
		t.Fatal("want a limit-reached error")
	}
	for _, want := range []string{"workspace limit reached", "at most 2 workspaces", "upgrade to DBOS Teams or Enterprise"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	if code := exitCodeFor(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Errorf("a refused create printed to stdout: %q", out.String())
	}
}

func TestRunWorkspaceRename(t *testing.T) {
	isolateConfig(t)
	srv, body := workspaceWriteServer(t, http.MethodPatch, "/v2/orgs/local/workspaces/default",
		http.StatusOK, `{"name":"production","createdAt":"2026-10-20T10:00:00Z"}`)

	cmd, out := appReadCmd(t, srv.URL)
	if err := runWorkspaceRename(cmd, []string{"default", "production"}); err != nil {
		t.Fatal(err)
	}
	if (*body)["name"] != "production" {
		t.Errorf("rename body = %v, want name production", *body)
	}
	if got := out.String(); got != "renamed workspace \"default\" to \"production\"\n" {
		t.Errorf("unexpected rename output: %q", got)
	}
}

func TestRunWorkspaceDelete(t *testing.T) {
	isolateConfig(t)
	srv, _ := workspaceWriteServer(t, http.MethodDelete, "/v2/orgs/local/workspaces/staging", http.StatusNoContent, "")

	cmd, out := appReadCmd(t, srv.URL)
	if err := runWorkspaceDelete(cmd, []string{"staging"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "deleted workspace \"staging\"\n" {
		t.Errorf("unexpected delete output: %q", got)
	}
}

// Each delete refusal surfaces Conductor's explanation, which says what to do.
func TestRunWorkspaceDeleteRefusals(t *testing.T) {
	for name, detail := range map[string]string{
		"not empty":          `[2028] workspace not empty: "staging" holds applications; move each to another workspace before deleting it`,
		"last workspace":     `[2029] last workspace: "staging" is the organization's only workspace; create another before deleting it`,
		"referenced by keys": `[2031] workspace referenced by API keys: "staging" is the scope of API key(s) ci; delete those keys before deleting it`,
	} {
		t.Run(name, func(t *testing.T) {
			isolateConfig(t)
			srv := problemServer(t, http.StatusConflict, "Conflict", detail)

			cmd, out := appReadCmd(t, srv.URL)
			err := runWorkspaceDelete(cmd, []string{"staging"})
			if err == nil {
				t.Fatal("want a refusal")
			}
			if !strings.Contains(err.Error(), detail) {
				t.Errorf("error lost Conductor's explanation: %v", err)
			}
			if code := exitCodeFor(err); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if out.Len() != 0 {
				t.Errorf("a refused delete printed to stdout: %q", out.String())
			}
		})
	}
}
