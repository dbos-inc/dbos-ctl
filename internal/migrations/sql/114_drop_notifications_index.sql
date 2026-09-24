-- Migration 114: Drop idx_notifications (migration 12). It duplicates
-- idx_workflow_topic, which covers the same columns in the same order.

DROP INDEX %[1]s IF EXISTS %[2]s."idx_notifications";
