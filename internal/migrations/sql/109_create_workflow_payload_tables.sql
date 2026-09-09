-- Migration 109: Move workflow payloads off workflow_status into their own
-- tables, so a status update no longer rewrites a large input or output.
-- retention_timestamp bounds a sweep round rather than deciding what may be
-- deleted; the sweep itself works from the absence of a status row.
-- CREATE TABLE and CREATE INDEX on a table this migration just created lock
-- nothing an application holds, so no CONCURRENTLY is needed.

CREATE TABLE IF NOT EXISTS %[1]s."workflow_input" (
    workflow_uuid TEXT NOT NULL PRIMARY KEY,
    inputs TEXT,
    retention_timestamp BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now()) * 1000.0)::bigint
);

CREATE TABLE IF NOT EXISTS %[1]s."workflow_output" (
    workflow_uuid TEXT NOT NULL PRIMARY KEY,
    output TEXT,
    error TEXT,
    retention_timestamp BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now()) * 1000.0)::bigint
);

CREATE INDEX IF NOT EXISTS "idx_workflow_input_retention" ON %[1]s."workflow_input" ("retention_timestamp");

CREATE INDEX IF NOT EXISTS "idx_workflow_output_retention" ON %[1]s."workflow_output" ("retention_timestamp");
