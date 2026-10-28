-- Migration 125: Index application_versions on (application_name,
-- version_timestamp DESC), so a named latest-version lookup finds the
-- application's newest version and the newest unclaimed one with one probe
-- each, however many newer versions other applications have registered.

CREATE INDEX %[1]s IF NOT EXISTS "idx_application_versions_owner_timestamp" ON %[2]s."application_versions" ("application_name", "version_timestamp" DESC);
