package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/check-spec-drift.sh is the only thing that notices when the vendored
// spec stops matching the deployed API, so its own failure modes matter: a
// regression that made it always report "match" would silently retire the check
// rather than break loudly. These tests pin the two behaviors that could do
// that — drift is detected, and an unreachable server is an error rather than a
// pass — plus the normalization that keeps it from crying wolf every day.
//
// Hermetic: the deployed spec comes from an httptest server, never the network.

const scriptPath = "../../scripts/check-spec-drift.sh"

// specJSON builds a minimal spec with the given servers URL and operations,
// each given as "method path".
func specJSON(serverURL string, operations ...string) string {
	paths := map[string][]string{}
	for _, op := range operations {
		method, path, _ := strings.Cut(op, " ")
		paths[path] = append(paths[path], method)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `{"openapi":"3.1.0","servers":[{"url":%q}],"paths":{`, serverURL)
	first := true
	for path, methods := range paths {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, "%q:{", path)
		for i, m := range methods {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `%q:{"operationId":"op"}`, m)
		}
		b.WriteString("}")
	}
	b.WriteString("}}")
	return b.String()
}

// runDriftCheck runs the script against a vendored spec on disk and a deployed
// spec served over HTTP, returning its exit code and combined output.
func runDriftCheck(t *testing.T, vendored, deployed string) (int, string) {
	t.Helper()
	for _, tool := range []string{"jq", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed; the drift script needs it", tool)
		}
	}

	vendoredPath := filepath.Join(t.TempDir(), "openapi.json")
	if err := os.WriteFile(vendoredPath, []byte(vendored), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(deployed))
	}))
	t.Cleanup(srv.Close)

	return runDriftCheckAt(t, vendoredPath, srv.URL)
}

func runDriftCheckAt(t *testing.T, vendoredPath, specURL string) (int, string) {
	t.Helper()
	cmd := exec.Command(scriptPath)
	cmd.Env = append(os.Environ(), "VENDORED="+vendoredPath, "CLOUD_SPEC_URL="+specURL)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("running %s: %v\n%s", scriptPath, err, out)
	return 0, ""
}

func TestSpecDriftMatch(t *testing.T) {
	spec := specJSON("/", "get /v2/orgs/{orgName}/apps")
	code, out := runDriftCheck(t, spec, spec)
	if code != 0 {
		t.Errorf("identical specs: exit %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "match") {
		t.Errorf("identical specs: output does not report a match:\n%s", out)
	}
}

// Cloud repoints `servers` at /conductor on every response, so if the script
// stopped excluding that field it would report drift every single day and the
// daily job would be trained-away noise rather than a signal.
func TestSpecDriftIgnoresServers(t *testing.T) {
	const op = "get /v2/orgs/{orgName}/apps"
	code, out := runDriftCheck(t, specJSON("/", op), specJSON("/conductor", op))
	if code != 0 {
		t.Errorf("specs differing only in servers: exit %d, want 0\n%s", code, out)
	}
}

func TestSpecDriftDetectsNewOperation(t *testing.T) {
	const existing = "get /v2/orgs/{orgName}/apps"
	code, out := runDriftCheck(t,
		specJSON("/", existing),
		specJSON("/conductor", existing, "get /v2/orgs/{orgName}/apps/{appName}/autoscale"),
	)
	if code != 1 {
		t.Errorf("deployed spec with an extra operation: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "DRIFT") {
		t.Errorf("drift not reported:\n%s", out)
	}
	// The operation-level summary is what makes the failure actionable.
	if !strings.Contains(out, "autoscale") {
		t.Errorf("output does not name the operation that differs:\n%s", out)
	}
}

func TestSpecDriftDetectsChangedSchema(t *testing.T) {
	const op = "get /v2/orgs/{orgName}/apps"
	vendored := specJSON("/", op)
	deployed := strings.Replace(specJSON("/conductor", op), `"operationId":"op"`, `"operationId":"op","deprecated":true`, 1)
	code, out := runDriftCheck(t, vendored, deployed)
	if code != 1 {
		t.Errorf("same operations but a changed schema: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "operation list is identical") {
		t.Errorf("output does not explain that only schemas differ:\n%s", out)
	}
}

// An unreachable server must fail, not pass: a check that reports success when
// it cannot reach the API is worse than no check at all.
func TestSpecDriftUnreachableServerFails(t *testing.T) {
	for _, tool := range []string{"jq", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed; the drift script needs it", tool)
		}
	}
	vendoredPath := filepath.Join(t.TempDir(), "openapi.json")
	if err := os.WriteFile(vendoredPath, []byte(specJSON("/")), 0o600); err != nil {
		t.Fatal(err)
	}
	// A server that is closed before use: its URL is well-formed but refuses.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	code, out := runDriftCheckAt(t, vendoredPath, url)
	if code == 0 {
		t.Errorf("unreachable spec URL: exit 0, want non-zero\n%s", out)
	}
	if !strings.Contains(out, "could not fetch") {
		t.Errorf("output does not explain the fetch failure:\n%s", out)
	}
}

// A missing vendored spec is a broken checkout, not a match.
func TestSpecDriftMissingVendoredFails(t *testing.T) {
	for _, tool := range []string{"jq", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed; the drift script needs it", tool)
		}
	}
	code, out := runDriftCheckAt(t, filepath.Join(t.TempDir(), "absent.json"), "http://127.0.0.1:1")
	if code == 0 {
		t.Errorf("missing vendored spec: exit 0, want non-zero\n%s", out)
	}
	if !strings.Contains(out, "not found") {
		t.Errorf("output does not explain the missing file:\n%s", out)
	}
}
