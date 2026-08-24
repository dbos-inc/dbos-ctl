-- Migration 10 (CockroachDB variant): add the notifications primary key that a
-- pre-fix migration 1 did not create. CockroachDB cannot run the DO block the
-- PostgreSQL file uses to make itself conditional, but it accepts ADD
-- CONSTRAINT ... IF NOT EXISTS, which PostgreSQL does not — so each dialect
-- reaches the same idempotence by the only route it has.
--
-- Verified idempotent on v24.1 (the oldest release DBOS supports) and v26.2:
-- re-running it against a table that already has the key is accepted and
-- changes nothing, and on a table without one it replaces CockroachDB's
-- implicit rowid key with this one.

ALTER TABLE %[1]s.notifications ADD CONSTRAINT IF NOT EXISTS notifications_pkey PRIMARY KEY (message_uuid);
