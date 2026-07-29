package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateBaseURL(t *testing.T) {
	valid := []string{
		"http://localhost:8090",
		"https://cloud.dbos.dev/conductor",
		"http://host/api/conductor",
	}
	for _, u := range valid {
		if err := validateBaseURL(u); err != nil {
			t.Errorf("validateBaseURL(%q) = %v, want nil", u, err)
		}
	}

	invalid := []string{
		"localhost:8090", // no scheme (parses as scheme "localhost")
		"ftp://host",     // unsupported scheme
		"http://",        // no host
		"://nope",        // unparseable
	}
	for _, u := range invalid {
		if err := validateBaseURL(u); err == nil {
			t.Errorf("validateBaseURL(%q) = nil, want error", u)
		}
	}
}

func TestNew(t *testing.T) {
	if _, err := New(Config{BaseURL: ""}); err == nil {
		t.Error("New with empty BaseURL = nil error, want error")
	}
	if _, err := New(Config{BaseURL: "not-a-url"}); err == nil {
		t.Error("New with invalid BaseURL = nil error, want error")
	}
	c, err := New(Config{BaseURL: "http://localhost:8090"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c == nil {
		t.Fatal("New returned a nil client")
	}
}

func TestBearerInjection(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	// With a token, every request carries the bearer header.
	c, err := New(Config{BaseURL: srv.URL, Token: "dbos_secret", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListAppsWithResponse(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	if want := "Bearer dbos_secret"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}

	// Without a token, no Authorization header is sent.
	gotAuth = ""
	c2, err := New(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.ListAppsWithResponse(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("no token should send no Authorization header, got %q", gotAuth)
	}
}
