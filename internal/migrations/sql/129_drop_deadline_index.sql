-- Migration 129: Drop idx_workflow_status_deadline, superseded by
-- idx_workflow_status_deadline_v2.

DROP INDEX %[1]s IF EXISTS %[2]s."idx_workflow_status_deadline";
