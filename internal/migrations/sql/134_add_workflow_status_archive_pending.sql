-- Migration 134: Mark workflows awaiting archival. Set with completed_at while
-- workflow archiving is configured; cleared once the workflow is archived.

ALTER TABLE %[1]s."workflow_status"
    ADD COLUMN IF NOT EXISTS "archive_pending" BOOLEAN DEFAULT NULL;
