-- Migration 36 (statement 2 of 2): index the completed_at column added by
-- 36_add_completed_at.sql. The partial index covers zero rows when it is built,
-- so no CONCURRENTLY is needed. See that file for why the two are separate.

CREATE INDEX IF NOT EXISTS "idx_workflow_status_completed_at" ON %[1]s."workflow_status" ("completed_at") WHERE "completed_at" IS NOT NULL;
