-- Migration 131: Record the objects stored with store_external_object, so
-- garbage collection can find those whose owner is gone. CREATE INDEX on a
-- table this migration just created locks nothing an application holds, so no
-- CONCURRENTLY is needed.

CREATE TABLE IF NOT EXISTS %[1]s."external_objects" (
    "object_key" TEXT NOT NULL,
    "store_id" TEXT NOT NULL,
    "workflow_uuid" TEXT NOT NULL,
    "created_at" BIGINT NOT NULL,
    PRIMARY KEY ("store_id", "object_key")
);

CREATE INDEX IF NOT EXISTS "idx_external_objects_store_created_at" ON %[1]s."external_objects" ("store_id", "created_at");
