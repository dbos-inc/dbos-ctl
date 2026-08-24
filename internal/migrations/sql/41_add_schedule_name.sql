-- Migration 41 (statement 1 of 2): Add schedule_name to workflow_status,
-- naming the schedule that started a scheduled workflow. ADD COLUMN with no
-- default is catalog-only, so no CONCURRENTLY is needed.
--
-- Split from its index (41_create_schedule_name_index.sql) for the reason given
-- in 36_add_completed_at.sql: CockroachDB before v25 will not index a column
-- that became visible in the same transaction.

ALTER TABLE %[1]s."workflow_status" ADD COLUMN IF NOT EXISTS "schedule_name" TEXT;
