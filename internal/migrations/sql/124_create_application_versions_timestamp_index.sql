-- Migration 124: Index application_versions on version_timestamp, newest first,
-- so a latest-version lookup with no application name reads one index entry
-- instead of sorting the whole version history.

CREATE INDEX %[1]s IF NOT EXISTS "idx_application_versions_timestamp" ON %[2]s."application_versions" ("version_timestamp" DESC);
