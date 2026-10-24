//go:build integration

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/conductortest"
	"github.com/dbos-inc/dbos-ctl/internal/config"
)

// TestAppWorkspaceIntegration drives the workspace-aware app commands against a
// real no-auth Conductor: registering into a workspace from the flag, the
// environment and the profile; the "workspace required" refusal once the org has
// two; the list column and filter; `app get`; and `app move` through its
// refusal, cancellation, confirmation and --force paths.
func TestAppWorkspaceIntegration(t *testing.T) {
	isolateConfig(t)
	baseURL := conductortest.Start(t)

	// appCmd targets the harness through --url; the profile case below drops it.
	appCmd := func() (*cobra.Command, *bytes.Buffer) {
		return appReadCmd(t, baseURL)
	}
	register := func(cmd *cobra.Command, name string) {
		t.Helper()
		if err := runAppRegister(cmd, []string{name}); err != nil {
			t.Fatalf("app register %q: %v", name, err)
		}
	}
	list := func(workspace string) string {
		t.Helper()
		cmd, out := appCmd()
		if workspace != "" {
			_ = cmd.Flags().Set("workspace", workspace)
		}
		if err := runAppList(cmd, nil); err != nil {
			t.Fatalf("app list: %v", err)
		}
		return out.String()
	}
	// workspaceOf reads the WORKSPACE cell of an app's row in the full list.
	workspaceOf := func(app string) string {
		t.Helper()
		for _, line := range strings.Split(list(""), "\n") {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == app {
				return f[1]
			}
		}
		t.Fatalf("app %q not listed", app)
		return ""
	}

	// With one workspace, naming none registers into it.
	cmd, _ := appCmd()
	register(cmd, "ws-implicit")
	if got := workspaceOf("ws-implicit"); got != "default" {
		t.Errorf("ws-implicit is in %q, want default", got)
	}

	cmd, _ = appCmd()
	if err := runWorkspaceCreate(cmd, []string{"staging"}); err != nil {
		t.Fatalf("workspace create: %v", err)
	}

	// With two, naming none is refused, and the error says how to name one.
	cmd, _ = appCmd()
	err := runAppRegister(cmd, []string{"ws-unnamed"})
	if err == nil {
		t.Fatal("registering without a workspace in a two-workspace org succeeded")
	}
	for _, want := range []string{"workspace required", "more than one workspace", "-w/--workspace", "$DBOS_WORKSPACE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("workspace-required error missing %q: %v", want, err)
		}
	}

	// flag
	cmd, _ = appCmd()
	_ = cmd.Flags().Set("workspace", "staging")
	register(cmd, "ws-flag")

	// environment
	t.Setenv("DBOS_WORKSPACE", "staging")
	cmd, _ = appCmd()
	register(cmd, "ws-env")
	t.Setenv("DBOS_WORKSPACE", "")

	// profile, which here also carries the URL
	if err := config.Save(&config.File{Current: "p", Profiles: map[string]config.Profile{
		"p": {URL: baseURL, Workspace: "staging"},
	}}); err != nil {
		t.Fatal(err)
	}
	cmd = newCmdWithGlobals()
	cmd.SetOut(&bytes.Buffer{})
	register(cmd, "ws-profile")

	for _, app := range []string{"ws-flag", "ws-env", "ws-profile"} {
		if got := workspaceOf(app); got != "staging" {
			t.Errorf("%s is in %q, want staging", app, got)
		}
	}

	// The filter lists one workspace's apps only.
	staging := list("staging")
	for _, app := range []string{"ws-flag", "ws-env", "ws-profile"} {
		if !strings.Contains(staging, app) {
			t.Errorf("--workspace staging missing %s:\n%s", app, staging)
		}
	}
	if strings.Contains(staging, "ws-implicit") {
		t.Errorf("--workspace staging lists an app from default:\n%s", staging)
	}

	cmd, out := appCmd()
	if err := runAppGet(cmd, []string{"ws-flag"}); err != nil {
		t.Fatalf("app get: %v", err)
	}
	if !strings.Contains(out.String(), "staging") {
		t.Errorf("app get does not show the workspace:\n%s", out.String())
	}

	// move runs one `app move ws-implicit staging`, answering stdin if prompted.
	move := func(force bool, stdin string) (string, error) {
		t.Helper()
		cmd, out := appCmd()
		cmd.Flags().Bool("force", false, "")
		if force {
			_ = cmd.Flags().Set("force", "true")
		}
		cmd.SetIn(strings.NewReader(stdin))
		cmd.SetErr(&bytes.Buffer{})
		err := runAppMove(cmd, []string{"ws-implicit", "staging"})
		return out.String(), err
	}

	setInteractive(t, false)
	if _, err := move(false, ""); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("a non-interactive move without --force was not refused: %v", err)
	}
	if got := workspaceOf("ws-implicit"); got != "default" {
		t.Errorf("a refused move moved the app to %q", got)
	}

	setInteractive(t, true)
	if _, err := move(false, "n\n"); err != nil {
		t.Fatalf("cancelled move: %v", err)
	}
	if got := workspaceOf("ws-implicit"); got != "default" {
		t.Errorf("a cancelled move moved the app to %q", got)
	}

	stdout, err := move(false, "y\n")
	if err != nil {
		t.Fatalf("confirmed move: %v", err)
	}
	if stdout != "moved app \"ws-implicit\" to workspace \"staging\"\n" {
		t.Errorf("unexpected move output: %q", stdout)
	}
	if got := workspaceOf("ws-implicit"); got != "staging" {
		t.Errorf("after a confirmed move the app is in %q, want staging", got)
	}

	// --force acknowledges without a terminal, and moving back works the same.
	setInteractive(t, false)
	cmd, _ = appCmd()
	cmd.Flags().Bool("force", false, "")
	_ = cmd.Flags().Set("force", "true")
	cmd.SetErr(&bytes.Buffer{})
	if err := runAppMove(cmd, []string{"ws-implicit", "default"}); err != nil {
		t.Fatalf("forced move: %v", err)
	}
	if got := workspaceOf("ws-implicit"); got != "default" {
		t.Errorf("after a forced move the app is in %q, want default", got)
	}
}
