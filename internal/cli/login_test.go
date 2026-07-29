package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-cli/internal/config"
	"github.com/dbos-inc/dbos-cli/internal/creds"
)

func TestBearerTokenDBOSTokenEnv(t *testing.T) {
	isolateConfig(t)
	t.Setenv("DBOS_TOKEN", "dbos_fromenv")
	got, err := bearerToken(newCmdWithGlobals(), config.Settings{Auth: config.AuthBearer, Profile: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "dbos_fromenv" {
		t.Errorf("got %q, want the env token", got)
	}
}

func TestBearerTokenNoAuth(t *testing.T) {
	isolateConfig(t)
	got, err := bearerToken(newCmdWithGlobals(), config.Settings{Auth: config.AuthNone})
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("no-auth should yield no token, got %q", got)
	}
}

func TestBearerTokenStoredKey(t *testing.T) {
	isolateConfig(t)
	store, err := creds.NewFileStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("p", &creds.Creds{Token: "dbos_stored"}); err != nil {
		t.Fatal(err)
	}
	got, err := bearerToken(newCmdWithGlobals(), config.Settings{Auth: config.AuthBearer, Profile: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "dbos_stored" {
		t.Errorf("got %q, want the stored key", got)
	}
}

func TestBearerTokenNotLoggedIn(t *testing.T) {
	isolateConfig(t)
	_, err := bearerToken(newCmdWithGlobals(), config.Settings{Auth: config.AuthBearer, Profile: "p"})
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("want a not-logged-in error, got %v", err)
	}
}

// oidcMock serves discovery + device + token for the login/refresh tests.
func oidcMock(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			base := "http://" + r.Host
			fmt.Fprintf(w, `{"issuer":%q,"device_authorization_endpoint":%q,"token_endpoint":%q}`, base, base+"/device", base+"/token")
		case strings.HasSuffix(r.URL.Path, "/device"):
			io.WriteString(w, `{"device_code":"DC","user_code":"WXYZ","verification_uri":"http://verify","verification_uri_complete":"http://verify?c=WXYZ","expires_in":600,"interval":1}`)
		case strings.HasSuffix(r.URL.Path, "/token"):
			_ = r.ParseForm()
			switch r.Form.Get("grant_type") {
			case "refresh_token":
				if r.Form.Get("refresh_token") == "rt" {
					io.WriteString(w, `{"access_token":"NEWJWT","refresh_token":"rt2","token_type":"Bearer","expires_in":3600}`)
				} else {
					w.WriteHeader(400)
					io.WriteString(w, `{"error":"invalid_grant"}`)
				}
			default: // device_code
				io.WriteString(w, `{"access_token":"JWT","refresh_token":"RT","token_type":"Bearer","expires_in":3600}`)
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBearerTokenRefresh(t *testing.T) {
	isolateConfig(t)
	srv := oidcMock(t)

	store, err := creds.NewFileStore()
	if err != nil {
		t.Fatal(err)
	}
	// An expired access token with a refresh token.
	if err := store.Save("p", &creds.Creds{Token: "OLDJWT", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}

	s := config.Settings{Auth: config.AuthBearer, Profile: "p", OIDC: &config.OIDC{Issuer: srv.URL, ClientID: "dbos-cli"}}
	got, err := bearerToken(newCmdWithGlobals(), s)
	if err != nil {
		t.Fatal(err)
	}
	if got != "NEWJWT" {
		t.Errorf("got %q, want the refreshed token", got)
	}
	// The rotated access + refresh tokens are persisted.
	reloaded, err := store.Load("p")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Token != "NEWJWT" || reloaded.RefreshToken != "rt2" {
		t.Errorf("refresh not persisted: %+v", reloaded)
	}
}

func TestRunLoginLogout(t *testing.T) {
	isolateConfig(t)
	srv := oidcMock(t)

	// A bearer profile whose OIDC points at the mock issuer.
	f := &config.File{Current: "p", Profiles: map[string]config.Profile{
		"p": {Auth: config.AuthBearer, URL: "http://localhost:8090", OIDC: &config.OIDC{Issuer: srv.URL, ClientID: "dbos-cli"}},
	}}
	if err := config.Save(f); err != nil {
		t.Fatal(err)
	}

	cmd := newCmdWithGlobals()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runLogin(cmd, nil); err != nil {
		t.Fatalf("login: %v", err)
	}

	store, err := creds.NewFileStore()
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Load("p")
	if err != nil {
		t.Fatalf("token not stored after login: %v", err)
	}
	if c.Token != "JWT" || c.RefreshToken != "RT" || c.ExpiresAt == 0 {
		t.Errorf("stored creds wrong: %+v", c)
	}

	if err := runLogout(cmd, nil); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := store.Load("p"); err == nil {
		t.Error("creds should be gone after logout")
	}
}
