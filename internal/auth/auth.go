// Package auth implements the OAuth 2.0 Device Authorization Grant (RFC 8628)
// over OIDC discovery: discover the issuer's endpoints, request a device code,
// prompt the user to approve in a browser, then poll for the token. One code
// path serves any OIDC provider with discovery and the device grant — e.g.
// Keycloak or Okta for self-hosted, Auth0 for DBOS-managed.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// pollInterval is the unit the device-code interval multiplies; a package var so
// tests can shrink it. Production leaves it at one second.
var pollInterval = time.Second

// Provider holds the OIDC endpoints discovered from an issuer.
type Provider struct {
	Issuer                      string
	DeviceAuthorizationEndpoint string
	TokenEndpoint               string
}

// Discover fetches {issuer}/.well-known/openid-configuration.
func Discover(ctx context.Context, hc *http.Client, issuer string) (*Provider, error) {
	endpoint := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery: %s returned %s", endpoint, resp.Status)
	}
	var doc struct {
		Issuer                      string `json:"issuer"`
		DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
		TokenEndpoint               string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("OIDC discovery: decoding %s: %w", endpoint, err)
	}
	if doc.DeviceAuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return nil, fmt.Errorf("issuer %q does not advertise a device authorization endpoint", issuer)
	}
	return &Provider{
		Issuer:                      doc.Issuer,
		DeviceAuthorizationEndpoint: doc.DeviceAuthorizationEndpoint,
		TokenEndpoint:               doc.TokenEndpoint,
	}, nil
}

// DeviceAuth is what the user must act on to approve the login.
type DeviceAuth struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int
	Interval                int
}

// Token is a successful device-flow result.
type Token struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int
}

// Config configures a device-flow login.
type Config struct {
	Issuer     string
	ClientID   string
	Audience   string   // optional; Auth0 requires it, Keycloak ignores it
	Scopes     []string // e.g. "openid", "offline_access"
	HTTPClient *http.Client
}

// Login runs the full device flow: discover → request device code → prompt →
// poll → token. prompt is called once with the verification details so the
// caller can tell the user where to approve.
func Login(ctx context.Context, cfg Config, prompt func(DeviceAuth)) (*Token, error) {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	prov, err := Discover(ctx, hc, cfg.Issuer)
	if err != nil {
		return nil, err
	}
	da, err := requestDeviceCode(ctx, hc, prov, cfg)
	if err != nil {
		return nil, err
	}
	if prompt != nil {
		prompt(*da)
	}
	return pollToken(ctx, hc, prov, cfg, da)
}

// Refresh exchanges a refresh token for a fresh access token (RFC 6749 §6),
// rediscovering the token endpoint from the issuer. A rotated refresh token, if
// the provider returns one, is on the result.
func Refresh(ctx context.Context, cfg Config, refreshToken string) (*Token, error) {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	prov, err := Discover(ctx, hc, cfg.Issuer)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", cfg.ClientID)
	resp, err := postForm(ctx, hc, prov.TokenEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}
	unused := 0
	tok, _, err := parseTokenResponse(resp, &unused)
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, errors.New("refresh did not return a token")
	}
	return tok, nil
}

func requestDeviceCode(ctx context.Context, hc *http.Client, prov *Provider, cfg Config) (*DeviceAuth, error) {
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	if len(cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	if cfg.Audience != "" {
		form.Set("audience", cfg.Audience)
	}
	resp, err := postForm(ctx, hc, prov.DeviceAuthorizationEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("requesting device code: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	if out.DeviceCode == "" {
		return nil, errors.New("device authorization response carried no device_code")
	}
	return &DeviceAuth{
		DeviceCode:              out.DeviceCode,
		UserCode:                out.UserCode,
		VerificationURI:         out.VerificationURI,
		VerificationURIComplete: out.VerificationURIComplete,
		ExpiresIn:               out.ExpiresIn,
		Interval:                out.Interval,
	}, nil
}

func pollToken(ctx context.Context, hc *http.Client, prov *Provider, cfg Config, da *DeviceAuth) (*Token, error) {
	interval := da.Interval
	if interval <= 0 {
		interval = 5 // RFC 8628 default
	}
	expiresIn := da.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 600
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(interval) * pollInterval):
		}
		if time.Now().After(deadline) {
			return nil, errors.New("device code expired before it was approved")
		}

		form := url.Values{}
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		form.Set("device_code", da.DeviceCode)
		form.Set("client_id", cfg.ClientID)
		resp, err := postForm(ctx, hc, prov.TokenEndpoint, form)
		if err != nil {
			return nil, fmt.Errorf("polling for token: %w", err)
		}
		tok, retry, err := parseTokenResponse(resp, &interval)
		if err != nil {
			return nil, err
		}
		if tok != nil {
			return tok, nil
		}
		_ = retry // retry == true: keep polling
	}
}

// parseTokenResponse interprets a token-endpoint reply: a Token on success, or
// (nil,true,nil) to keep polling, adjusting interval on slow_down.
func parseTokenResponse(resp *http.Response, interval *int) (*Token, bool, error) {
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK {
		var t struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			TokenType    string `json:"token_type"`
			ExpiresIn    int    `json:"expires_in"`
		}
		if err := json.Unmarshal(body, &t); err != nil {
			return nil, false, fmt.Errorf("decoding token response: %w", err)
		}
		if t.AccessToken == "" {
			return nil, false, errors.New("token response carried no access_token")
		}
		return &Token{
			AccessToken:  t.AccessToken,
			RefreshToken: t.RefreshToken,
			TokenType:    t.TokenType,
			ExpiresIn:    t.ExpiresIn,
		}, false, nil
	}

	var e struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &e)
	switch e.Error {
	case "authorization_pending":
		return nil, true, nil
	case "slow_down":
		*interval += 5
		return nil, true, nil
	case "expired_token":
		return nil, false, errors.New("device code expired before it was approved")
	case "access_denied":
		return nil, false, errors.New("device authorization was denied")
	default:
		msg := e.Error
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		if e.ErrorDescription != "" {
			msg += ": " + e.ErrorDescription
		}
		return nil, false, fmt.Errorf("token endpoint error: %s", msg)
	}
}

func postForm(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return hc.Do(req)
}
