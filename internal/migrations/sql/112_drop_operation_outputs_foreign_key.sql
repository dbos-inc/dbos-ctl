-- Migration 112: Drop the operation_outputs -> workflow_status foreign key.
-- Steps are reclaimed by the payload sweep now, so the cascade that used to do
-- it is gone. Both names are dropped: the TypeScript SDK created the
-- constraint under the knex name, every other SDK under the PostgreSQL
-- default, and a database may carry either.

ALTER TABLE %[1]s."operation_outputs" DROP CONSTRAINT IF EXISTS "operation_outputs_workflow_uuid_foreign";

ALTER TABLE %[1]s."operation_outputs" DROP CONSTRAINT IF EXISTS "operation_outputs_workflow_uuid_fkey";
