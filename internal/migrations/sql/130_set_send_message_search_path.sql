-- Migration 130 (Postgres-only tail): pin search_path on the recreated
-- send_message, matching the hardening applied in migration 20. Dropping the
-- function discarded the setting, so without this it would be lost. Skipped on
-- CockroachDB, which does not support ALTER FUNCTION ... SET.

ALTER FUNCTION %[1]s.send_message(
    TEXT, JSON, TEXT, TEXT
) SET search_path = pg_catalog, pg_temp;
