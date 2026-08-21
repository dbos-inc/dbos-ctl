//go:build integration

package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// The migration tests run against one engine per process, named by
// DBOS_TEST_ENGINE, so CI can matrix over both and report each as its own
// check. Both engines are supported system databases; the migration set differs
// between them in a dozen places, and only running the same suite twice says
// whether that difference is the intended one.
const (
	engineEnv       = "DBOS_TEST_ENGINE"
	enginePostgres  = "postgres"
	engineCockroach = "cockroach"
)

// pgImage and crdbImage are pinned to a minor stream rather than :latest so a
// database release cannot turn a job red with no change in this repo — the same
// reasoning as CONDUCTOR_IMAGE. Override either with DBOS_TEST_<ENGINE>_IMAGE.
const (
	pgImage   = "postgres:16"
	crdbImage = "cockroachdb/cockroach:latest-v26.2"
)

// engine is the database under test: a URL template with one %s for the
// database name, plus what the suite needs to know about the dialect.
type engine struct {
	urlFor       string
	cockroach    bool
	name         string
	listenNotify bool // whether this engine can install the pg_notify triggers
}

// url names one database on the engine under test.
func (e engine) url(database string) string {
	return fmt.Sprintf(e.urlFor, database)
}

// startEngine brings up the engine named by DBOS_TEST_ENGINE. Unset is a skip
// rather than a default: these tests pull a database image and take minutes, so
// running them is a choice, and `make test-migrations` is how it is made.
func startEngine(t *testing.T) engine {
	t.Helper()
	// Tests run in the package directory, two levels below the module root.
	_ = godotenv.Load("../../.env")

	switch os.Getenv(engineEnv) {
	case enginePostgres:
		return engine{urlFor: startPostgres(t), name: enginePostgres, listenNotify: true}
	case engineCockroach:
		return engine{urlFor: startCockroach(t), cockroach: true, name: engineCockroach}
	case "":
		t.Skipf("integration: %s not set (try `make test-migrations ENGINE=postgres`)", engineEnv)
		return engine{}
	default:
		t.Fatalf("%s=%q: want %q or %q", engineEnv, os.Getenv(engineEnv), enginePostgres, engineCockroach)
		return engine{}
	}
}

// onlyOn skips a test whose subject exists on one engine and not the other.
func (e engine) onlyOn(t *testing.T, name string) {
	t.Helper()
	if e.name != name {
		t.Skipf("engine is %s; this behavior is %s-only", e.name, name)
	}
}

func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	pg, err := postgres.Run(ctx, imageFor("POSTGRES", pgImage),
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("dbos"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	host, err := pg.Host(ctx)
	if err != nil {
		t.Fatalf("postgres host: %v", err)
	}
	port, err := pg.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("postgres port: %v", err)
	}
	return fmt.Sprintf("postgres://postgres:dbos@%s:%s/%%s?sslmode=disable", host, port.Port())
}

func startCockroach(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	// Insecure: the container's secure mode issues client certificates, and the
	// module hands those back as a registered driver name rather than a URL,
	// which is not something the command under test can be given.
	crdb, err := cockroachdb.Run(ctx, imageFor("COCKROACH", crdbImage), cockroachdb.WithInsecure())
	if err != nil {
		t.Fatalf("start cockroachdb: %v", err)
	}
	t.Cleanup(func() { _ = crdb.Terminate(ctx) })

	host, err := crdb.Host(ctx)
	if err != nil {
		t.Fatalf("cockroachdb host: %v", err)
	}
	port, err := crdb.MappedPort(ctx, "26257/tcp")
	if err != nil {
		t.Fatalf("cockroachdb port: %v", err)
	}
	return fmt.Sprintf("postgres://root@%s:%s/%%s?sslmode=disable", host, port.Port())
}

func imageFor(engineName, fallback string) string {
	if override := os.Getenv("DBOS_TEST_" + engineName + "_IMAGE"); override != "" {
		return override
	}
	return fallback
}

// runMigrateOrFail runs the command the way the binary does, returning what the
// operator would have seen on stderr.
func runMigrateOrFail(t *testing.T, args ...string) string {
	t.Helper()
	cmd, out := newMigrateCmd(t, args...)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dbosctl migrate %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out.String()
}

func connect(t *testing.T, url string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect %s: %v", url, err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func scalar[T any](t *testing.T, conn *pgx.Conn, query string, args ...any) T {
	t.Helper()
	var v T
	if err := conn.QueryRow(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return v
}

// userTriggers counts the notification triggers in a schema, which is the one
// thing the two engines are never expected to agree on.
func userTriggers(t *testing.T, conn *pgx.Conn, schema string) int64 {
	t.Helper()
	return scalar[int64](t, conn, `SELECT count(*) FROM pg_trigger tg
		JOIN pg_class c ON c.oid = tg.tgrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND NOT tg.tgisinternal`, schema)
}

func functionExists(t *testing.T, conn *pgx.Conn, schema, name string) bool {
	t.Helper()
	return scalar[bool](t, conn, `SELECT EXISTS(SELECT 1 FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = $1 AND p.proname = $2)`, schema, name)
}
