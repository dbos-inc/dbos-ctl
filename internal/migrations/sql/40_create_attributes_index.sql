-- Migration 40 (statement 2 of 2): index the attributes column added by
-- 40_add_attributes.sql. Supports containment (@>) filters; on CockroachDB,
-- USING GIN creates an inverted index. The partial index covers zero rows when
-- it is built, so no CONCURRENTLY is needed.

CREATE INDEX IF NOT EXISTS "idx_workflow_status_attributes" ON %[1]s."workflow_status" USING GIN ("attributes") WHERE "attributes" IS NOT NULL;
