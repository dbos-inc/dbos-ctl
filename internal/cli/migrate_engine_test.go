//go:build integration

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	"github.com/testcontainers/testcontainers-go"
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

// shared is the one engine every test in the binary uses, started by the first
// test that asks for it and stopped by TestMain. A CockroachDB container takes
// around ten seconds to start, and each test gets a database of its own on the
// shared one instead, which is also what lets the tests run side by side.
var shared struct {
	once sync.Once
	e    engine
	stop func()
	err  error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if shared.stop != nil {
		shared.stop()
	}
	os.Exit(code)
}

// startEngine brings up the engine named by DBOS_TEST_ENGINE, or returns the one
// already up. Unset is a skip rather than a default: these tests pull a database
// image and take minutes, so running them is a choice, and `make test-sysdb` is
// how it is made.
func startEngine(t *testing.T) engine {
	t.Helper()
	// Tests run in the package directory, two levels below the module root.
	_ = godotenv.Load("../../.env")

	name := os.Getenv(engineEnv)
	switch name {
	case enginePostgres, engineCockroach:
	case "":
		t.Skipf("integration: %s not set (try `make test-sysdb ENGINE=postgres`)", engineEnv)
	default:
		t.Fatalf("%s=%q: want %q or %q", engineEnv, name, enginePostgres, engineCockroach)
	}

	shared.once.Do(func() {
		if name == enginePostgres {
			shared.e = engine{name: enginePostgres, listenNotify: true}
			shared.e.urlFor, shared.stop, shared.err = startPostgres()
		} else {
			shared.e = engine{name: engineCockroach, cockroach: true}
			shared.e.urlFor, shared.stop, shared.err = startCockroach()
		}
	})
	if shared.err != nil {
		t.Fatalf("start %s: %v", name, shared.err)
	}
	return shared.e
}

var (
	databaseSeq atomic.Int64
	notIdent    = regexp.MustCompile(`[^a-z0-9]+`)
)

// database names a database for the calling test alone, which nothing has
// created yet. Nothing drops it either: the container goes when the binary
// exits. The name carries the test's, so a database found while debugging says
// which test made it.
func database(t *testing.T) string {
	t.Helper()
	slug := strings.TrimSuffix(strings.TrimPrefix(t.Name(), "Test"), "Integration")
	slug = notIdent.ReplaceAllString(strings.ToLower(slug), "_")
	if len(slug) > 48 {
		slug = slug[:48]
	}
	return fmt.Sprintf("t%d_%s", databaseSeq.Add(1), slug)
}

// The fixture is a database migrated once, on first use, and copied for every
// test that needs a migrated database without being about migrating one. A copy
// takes about a second on either engine; migrating takes about a minute on
// CockroachDB, where every DDL statement is an online schema change.
const (
	fixtureDatabase = "dbos_fixture"
	fixtureBackup   = "nodelocal://1/" + fixtureDatabase
)

var fixture struct {
	once sync.Once
	err  error
}

// migratedDatabase creates a database for the calling test alone, at the latest
// migration, and returns its name.
//
// PostgreSQL copies the fixture as a template. CockroachDB has no templates, so
// the fixture is backed up once, to the node's own disk, and restored under each
// new name.
func (e engine) migratedDatabase(t *testing.T) string {
	t.Helper()
	fixture.once.Do(func() {
		// Set first, so a build that ends in runtime.Goexit -- a t.Fatal on the
		// way -- still leaves every later caller an error rather than a spent
		// Once and nothing to copy.
		fixture.err = errors.New("the fixture build did not finish")
		fixture.err = e.buildFixture(t)
	})
	if fixture.err != nil {
		t.Fatalf("build the migrated fixture: %v", fixture.err)
	}

	// Names from database are [a-z0-9_] only, so they need no quoting.
	name := database(t)
	admin := connect(t, e.url("postgres"))
	if e.cockroach {
		exec(t, admin, fmt.Sprintf(`RESTORE DATABASE %s FROM LATEST IN '%s' WITH new_db_name = '%s'`,
			fixtureDatabase, fixtureBackup, name))
	} else {
		exec(t, admin, fmt.Sprintf(`CREATE DATABASE %s TEMPLATE %s`, name, fixtureDatabase))
	}
	return name
}

func (e engine) buildFixture(t *testing.T) error {
	cmd, out := newMigrateCmd(t, "--db-url", e.url(fixtureDatabase))
	if err := cmd.Execute(); err != nil {
		return fmt.Errorf("dbosctl migrate: %w\n%s", err, out)
	}
	if !e.cockroach {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, e.url(fixtureDatabase))
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	_, err = conn.Exec(ctx, fmt.Sprintf(`BACKUP DATABASE %s INTO '%s'`, fixtureDatabase, fixtureBackup))
	return err
}

// onlyOn skips a test whose subject exists on one engine and not the other.
func (e engine) onlyOn(t *testing.T, name string) {
	t.Helper()
	if e.name != name {
		t.Skipf("engine is %s; this behavior is %s-only", e.name, name)
	}
}

func startPostgres() (urlFor string, stop func(), err error) {
	ctx := context.Background()

	pg, err := postgres.Run(ctx, imageFor("POSTGRES", pgImage),
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("dbos"),
		postgres.BasicWaitStrategies(),
	)
	// Run hands back the container even when it fails to come up, and it still
	// has to go.
	if pg != nil {
		stop = func() { _ = pg.Terminate(ctx) }
	}
	if err != nil {
		return "", stop, err
	}

	host, err := pg.Host(ctx)
	if err != nil {
		return "", stop, fmt.Errorf("host: %w", err)
	}
	port, err := pg.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return "", stop, fmt.Errorf("port: %w", err)
	}
	return fmt.Sprintf("postgres://postgres:dbos@%s:%s/%%s?sslmode=disable", host, port.Port()), stop, nil
}

func startCockroach() (urlFor string, stop func(), err error) {
	ctx := context.Background()

	// Insecure: the container's secure mode issues client certificates, and the
	// module hands those back as a registered driver name rather than a URL,
	// which is not something the command under test can be given.
	//
	// The module runs an in-memory store, which leaves the node no directory for
	// nodelocal:// files, and the fixture's backup needs one.
	crdb, err := cockroachdb.Run(ctx, imageFor("COCKROACH", crdbImage),
		cockroachdb.WithInsecure(),
		testcontainers.WithCmdArgs("--external-io-dir=/cockroach/extern"),
	)
	if crdb != nil {
		stop = func() { _ = crdb.Terminate(ctx) }
	}
	if err != nil {
		return "", stop, err
	}

	host, err := crdb.Host(ctx)
	if err != nil {
		return "", stop, fmt.Errorf("host: %w", err)
	}
	port, err := crdb.MappedPort(ctx, "26257/tcp")
	if err != nil {
		return "", stop, fmt.Errorf("port: %w", err)
	}
	return fmt.Sprintf("postgres://root@%s:%s/%%s?sslmode=disable", host, port.Port()), stop, nil
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

// notificationsPrimaryKey returns the definition of the notifications table's
// primary key — "PRIMARY KEY (message_uuid)" and the like — or "" if it has
// none.
//
// The definition rather than the constraint name, because the name cannot tell
// the two engines apart in the one situation that matters. CockroachDB gives a
// table declared without a key an implicit one over its hidden rowid column,
// and calls it notifications_pkey: exactly the name migration 10 adds. A test
// asking only whether a constraint by that name exists would pass on a database
// whose key is still rowid, which is the bug it is supposed to catch.
func notificationsPrimaryKey(t *testing.T, conn *pgx.Conn, schema string) string {
	t.Helper()
	var def string
	err := conn.QueryRow(context.Background(), `SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c
		JOIN pg_class cl ON cl.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = cl.relnamespace
		WHERE n.nspname = $1 AND cl.relname = 'notifications' AND c.contype = 'p'`, schema).Scan(&def)
	if err == pgx.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("look up the notifications primary key: %v", err)
	}
	return def
}
