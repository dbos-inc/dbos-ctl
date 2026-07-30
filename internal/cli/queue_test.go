package cli

import (
	"strings"
	"testing"
)

const oneQueueJSON = `{"name":"myqueue","concurrency":4,"partitionQueue":false,"pollingIntervalSecs":1.5,"priorityEnabled":true,"rateLimitMax":10,"rateLimitPeriodSecs":60,"workerConcurrency":2}`

func TestRunQueueList(t *testing.T) {
	isolateConfig(t)
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/queues", "["+oneQueueJSON+"]")

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runQueueList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "CONCURRENCY", "myqueue", "4", "true"} {
		if !strings.Contains(got, want) {
			t.Errorf("queue list missing %q:\n%s", want, got)
		}
	}
}

func TestRunQueueGet(t *testing.T) {
	isolateConfig(t)
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/queues/myqueue", oneQueueJSON)

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runQueueGet(cmd, []string{"myqueue"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"name", "myqueue", "concurrency", "workerConcurrency", "rateLimitMax"} {
		if !strings.Contains(got, want) {
			t.Errorf("queue get detail missing %q:\n%s", want, got)
		}
	}
}

func TestRunQueueGetJSON(t *testing.T) {
	isolateConfig(t)
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/queues/myqueue", oneQueueJSON)

	cmd, out := workflowCmdAt(t, srv.URL)
	_ = cmd.Flags().Set("output", "json")
	if err := runQueueGet(cmd, []string{"myqueue"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `"name": "myqueue"`) {
		t.Errorf("queue get -o json not the raw shape:\n%s", got)
	}
}
