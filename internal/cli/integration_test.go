//go:build integration

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/conductortest"
)

// TestAppListIntegration proves `dbos app list` end to end against a real
// Conductor: it seeds an app via the generated client, then lists it through
// the CLI command and asserts the row appears.
func TestAppListIntegration(t *testing.T) {
	baseURL := conductortest.Start(t)
	ctx := context.Background()

	c, err := api.NewClientWithResponses(baseURL)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	// A fresh no-auth conductor has the local org but no apps; seed one.
	const appName = "test-app"
	reg, err := c.RegisterAppWithResponse(ctx, "local", appName, api.RegisterAppJSONRequestBody{})
	if err != nil {
		t.Fatalf("register app: %v", err)
	}
	if reg.StatusCode() >= 300 {
		t.Fatalf("register app: HTTP %d: %s", reg.StatusCode(), reg.Body)
	}

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", baseURL)
	_ = cmd.Flags().Set("org", "local")
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runAppList(cmd, nil); err != nil {
		t.Fatalf("app list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, appName) {
		t.Errorf("app list output missing seeded app %q:\n%s", appName, got)
	}
}
