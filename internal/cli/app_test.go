package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/config"
	"github.com/dbos-inc/dbos-ctl/internal/creds"
)

// newCmdWithGlobals returns a throwaway command carrying the same global flags
// rootCmd declares, so a RunE can be exercised in isolation.
func newCmdWithGlobals() *cobra.Command {
	c := &cobra.Command{}
	c.Flags().String("url", "", "")
	c.Flags().String("org", "", "")
	c.Flags().String("app", "", "")
	c.Flags().String("output", "table", "")
	c.Flags().String("profile", "", "")
	c.SetContext(context.Background())
	return c
}

// isolateConfig points config at an empty temp dir and clears DBOS_* env, so a
// test resolves settings from flags alone, independent of any real user config.
func isolateConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DBOS_PROFILE", "")
	t.Setenv("DBOS_URL", "")
	t.Setenv("DBOS_ORG", "")
	t.Setenv("DBOS_APP", "")
	t.Setenv("DBOS_TOKEN", "")
}

const oneAppJSON = `[{"name":"myapp","status":"ACTIVE","language":"python","id":"a1","orgId":"local","dbosCloud":false,"executorTimeoutSecs":0,"privateMode":false}]`

func appServer(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/orgs/local/apps" {
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunAppListJSON(t *testing.T) {
	isolateConfig(t)
	srv := appServer(t, http.StatusOK, "application/json", oneAppJSON)

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", srv.URL)
	_ = cmd.Flags().Set("output", "json")
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runAppList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `"name": "myapp"`) {
		t.Errorf("json output missing app:\n%s", got)
	}
}

func TestRunAppListTable(t *testing.T) {
	isolateConfig(t)
	srv := appServer(t, http.StatusOK, "application/json", oneAppJSON)

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", srv.URL) // output defaults to table
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runAppList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "STATUS", "LANGUAGE", "myapp", "ACTIVE", "python"} {
		if !strings.Contains(got, want) {
			t.Errorf("table output missing %q:\n%s", want, got)
		}
	}
}

func TestEffectiveOrg(t *testing.T) {
	ctx := context.Background()
	// Explicit org wins — no client call (nil client proves it).
	if got, err := effectiveOrg(ctx, nil, config.Settings{Auth: config.AuthBearer, Org: "myorg"}); err != nil || got != "myorg" {
		t.Errorf("explicit org: got %q, %v; want myorg", got, err)
	}
	// A no-auth target is "local" without a client call.
	if got, err := effectiveOrg(ctx, nil, config.Settings{Auth: config.AuthNone}); err != nil || got != "local" {
		t.Errorf("no-auth org: got %q, %v; want local", got, err)
	}
}

// TestRunAppListDerivesOrgLive proves the fallback: a bearer target with an
// ad-hoc $DBOS_TOKEN (no stored login) and no configured org derives the org
// live from getCurrentUser rather than sending an empty org segment (404).
func TestRunAppListDerivesOrgLive(t *testing.T) {
	isolateConfig(t)
	t.Setenv("DBOS_TOKEN", "jwt")
	const org = "acme"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/users/me":
			_, _ = w.Write([]byte(`{"name":"alice","email":"a@x","orgName":"` + org + `","orgId":"o1","createdAt":"2026-01-01T00:00:00Z","subscriptionPlan":"free"}`))
		case "/v2/orgs/" + org + "/apps":
			_, _ = w.Write([]byte(oneAppJSON))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	f := &config.File{Current: "p", Profiles: map[string]config.Profile{
		"p": {Auth: config.AuthBearer, URL: srv.URL}, // bearer, no org
	}}
	if err := config.Save(f); err != nil {
		t.Fatal(err)
	}

	cmd := newCmdWithGlobals()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runAppList(cmd, nil); err != nil {
		t.Fatalf("app list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "myapp") {
		t.Errorf("app list did not derive org from identity:\n%s", got)
	}
}

// TestRunAppListUsesStoredOrg proves the common path: with an org captured at
// login, app list uses it directly and makes no getCurrentUser request.
func TestRunAppListUsesStoredOrg(t *testing.T) {
	isolateConfig(t)
	const org = "acme"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/users/me" {
			t.Errorf("getCurrentUser should not be called when the org is stored")
		}
		if r.URL.Path != "/v2/orgs/"+org+"/apps" {
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(oneAppJSON))
	}))
	t.Cleanup(srv.Close)

	f := &config.File{Current: "p", Profiles: map[string]config.Profile{
		"p": {Auth: config.AuthBearer, URL: srv.URL},
	}}
	if err := config.Save(f); err != nil {
		t.Fatal(err)
	}
	store, err := creds.NewFileStore()
	if err != nil {
		t.Fatal(err)
	}
	// A stored login that captured the org (as `dbosctl login` now does).
	if err := store.Save("p", &creds.Creds{Token: "jwt", Organization: org}); err != nil {
		t.Fatal(err)
	}

	cmd := newCmdWithGlobals()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runAppList(cmd, nil); err != nil {
		t.Fatalf("app list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "myapp") {
		t.Errorf("app list did not use the stored org:\n%s", got)
	}
}

func TestConfirm(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"y\n", true}, {"yes\n", true}, {"Y\n", true}, {"YES\n", true},
		{" y \n", true},
		{"n\n", false}, {"no\n", false}, {"\n", false}, {"", false}, {"maybe\n", false},
	}
	for _, c := range cases {
		var out bytes.Buffer
		got, err := confirm(strings.NewReader(c.in), &out, "Delete?")
		if err != nil {
			t.Fatalf("confirm(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("confirm(%q) = %v, want %v", c.in, got, c.want)
		}
		if !strings.Contains(out.String(), "Delete? [y/N]") {
			t.Errorf("confirm did not write the prompt: %q", out.String())
		}
	}
}

// appItemServer handles PUT/DELETE on /v2/orgs/local/apps/<name>, echoing the
// method it saw so a test can assert the CLI used the right verb.
func appItemServer(t *testing.T, name string, status int, contentType, body string) (*httptest.Server, *string) {
	t.Helper()
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/orgs/local/apps/"+name {
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		gotMethod = r.Method
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotMethod
}

func TestRunAppRegister(t *testing.T) {
	isolateConfig(t)
	srv, method := appItemServer(t, "myapp", http.StatusOK, "", "")

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", srv.URL)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runAppRegister(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	if *method != http.MethodPut {
		t.Errorf("register used %s, want PUT", *method)
	}
	if got := out.String(); !strings.Contains(got, `registered app "myapp"`) {
		t.Errorf("unexpected register output: %q", got)
	}
}

func TestRunAppRegisterError(t *testing.T) {
	isolateConfig(t)
	srv, _ := appItemServer(t, "myapp", http.StatusForbidden, "application/problem+json",
		`{"title":"Forbidden","detail":"nope","status":403}`)

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", srv.URL)
	cmd.SetOut(&bytes.Buffer{})
	err := runAppRegister(cmd, []string{"myapp"})
	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("want a Forbidden error, got %v", err)
	}
}

// setInteractive forces the prompt-or-not decision for a test, restoring it
// afterward, so neither path depends on the test process's real stdin.
func setInteractive(t *testing.T, v bool) {
	t.Helper()
	prev := isInteractive
	isInteractive = func() bool { return v }
	t.Cleanup(func() { isInteractive = prev })
}

func newDeleteCmd(t *testing.T, url string) *cobra.Command {
	t.Helper()
	cmd := newCmdWithGlobals()
	cmd.Flags().Bool("force", false, "")
	_ = cmd.Flags().Set("url", url)
	return cmd
}

// TestRunAppDeleteNonInteractiveRequiresForce: a non-TTY stdin can't answer the
// prompt, so without --force the delete is refused and nothing is sent.
func TestRunAppDeleteNonInteractiveRequiresForce(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, false)
	srv, method := appItemServer(t, "myapp", http.StatusOK, "", "")

	cmd := newDeleteCmd(t, srv.URL)
	cmd.SetOut(&bytes.Buffer{})
	err := runAppDelete(cmd, []string{"myapp"})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want a refusal mentioning --force, got %v", err)
	}
	if *method != "" {
		t.Errorf("a refused delete still sent %q to the server", *method)
	}
}

// TestRunAppDeleteNonInteractiveForce: the CI path — non-TTY, but --force lets
// the delete proceed.
func TestRunAppDeleteNonInteractiveForce(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, false)
	srv, method := appItemServer(t, "myapp", http.StatusOK, "", "")

	cmd := newDeleteCmd(t, srv.URL)
	_ = cmd.Flags().Set("force", "true")
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runAppDelete(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	if *method != http.MethodDelete {
		t.Errorf("--force delete used %s, want DELETE", *method)
	}
	if got := out.String(); !strings.Contains(got, `deleted app "myapp"`) {
		t.Errorf("unexpected delete output: %q", got)
	}
}

// TestRunAppDeleteConfirmYes: interactive, user answers "y", delete proceeds.
func TestRunAppDeleteConfirmYes(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, true)
	srv, method := appItemServer(t, "myapp", http.StatusOK, "", "")

	cmd := newDeleteCmd(t, srv.URL)
	cmd.SetIn(strings.NewReader("y\n"))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runAppDelete(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	if *method != http.MethodDelete {
		t.Errorf("after confirming, delete used %q, want DELETE", *method)
	}
}

// TestRunAppDeleteConfirmNo: interactive, user answers "n", nothing is deleted.
func TestRunAppDeleteConfirmNo(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, true)
	srv, method := appItemServer(t, "myapp", http.StatusOK, "", "")

	cmd := newDeleteCmd(t, srv.URL)
	cmd.SetIn(strings.NewReader("n\n"))
	cmd.SetOut(&bytes.Buffer{})
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	if err := runAppDelete(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	if *method != "" {
		t.Errorf("declining the prompt still sent %q to the server", *method)
	}
	if !strings.Contains(errBuf.String(), "aborted") {
		t.Errorf("expected an abort notice, got %q", errBuf.String())
	}
}

// TestRunAppDeleteForce: --force skips the prompt even when interactive.
func TestRunAppDeleteForce(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, true) // would prompt, but --force must not read stdin
	srv, method := appItemServer(t, "myapp", http.StatusOK, "", "")

	cmd := newDeleteCmd(t, srv.URL)
	_ = cmd.Flags().Set("force", "true")
	cmd.SetIn(strings.NewReader("")) // no input available; a prompt would hang/EOF-abort
	cmd.SetOut(&bytes.Buffer{})
	if err := runAppDelete(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	if *method != http.MethodDelete {
		t.Errorf("--force delete used %q, want DELETE", *method)
	}
}

func TestRunAppDeleteError(t *testing.T) {
	isolateConfig(t)
	setInteractive(t, false)
	srv, _ := appItemServer(t, "ghost", http.StatusNotFound, "application/problem+json",
		`{"title":"Not Found","detail":"no such app","status":404}`)

	cmd := newDeleteCmd(t, srv.URL)
	_ = cmd.Flags().Set("force", "true") // reach the server (a bare non-TTY delete is refused first)
	cmd.SetOut(&bytes.Buffer{})
	err := runAppDelete(cmd, []string{"ghost"})
	if err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("want a Not Found error, got %v", err)
	}
}

// appReadServer serves 200 JSON at exactly wantPath, capturing the request
// query so a test can assert params (e.g. the metrics window) were sent.
func appReadServer(t *testing.T, wantPath, body string) (*httptest.Server, *url.Values) {
	t.Helper()
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath {
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotQuery
}

func appReadCmd(t *testing.T, url string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", url)
	var out bytes.Buffer
	cmd.SetOut(&out)
	return cmd, &out
}

func TestRunAppGet(t *testing.T) {
	isolateConfig(t)
	const body = `{"name":"myapp","id":"a1","status":"ACTIVE","language":"python","orgId":"local","dbosCloud":false,"privateMode":false,"executorTimeoutSecs":60,"gcRowsThreshold":null,"gcTimeThresholdMs":null,"globalTimeoutMs":null}`
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp", body)

	cmd, out := appReadCmd(t, srv.URL)
	if err := runAppGet(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"name", "myapp", "status", "ACTIVE", "language", "python", "executorTimeoutSecs", "60"} {
		if !strings.Contains(got, want) {
			t.Errorf("app get detail missing %q:\n%s", want, got)
		}
	}
	// Absent (null) pointer fields are omitted from the detail table.
	if strings.Contains(got, "gcRowsThreshold") {
		t.Errorf("app get should omit the null gcRowsThreshold field:\n%s", got)
	}
}

func TestRunAppGetJSON(t *testing.T) {
	isolateConfig(t)
	const body = `{"name":"myapp","id":"a1","status":"ACTIVE","language":"python","orgId":"local","dbosCloud":false,"privateMode":false,"executorTimeoutSecs":60,"gcRowsThreshold":null,"gcTimeThresholdMs":null,"globalTimeoutMs":null}`
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp", body)

	cmd, out := appReadCmd(t, srv.URL)
	_ = cmd.Flags().Set("output", "json")
	if err := runAppGet(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	// json is the raw shape, so even the null gc fields appear.
	if got := out.String(); !strings.Contains(got, `"gcRowsThreshold"`) || !strings.Contains(got, `"name": "myapp"`) {
		t.Errorf("app get -o json not the raw shape:\n%s", got)
	}
}

func TestRunAppVersions(t *testing.T) {
	isolateConfig(t)
	const body = `[{"versionName":"v1","versionId":"ver-1","versionTimestamp":"2026-07-30T10:00:00Z","createdAt":"2026-07-30T10:00:00Z"}]`
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/versions", body)

	cmd, out := appReadCmd(t, srv.URL)
	if err := runAppVersions(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "ID", "TIMESTAMP", "v1", "ver-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("app versions missing %q:\n%s", want, got)
		}
	}
}

func TestRunAppExecutors(t *testing.T) {
	isolateConfig(t)
	const body = `[{"executorId":"exec-1","status":"HEALTHY","appId":"a1","appVersion":"v1","language":"go","hostname":"host-1","createdAt":"2026-07-30T10:00:00Z","updatedAt":"2026-07-30T10:00:00Z"}]`
	srv, _ := appReadServer(t, "/v2/orgs/local/apps/myapp/executors", body)

	cmd, out := appReadCmd(t, srv.URL)
	if err := runAppExecutors(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"EXECUTOR", "STATUS", "exec-1", "HEALTHY", "host-1", "go"} {
		if !strings.Contains(got, want) {
			t.Errorf("app executors missing %q:\n%s", want, got)
		}
	}
}

func TestRunAppMetrics(t *testing.T) {
	isolateConfig(t)
	const body = `[{"appId":"a1","granularity":60,"metricName":"workflow_count","metricType":"workflow_count","timeBucket":"2026-07-30T10:00:00Z","value":5}]`
	srv, query := appReadServer(t, "/v2/orgs/local/apps/myapp/metrics", body)

	cmd, out := appReadCmd(t, srv.URL)
	cmd.Flags().Duration("since", 24*time.Hour, "")
	if err := runAppMetrics(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"METRIC", "VALUE", "workflow_count", "5"} {
		if !strings.Contains(got, want) {
			t.Errorf("app metrics missing %q:\n%s", want, got)
		}
	}
	// The required window must be sent as query params.
	if query.Get("startTime") == "" || query.Get("endTime") == "" {
		t.Errorf("metrics request missing time window: %v", query)
	}
}

// patchServer captures a PATCH request's method and JSON body at wantPath.
func patchServer(t *testing.T, wantPath string) (*httptest.Server, *map[string]any, *string) {
	t.Helper()
	var gotBody map[string]any
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath {
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		gotMethod = r.Method
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &gotBody, &gotMethod
}

func newAppUpdateCmd(t *testing.T, url string) *cobra.Command {
	t.Helper()
	cmd := newCmdWithGlobals()
	cmd.Flags().Int64("executor-timeout-secs", 0, "")
	cmd.Flags().Int64("gc-rows-threshold", 0, "")
	cmd.Flags().Int64("gc-time-threshold-ms", 0, "")
	cmd.Flags().Int64("global-timeout-ms", 0, "")
	cmd.Flags().Bool("private-mode", false, "")
	_ = cmd.Flags().Set("url", url)
	return cmd
}

func TestRunAppUpdate(t *testing.T) {
	isolateConfig(t)
	srv, body, method := patchServer(t, "/v2/orgs/local/apps/myapp")

	cmd := newAppUpdateCmd(t, srv.URL)
	_ = cmd.Flags().Set("executor-timeout-secs", "30")
	_ = cmd.Flags().Set("private-mode", "true")
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runAppUpdate(cmd, []string{"myapp"}); err != nil {
		t.Fatal(err)
	}
	if *method != http.MethodPatch {
		t.Errorf("update used %s, want PATCH", *method)
	}
	// Only the named fields are sent; the untouched gc fields are omitted.
	if got := (*body)["executorTimeoutSecs"]; got != float64(30) {
		t.Errorf("executorTimeoutSecs = %v, want 30", got)
	}
	if got := (*body)["privateMode"]; got != true {
		t.Errorf("privateMode = %v, want true", got)
	}
	if _, ok := (*body)["gcRowsThreshold"]; ok {
		t.Errorf("gcRowsThreshold should be omitted, got %v", (*body)["gcRowsThreshold"])
	}
	if !strings.Contains(out.String(), `updated app "myapp"`) {
		t.Errorf("unexpected update output: %q", out.String())
	}
}

func TestRunAppUpdateNoFields(t *testing.T) {
	isolateConfig(t)
	// No fields → error before any request (nil URL server proves nothing is sent).
	cmd := newAppUpdateCmd(t, "http://127.0.0.1:0")
	cmd.SetOut(&bytes.Buffer{})
	err := runAppUpdate(cmd, []string{"myapp"})
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Errorf("want a nothing-to-update error, got %v", err)
	}
}

func TestRunAppUpdateError(t *testing.T) {
	isolateConfig(t)
	srv, _ := appItemServer(t, "myapp", http.StatusForbidden, "application/problem+json",
		`{"title":"Forbidden","detail":"nope","status":403}`)

	cmd := newAppUpdateCmd(t, srv.URL)
	_ = cmd.Flags().Set("global-timeout-ms", "1000")
	cmd.SetOut(&bytes.Buffer{})
	err := runAppUpdate(cmd, []string{"myapp"})
	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("want a Forbidden error, got %v", err)
	}
}

func TestRunAppSetVersion(t *testing.T) {
	isolateConfig(t)
	srv, body, method := patchServer(t, "/v2/orgs/local/apps/myapp/versions/latest")

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", srv.URL)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runAppSetVersion(cmd, []string{"myapp", "v2"}); err != nil {
		t.Fatal(err)
	}
	if *method != http.MethodPatch {
		t.Errorf("set-version used %s, want PATCH", *method)
	}
	if got := (*body)["versionName"]; got != "v2" {
		t.Errorf("versionName = %v, want v2", got)
	}
	if !strings.Contains(out.String(), `set latest version of "myapp" to "v2"`) {
		t.Errorf("unexpected set-version output: %q", out.String())
	}
}

func TestRunAppSetVersionError(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"Not Found","detail":"no such app","status":404}`))
	}))
	t.Cleanup(srv.Close)

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", srv.URL)
	cmd.SetOut(&bytes.Buffer{})
	err := runAppSetVersion(cmd, []string{"ghost", "v1"})
	if err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("want a Not Found error, got %v", err)
	}
}

// TestRequestRespectsContextCancellation proves the mechanism Execute's signal
// handling relies on: cancelling the command's context aborts an in-flight
// request instead of hanging, so Ctrl-C cleanly interrupts a slow call.
func TestRequestRespectsContextCancellation(t *testing.T) {
	isolateConfig(t)
	// A server that hangs until the client cancels the request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := newCmdWithGlobals()
	cmd.SetContext(ctx)
	_ = cmd.Flags().Set("url", srv.URL)
	cmd.SetOut(&bytes.Buffer{})

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() { done <- runAppList(cmd, nil) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a cancellation error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not abort on context cancellation")
	}
}

func TestRunAppListError(t *testing.T) {
	isolateConfig(t)
	srv := appServer(t, http.StatusForbidden, "application/problem+json",
		`{"title":"Forbidden","detail":"past limit","status":403}`)

	cmd := newCmdWithGlobals()
	_ = cmd.Flags().Set("url", srv.URL)
	cmd.SetOut(&bytes.Buffer{})

	err := runAppList(cmd, nil)
	if err == nil {
		t.Fatal("expected an error for a 403 response, got nil")
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("error missing problem title: %v", err)
	}
}
