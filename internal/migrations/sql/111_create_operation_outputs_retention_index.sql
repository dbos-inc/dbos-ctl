-- Migration 111: Index the retention stamp added by migration 110, so the
-- sweep orders by it without scanning the table.

CREATE INDEX %[1]s IF NOT EXISTS "idx_operation_outputs_retention" ON %[2]s."operation_outputs" ("retention_timestamp");
