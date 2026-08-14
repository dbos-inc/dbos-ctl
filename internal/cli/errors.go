package cli

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dbos-inc/dbos-ctl/internal/api"
)

// exitError carries a process exit code alongside its message. See the Exit
// codes section of AGENTS.md: 1 general, 2 usage, 3 auth-required, 4 not-found.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// exitCodeFor returns the process exit code for an error: an exitError's code
// (unwrapping through wraps), else 1.
func exitCodeFor(err error) int {
	var e *exitError
	if errors.As(err, &e) {
		return e.code
	}
	return 1
}

// checkStatus turns a non-2xx response into an apiError, for the empty-body
// (no typed JSON2xx) operations where success is just the status code.
func checkStatus(status int, resp *http.Response, problem *api.ErrorModel, body []byte) error {
	if status < 200 || status >= 300 {
		var h http.Header
		if resp != nil {
			h = resp.Header
		}
		return apiError(status, h, problem, body)
	}
	return nil
}

// apiError maps a non-2xx Conductor response to an error with a helpful message
// and the right exit code: 401 -> "run dbosctl login" (3), 404 -> (4), a past-limit
// 403 -> the upgrade hint, everything else -> (1). A Teams-only 403 needs no
// special case — conductor's problem+json detail already says so.
func apiError(status int, header http.Header, problem *api.ErrorModel, body []byte) error {
	msg := problemMessage(status, problem, body)
	switch status {
	case http.StatusUnauthorized:
		return &exitError{code: 3, msg: msg + " — run `dbosctl login`"}
	case http.StatusForbidden:
		// past_limit is the one machine-readable X-DBOS-Error value.
		if header.Get("X-DBOS-Error") == "past_limit" {
			return &exitError{code: 1, msg: msg + " — your organization is over its plan limit; upgrade to continue"}
		}
		return &exitError{code: 1, msg: msg}
	case http.StatusNotFound:
		return &exitError{code: 4, msg: msg}
	default:
		return &exitError{code: 1, msg: msg}
	}
}

// problemMessage extracts the best human message from a problem+json body,
// falling back to a non-JSON raw body, then the HTTP status text.
func problemMessage(status int, problem *api.ErrorModel, body []byte) string {
	if problem != nil {
		var parts []string
		if problem.Title != nil && *problem.Title != "" {
			parts = append(parts, *problem.Title)
		}
		if problem.Detail != nil && *problem.Detail != "" {
			parts = append(parts, *problem.Detail)
		}
		if len(parts) > 0 {
			return strings.Join(parts, ": ")
		}
	}
	if b := strings.TrimSpace(string(body)); b != "" && !strings.HasPrefix(b, "{") {
		return b
	}
	if t := http.StatusText(status); t != "" {
		return fmt.Sprintf("%s (HTTP %d)", t, status)
	}
	return fmt.Sprintf("HTTP %d", status)
}
