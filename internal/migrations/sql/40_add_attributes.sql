-- Migration 40 (statement 1 of 2): Add a JSONB attributes column to
-- workflow_status for arbitrary user-supplied workflow metadata. ADD COLUMN
-- with no default is catalog-only, so no CONCURRENTLY is needed.
--
-- Split from its index (40_create_attributes_index.sql) for the reason given in
-- 36_add_completed_at.sql: CockroachDB before v25 will not index a column that
-- became visible in the same transaction.

ALTER TABLE %[1]s."workflow_status" ADD COLUMN IF NOT EXISTS "attributes" JSONB;
