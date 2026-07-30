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
