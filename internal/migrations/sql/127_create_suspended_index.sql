-- Migration 127: Index SUSPENDED workflows on their wake time, so the
-- background pass reads just the ones that are due.

CREATE INDEX %[1]s IF NOT EXISTS "idx_workflow_status_suspended" ON %[2]s."workflow_status" ("delay_until_epoch_ms") WHERE "status" = 'SUSPENDED';
