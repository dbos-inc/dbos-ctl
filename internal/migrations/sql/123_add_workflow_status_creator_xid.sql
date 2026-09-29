-- Migration 123: Record the token of the insert that created a workflow_status
-- row; owner_xid holds the executing owner's.

ALTER TABLE %[1]s."workflow_status"
    ADD COLUMN IF NOT EXISTS "creator_xid" TEXT DEFAULT NULL;
