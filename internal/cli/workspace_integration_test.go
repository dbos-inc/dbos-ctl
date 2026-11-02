//go:build integration

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/api"
	"github.com/dbos-inc/dbos-ctl/internal/conductortest"
)

// TestWorkspaceCommandsIntegration drives every `workspace` command, and each
// delete refusal a self-hosted Conductor can produce, against a real no-auth
// Conductor. The Workspace Limit is enforced only on DBOS-managed Conductor, so
// its refusal is covered by the unit tests instead.
func TestWorkspaceCommandsIntegration(t *testing.T) {
	baseURL := conductortest.Start(t)
	ctx := context.Background()

	seed, err := newSeedClient(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	wsCmd := func() (*cobra.Command, *bytes.Buffer) {
		cmd, out := appReadCmd(t, baseURL)
		_ = cmd.Flags().Set("org", "local")
		return cmd, out
	}
	list := func() string {
		cmd, out := wsCmd()
		if err := runWorkspaceList(cmd, nil); err != nil {
			t.Fatalf("workspace list: %v", err)
		}
		return out.String()
	}
	run := func(desc string, fn func(*cobra.Command, []string) error, args ...string) {
		t.Helper()
		cmd, _ := wsCmd()
		if err := fn(cmd, args); err != nil {
			t.Fatalf("%s: %v", desc, err)
		}
	}
	// refused runs `workspace delete` and returns its error, which must be one.
	refused := func(name string) string {
		t.Helper()
		cmd, out := wsCmd()
		err := runWorkspaceDelete(cmd, []string{name})
		if err == nil {
			t.Fatalf("workspace delete %q succeeded, want a refusal", name)
		}
		if out.Len() != 0 {
			t.Errorf("a refused delete printed to stdout: %q", out.String())
		}
		return err.Error()
	}

	// A fresh org has exactly `default`.
	if got := list(); !strings.Contains(got, "default") {
		t.Fatalf("workspace list missing default:\n%s", got)
	}

	// Deleting the only workspace is refused.
	if msg := refused("default"); !strings.Contains(msg, "last workspace") {
		t.Errorf("last-workspace refusal not explained: %s", msg)
	}

	// create, then rename: list follows.
	run("workspace create", runWorkspaceCreate, "staging")
	run("workspace rename", runWorkspaceRename, "staging", "stage")
	if got := list(); !strings.Contains(got, "stage") || strings.Contains(got, "staging") {
		t.Fatalf("list does not reflect create + rename:\n%s", got)
	}

	// A workspace holding an application is refused, saying to move it out.
	// The org now has two workspaces, so registration has to name one.
	target := "default"
	reg, err := seed.RegisterAppWithResponse(ctx, "local", "ws-app", api.RegisterAppJSONRequestBody{Workspace: &target})
	if err != nil {
		t.Fatalf("register app: %v", err)
	}
	if reg.StatusCode() >= 300 {
		t.Fatalf("register app: HTTP %d: %s", reg.StatusCode(), reg.Body)
	}
	if msg := refused("default"); !strings.Contains(msg, "workspace not empty") ||
		!strings.Contains(msg, "move each to another workspace") {
		t.Errorf("not-empty refusal does not say to move the applications out: %s", msg)
	}

	// An empty workspace deletes even when an API key is scoped to it: the
	// key's binding there cascades.
	scope, perms := []string{"stage"}, []string{"websocket.connect"}
	tok, err := seed.CreateTokenWithResponse(ctx, "local", "stage-key",
		api.CreateTokenJSONRequestBody{Workspaces: &scope, Permissions: &perms})
	if err != nil {
		t.Fatalf("create workspace-scoped key: %v", err)
	}
	if tok.JSON201 == nil {
		t.Fatalf("create workspace-scoped key: HTTP %d: %s", tok.StatusCode(), tok.Body)
	}
	cmd, out := wsCmd()
	if err := runWorkspaceDelete(cmd, []string{"stage"}); err != nil {
		t.Fatalf("workspace delete: %v", err)
	}
	if got := out.String(); got != "deleted workspace \"stage\"\n" {
		t.Errorf("unexpected delete output: %q", got)
	}
	if got := list(); strings.Contains(got, "stage") {
		t.Errorf("deleted workspace still listed:\n%s", got)
	}
}
