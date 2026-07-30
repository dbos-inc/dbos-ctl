//go:build integration

package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "github.com/dbos-inc/dbos-transact-golang/dbos/driver/sqlite"

	"github.com/dbos-inc/dbos-cli/internal/conductortest"
)

// spikeWorkflow is a trivial registered workflow so the executor has something
// to run (needed later for the metrics read).
func spikeWorkflow(_ dbos.Context, in string) (string, error) { return in, nil }

// startExecutor launches an in-process Go DBOS app that connects to the harness
// conductor as an executor for appName, backed by a temp-file SQLite system DB
// (no Postgres needed). The connection happens in the background after Launch,
// so callers poll the CLI reads until the executor appears. Shutdown is
// registered as a cleanup.
func startExecutor(t *testing.T, conductorHTTPURL, apiKey, appName string) dbos.Context {
	t.Helper()
	// The SDK appends /websocket/<app>/<key>; against a local conductor the base
	// is a bare ws://host:port (the /conductor/v1alpha1 prefix is cloud-only).
	wsURL := "ws://" + strings.TrimPrefix(conductorHTTPURL, "http://")

	dctx, err := dbos.NewContext(context.Background(), dbos.Config{
		AppName:         appName,
		DatabaseURL:     "sqlite:" + filepath.Join(t.TempDir(), "sys.db"),
		ConductorURL:    wsURL,
		ConductorAPIKey: apiKey,
	})
	if err != nil {
		t.Fatalf("new dbos context: %v", err)
	}
	dbos.RegisterWorkflow(dctx, spikeWorkflow)
	if err := dbos.Launch(dctx); err != nil {
		t.Fatalf("launch dbos app: %v", err)
	}
	t.Cleanup(func() { _ = dbos.Shutdown(dctx, 5*time.Second) })
	return dctx
}

// pollForRow polls an app read (built on a fresh org-scoped command each try)
// until its table has at least one data row, or the timeout passes. It returns
// the last output and whether a row appeared.
func pollForRow(t *testing.T, baseURL string, run func(*cobra.Command) error, timeout time.Duration) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for {
		cmd := newCmdWithGlobals()
		_ = cmd.Flags().Set("url", baseURL)
		_ = cmd.Flags().Set("org", "local")
		out := &strings.Builder{}
		cmd.SetOut(out)
		if err := run(cmd); err != nil {
			t.Fatalf("read: %v", err)
		}
		last = out.String()
		// An empty table is just the header; a data row means populated.
		if strings.Count(strings.TrimSpace(last), "\n") >= 1 {
			return last, true
		}
		if time.Now().After(deadline) {
			return last, false
		}
		time.Sleep(time.Second)
	}
}

// TestAppReadsExecutorIntegration is the D4 payload: it stands up a Go Transact
// executor against the no-auth harness (the spike proved this works), runs a
// workflow, and asserts the three live-executor reads return populated data
// through the CLI — `app executors` (the connected executor), `app versions`
// (dispatched to the live executor), and `app metrics` (collected after the
// workflow ran, with a 1s collection period so the test doesn't wait 5 minutes).
func TestAppReadsExecutorIntegration(t *testing.T) {
	// Short metrics window so the leader's collection loop runs every second.
	baseURL := conductortest.StartWithEnv(t, map[string]string{"DBOS__METRICS_COLLECTION_PERIOD": "1"})
	const appName = "d4-app"

	seed, err := newSeedClient(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	registerAppOrFail(t, seed, "local", appName)
	apiKey := mintKeyOrFail(t, seed, "local", appName+"-key")

	dctx := startExecutor(t, baseURL, apiKey, appName)

	// Run a workflow so there's execution history for the metrics read.
	h, err := dbos.RunWorkflow(dctx, spikeWorkflow, "hi")
	if err != nil {
		t.Fatalf("run workflow: %v", err)
	}
	if _, err := h.GetResult(); err != nil {
		t.Fatalf("workflow result: %v", err)
	}

	// executors — DB-backed, appears as soon as the executor connects.
	if out, ok := pollForRow(t, baseURL, func(c *cobra.Command) error {
		return runAppExecutors(c, []string{appName})
	}, 45*time.Second); !ok {
		t.Fatalf("app executors never populated:\n%s", out)
	} else if !strings.Contains(out, "HEALTHY") {
		t.Errorf("expected a HEALTHY executor:\n%s", out)
	}

	// versions — dispatched live to the healthy executor.
	if out, ok := pollForRow(t, baseURL, func(c *cobra.Command) error {
		return runAppVersions(c, []string{appName})
	}, 30*time.Second); !ok {
		t.Fatalf("app versions never populated:\n%s", out)
	}

	// metrics — collected by the leader (1s period) after the workflow ran.
	if out, ok := pollForRow(t, baseURL, func(c *cobra.Command) error {
		c.Flags().Duration("since", time.Hour, "") // window must span the metric bucket
		return runAppMetrics(c, []string{appName})
	}, 30*time.Second); !ok {
		t.Fatalf("app metrics never populated:\n%s", out)
	}
}
