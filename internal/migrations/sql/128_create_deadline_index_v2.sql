-- Migration 128: idx_workflow_status_deadline, with a predicate that also
-- covers SUSPENDED workflows, so the timeout sweep finds those too.

CREATE INDEX %[1]s IF NOT EXISTS "idx_workflow_status_deadline_v2" ON %[2]s."workflow_status" ("workflow_deadline_epoch_ms") WHERE "status" IN ('ENQUEUED', 'PENDING', 'DELAYED', 'SUSPENDED') AND "workflow_deadline_epoch_ms" IS NOT NULL;
