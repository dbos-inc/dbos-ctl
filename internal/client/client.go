// Package client builds a configured Conductor API client from a resolved base
// URL. This is the no-auth path; bearer injection for authenticated
// deployments is added by the auth milestone via a request editor, which is why
// New already funnels through a single construction point.
package client

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dbos-inc/dbos-cli/internal/api"
)

// defaultTimeout bounds a single API call.
const defaultTimeout = 30 * time.Second

// Config configures the Conductor API client.
type Config struct {
	// BaseURL is the full API base, including any reverse-proxy prefix
	// (/conductor, /api/conductor, or none — see the Container topology section
	// of AGENTS.md). It is used verbatim as the client's Server, so the
	// generated /v2/... operation paths land correctly.
	BaseURL string

	// HTTPClient, when set, replaces the default HTTP client — tests point it at
	// an httptest server; production leaves it nil.
	HTTPClient api.HttpRequestDoer
}

// New builds a Conductor API client. It performs no authentication.
func New(cfg Config) (*api.ClientWithResponses, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return nil, fmt.Errorf("no Conductor URL configured (pass --url or set $DBOS_URL)")
	}
	if err := validateBaseURL(base); err != nil {
		return nil, err
	}

	doer := cfg.HTTPClient
	if doer == nil {
		doer = &http.Client{Timeout: defaultTimeout}
	}
	return api.NewClientWithResponses(base, api.WithHTTPClient(doer))
}

// validateBaseURL rejects obviously-unusable URLs up front, so the failure
// names the URL instead of surfacing later as an opaque request error.
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid Conductor URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid Conductor URL %q: want an http:// or https:// URL", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid Conductor URL %q: missing host", raw)
	}
	return nil
}
