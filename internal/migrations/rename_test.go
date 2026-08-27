package migrations

import (
	"strings"
	"testing"
)

// A rename UPDATEs these tables by name, so every one of them has to carry
// application_name. The two ways to fall short want different things said: a
// schema with none of the columns predates the feature, while a schema with some
// of them is a migration that stopped half way.
func TestOwnershipColumnErrorSeparatesOldFromHalfMigrated(t *testing.T) {
	all := []string{"operation_outputs", "workflow_status", "queues", "workflow_schedules", "application_versions"}

	for _, tc := range []struct {
		name  string
		owned []string
		want  []string // substrings the message must carry; empty means no error
	}{
		{
			name:  "fully migrated",
			owned: all,
		},
		{
			// Interrupted inside migrations 100-104: 104 never ran.
			name:  "stopped before the steps column",
			owned: []string{"workflow_status", "queues", "workflow_schedules", "application_versions"},
			want:  []string{"operation_outputs", "part way", "sysdb migrate"},
		},
		{
			// Interrupted right after 100.
			name:  "stopped after the first column",
			owned: []string{"workflow_status"},
			want:  []string{"operation_outputs", "queues", "part way", "sysdb migrate"},
		},
		{
			name: "predates the whole range",
			want: []string{"predates migration 100", "no application_name columns"},
		},
		{
			// A schema holding application tables of its own: a column named
			// application_name on one of them is not this command's business,
			// and must not be mistaken for coverage.
			name:  "ignores tables that are not ours",
			owned: []string{"billing_invoices"},
			want:  []string{"predates migration 100"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ownershipColumnError("dbos", set(tc.owned...))
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("a fully migrated schema was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a schema that cannot be renamed was accepted")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// A half-migrated schema is reported as such rather than as an old one: the
// fix is to finish migrating, and saying "predates migration 100" would send
// the operator somewhere else entirely.
func TestOwnershipColumnErrorDoesNotCallAHalfMigratedSchemaOld(t *testing.T) {
	err := ownershipColumnError("dbos", set("workflow_status"))
	if err == nil {
		t.Fatal("a half-migrated schema was accepted")
	}
	if strings.Contains(err.Error(), "predates") {
		t.Errorf("half-migrated schema reported as predating the feature: %v", err)
	}
}

// The probe order is applicationOwnedTables reordered, and reordering is the
// kind of duplication that goes stale: a migration that gives another table an
// application_name gets added to the list a rename re-owns and forgotten here,
// and the name check then quietly stops looking at it.
func TestApplicationNameProbeOrderCoversOwnedTables(t *testing.T) {
	probed := map[string]int{}
	for _, table := range applicationNameProbeOrder {
		probed[table]++
	}
	for _, table := range applicationOwnedTables {
		switch probed[table] {
		case 1:
			delete(probed, table)
		case 0:
			t.Errorf("%s carries application_name but is never probed for it", table)
		default:
			t.Errorf("%s is probed %d times", table, probed[table])
		}
	}
	for table := range probed {
		t.Errorf("%s is probed but carries no application_name", table)
	}
}
