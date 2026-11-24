-- Migration 135: Index workflows awaiting archival. Empty unless archiving is
-- configured; each application's archiver scans its own rows in completion
-- order.

CREATE INDEX %[1]s IF NOT EXISTS "idx_workflow_status_archive_pending" ON %[2]s."workflow_status" ("application_name", "completed_at", "workflow_uuid") WHERE "archive_pending" IS TRUE;
