package auth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// shrinkPollInterval makes the poll unit tiny so tests don't wait real seconds.
func shrinkPollInterval(t *testing.T) {
	old := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = old })
}

type tokenStep func(w http.ResponseWriter)

func stepPending(w http.ResponseWriter) {
	w.WriteHeader(400)
	io.WriteString(w, `{"error":"authorization_pending"}`)
}
func stepSlowDown(w http.ResponseWriter) {
	w.WriteHeader(400)
	io.WriteString(w, `{"error":"slow_down"}`)
}
func stepDenied(w http.ResponseWriter) {
	w.WriteHeader(400)
	io.WriteString(w, `{"error":"access_denied"}`)
}
func stepSuccess(w http.ResponseWriter) {
	io.WriteString(w, `{"access_token":"AT","refresh_token":"RT","token_type":"Bearer","expires_in":3600}`)
}

// newMockOIDC serves discovery + device + token, playing the token steps in
// order (the last repeats).
func newMockOIDC(t *testing.T, steps ...tokenStep) *httptest.Server {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		fmt.Fprintf(w, `{"issuer":%q,"device_authorization_endpoint":%q,"token_endpoint":%q}`, base, base+"/device", base+"/token")
	})
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"device_code":"DC","user_code":"WXYZ","verification_uri":"http://verify","verification_uri_complete":"http://verify?code=WXYZ","expires_in":600,"interval":1}`)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		if n >= len(steps) {
			n = len(steps) - 1
		}
		steps[n](w)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func login(srv *httptest.Server) (*Token, DeviceAuth, error) {
	var prompted DeviceAuth
	tok, err := Login(context.Background(), Config{
		Issuer:     srv.URL,
		ClientID:   "dbos-cli",
		HTTPClient: srv.Client(),
	}, func(da DeviceAuth) { prompted = da })
	return tok, prompted, err
}

func TestLoginSuccess(t *testing.T) {
	shrinkPollInterval(t)
	srv := newMockOIDC(t, stepPending, stepPending, stepSuccess)

	tok, prompted, err := login(srv)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "AT" || tok.RefreshToken != "RT" || tok.ExpiresIn != 3600 {
		t.Errorf("unexpected token: %+v", tok)
	}
	if prompted.UserCode != "WXYZ" || prompted.VerificationURIComplete == "" {
		t.Errorf("prompt did not receive device details: %+v", prompted)
	}
}

func TestLoginSlowDown(t *testing.T) {
	shrinkPollInterval(t)
	srv := newMockOIDC(t, stepSlowDown, stepSuccess)
	tok, _, err := login(srv)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "AT" {
		t.Errorf("slow_down should be retried; got %+v", tok)
	}
}

func TestLoginDenied(t *testing.T) {
	shrinkPollInterval(t)
	srv := newMockOIDC(t, stepDenied)
	if _, _, err := login(srv); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("access_denied should surface an error, got %v", err)
	}
}

func TestLoginContextCancel(t *testing.T) {
	shrinkPollInterval(t)
	srv := newMockOIDC(t, stepPending) // never approves

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Login(ctx, Config{Issuer: srv.URL, ClientID: "dbos-cli", HTTPClient: srv.Client()}, nil)
	if err == nil {
		t.Fatal("expected a context error, got nil")
	}
}

func TestDiscoverMissingDeviceEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"issuer":"x","token_endpoint":"x/token"}`) // no device endpoint
	}))
	t.Cleanup(srv.Close)
	if _, err := Discover(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Error("Discover should fail when no device endpoint is advertised")
	}
}
