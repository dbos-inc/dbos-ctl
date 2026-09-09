-- Migration 110: Give operation_outputs the same retention stamp the payload
-- tables carry. Sweep order only: the payload sweep deletes by absence of a
-- status row, so this bounds a round rather than deciding what it may delete.
-- ADD COLUMN with a constant default is catalog-only, so no CONCURRENTLY.

ALTER TABLE %[1]s."operation_outputs" ADD COLUMN IF NOT EXISTS "retention_timestamp" BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now()) * 1000.0)::bigint;
