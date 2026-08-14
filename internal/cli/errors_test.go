package cli

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/dbos-inc/dbos-ctl/internal/api"
)

func ptr(s string) *string { return &s }

func TestApiError401(t *testing.T) {
	err := apiError(http.StatusUnauthorized, http.Header{}, nil, nil)
	if exitCodeFor(err) != 3 {
		t.Errorf("401 exit code = %d, want 3", exitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "dbosctl login") {
		t.Errorf("401 message missing the login hint: %v", err)
	}
}

func TestApiError403PastLimit(t *testing.T) {
	h := http.Header{}
	h.Set("X-DBOS-Error", "past_limit")
	err := apiError(http.StatusForbidden, h, &api.ErrorModel{Title: ptr("Forbidden"), Detail: ptr("over limit")}, nil)
	if exitCodeFor(err) != 1 {
		t.Errorf("past_limit exit = %d, want 1", exitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "plan limit") {
		t.Errorf("past_limit message missing the upgrade hint: %v", err)
	}
}

func TestApiError403Teams(t *testing.T) {
	// A Teams-plan 403 carries no header — the problem detail is the message.
	err := apiError(http.StatusForbidden, http.Header{},
		&api.ErrorModel{Title: ptr("Forbidden"), Detail: ptr("this endpoint requires a DBOS Teams subscription")}, nil)
	if exitCodeFor(err) != 1 {
		t.Errorf("teams 403 exit = %d, want 1", exitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "Teams subscription") {
		t.Errorf("teams 403 should surface the detail: %v", err)
	}
	if strings.Contains(err.Error(), "plan limit") {
		t.Errorf("teams 403 should not get the past-limit hint: %v", err)
	}
}

func TestApiError404(t *testing.T) {
	if got := exitCodeFor(apiError(http.StatusNotFound, http.Header{}, nil, nil)); got != 4 {
		t.Errorf("404 exit = %d, want 4", got)
	}
}

func TestApiErrorProblemMessage(t *testing.T) {
	err := apiError(500, http.Header{}, &api.ErrorModel{Title: ptr("Internal Server Error"), Detail: ptr("boom")}, nil)
	if got := err.Error(); got != "Internal Server Error: boom" {
		t.Errorf("message = %q, want \"title: detail\"", got)
	}
	if exitCodeFor(err) != 1 {
		t.Errorf("500 exit = %d, want 1", exitCodeFor(err))
	}
}

func TestApiErrorRawBodyFallback(t *testing.T) {
	err := apiError(502, http.Header{}, nil, []byte("upstream down"))
	if !strings.Contains(err.Error(), "upstream down") {
		t.Errorf("should fall back to the raw body: %v", err)
	}
}

func TestExitCodeFor(t *testing.T) {
	if exitCodeFor(errors.New("plain")) != 1 {
		t.Error("a plain error should be exit 1")
	}
	if exitCodeFor(&exitError{code: 4, msg: "x"}) != 4 {
		t.Error("exitError should return its code")
	}
	if got := exitCodeFor(fmt.Errorf("context: %w", &exitError{code: 3, msg: "auth"})); got != 3 {
		t.Errorf("a wrapped exitError should return its code, got %d", got)
	}
}
