package migrations

import (
	"strings"
	"testing"
)

// Migrations 100-104 add application_name one table at a time, so a schema can
// sit anywhere in that range. A table without the column records no ownership,
// so there is nothing in it under the old name and passing it over is correct.
func TestRenameTargetsFollowTheColumnsTheSchemaHas(t *testing.T) {
	for _, tc := range []struct {
		name        string
		owned       []string
		wantMovable []string
		wantSkipped []string
	}{
		{
			name:        "fully migrated",
			owned:       []string{"operation_outputs", "workflow_status", "queues", "workflow_schedules", "application_versions"},
			wantMovable: []string{"operation_outputs", "workflow_status", "queues", "workflow_schedules", "application_versions"},
		},
		{
			// Migration 103: everything but operation_outputs, which 104 adds.
			name:        "before the steps column",
			owned:       []string{"workflow_status", "queues", "workflow_schedules", "application_versions"},
			wantMovable: []string{"workflow_status", "queues", "workflow_schedules", "application_versions"},
			wantSkipped: []string{"operation_outputs"},
		},
		{
			// Migration 100 alone: workflow_status and nothing else.
			name:        "only the first column",
			owned:       []string{"workflow_status"},
			wantMovable: []string{"workflow_status"},
			wantSkipped: []string{"operation_outputs", "queues", "workflow_schedules", "application_versions"},
		},
		{
			name:        "predates the whole range",
			wantSkipped: []string{"operation_outputs", "workflow_status", "queues", "workflow_schedules", "application_versions"},
		},
		{
			// A schema holding application tables of its own: a column named
			// application_name on one of them is not this command's business.
			name:        "ignores tables that are not ours",
			owned:       []string{"workflow_status", "billing_invoices"},
			wantMovable: []string{"workflow_status"},
			wantSkipped: []string{"operation_outputs", "queues", "workflow_schedules", "application_versions"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			movable, skipped := renameTargets(set(tc.owned...))
			if len(movable) != len(tc.wantMovable) {
				t.Errorf("movable has %d table(s), want %d: %v", len(movable), len(tc.wantMovable), movable)
			}
			for _, want := range tc.wantMovable {
				if _, ok := movable[want]; !ok {
					t.Errorf("%s is missing from movable", want)
				}
			}
			if got := strings.Join(skipped, ","); got != strings.Join(tc.wantSkipped, ",") {
				t.Errorf("skipped = %q, want %q", got, strings.Join(tc.wantSkipped, ","))
			}
		})
	}
}

// The skip list is reported to the operator, so its order has to be stable
// rather than a map's.
func TestRenameTargetsSkipListFollowsTheCanonicalOrder(t *testing.T) {
	_, skipped := renameTargets(set())
	if strings.Join(skipped, ",") != strings.Join(applicationOwnedTables, ",") {
		t.Errorf("skipped = %v, want applicationOwnedTables order %v", skipped, applicationOwnedTables)
	}
}
