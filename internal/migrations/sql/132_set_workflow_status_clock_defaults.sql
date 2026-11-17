-- Migration 132 (tail, every dialect): default workflow_status.created_at and
-- updated_at to clock_timestamp(), the time the row is written, rather than
-- now(), the time its transaction started. Runs after the Postgres-only
-- search_path re-pin, matching the statement order of the other SDKs.

ALTER TABLE %[1]s."workflow_status"
    ALTER COLUMN "created_at" SET DEFAULT (EXTRACT(EPOCH FROM clock_timestamp()) * 1000.0)::bigint,
    ALTER COLUMN "updated_at" SET DEFAULT (EXTRACT(EPOCH FROM clock_timestamp()) * 1000.0)::bigint;
