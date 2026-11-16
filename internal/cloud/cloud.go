// Package cloud calls the DBOS Cloud API: the routes cloud.dbos.dev serves
// itself, as opposed to the Conductor API it proxies under /conductor (which
// internal/api covers). Cloud publishes no OpenAPI spec for these routes, so the
// few dbosctl uses are written out by hand.
package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultTimeout bounds a single API call, matching the Conductor client.
const defaultTimeout = 30 * time.Second

// maxBody caps how much of a response is read. Every body these routes return
// is a small JSON object, so this only guards against a misbehaving proxy.
const maxBody = 1 << 20

// Config configures a Client.
type Config struct {
	// BaseURL is the cloud origin, e.g. https://cloud.dbos.dev, with no path.
	BaseURL string
	// Token is sent as `Authorization: Bearer <token>`. Cloud's own routes
	// accept only a user's Auth0 access token, not a dbos_ API key.
	Token string
	// HTTPClient, when set, replaces the default HTTP client.
	HTTPClient *http.Client
}

// Client calls the DBOS Cloud API.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// New builds a Client.
func New(cfg Config) *Client {
	h := cfg.HTTPClient
	if h == nil {
		h = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{base: strings.TrimRight(cfg.BaseURL, "/"), token: cfg.Token, http: h}
}

// LicenseKey is the part of a newly issued license key dbosctl renders; the
// raw response carries more (key ID, type, usage count).
type LicenseKey struct {
	Key            string     `json:"key"`
	ExpirationDate *time.Time `json:"expiration_date"`
}

// CreateLicenseKey issues a self-hosted Conductor license key to org. Cloud
// decides the key's type and lifetime; the request has no body. It returns the
// decoded key and the raw response, so a caller can print the API's shape
// unaltered.
func (c *Client) CreateLicenseKey(ctx context.Context, org string) (*LicenseKey, json.RawMessage, error) {
	path := "/v1alpha1/" + url.PathEscape(org) + "/conductor-api-keys"
	body, err := c.do(ctx, http.MethodPost, path)
	if err != nil {
		return nil, nil, err
	}
	var k LicenseKey
	if err := json.Unmarshal(body, &k); err != nil {
		return nil, nil, fmt.Errorf("decoding license key response: %w", err)
	}
	if k.Key == "" {
		return nil, nil, fmt.Errorf("cloud returned no license key")
	}
	return &k, json.RawMessage(body), nil
}

// do sends a bodiless request and returns the body of a 2xx response, or an
// *Error for anything else.
func (c *Client) do(ctx context.Context, method, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newError(resp.StatusCode, body)
	}
	return body, nil
}

// Error is a non-2xx response from cloud.
type Error struct {
	StatusCode int
	Message    string
	RequestID  string
}

func (e *Error) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("%s (request ID %s)", e.Message, e.RequestID)
	}
	return e.Message
}

// newError reads cloud's error body, {"message", "statusCode", "requestID"},
// falling back to a non-JSON body (a gateway's, say), then the status text.
func newError(status int, body []byte) *Error {
	e := &Error{StatusCode: status}
	var wire struct {
		Message   string `json:"message"`
		RequestID string `json:"requestID"`
	}
	if json.Unmarshal(body, &wire) == nil && wire.Message != "" {
		e.Message, e.RequestID = wire.Message, wire.RequestID
		return e
	}
	if b := strings.TrimSpace(string(body)); b != "" && !strings.HasPrefix(b, "{") && !strings.HasPrefix(b, "<") {
		e.Message = b
		return e
	}
	if t := http.StatusText(status); t != "" {
		e.Message = fmt.Sprintf("%s (HTTP %d)", t, status)
	} else {
		e.Message = fmt.Sprintf("HTTP %d", status)
	}
	return e
}
