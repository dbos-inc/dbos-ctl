-- Migration 101: Add application_name to queues. NULL means unclaimed.

ALTER TABLE %[1]s."queues" ADD COLUMN IF NOT EXISTS "application_name" TEXT DEFAULT NULL;
