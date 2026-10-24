package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// moveServer answers a move with 204 and records every request it sees, so a
// test can assert the move was the only one (no preview, no lookup).
func moveServer(t *testing.T, status int, contentType, reply string) (*httptest.Server, *[]string, *map[string]any) {
	t.Helper()
	var requests []string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &requests, &gotBody
}

type moveCmd struct {
	*cobra.Command
	out, errOut *bytes.Buffer
}

func newMoveCmd(t *testing.T, url, stdin string) moveCmd {
	t.Helper()
	cmd := newCmdWithGlobals()
	cmd.Flags().Bool("force", false, "")
	_ = cmd.Flags().Set("url", url)
	cmd.SetIn(strings.NewReader(stdin))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	return moveCmd{cmd, &out, &errOut}
}

const movePath = "POST /v2/orgs/local/apps/myapp/move"

// assertMoveWarning checks the general access/history/reconnect warning went to
// stderr, naming the destination.
func assertMoveWarning(t *testing.T, stderr string) {
	t.Helper()
	for _, want := range []string{
		`workspace "prod"`,
		"API keys scoped to its current workspace stop covering it",
		"may fail to reconnect",
		"API keys scoped to this application keep their access",
		"even if their creator's role no longer covers it",
		"all of the app's existing history",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("move warning missing %q:\n%s", want, stderr)
		}
	}
}

func TestRunAppMoveConfirmYes(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, true)
	srv, requests, body := moveServer(t, http.StatusNoContent, "", "")

	cmd := newMoveCmd(t, srv.URL, "y\n")
	if err := runAppMove(cmd.Command, []string{"myapp", "prod"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*requests, ", "); got != movePath {
		t.Errorf("requests = %q, want only %q", got, movePath)
	}
	if (*body)["workspace"] != "prod" {
		t.Errorf("move body = %v, want workspace prod", *body)
	}
	assertMoveWarning(t, cmd.errOut.String())
	if !strings.Contains(cmd.errOut.String(), `Move app "myapp" to workspace "prod"? [y/N]`) {
		t.Errorf("no prompt on stderr:\n%s", cmd.errOut.String())
	}
	// stdout carries only the result, so it stays usable from a script.
	if got := cmd.out.String(); got != "moved app \"myapp\" to workspace \"prod\"\n" {
		t.Errorf("unexpected move output: %q", got)
	}
}

func TestRunAppMoveConfirmNo(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, true)
	srv, requests, _ := moveServer(t, http.StatusNoContent, "", "")

	cmd := newMoveCmd(t, srv.URL, "n\n")
	if err := runAppMove(cmd.Command, []string{"myapp", "prod"}); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 0 {
		t.Errorf("declining the prompt still sent %v", *requests)
	}
	assertMoveWarning(t, cmd.errOut.String())
	if !strings.Contains(cmd.errOut.String(), "aborted") {
		t.Errorf("expected an abort notice, got %q", cmd.errOut.String())
	}
	if cmd.out.Len() != 0 {
		t.Errorf("a cancelled move printed to stdout: %q", cmd.out.String())
	}
}

// Without a terminal nobody can acknowledge the warning, so the move is refused
// unless --force acknowledges it up front.
func TestRunAppMoveNonInteractiveRequiresForce(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, false)
	srv, requests, _ := moveServer(t, http.StatusNoContent, "", "")

	cmd := newMoveCmd(t, srv.URL, "")
	err := runAppMove(cmd.Command, []string{"myapp", "prod"})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want a refusal mentioning --force, got %v", err)
	}
	if len(*requests) != 0 {
		t.Errorf("a refused move still sent %v", *requests)
	}
	assertMoveWarning(t, cmd.errOut.String())
}

func TestRunAppMoveForce(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, false)
	srv, requests, _ := moveServer(t, http.StatusNoContent, "", "")

	cmd := newMoveCmd(t, srv.URL, "")
	_ = cmd.Flags().Set("force", "true")
	if err := runAppMove(cmd.Command, []string{"myapp", "prod"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*requests, ", "); got != movePath {
		t.Errorf("requests = %q, want only %q", got, movePath)
	}
	// --force acknowledges the warning; it still lands in the script's logs.
	assertMoveWarning(t, cmd.errOut.String())
	if strings.Contains(cmd.errOut.String(), "[y/N]") {
		t.Errorf("--force still prompted:\n%s", cmd.errOut.String())
	}
}

func TestRunAppMoveError(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, false)
	srv, _, _ := moveServer(t, http.StatusConflict, "application/problem+json",
		`{"title":"Conflict","detail":"[2026] DBOS Cloud application cannot be moved","status":409}`)

	cmd := newMoveCmd(t, srv.URL, "")
	_ = cmd.Flags().Set("force", "true")
	err := runAppMove(cmd.Command, []string{"myapp", "prod"})
	if err == nil || !strings.Contains(err.Error(), "DBOS Cloud application cannot be moved") {
		t.Errorf("want Conductor's refusal, got %v", err)
	}
	if cmd.out.Len() != 0 {
		t.Errorf("a refused move printed to stdout: %q", cmd.out.String())
	}
}
