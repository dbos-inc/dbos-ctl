// Package client builds a configured Conductor API client from a resolved base
// URL and optional bearer token. A single construction point attaches the
// Authorization header via a request editor when a token is present.
package client

import (
	"context"
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

	// Token, when non-empty, is sent as `Authorization: Bearer <token>` on every
	// request. Empty means no authentication (selfhosted). The value is either an
	// OIDC access token or a dbos_ API key — both are bearer tokens to conductor.
	Token string

	// HTTPClient, when set, replaces the default HTTP client — tests point it at
	// an httptest server; production leaves it nil.
	HTTPClient api.HttpRequestDoer
}

// New builds a Conductor API client, attaching a bearer token when one is set.
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

	opts := []api.ClientOption{api.WithHTTPClient(doer)}
	if cfg.Token != "" {
		token := cfg.Token
		opts = append(opts, api.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		}))
	}
	return api.NewClientWithResponses(base, opts...)
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
