-- Migration 12: Add consumed column to notifications and index for unconsumed lookups.

ALTER TABLE %[1]s.notifications ADD COLUMN consumed BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX "idx_notifications" ON %[1]s.notifications (destination_uuid, topic);
