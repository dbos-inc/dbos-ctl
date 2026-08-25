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
