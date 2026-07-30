//go:build integration

// Package conductortest starts a real Conductor (no-auth) backed by Postgres in
// throwaway containers, for integration tests. It compiles only under the
// `integration` build tag.
//
// Prerequisites — the license key, and either a prebuilt image or a conductor
// checkout to build — are checked up front; when any is missing the tier is
// skipped (never failed), so it is safe on machines and fork PRs without
// secrets.
package conductortest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	pgImage   = "postgres:16"
	pgDB      = "dbos_conductor"
	pgUser    = "postgres"
	pgPass    = "dbos"
	pgAlias   = "postgres"
	condAlias = "conductor"
	condPort  = "8090/tcp"
)

// Start brings up Postgres + Conductor (no auth) on a shared network and
// returns the Conductor base URL. The whole stack is torn down via t.Cleanup.
func Start(t *testing.T) string {
	return startStack(t, nil, nil)
}

// StartWithEnv is Start with extra Conductor environment — e.g. a short
// DBOS__METRICS_COLLECTION_PERIOD so a metrics test doesn't wait the 5-minute
// default for the leader's collection loop.
func StartWithEnv(t *testing.T, extraEnv map[string]string) string {
	return startStack(t, extraEnv, nil)
}

// startStack is the shared launcher: it brings up Postgres + Conductor with any
// extra environment (e.g. the OAuth knobs) and host-access ports (so the
// container can reach a host-run OIDC mock via host.testcontainers.internal).
func startStack(t *testing.T, extraEnv map[string]string, hostPorts []int) string {
	t.Helper()
	loadDotEnv()

	licenseKey := os.Getenv("DBOS_CONDUCTOR_LICENSE_KEY")
	if licenseKey == "" {
		t.Skip("integration: DBOS_CONDUCTOR_LICENSE_KEY not set (see .env.example)")
	}
	// The license key gates the tier (fork PRs get no secret → skip). The
	// conductor source is a configuration the runner must make explicitly: with
	// neither set we fail rather than guess a location.
	image := os.Getenv("CONDUCTOR_IMAGE")
	dir := os.Getenv("CONDUCTOR_DIR")
	if image == "" && dir == "" {
		t.Fatal("integration: set CONDUCTOR_IMAGE to a conductor image with the API, or CONDUCTOR_DIR to a conductor checkout (see .env.example)")
	}

	ctx := context.Background()

	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	// Registered first so it runs last (LIFO): containers terminate before the
	// network is removed.
	t.Cleanup(func() { _ = nw.Remove(ctx) })

	pg, err := postgres.Run(ctx, pgImage,
		postgres.WithDatabase(pgDB),
		postgres.WithUsername(pgUser),
		postgres.WithPassword(pgPass),
		network.WithNetwork([]string{pgAlias}, nw),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	// Conductor reaches Postgres by network alias. Its entrypoint also waits for
	// Postgres and runs migrations before serving, so a loose Postgres readiness
	// check is fine.
	dbURL := fmt.Sprintf("postgresql://%s:%s@%s:5432/%s?sslmode=disable", pgUser, pgPass, pgAlias, pgDB)

	env := map[string]string{
		"DBOS__CONDUCTOR_DB_URL":     dbURL,
		"DBOS_CONDUCTOR_LICENSE_KEY": licenseKey,
	}
	for k, v := range extraEnv {
		env[k] = v
	}

	req := testcontainers.ContainerRequest{
		Env:            env,
		ExposedPorts:   []string{condPort},
		Networks:       []string{nw.Name},
		NetworkAliases: map[string][]string{nw.Name: {condAlias}},
		WaitingFor:     wait.ForHTTP("/healthz").WithPort(condPort).WithStartupTimeout(3 * time.Minute),
	}
	if len(hostPorts) > 0 {
		// Expose these host ports to the container via host.testcontainers.internal
		// (an SSH reverse tunnel), so Conductor can fetch JWKS from a host-run mock.
		req.HostAccessPorts = hostPorts
	}
	var buildLog bytes.Buffer
	if image != "" {
		req.Image = image
	} else {
		// TARGETARCH is a BuildKit-provided arg the conductor Dockerfile relies on
		// (to fetch the right golang-migrate binary). testcontainers' build does
		// not set it, so pass it explicitly for the host arch. KeepImage so repeat
		// runs reuse the (slow) conductor build.
		arch := runtime.GOARCH
		req.FromDockerfile = testcontainers.FromDockerfile{
			Context:        dir,
			Dockerfile:     "docker/Dockerfile",
			KeepImage:      true,
			BuildArgs:      map[string]*string{"TARGETARCH": &arch},
			BuildLogWriter: &buildLog,
		}
	}

	cond, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start conductor: %v\n--- conductor build log ---\n%s", err, buildLog.String())
	}
	t.Cleanup(func() { _ = cond.Terminate(ctx) })

	host, err := cond.Host(ctx)
	if err != nil {
		t.Fatalf("conductor host: %v", err)
	}
	port, err := cond.MappedPort(ctx, condPort)
	if err != nil {
		t.Fatalf("conductor mapped port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

// loadDotEnv loads .env from the repo root if present. godotenv does not
// override variables already set in the environment, so both `make
// test-integration` and CI (which sets the key from a secret) work.
func loadDotEnv() {
	if root := repoRoot(); root != "" {
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}
}

// repoRoot walks up from the working directory to the module root (the dir
// holding go.mod).
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
