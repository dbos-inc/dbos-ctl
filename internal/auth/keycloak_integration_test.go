//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const keycloakImage = "quay.io/keycloak/keycloak:26.0"

// startKeycloak boots Keycloak with the dbos realm imported and returns its base
// URL. No secrets and no conductor are involved (tier 2).
func startKeycloak(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	realm, err := filepath.Abs(filepath.Join("testdata", "dbos-realm.json"))
	if err != nil {
		t.Fatal(err)
	}

	kc, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        keycloakImage,
			Cmd:          []string{"start-dev", "--import-realm"},
			ExposedPorts: []string{"8080/tcp"},
			Env: map[string]string{
				"KC_BOOTSTRAP_ADMIN_USERNAME": "admin",
				"KC_BOOTSTRAP_ADMIN_PASSWORD": "admin",
			},
			Files: []testcontainers.ContainerFile{{
				HostFilePath:      realm,
				ContainerFilePath: "/opt/keycloak/data/import/dbos-realm.json",
				FileMode:          0o644,
			}},
			WaitingFor: wait.ForHTTP("/realms/dbos/.well-known/openid-configuration").
				WithPort("8080/tcp").
				WithStatusCodeMatcher(func(status int) bool { return status == 200 }).
				WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start keycloak: %v", err)
	}
	t.Cleanup(func() { _ = kc.Terminate(ctx) })

	host, err := kc.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := kc.MappedPort(ctx, "8080/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

// TestDeviceFlowAgainstKeycloak exercises every part of our device flow that
// touches a real OIDC provider: discovery, the device-code request, and a poll
// that returns a real authorization_pending. The final approval is a human
// browser step (there is no API for it) and tests Keycloak's UI, not our code;
// the token-success path is covered by the mock in auth_test.go.
func TestDeviceFlowAgainstKeycloak(t *testing.T) {
	base := startKeycloak(t)
	issuer := base + "/realms/dbos"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	hc := http.DefaultClient
	cfg := Config{Issuer: issuer, ClientID: "dbos-cli", Scopes: []string{"openid"}}

	// Discovery against real Keycloak.
	prov, err := Discover(ctx, hc, issuer)
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	if !strings.Contains(prov.DeviceAuthorizationEndpoint, "/protocol/openid-connect/auth/device") {
		t.Errorf("unexpected device endpoint: %s", prov.DeviceAuthorizationEndpoint)
	}
	if !strings.HasSuffix(prov.TokenEndpoint, "/protocol/openid-connect/token") {
		t.Errorf("unexpected token endpoint: %s", prov.TokenEndpoint)
	}

	// Real device-code request.
	da, err := requestDeviceCode(ctx, hc, prov, cfg)
	if err != nil {
		t.Fatalf("device code request: %v", err)
	}
	if da.DeviceCode == "" || da.UserCode == "" || da.VerificationURI == "" {
		t.Fatalf("incomplete device authorization: %+v", da)
	}

	// Poll once, before approval: Keycloak must return authorization_pending,
	// which our parser treats as "keep polling" (retry, no error, no token).
	interval := da.Interval
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("device_code", da.DeviceCode)
	form.Set("client_id", cfg.ClientID)
	resp, err := postForm(ctx, hc, prov.TokenEndpoint, form)
	if err != nil {
		t.Fatalf("token poll: %v", err)
	}
	tok, retry, err := parseTokenResponse(resp, &interval)
	if err != nil {
		t.Fatalf("token poll returned an error, want authorization_pending: %v", err)
	}
	if tok != nil {
		t.Fatal("unexpectedly received a token before approval")
	}
	if !retry {
		t.Fatal("Keycloak did not return authorization_pending")
	}
	t.Log("real Keycloak: discovery + device-code request + authorization_pending all OK")

	// Also confirm the realm issues a real signed token end to end. The device
	// grant's approval is browser-only with no API, so we obtain the token via
	// the password grant (conductor's approach). This does not run through our
	// device-flow code — it is a provider/fixture smoke test — but it proves the
	// discovered token endpoint, realm, client, and user actually mint a JWT.
	pw := url.Values{}
	pw.Set("grant_type", "password")
	pw.Set("client_id", cfg.ClientID)
	pw.Set("username", "testuser")
	pw.Set("password", "testpass")
	pw.Set("scope", "openid")
	pwResp, err := postForm(ctx, hc, prov.TokenEndpoint, pw)
	if err != nil {
		t.Fatalf("password grant: %v", err)
	}
	pwBody, _ := io.ReadAll(pwResp.Body)
	pwResp.Body.Close()
	if pwResp.StatusCode != http.StatusOK {
		t.Fatalf("password grant: %s: %s", pwResp.Status, strings.TrimSpace(string(pwBody)))
	}
	var minted struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(pwBody, &minted); err != nil {
		t.Fatal(err)
	}
	// A signed JWT is header.payload.signature — three dot-separated segments.
	if strings.Count(minted.AccessToken, ".") != 2 {
		t.Fatalf("expected a signed JWT, got %q", minted.AccessToken)
	}
	t.Logf("real Keycloak minted a signed JWT (%d bytes)", len(minted.AccessToken))
}
