-- Migration 22: Drop the index on executor_id. The recovery query that used
-- this index no longer relies on it.

DROP INDEX %[1]s IF EXISTS %[2]s."workflow_status_executor_id_index";
