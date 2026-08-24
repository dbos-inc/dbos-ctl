-- Migration 20, LISTEN/NOTIFY half: pin search_path on the trigger functions
-- from migration 1's LISTEN/NOTIFY block. Appended only where that block ran —
-- without it these functions do not exist, and ALTER FUNCTION would fail.

ALTER FUNCTION %[1]s.notifications_function() SET search_path = pg_catalog, pg_temp;
ALTER FUNCTION %[1]s.workflow_events_function() SET search_path = pg_catalog, pg_temp;
