-- Migration 40: Add a JSONB attributes column to workflow_status for arbitrary
-- user-supplied workflow metadata. ADD COLUMN with no default is catalog-only;
-- the partial index built in the same transaction covers zero rows, so no
-- CONCURRENTLY is needed. The index supports containment (@>) filters; on
-- CockroachDB, USING GIN creates an inverted index.
--
-- WHY V25.2 IS THE FLOOR: like migration 36, this cannot be applied to
-- CockroachDB v24 — the partial index has the just-added column in its
-- predicate, and that release line will not index a column that became visible
-- in the same transaction. v24 is not a supported system database, so this is
-- settled rather than outstanding; see 36_add_completed_at.sql for the error,
-- the versions it was measured on, and what supporting v24 would have taken.

ALTER TABLE %[1]s."workflow_status" ADD COLUMN IF NOT EXISTS "attributes" JSONB;
CREATE INDEX IF NOT EXISTS "idx_workflow_status_attributes" ON %[1]s."workflow_status" USING GIN ("attributes") WHERE "attributes" IS NOT NULL;
