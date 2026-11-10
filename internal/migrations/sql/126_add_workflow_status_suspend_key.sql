-- Migration 126: Record the topic a SUSPENDED recv waits on, so a send on that
-- topic can find and wake it.

ALTER TABLE %[1]s."workflow_status"
    ADD COLUMN IF NOT EXISTS "suspend_key" TEXT DEFAULT NULL;
