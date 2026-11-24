-- Migration 133: Default workflow_input.retention_timestamp to
-- clock_timestamp(), the time the row is written, rather than now(), the time
-- its transaction started. Inputs follow their status row, so this keeps an
-- input from being stamped before its workflow's created_at, which migration
-- 132 moved to statement time.

ALTER TABLE %[1]s."workflow_input"
    ALTER COLUMN "retention_timestamp" SET DEFAULT (EXTRACT(EPOCH FROM clock_timestamp()) * 1000.0)::bigint;
