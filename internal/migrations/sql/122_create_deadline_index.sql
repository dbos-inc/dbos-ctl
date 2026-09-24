-- Migration 122: Index active workflows that carry a deadline, so the timeout
-- sweep reads just the expired ones rather than every in-flight row.

CREATE INDEX %[1]s IF NOT EXISTS "idx_workflow_status_deadline" ON %[2]s."workflow_status" ("workflow_deadline_epoch_ms") WHERE "status" IN ('ENQUEUED', 'PENDING', 'DELAYED') AND "workflow_deadline_epoch_ms" IS NOT NULL;
