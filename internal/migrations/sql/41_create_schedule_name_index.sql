-- Migration 41 (statement 2 of 2): index the schedule_name column added by
-- 41_add_schedule_name.sql. The partial index covers zero rows when it is
-- built, so no CONCURRENTLY is needed.

CREATE INDEX IF NOT EXISTS "idx_workflow_status_schedule_name" ON %[1]s."workflow_status" ("schedule_name") WHERE "schedule_name" IS NOT NULL;
