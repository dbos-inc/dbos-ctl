-- Migration 136: Record the workflows SUSPENDED awaiting this one's result, so
-- completing it can find and wake them.

ALTER TABLE %[1]s."workflow_status"
    ADD COLUMN IF NOT EXISTS "result_waiter_ids" TEXT[] DEFAULT NULL;
