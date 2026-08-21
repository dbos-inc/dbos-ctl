package migrations

import (
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
	for _, isCockroach := range []bool{false, true} {
		for _, m := range BuildMigrations("dbos", isCockroach) {
			if strings.Contains(m.SQL, "%!") {
				t.Errorf("migration %d (cockroach=%v) has a placeholder mismatch:\n%s",
					m.Version, isCockroach, m.SQL)
			}
		}
	}
}

// TestVersionsAreOrderedAndUnique proves the runner's assumptions: it applies
// migrations in slice order, skips any at or below the recorded version, and
// treats the last one as latest.
func TestVersionsAreOrderedAndUnique(t *testing.T) {
	var last int64
	for _, m := range BuildMigrations("dbos", false) {
		if m.Version <= last {
			t.Fatalf("migration versions are not strictly increasing: %d after %d", m.Version, last)
		}
		last = m.Version
	}
	if last < SharedMigrationBase {
		t.Errorf("latest migration is %d, below the cross-SDK base %d", last, SharedMigrationBase)
	}
}

// TestEverySQLFileIsBuilt proves no file is orphaned. A new migration lands in
// sql/ first, but nothing renders it until someone adds its embed var and its
// entry in BuildMigrations — this is what says so.
func TestEverySQLFileIsBuilt(t *testing.T) {
	built := map[int64]bool{}
	for _, m := range BuildMigrations("dbos", false) {
		built[m.Version] = true
	}
	for _, m := range BuildMigrations("dbos", true) {
		built[m.Version] = true
	}

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
	for _, m := range BuildMigrations(`we"ird`, false) {
		if strings.Contains(m.SQL, `we"ird`) && !strings.Contains(m.SQL, `"we""ird"`) {
			t.Fatalf("migration %d embeds the schema name unquoted", m.Version)
		}
	}
}
