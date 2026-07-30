package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func workflowCmdAt(t *testing.T, url string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", url)
	_ = cmd.Flags().Set("app", "myapp")
	var out bytes.Buffer
	cmd.SetOut(&out)
	return cmd, &out
}

const oneWorkflowJSON = `{"workflowId":"wf-1","status":"SUCCESS","workflowName":"myWf","createdAt":"2026-07-30T10:00:00Z","updatedAt":"2026-07-30T10:00:05Z","priority":0,"wasForkedFrom":false,"executorId":"exec-1","output":"42"}`

func TestRunWorkflowGet(t *testing.T) {
	isolateConfig(t)
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/workflows/wf-1", oneWorkflowJSON)

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runWorkflowGet(cmd, []string{"wf-1"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"workflowId", "wf-1", "status", "SUCCESS", "myWf", "executorId", "42"} {
		if !strings.Contains(got, want) {
			t.Errorf("workflow get detail missing %q:\n%s", want, got)
		}
	}
}

func TestRunWorkflowGetJSON(t *testing.T) {
	isolateConfig(t)
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/workflows/wf-1", oneWorkflowJSON)

	cmd, out := workflowCmdAt(t, srv.URL)
	_ = cmd.Flags().Set("output", "json")
	if err := runWorkflowGet(cmd, []string{"wf-1"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `"workflowId": "wf-1"`) {
		t.Errorf("workflow get -o json not the raw shape:\n%s", got)
	}
}

func TestRunWorkflowSteps(t *testing.T) {
	isolateConfig(t)
	const body = `[{"stepId":0,"stepName":"step1","startedAt":"2026-07-30T10:00:01Z","completedAt":"2026-07-30T10:00:02Z"}]`
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/workflows/wf-1/steps", body)

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runWorkflowSteps(cmd, []string{"wf-1"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"STEP", "NAME", "step1"} {
		if !strings.Contains(got, want) {
			t.Errorf("workflow steps missing %q:\n%s", want, got)
		}
	}
}

func TestRunWorkflowEvents(t *testing.T) {
	isolateConfig(t)
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/workflows/wf-1/events", `[{"key":"k1","value":"v1"}]`)

	cmd, out := workflowCmdAt(t, srv.URL)
	if err := runWorkflowEvents(cmd, []string{"wf-1"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"KEY", "VALUE", "k1", "v1"} {
		if !strings.Contains(got, want) {
			t.Errorf("workflow events missing %q:\n%s", want, got)
		}
	}
}

// searchServer captures the POST search body and replies with a workflow list.
func searchServer(t *testing.T, wantPath, respBody string) (*httptest.Server, *map[string]any) {
	t.Helper()
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotBody
}

func newWorkflowListCmd(t *testing.T, url string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := newCmdWithGlobals()
	f := cmd.Flags()
	f.Int64P("limit", "l", 0, "")
	f.Int64("offset", 0, "")
	f.StringSlice("id", nil, "")
	f.StringSliceP("user", "u", nil, "")
	f.StringSliceP("status", "s", nil, "")
	f.StringSliceP("name", "n", nil, "")
	f.StringSlice("app-version", nil, "")
	f.StringSlice("queue", nil, "")
	f.String("since", "", "")
	f.String("until", "", "")
	f.Bool("desc", false, "")
	f.Bool("queued", false, "")
	_ = f.Set("url", url)
	_ = f.Set("app", "myapp")
	var out bytes.Buffer
	cmd.SetOut(&out)
	return cmd, &out
}

func TestRunWorkflowList(t *testing.T) {
	isolateConfig(t)
	srv, body := searchServer(t, "/v2/orgs/local/apps/myapp/workflows/search", "["+oneWorkflowJSON+"]")

	cmd, out := newWorkflowListCmd(t, srv.URL)
	_ = cmd.Flags().Set("status", "SUCCESS")
	_ = cmd.Flags().Set("name", "myWf")
	_ = cmd.Flags().Set("limit", "5")
	_ = cmd.Flags().Set("desc", "true")
	if err := runWorkflowList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	// Table rendered from the response.
	if got := out.String(); !strings.Contains(got, "WORKFLOW-ID") || !strings.Contains(got, "wf-1") {
		t.Errorf("workflow list table missing rows:\n%s", got)
	}
	// Filters mapped into the search body; unset filters are omitted.
	if s, _ := (*body)["status"].([]any); len(s) != 1 || s[0] != "SUCCESS" {
		t.Errorf("status not sent: %v", (*body)["status"])
	}
	if n, _ := (*body)["workflowName"].([]any); len(n) != 1 || n[0] != "myWf" {
		t.Errorf("workflowName not sent: %v", (*body)["workflowName"])
	}
	if (*body)["limit"] != float64(5) {
		t.Errorf("limit = %v, want 5", (*body)["limit"])
	}
	if (*body)["sortDesc"] != true {
		t.Errorf("sortDesc = %v, want true", (*body)["sortDesc"])
	}
	if _, ok := (*body)["user"]; ok {
		t.Errorf("unset --user should be omitted, got %v", (*body)["user"])
	}
}

func TestRunWorkflowListSince(t *testing.T) {
	isolateConfig(t)
	srv, body := searchServer(t, "/v2/orgs/local/apps/myapp/workflows/search", "[]")

	cmd, _ := newWorkflowListCmd(t, srv.URL)
	_ = cmd.Flags().Set("since", "1h")
	if err := runWorkflowList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := (*body)["startTime"]; !ok {
		t.Errorf("--since 1h should send startTime, got body %v", *body)
	}
}

func TestRunWorkflowListBadSince(t *testing.T) {
	isolateConfig(t)
	cmd, _ := newWorkflowListCmd(t, "http://127.0.0.1:0")
	_ = cmd.Flags().Set("since", "not-a-time")
	err := runWorkflowList(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "--since") {
		t.Errorf("want a bad --since error, got %v", err)
	}
}

func TestParseSearchTime(t *testing.T) {
	cmd, _ := newWorkflowListCmd(t, "http://x")
	_ = cmd.Flags().Set("since", "2026-07-30T10:00:00Z")
	got, err := parseSearchTime(cmd, "since")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("parseSearchTime(RFC3339) = %v, want %v", got, want)
	}
}

// TestRunWorkflowNoApp: an app-scoped workflow read with no app configured fails
// with a clear error before sending.
func TestRunWorkflowNoApp(t *testing.T) {
	isolateConfig(t)
	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", "http://127.0.0.1:0") // auth none → org local, but no app
	cmd.SetOut(&bytes.Buffer{})
	err := runWorkflowGet(cmd, []string{"wf-1"})
	if err == nil || !strings.Contains(err.Error(), "app") {
		t.Errorf("want a no-app error, got %v", err)
	}
}
