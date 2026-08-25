package migrations

import "testing"

// Every foreign key in the schema points at workflow_status, so it is emptied
// last: doing it first would cascade the referencing tables away and leave
// their own DELETE reporting zero rows for a table it had just cleared.
func TestSystemTablesEndWithTheCascadeParent(t *testing.T) {
	if len(SystemTables) == 0 {
		t.Fatal("SystemTables is empty")
	}
	if last := SystemTables[len(SystemTables)-1]; last != "workflow_status" {
		t.Errorf("SystemTables ends with %q, want workflow_status last so the reported counts stay accurate", last)
	}
	seen := map[string]bool{}
	for _, table := range SystemTables {
		if seen[table] {
			t.Errorf("SystemTables lists %q twice", table)
		}
		seen[table] = true
	}
	if seen[MigrationTable] {
		t.Errorf("SystemTables includes %s, which a reset must spare", MigrationTable)
	}
}

func set(names ...string) map[string]struct{} {
	s := map[string]struct{}{}
	for _, n := range names {
		s[n] = struct{}{}
	}
	return s
}

// A schema that never reached migration 100 still has every table an
// application-scoped reset would name — they predate it by a long way — so the
// refusal has to key off the application_name column instead. Checking presence
// let this case through to a bare SQLSTATE 42703 from the first DELETE.
func TestResetTargetsRefusesASchemaWithNoApplicationNameColumns(t *testing.T) {
	present := set(SystemTables...)
	if _, err := resetTargets("dbos", "app", present, set()); err == nil {
		t.Fatal("resetTargets accepted a pre-100 schema; want a refusal naming migration 100")
	}
}

// Migrations 100-104 add the column one table at a time, so a schema stopped
// part way through resets the tables that reached it and leaves the rest.
func TestResetTargetsScopesToTheTablesCarryingTheColumn(t *testing.T) {
	present := set(SystemTables...)
	owned := set("workflow_status", "queues")

	got, err := resetTargets("dbos", "app", present, owned)
	if err != nil {
		t.Fatalf("resetTargets: %v", err)
	}
	for _, table := range got {
		if _, ok := owned[table]; !ok {
			t.Errorf("resetTargets returned %q, which has no application_name column", table)
		}
	}
	if len(got) != len(owned) {
		t.Errorf("resetTargets returned %v, want the %d owned tables", got, len(owned))
	}
}

// A full reset still keys off presence: it deletes every row regardless of who
// owns it, so the column has nothing to say about it.
func TestResetTargetsForAFullResetIgnoresOwnership(t *testing.T) {
	got, err := resetTargets("dbos", "", set(SystemTables...), nil)
	if err != nil {
		t.Fatalf("resetTargets: %v", err)
	}
	if len(got) != len(SystemTables) {
		t.Errorf("resetTargets returned %d tables, want all %d", len(got), len(SystemTables))
	}
}
