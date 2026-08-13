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

// spikeWorkflow is a trivial registered workflow that runs one step and sets one
// event, so the executor has something to run (metrics) and `workflow steps` /
// `workflow events` have a row to return.
func spikeWorkflow(ctx dbos.Context, in string) (string, error) {
	if err := dbos.SetEvent(ctx, "status", "done"); err != nil {
		return "", err
	}
	return dbos.RunAsStep(ctx, func(context.Context) (string, error) { return in, nil })
}

// scheduledWorkflow is a cron-schedulable workflow (its signature is
// (Context, ScheduledWorkflowInput)), so E3 can attach a schedule to it.
func scheduledWorkflow(_ dbos.Context, _ dbos.ScheduledWorkflowInput) (any, error) {
	return nil, nil
}

// startExecutor launches an in-process Go DBOS app that connects to the harness
// conductor as an executor for appName, backed by a temp-file SQLite system DB
// (no Postgres needed). The connection happens in the background after Launch,
// so callers poll the CLI reads until the executor appears. beforeLaunch hooks
// run after the base registration but before Launch (e.g. to register extra
// workflows). Shutdown is registered as a cleanup.
func startExecutor(t *testing.T, conductorHTTPURL, apiKey, appName string, beforeLaunch ...func(dbos.Context)) dbos.Context {
	t.Helper()
	// The SDK appends /websocket/<app>/<key>; against a local conductor the base
	// is a bare ws://host:port (the /conductor/v1alpha1 prefix is managed-only).
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
	for _, fn := range beforeLaunch {
		fn(dctx)
	}
	if err := dbos.Launch(dctx); err != nil {
		t.Fatalf("launch dbos app: %v", err)
	}
	t.Cleanup(func() { _ = dbos.Shutdown(dctx, 5*time.Second) })
	return dctx
}

// TestWorkflowReadsIntegration (E1a) reuses the executor fixture: it runs a
// workflow on a connected executor and reads it back through the CLI — `workflow
// get` (dispatched live to the executor, exercising the detail renderer),
// `workflow steps` (the one step), and `workflow events` (empty but valid).
func TestWorkflowReadsIntegration(t *testing.T) {
	baseURL := conductortest.Start(t)
	const appName = "e1-app"

	seed, err := newSeedClient(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	registerAppOrFail(t, seed, "local", appName)
	apiKey := mintKeyOrFail(t, seed, "local", appName+"-key")

	dctx := startExecutor(t, baseURL, apiKey, appName)
	h, err := dbos.RunWorkflow(dctx, spikeWorkflow, "hi")
	if err != nil {
		t.Fatalf("run workflow: %v", err)
	}
	if _, err := h.GetResult(); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	id := h.GetWorkflowID()

	wfCmd := func() (*cobra.Command, *strings.Builder) {
		cmd := newCmdWithGlobals()
		_ = cmd.Flags().Set("url", baseURL)
		_ = cmd.Flags().Set("org", "local")
		_ = cmd.Flags().Set("app", appName)
		out := &strings.Builder{}
		cmd.SetOut(out)
		return cmd, out
	}

	// workflow get — dispatched live to the executor; poll (tolerating a brief
	// not-yet-visible window) until it returns the workflow.
	var getOut string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cmd, out := wfCmd()
		if err := runWorkflowGet(cmd, []string{id}); err == nil && strings.Contains(out.String(), id) {
			getOut = out.String()
			break
		}
		time.Sleep(time.Second)
	}
	if !strings.Contains(getOut, id) {
		t.Fatalf("workflow get never returned %q", id)
	}
	if !strings.Contains(getOut, "SUCCESS") {
		t.Errorf("expected SUCCESS status in:\n%s", getOut)
	}

	// workflow steps — the workflow ran one step.
	stepsCmd, stepsOut := wfCmd()
	if err := runWorkflowSteps(stepsCmd, []string{id}); err != nil {
		t.Fatalf("workflow steps: %v", err)
	}
	if strings.Count(strings.TrimSpace(stepsOut.String()), "\n") < 1 {
		t.Errorf("expected at least one step:\n%s", stepsOut.String())
	}

	// workflow events — the workflow set a "status" event.
	eventsCmd, eventsOut := wfCmd()
	if err := runWorkflowEvents(eventsCmd, []string{id}); err != nil {
		t.Fatalf("workflow events: %v", err)
	}
	if !strings.Contains(eventsOut.String(), "status") {
		t.Errorf("expected the 'status' event:\n%s", eventsOut.String())
	}

	// workflow list — the workflow appears in the (unfiltered) search.
	listCmd, listOut := wfCmd()
	if err := runWorkflowList(listCmd, nil); err != nil {
		t.Fatalf("workflow list: %v", err)
	}
	if !strings.Contains(listOut.String(), id) {
		t.Errorf("workflow list missing %q:\n%s", id, listOut.String())
	}
}

// TestWorkflowMutationsIntegration (E2) exercises the mutation pipeline live: it
// runs a workflow, lists it as `-o ids`, pipes those IDs into `workflow delete -`
// (stdin), and confirms the workflow is gone.
func TestWorkflowMutationsIntegration(t *testing.T) {
	baseURL := conductortest.Start(t)
	const appName = "e2-app"

	seed, err := newSeedClient(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	registerAppOrFail(t, seed, "local", appName)
	apiKey := mintKeyOrFail(t, seed, "local", appName+"-key")

	dctx := startExecutor(t, baseURL, apiKey, appName)
	h, err := dbos.RunWorkflow(dctx, spikeWorkflow, "hi")
	if err != nil {
		t.Fatalf("run workflow: %v", err)
	}
	if _, err := h.GetResult(); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	id := h.GetWorkflowID()

	wfCmd := func(stdin string) (*cobra.Command, *strings.Builder) {
		cmd := newCmdWithGlobals()
		cmd.Flags().Bool("children", false, "") // delete reads --children
		_ = cmd.Flags().Set("url", baseURL)
		_ = cmd.Flags().Set("org", "local")
		_ = cmd.Flags().Set("app", appName)
		if stdin != "" {
			cmd.SetIn(strings.NewReader(stdin))
		}
		out := &strings.Builder{}
		cmd.SetOut(out)
		return cmd, out
	}

	// fork the workflow into a new execution; the new ID is retrievable. This is
	// the first command here that conductor dispatches to the executor, so poll
	// it like E1 polls its first read: the executor's registration arrives over
	// the WebSocket asynchronously after Launch, and until conductor has it the
	// dispatch fails with "no healthy executors available".
	var forkID string
	var forkErr error
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		fcmd, fout := wfCmd("")
		if forkErr = runWorkflowFork(fcmd, []string{id}); forkErr == nil {
			forkID = strings.TrimSpace(fout.String())
			break
		}
		time.Sleep(time.Second)
	}
	if forkErr != nil {
		t.Fatalf("workflow fork: %v", forkErr)
	}
	if forkID == "" || forkID == id {
		t.Fatalf("fork returned no new workflow ID: %q", forkID)
	}
	forked := false
	for i := 0; i < 15; i++ {
		g, _ := wfCmd("")
		if err := runWorkflowGet(g, []string{forkID}); err == nil {
			forked = true
			break
		}
		time.Sleep(time.Second)
	}
	if !forked {
		t.Errorf("forked workflow %q not retrievable", forkID)
	}

	// list -o ids until the workflow shows up.
	var ids string
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cmd, out := wfCmd("")
		_ = cmd.Flags().Set("output", "ids")
		if err := runWorkflowList(cmd, nil); err == nil && strings.Contains(out.String(), id) {
			ids = out.String()
			break
		}
		time.Sleep(time.Second)
	}
	if !strings.Contains(ids, id) {
		t.Fatalf("workflow %q never appeared in list -o ids", id)
	}

	// delete - (IDs from stdin, the piped `list -o ids` output).
	del, _ := wfCmd(ids)
	if err := runWorkflowDelete(del, []string{"-"}); err != nil {
		t.Fatalf("workflow delete -: %v", err)
	}

	// get → gone.
	gone := false
	for i := 0; i < 10; i++ {
		g, _ := wfCmd("")
		if err := runWorkflowGet(g, []string{id}); err != nil {
			gone = true
			break
		}
		time.Sleep(time.Second)
	}
	if !gone {
		t.Errorf("workflow %q still retrievable after delete", id)
	}
}

// TestQueueScheduleReadsIntegration (E3) registers a queue and a schedule on the
// fixture app, then reads them back through the CLI (dispatched live to the
// executor): queue list/get and schedule list/get.
func TestQueueScheduleReadsIntegration(t *testing.T) {
	baseURL := conductortest.Start(t)
	const appName = "e3-app"

	seed, err := newSeedClient(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	registerAppOrFail(t, seed, "local", appName)
	apiKey := mintKeyOrFail(t, seed, "local", appName+"-key")

	// The scheduled workflow must be registered before Launch; the queue and the
	// schedule itself are runtime (post-Launch) calls.
	dctx := startExecutor(t, baseURL, apiKey, appName, func(c dbos.Context) {
		dbos.RegisterWorkflow(c, scheduledWorkflow)
	})
	if _, err := dbos.RegisterQueue(dctx, "e3-queue"); err != nil {
		t.Fatalf("register queue: %v", err)
	}
	if err := dbos.CreateSchedule(dctx, dbos.ScheduleSpec{
		ScheduleName: "e3-sched",
		Schedule:     "0 0 0 * * *", // 6-field cron (sec min hour dom mon dow): daily at midnight
		Workflow:     scheduledWorkflow,
	}); err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	appCmd := func() (*cobra.Command, *strings.Builder) {
		cmd := newCmdWithGlobals()
		_ = cmd.Flags().Set("url", baseURL)
		_ = cmd.Flags().Set("org", "local")
		_ = cmd.Flags().Set("app", appName)
		out := &strings.Builder{}
		cmd.SetOut(out)
		return cmd, out
	}
	// pollContains runs a read until its output contains want (reads dispatch to
	// the executor, which connects asynchronously).
	pollContains := func(name string, run func(*cobra.Command) error, want string) {
		deadline := time.Now().Add(30 * time.Second)
		var last string
		for time.Now().Before(deadline) {
			cmd, out := appCmd()
			if err := run(cmd); err == nil {
				last = out.String()
				if strings.Contains(last, want) {
					return
				}
			}
			time.Sleep(time.Second)
		}
		t.Fatalf("%s never contained %q; last:\n%s", name, want, last)
	}

	pollContains("queue list", func(c *cobra.Command) error { return runQueueList(c, nil) }, "e3-queue")
	pollContains("schedule list", func(c *cobra.Command) error { return runScheduleList(c, nil) }, "e3-sched")

	qc, _ := appCmd()
	if err := runQueueGet(qc, []string{"e3-queue"}); err != nil {
		t.Errorf("queue get: %v", err)
	}
	sc, _ := appCmd()
	if err := runScheduleGet(sc, []string{"e3-sched"}); err != nil {
		t.Errorf("schedule get: %v", err)
	}
}

// TestScheduleMutationsIntegration (E4) sets up a schedule on the fixture and
// exercises the mutations through the CLI: pause (verified via get→PAUSED),
// resume (→ACTIVE), trigger (prints a started workflow ID), and backfill.
func TestScheduleMutationsIntegration(t *testing.T) {
	baseURL := conductortest.Start(t)
	const appName = "e4-app"
	const schedName = "e4-sched"

	seed, err := newSeedClient(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	registerAppOrFail(t, seed, "local", appName)
	apiKey := mintKeyOrFail(t, seed, "local", appName+"-key")

	dctx := startExecutor(t, baseURL, apiKey, appName, func(c dbos.Context) {
		dbos.RegisterWorkflow(c, scheduledWorkflow)
	})
	if err := dbos.CreateSchedule(dctx, dbos.ScheduleSpec{
		ScheduleName: schedName,
		Schedule:     "0 0 0 * * *",
		Workflow:     scheduledWorkflow,
	}); err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	appCmd := func() (*cobra.Command, *strings.Builder) {
		cmd := newCmdWithGlobals()
		cmd.Flags().String("since", "", "") // for backfill
		cmd.Flags().String("until", "", "")
		_ = cmd.Flags().Set("url", baseURL)
		_ = cmd.Flags().Set("org", "local")
		_ = cmd.Flags().Set("app", appName)
		out := &strings.Builder{}
		cmd.SetOut(out)
		return cmd, out
	}
	waitFor := func(name string, run func(*cobra.Command) error, want string) {
		deadline := time.Now().Add(30 * time.Second)
		var last string
		for time.Now().Before(deadline) {
			cmd, out := appCmd()
			if err := run(cmd); err == nil {
				last = out.String()
				if strings.Contains(last, want) {
					return
				}
			}
			time.Sleep(time.Second)
		}
		t.Fatalf("%s never contained %q; last:\n%s", name, want, last)
	}

	// Wait for the schedule to be visible (executor connects asynchronously).
	waitFor("schedule list", func(c *cobra.Command) error { return runScheduleList(c, nil) }, schedName)

	// pause → get reports PAUSED.
	pc, _ := appCmd()
	if err := runSchedulePause(pc, []string{schedName}); err != nil {
		t.Fatalf("schedule pause: %v", err)
	}
	waitFor("schedule get after pause", func(c *cobra.Command) error { return runScheduleGet(c, []string{schedName}) }, "PAUSED")

	// resume → get reports ACTIVE.
	rc, _ := appCmd()
	if err := runScheduleResume(rc, []string{schedName}); err != nil {
		t.Fatalf("schedule resume: %v", err)
	}
	waitFor("schedule get after resume", func(c *cobra.Command) error { return runScheduleGet(c, []string{schedName}) }, "ACTIVE")

	// trigger → a started workflow ID on stdout.
	tc, tout := appCmd()
	if err := runScheduleTrigger(tc, []string{schedName}); err != nil {
		t.Fatalf("schedule trigger: %v", err)
	}
	if strings.TrimSpace(tout.String()) == "" {
		t.Errorf("schedule trigger printed no workflow ID")
	}

	// backfill over a window succeeds (the replayed IDs may be empty for the range).
	bc, _ := appCmd()
	_ = bc.Flags().Set("since", "1h")
	_ = bc.Flags().Set("until", "0s")
	if err := runScheduleBackfill(bc, []string{schedName}); err != nil {
		t.Fatalf("schedule backfill: %v", err)
	}
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
