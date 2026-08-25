-- Migration 47: Drop the v1 partitioned-queue dequeue index (migration 45),
-- superseded by idx_workflow_status_partition_dequeue_v2 (migration 46).

DROP INDEX %[1]s IF EXISTS %[2]s."idx_workflow_status_partition_dequeue";
