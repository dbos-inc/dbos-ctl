-- Migration 41: Add a schedule_name column to workflow_status recording which
-- named schedule (if any) enqueued the workflow. ADD COLUMN with no default is
-- catalog-only; the partial index built in the same transaction covers zero
-- rows (no existing row has a non-NULL schedule_name), so no CONCURRENTLY is
-- needed. The index supports filtering workflows by schedule name.
--
-- KNOWN ISSUE: like migration 36, this cannot be applied to CockroachDB before
-- v25 — the partial index has the just-added column in its predicate, and that
-- release line will not index a column that became visible in the same
-- transaction. See 36_add_completed_at.sql for the error, the versions it was
-- measured on, and why every SDK is affected.

ALTER TABLE %s."workflow_status" ADD COLUMN IF NOT EXISTS "schedule_name" TEXT;
CREATE INDEX IF NOT EXISTS "idx_workflow_status_schedule_name" ON %s."workflow_status" ("schedule_name") WHERE "schedule_name" IS NOT NULL;
