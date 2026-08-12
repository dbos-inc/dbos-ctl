package cli

import (
	"strings"
	"testing"
)

const oneQueueJSON = `{"name":"myqueue","concurrency":4,"partitionQueue":false,"pollingIntervalSecs":1.5,"priorityEnabled":true,"rateLimitMax":10,"rateLimitPeriodSecs":60,"workerConcurrency":2}`

func TestRunQueueList(t *testing.T) {
	isolateConfig(t)
	srv, query := appReadServer(t, "/v2/orgs/local/apps/myapp/queues", "["+oneQueueJSON+"]")

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
	// Without --owner-app the filter must stay unsent, so the server keeps
	// defaulting the scope to the app in the path rather than being pinned to a
	// literal empty name (which would scope to an application called "").
	if v, ok := (*query)["applicationName"]; ok {
		t.Errorf("queue list sent applicationName=%v, want it omitted", v)
	}
}

// --owner-app scopes the listing to another application's queues, which only
// differs from --app when several applications share a system database.
func TestRunQueueListOwnerApp(t *testing.T) {
	isolateConfig(t)
	srv, query := appReadServer(t, "/v2/orgs/local/apps/myapp/queues", "["+oneQueueJSON+"]")

	cmd, _ := workflowCmdAt(t, srv.URL)
	// Declared through the real definition rather than hand-rolled, so this
	// cannot drift from what `queue list` actually registers.
	addRequestFlags(cmd, "owner-app")
	if err := cmd.Flags().Set("owner-app", "otherapp"); err != nil {
		t.Fatal(err)
	}
	if err := runQueueList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	// The addressed application stays in the path; only the filter changes.
	if got := query.Get("applicationName"); got != "otherapp" {
		t.Errorf("applicationName = %q, want %q", got, "otherapp")
	}
}

// A queue reports its owning application when several apps share one system
// database. The field is nullable, so the detail view shows it only when set.
func TestRunQueueGetApplicationName(t *testing.T) {
	isolateConfig(t)
	const owned = `{"name":"myqueue","applicationName":"otherapp","partitionQueue":false,"pollingIntervalSecs":1.5,"priorityEnabled":true}`
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/queues/myqueue", owned)

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runQueueGet(cmd, []string{"myqueue"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "applicationName") || !strings.Contains(got, "otherapp") {
		t.Errorf("queue get detail missing the owning application:\n%s", got)
	}
}

// The same view omits the row entirely when the queue reports no owner (an
// in-memory queue, or one recorded before Transact tracked application names).
func TestRunQueueGetNullApplicationName(t *testing.T) {
	isolateConfig(t)
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/queues/myqueue", oneQueueJSON)

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runQueueGet(cmd, []string{"myqueue"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, "applicationName") {
		t.Errorf("queue get detail showed an empty applicationName row:\n%s", got)
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
