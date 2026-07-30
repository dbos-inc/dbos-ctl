package cli

import (
	"strings"
	"testing"
)

const oneScheduleJSON = `{"scheduleId":"sch-1","scheduleName":"nightly","status":"ACTIVE","cronExpression":"0 0 * * *","cronTimezone":"UTC","workflowName":"cleanup","automaticBackfill":false,"lastFiredAt":"2026-07-30T00:00:00Z"}`

func TestRunScheduleList(t *testing.T) {
	isolateConfig(t)
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/schedules", "["+oneScheduleJSON+"]")

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runScheduleList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "STATUS", "CRON", "nightly", "ACTIVE", "cleanup"} {
		if !strings.Contains(got, want) {
			t.Errorf("schedule list missing %q:\n%s", want, got)
		}
	}
}

func TestRunScheduleGet(t *testing.T) {
	isolateConfig(t)
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/schedules/nightly", oneScheduleJSON)

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runScheduleGet(cmd, []string{"nightly"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"scheduleName", "nightly", "cronExpression", "workflowName", "cleanup"} {
		if !strings.Contains(got, want) {
			t.Errorf("schedule get detail missing %q:\n%s", want, got)
		}
	}
}

func TestRunSchedulePause(t *testing.T) {
	isolateConfig(t)
	srv, rec := mutationServer(t)

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runSchedulePause(cmd, []string{"nightly"}); err != nil {
		t.Fatal(err)
	}
	if rec.path != "/v2/orgs/local/apps/myapp/schedules/nightly/pause" {
		t.Errorf("pause hit %q, want the pause path", rec.path)
	}
	if !strings.Contains(out.String(), `paused schedule "nightly"`) {
		t.Errorf("unexpected pause output: %q", out.String())
	}
}

func TestRunScheduleTrigger(t *testing.T) {
	isolateConfig(t)
	srv, _ := forkServer(t, "/v2/orgs/local/apps/myapp/schedules/nightly/trigger", "wf-trig")

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runScheduleTrigger(cmd, []string{"nightly"}); err != nil {
		t.Fatal(err)
	}
	// scalar output: the bare started workflow ID.
	if got := out.String(); got != "wf-trig\n" {
		t.Errorf("trigger output = %q, want the bare workflow ID", got)
	}
}

func TestRunScheduleBackfill(t *testing.T) {
	isolateConfig(t)
	srv, body := searchServer(t, "/v2/orgs/local/apps/myapp/schedules/nightly/backfill", `{"workflowIds":["wf-1","wf-2"]}`)

	cmd, out := workflowCmdAt(t, srv.URL)
	cmd.Flags().String("since", "", "")
	cmd.Flags().String("until", "", "")
	_ = cmd.Flags().Set("since", "2026-07-01T00:00:00Z")
	_ = cmd.Flags().Set("until", "2026-07-02T00:00:00Z")
	if err := runScheduleBackfill(cmd, []string{"nightly"}); err != nil {
		t.Fatal(err)
	}
	// scalar-list output: the backfilled workflow IDs, one per line.
	if got := out.String(); got != "wf-1\nwf-2\n" {
		t.Errorf("backfill output = %q, want the bare IDs", got)
	}
	if _, ok := (*body)["startTime"]; !ok {
		t.Errorf("backfill did not send startTime: %v", *body)
	}
	if _, ok := (*body)["endTime"]; !ok {
		t.Errorf("backfill did not send endTime: %v", *body)
	}
}

func TestRunScheduleBackfillNeedsWindow(t *testing.T) {
	isolateConfig(t)
	cmd, _ := workflowCmdAt(t, "http://127.0.0.1:0")
	cmd.Flags().String("since", "", "")
	cmd.Flags().String("until", "", "")
	_ = cmd.Flags().Set("since", "1h") // only one bound
	err := runScheduleBackfill(cmd, []string{"nightly"})
	if err == nil || !strings.Contains(err.Error(), "since") {
		t.Errorf("want a missing-window error, got %v", err)
	}
}
