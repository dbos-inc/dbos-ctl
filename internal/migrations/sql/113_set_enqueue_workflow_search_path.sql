-- Migration 113 (Postgres-only tail): re-pin search_path on the replaced
-- enqueue_workflow. CREATE OR REPLACE keeps the setting, but a database whose
-- function predates the hardening would otherwise never gain it. Skipped on
-- CockroachDB, which does not support ALTER FUNCTION ... SET.

ALTER FUNCTION %[1]s.enqueue_workflow(
    TEXT, TEXT, JSON[], JSON, TEXT, TEXT, TEXT, TEXT, BIGINT, BIGINT, TEXT, INT4, TEXT, TEXT, TEXT, BIGINT, TEXT
) SET search_path = pg_catalog, pg_temp;
