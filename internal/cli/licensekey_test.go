package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/config"
	"github.com/dbos-inc/dbos-ctl/internal/creds"
)

const licenseKeyJSON = `{"key_id":"k1","key":"lk-secret","org_id":"o1","key_type":"free","created_at":"2026-11-11T10:00:00Z","expiration_date":"2026-11-18T10:00:00Z","tag":null,"usage_count":0}`

// isolateLicenseKey isolates config like isolateConfig and also HOME, since
// os.UserConfigDir ignores XDG_CONFIG_HOME on macOS. It writes a current managed
// profile "m" with a stored login for org "acme", and points cloud at srv.
func isolateLicenseKey(t *testing.T, srv *httptest.Server) {
	t.Helper()
	isolateConfig(t)
	t.Setenv("HOME", t.TempDir())
	f := &config.File{Current: "m", Profiles: map[string]config.Profile{
		"m": {Domain: "cloud.example.test"},
	}}
	if err := config.Save(f); err != nil {
		t.Fatal(err)
	}
	store, err := creds.NewFileStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("m", &creds.Creds{Token: "jwt", Organization: "acme"}); err != nil {
		t.Fatal(err)
	}
	orig := cloudBaseURL
	cloudBaseURL = func(domain string) string {
		if domain != "cloud.example.test" {
			t.Errorf("cloud URL derived from domain %q, want the profile's", domain)
		}
		return srv.URL
	}
	t.Cleanup(func() { cloudBaseURL = orig })
}

// licenseKeyServer serves the create route for org, replying with status and
// body, and counts the requests it receives.
func licenseKeyServer(t *testing.T, org string, status int, body string) (*httptest.Server, *int) {
	t.Helper()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1alpha1/"+org+"/conductor-api-keys" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer jwt" {
			t.Errorf("Authorization = %q, want the stored login", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// newLicenseKeyCmd builds a command with the flags license-key create declares.
func newLicenseKeyCmd(t *testing.T) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	c := &cobra.Command{}
	addRequestFlags(c, "profile", "org", "output")
	c.SetContext(context.Background())
	var out, errBuf bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errBuf)
	return c, &out, &errBuf
}

func TestRunLicenseKeyCreate(t *testing.T) {
	srv, calls := licenseKeyServer(t, "acme", http.StatusCreated, licenseKeyJSON)
	isolateLicenseKey(t, srv)

	cmd, out, errBuf := newLicenseKeyCmd(t)
	if err := runLicenseKeyCreate(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Errorf("cloud received %d requests, want 1", *calls)
	}
	// The bare key goes to stdout (capturable), nothing else.
	if got := out.String(); got != "lk-secret\n" {
		t.Errorf("stdout = %q, want the bare key", got)
	}
	for _, want := range []string{"not be shown again", `"acme"`, "2026-11-18T10:00:00Z", "DBOS_CONDUCTOR_LICENSE_KEY"} {
		if !strings.Contains(errBuf.String(), want) {
			t.Errorf("stderr missing %q:\n%s", want, errBuf.String())
		}
	}
}

func TestRunLicenseKeyCreateJSON(t *testing.T) {
	srv, _ := licenseKeyServer(t, "acme", http.StatusCreated, licenseKeyJSON)
	isolateLicenseKey(t, srv)

	cmd, out, errBuf := newLicenseKeyCmd(t)
	_ = cmd.Flags().Set("output", "json")
	if err := runLicenseKeyCreate(cmd, nil); err != nil {
		t.Fatal(err)
	}
	// The raw API shape, including the fields the table path does not use.
	for _, want := range []string{`"key": "lk-secret"`, `"key_id": "k1"`, `"usage_count": 0`, `"tag": null`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("json output missing %s:\n%s", want, out.String())
		}
	}
	if errBuf.Len() != 0 {
		t.Errorf("json mode wrote to stderr: %q", errBuf.String())
	}
}

func TestRunLicenseKeyCreateOrgFlag(t *testing.T) {
	srv, calls := licenseKeyServer(t, "other", http.StatusCreated, licenseKeyJSON)
	isolateLicenseKey(t, srv)

	cmd, _, _ := newLicenseKeyCmd(t)
	_ = cmd.Flags().Set("org", "other")
	if err := runLicenseKeyCreate(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Errorf("cloud received %d requests, want 1", *calls)
	}
}

// A profile that is not DBOS-managed is refused before anything is sent: only
// cloud issues license keys.
func TestRunLicenseKeyCreateRequiresManaged(t *testing.T) {
	srv, calls := licenseKeyServer(t, "acme", http.StatusCreated, licenseKeyJSON)
	isolateLicenseKey(t, srv)
	f := &config.File{Current: "m", Profiles: map[string]config.Profile{
		"m":    {Domain: "cloud.example.test"},
		"self": {URL: "http://localhost:8090"},
	}}
	if err := config.Save(f); err != nil {
		t.Fatal(err)
	}

	cmd, _, _ := newLicenseKeyCmd(t)
	_ = cmd.Flags().Set("profile", "self")
	err := runLicenseKeyCreate(cmd, nil)
	if err == nil {
		t.Fatal("expected an error for a self-hosted profile")
	}
	for _, want := range []string{`profile "self" is not DBOS-managed`, "--managed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	if *calls != 0 {
		t.Errorf("cloud received %d requests, want none", *calls)
	}
}

func TestRunLicenseKeyCreateNoProfile(t *testing.T) {
	srv, calls := licenseKeyServer(t, "acme", http.StatusCreated, licenseKeyJSON)
	isolateLicenseKey(t, srv)
	if err := config.Save(&config.File{}); err != nil {
		t.Fatal(err)
	}

	cmd, _, _ := newLicenseKeyCmd(t)
	err := runLicenseKeyCreate(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "no profile is selected") {
		t.Fatalf("err = %v, want the no-profile error", err)
	}
	if *calls != 0 {
		t.Errorf("cloud received %d requests, want none", *calls)
	}
}

// Cloud's own routes take only a user login, so an API key is refused locally
// rather than relayed as a 401 that reads as an expired session.
func TestRunLicenseKeyCreateRejectsAPIKey(t *testing.T) {
	srv, calls := licenseKeyServer(t, "acme", http.StatusCreated, licenseKeyJSON)
	isolateLicenseKey(t, srv)
	t.Setenv("DBOS_TOKEN", "dbos_abc")

	cmd, _, _ := newLicenseKeyCmd(t)
	err := runLicenseKeyCreate(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "DBOS_TOKEN") {
		t.Fatalf("err = %v, want the API-key error naming $DBOS_TOKEN", err)
	}
	if code := exitCodeFor(err); code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if *calls != 0 {
		t.Errorf("cloud received %d requests, want none", *calls)
	}
}

func TestRunLicenseKeyCreateErrors(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		want     []string
		wantCode int
	}{
		{
			name:     "plan limit",
			status:   http.StatusInternalServerError,
			body:     `{"message":"you have reached your conductor API key limit, please contact support@dbos.dev to request additional keys","statusCode":500,"requestID":"req-1"}`,
			want:     []string{"reached your conductor API key limit", "request ID req-1"},
			wantCode: 1,
		},
		{
			name:     "unauthorized",
			status:   http.StatusUnauthorized,
			body:     `{"message":"invalid token","statusCode":401,"requestID":""}`,
			want:     []string{"invalid token", "dbosctl login"},
			wantCode: 3,
		},
		{
			name:     "gateway",
			status:   http.StatusBadGateway,
			body:     `<html>bad gateway</html>`,
			want:     []string{"Bad Gateway (HTTP 502)"},
			wantCode: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := licenseKeyServer(t, "acme", tc.status, tc.body)
			isolateLicenseKey(t, srv)

			cmd, out, _ := newLicenseKeyCmd(t)
			err := runLicenseKeyCreate(cmd, nil)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
			if code := exitCodeFor(err); code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}
			if out.Len() != 0 {
				t.Errorf("stdout should be empty on failure, got %q", out.String())
			}
		})
	}
}

// The command takes no --url: the request goes to cloud, not to the Conductor
// URL the flag would override.
func TestLicenseKeyCreateFlags(t *testing.T) {
	for _, name := range []string{"profile", "org", "output"} {
		if licenseKeyCreateCmd.Flags().Lookup(name) == nil {
			t.Errorf("license-key create is missing --%s", name)
		}
	}
	if licenseKeyCreateCmd.Flags().Lookup("url") != nil {
		t.Error("license-key create should not take --url")
	}
}
