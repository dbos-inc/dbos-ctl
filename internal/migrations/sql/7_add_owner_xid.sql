-- Migration 7: Add owner_xid column to workflow_status table

ALTER TABLE %[1]s.workflow_status ADD COLUMN owner_xid TEXT DEFAULT NULL;
