package client

import "testing"

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
