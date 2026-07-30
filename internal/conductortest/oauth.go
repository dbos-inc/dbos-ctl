//go:build integration

package conductortest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// hostAlias is the hostname a container uses to reach a host-run server exposed
// via testcontainers' HostAccessPorts.
const hostAlias = "host.testcontainers.internal"

// OAuthConductor is a Conductor started in OAuth mode together with the
// in-process OIDC issuer whose JWKS it trusts.
type OAuthConductor struct {
	BaseURL string
	Issuer  *Issuer
}

// StartOAuth brings up Postgres + Conductor with OAuth enabled, trusting an
// in-process RSA issuer that the container reaches over a host-access tunnel.
// getCurrentUser and the other identity routes exist only in this mode.
func StartOAuth(t *testing.T) *OAuthConductor {
	t.Helper()
	iss := newIssuer(t)
	env := map[string]string{
		"DBOS_OAUTH_ENABLED":  "true",
		"DBOS_OAUTH_ISSUER":   iss.URL,
		"DBOS_OAUTH_AUDIENCE": iss.Audience,
	}
	base := startStack(t, env, []int{iss.port})
	return &OAuthConductor{BaseURL: base, Issuer: iss}
}

// Issuer is a minimal RS256 OIDC issuer: it serves a JWKS document and mints
// signed JWTs. The issuer URL points at host.testcontainers.internal so the
// Conductor container can fetch the JWKS from this host-run server.
type Issuer struct {
	URL      string
	Audience string
	port     int
	key      *rsa.PrivateKey
	kid      string
}

func newIssuer(t *testing.T) *Issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate issuer key: %v", err)
	}
	iss := &Issuer{Audience: "dbos-conductor-test", key: key, kid: "test-key"}

	mux := http.NewServeMux()
	// Conductor's JWKS provider does OIDC discovery: it fetches the
	// openid-configuration to learn jwks_uri, then the keys. Both handlers read
	// iss.URL at request time, once the listening port is known.
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":   iss.URL,
			"jwks_uri": iss.URL + "/.well-known/jwks.json",
		})
	})
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(iss.jwks())
	})

	// Bind to all interfaces so the Conductor container reaches it over the
	// host-access tunnel; httptest defaults to loopback only.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen for issuer: %v", err)
	}
	srv := httptest.NewUnstartedServer(mux)
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	iss.port = ln.Addr().(*net.TCPAddr).Port
	iss.URL = fmt.Sprintf("http://%s:%d", hostAlias, iss.port)
	return iss
}

// Token mints a signed RS256 JWT for the given subject and email, valid for an
// hour, with the issuer/audience Conductor is configured to accept.
func (i *Issuer) Token(t *testing.T, subject, email string) string {
	t.Helper()
	now := time.Now()
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": i.kid}
	claims := map[string]any{
		"iss":   i.URL,
		"aud":   i.Audience,
		"sub":   subject,
		"email": email,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	signingInput := b64(mustJSON(t, header)) + "." + b64(mustJSON(t, claims))
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signingInput + "." + b64(sig)
}

// jwks is the JSON Web Key Set advertising the issuer's public key.
func (i *Issuer) jwks() []byte {
	pub := i.key.PublicKey
	key := map[string]string{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": i.kid,
		"n":   b64(pub.N.Bytes()),
		"e":   b64(big.NewInt(int64(pub.E)).Bytes()),
	}
	b, _ := json.Marshal(map[string]any{"keys": []map[string]string{key}})
	return b
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
