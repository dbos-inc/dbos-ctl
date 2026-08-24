package migrations

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Adding a migration means editing three things — the SQL file, its embed var,
// and the list BuildMigrations returns — and nothing but these tests notices
// when only two of them happened. They are what turns a half-finished migration
// into a failed build rather than a failed customer database.

// TestEveryRenderedMigrationConsumesItsPlaceholders proves each file's %s verbs
// match the arguments BuildMigrations passes. fmt reports both directions in
// the rendered string (%!s(MISSING) for too few arguments, %!(EXTRA for too
// many), which is why the check is a substring scan and not a count. A bare %s
// surviving in the output is not an error: PL/pgSQL format() calls carry their
// own %%s escapes, which render to exactly that.
func TestEveryRenderedMigrationConsumesItsPlaceholders(t *testing.T) {
	forEachVariant(func(name string, isCockroach, listenNotify bool) {
		for _, m := range BuildMigrations("dbos", isCockroach, listenNotify) {
			if strings.Contains(m.SQL, "%!") {
				t.Errorf("migration %d (%s) has a placeholder mismatch:\n%s", m.Version, name, m.SQL)
			}
		}
	})
}

// forEachVariant runs fn over all four combinations of the two switches that
// shape the migration set.
func forEachVariant(fn func(name string, isCockroach, listenNotify bool)) {
	for _, isCockroach := range []bool{false, true} {
		for _, listenNotify := range []bool{false, true} {
			fn(fmt.Sprintf("cockroach=%v listenNotify=%v", isCockroach, listenNotify), isCockroach, listenNotify)
		}
	}
}

// TestVersionsAreOrderedAndUnique proves the runner's assumptions: it applies
// migrations in slice order, skips any at or below the recorded version, and
// treats the last one as latest.
func TestVersionsAreOrderedAndUnique(t *testing.T) {
	var last int64
	for _, m := range BuildMigrations("dbos", false, true) {
		if m.Version <= last {
			t.Fatalf("migration versions are not strictly increasing: %d after %d", m.Version, last)
		}
		last = m.Version
	}
	if last < SharedMigrationBase {
		t.Errorf("latest migration is %d, below the cross-SDK base %d", last, SharedMigrationBase)
	}
}

// TestEveryVariantCountsToTheSameVersion proves a skipped migration still
// occupies its number. Version 108 has to mean the same thing on CockroachDB,
// on a pooled PostgreSQL, and on a plain one — otherwise two deployments of one
// application disagree about whether their databases are up to date.
func TestEveryVariantCountsToTheSameVersion(t *testing.T) {
	forEachVariant(func(name string, isCockroach, listenNotify bool) {
		m := BuildMigrations("dbos", isCockroach, listenNotify)
		if got := m[len(m)-1].Version; got != LatestVersion() {
			t.Errorf("%s reaches version %d, want %d", name, got, LatestVersion())
		}
		if len(m) != len(BuildMigrations("dbos", false, true)) {
			t.Errorf("%s has %d migrations, want the same count as a plain PostgreSQL", name, len(m))
		}
	})
}

// TestListenNotifyGating proves the flag decides exactly one thing: whether SQL
// that installs a pg_notify trigger is emitted. Nothing else may vary with it,
// or a database migrated without LISTEN/NOTIFY would differ from one migrated
// with it in ways no application knows about.
func TestListenNotifyGating(t *testing.T) {
	for _, isCockroach := range []bool{false, true} {
		off := renderedByVersion(BuildMigrations("dbos", isCockroach, false))
		for version, sql := range off {
			if strings.Contains(sql, "pg_notify") {
				t.Errorf("migration %d installs a pg_notify trigger with LISTEN/NOTIFY off (cockroach=%v):\n%s",
					version, isCockroach, sql)
			}
		}
		if strings.Contains(off[20], "notifications_function") {
			t.Errorf("migration 20 alters the notification functions that migration 1 did not create (cockroach=%v)", isCockroach)
		}
		if off[39] != "" {
			t.Errorf("migration 39 is not empty with LISTEN/NOTIFY off (cockroach=%v)", isCockroach)
		}
	}

	on := renderedByVersion(BuildMigrations("dbos", false, true))
	if !strings.Contains(on[1], "pg_notify") {
		t.Error("migration 1 installs no notification triggers with LISTEN/NOTIFY on")
	}
	if !strings.Contains(on[20], "notifications_function") {
		t.Error("migration 20 does not pin the notification functions with LISTEN/NOTIFY on")
	}
	if !strings.Contains(on[39], "pg_notify") {
		t.Error("migration 39 installs no streams trigger with LISTEN/NOTIFY on")
	}
}

// TestTriggerDropsIgnoreTheListenNotifyFlag pins the asymmetry in migrations 43
// and 44: the dialect decides whether they run, the LISTEN/NOTIFY flag does not.
//
// On PostgreSQL they run either way. Gating them on the flag would leave a hole
// — a database migrated with the triggers, later migrated by a process passing
// --no-listen-notify, would skip the drops while advancing the version past
// them, and no later process would retry. On CockroachDB they are skipped
// because it cannot always parse DROP TRIGGER, and the triggers were never
// created there to begin with.
func TestTriggerDropsIgnoreTheListenNotifyFlag(t *testing.T) {
	for _, listenNotify := range []bool{false, true} {
		rendered := renderedByVersion(BuildMigrations("dbos", false, listenNotify))
		for _, version := range []int64{43, 44} {
			sql := rendered[version]
			if sql == "" {
				t.Errorf("migration %d is empty on PostgreSQL with listenNotify=%v", version, listenNotify)
				continue
			}
			// Running where the trigger was never created is only safe because
			// every statement tolerates its absence.
			for _, stmt := range strings.Split(sql, ";") {
				if strings.TrimSpace(stmt) == "" || strings.HasPrefix(strings.TrimSpace(stmt), "--") {
					continue
				}
				if !strings.Contains(stmt, "IF EXISTS") {
					t.Errorf("migration %d runs unguarded but this statement is not IF EXISTS:\n%s", version, stmt)
				}
			}
		}

		crdb := renderedByVersion(BuildMigrations("dbos", true, listenNotify))
		for _, version := range []int64{43, 44} {
			if crdb[version] != "" {
				t.Errorf("migration %d sends DROP TRIGGER to CockroachDB (listenNotify=%v), which cannot always parse it:\n%s",
					version, listenNotify, crdb[version])
			}
		}
	}
}

func renderedByVersion(ms []MigrationFile) map[int64]string {
	out := make(map[int64]string, len(ms))
	for _, m := range ms {
		out[m.Version] = m.SQL
	}
	return out
}

// TestEverySQLFileIsBuilt proves no file is orphaned. A new migration lands in
// sql/ first, but nothing renders it until someone adds its embed var and its
// entry in BuildMigrations — this is what says so.
func TestEverySQLFileIsBuilt(t *testing.T) {
	built := map[int64]bool{}
	forEachVariant(func(_ string, isCockroach, listenNotify bool) {
		for _, m := range BuildMigrations("dbos", isCockroach, listenNotify) {
			built[m.Version] = true
		}
	})

	entries, err := os.ReadDir("sql")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			t.Errorf("%s is not named <version>_<description>.sql", name)
			continue
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			t.Errorf("%s does not start with a version number", name)
			continue
		}
		if !built[version] {
			t.Errorf("sql/%s is vendored but migration %d is not built: update BuildMigrations", name, version)
		}
		seen[version] = true

		body, err := os.ReadFile(filepath.Join("sql", name))
		if err != nil {
			t.Fatal(err)
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			t.Errorf("sql/%s is empty", name)
		}
	}
	for version := range built {
		if !seen[version] {
			t.Errorf("migration %d is built but has no file in sql/", version)
		}
	}
}

// TestSchemaNameIsQuoted proves the schema reaches the SQL as a quoted
// identifier, so a schema name needing quotes cannot break out of it.
func TestSchemaNameIsQuoted(t *testing.T) {
	for _, m := range BuildMigrations(`we"ird`, false, true) {
		if strings.Contains(m.SQL, `we"ird`) && !strings.Contains(m.SQL, `"we""ird"`) {
			t.Fatalf("migration %d embeds the schema name unquoted", m.Version)
		}
	}
}
