-- Migration 130 (Postgres-only tail): re-pin search_path on the replaced
-- send_message. CREATE OR REPLACE keeps the setting, but a database whose
-- function predates the hardening would otherwise never gain it. Skipped on
-- CockroachDB, which does not support ALTER FUNCTION ... SET.

ALTER FUNCTION %[1]s.send_message(
    TEXT, JSON, TEXT, TEXT
) SET search_path = pg_catalog, pg_temp;
