-- Migration 132: Rewrite enqueue_workflow so a workflow it enqueues takes its
-- created_at and updated_at from the workflow_status column defaults, which
-- the tail of this migration switches to clock_timestamp(). The function used
-- now(), the time the enqueuing transaction started, so a transaction that
-- first waited on a row lock stamped its workflow earlier than ones enqueued
-- while it waited, and queue order, which follows created_at, did not follow
-- lock order. The signature is unchanged from migration 105, so CREATE OR
-- REPLACE suffices and nothing is dropped first.
--
-- workflow_input.retention_timestamp now also comes from its column default
-- rather than from the function.

CREATE OR REPLACE FUNCTION %[1]s.enqueue_workflow(
    workflow_name TEXT,
    queue_name TEXT,
    positional_args JSON[] DEFAULT ARRAY[]::JSON[],
    named_args JSON DEFAULT '{}'::JSON,
    class_name TEXT DEFAULT NULL,
    config_name TEXT DEFAULT NULL,
    workflow_id TEXT DEFAULT NULL,
    app_version TEXT DEFAULT NULL,
    timeout_ms BIGINT DEFAULT NULL,
    deadline_epoch_ms BIGINT DEFAULT NULL,
    deduplication_id TEXT DEFAULT NULL,
    priority INT4 DEFAULT NULL,
    queue_partition_key TEXT DEFAULT NULL,
    authenticated_user TEXT DEFAULT NULL,
    authenticated_roles TEXT DEFAULT NULL,
    delay_until_epoch_ms BIGINT DEFAULT NULL,
    application_name TEXT DEFAULT NULL
) RETURNS TEXT AS $$
DECLARE
    v_workflow_id TEXT;
    v_serialized_inputs TEXT;
    v_owner_xid TEXT;
    v_recovery_attempts INT4 := 0;
    v_priority INT4;
    v_status TEXT;
BEGIN

    -- Validate required parameters
    IF workflow_name IS NULL OR workflow_name = '' THEN
        RAISE EXCEPTION 'Workflow name cannot be null or empty';
    END IF;
    IF queue_name IS NULL OR queue_name = '' THEN
        RAISE EXCEPTION 'Queue name cannot be null or empty';
    END IF;
    IF named_args IS NOT NULL AND jsonb_typeof(named_args::jsonb) != 'object' THEN
        RAISE EXCEPTION 'Named args must be a JSON object';
    END IF;
    IF workflow_id IS NOT NULL AND workflow_id = '' THEN
        RAISE EXCEPTION 'Workflow ID cannot be an empty string if provided.';
    END IF;
    IF delay_until_epoch_ms IS NOT NULL AND delay_until_epoch_ms < 0 THEN
        RAISE EXCEPTION 'delay_until_epoch_ms must be >= 0';
    END IF;

    v_workflow_id := COALESCE(workflow_id, gen_random_uuid()::TEXT);
    v_owner_xid := gen_random_uuid()::TEXT;
    v_priority := COALESCE(priority, 0);
    v_serialized_inputs := json_build_object(
        'positionalArgs', positional_args,
        'namedArgs', named_args
    )::TEXT;
    v_status := CASE WHEN delay_until_epoch_ms IS NULL THEN 'ENQUEUED' ELSE 'DELAYED' END;

    INSERT INTO %[1]s.workflow_status (
        workflow_uuid, status,
        name, class_name, config_name,
        queue_name, deduplication_id, priority, queue_partition_key,
        application_version,
        recovery_attempts,
        workflow_timeout_ms, workflow_deadline_epoch_ms,
        parent_workflow_id, owner_xid, serialization,
        authenticated_user, authenticated_roles,
        delay_until_epoch_ms, application_name
    ) VALUES (
        v_workflow_id, v_status,
        workflow_name, class_name, config_name,
        queue_name, deduplication_id, v_priority, queue_partition_key,
        app_version,
        v_recovery_attempts,
        timeout_ms, deadline_epoch_ms,
        NULL, v_owner_xid, 'portable_json',
        authenticated_user, authenticated_roles,
        delay_until_epoch_ms, application_name
    )
    ON CONFLICT (workflow_uuid)
    DO UPDATE SET
        updated_at = EXCLUDED.updated_at;

    INSERT INTO %[1]s.workflow_input (
        workflow_uuid, inputs
    ) VALUES (
        v_workflow_id, v_serialized_inputs
    )
    ON CONFLICT (workflow_uuid) DO NOTHING;

    RETURN v_workflow_id;

EXCEPTION
    WHEN unique_violation THEN
        RAISE EXCEPTION 'DBOS queue duplicated'
            USING DETAIL = format('Workflow %%s with queue %%s and deduplication ID %%s already exists', v_workflow_id, queue_name, deduplication_id),
                ERRCODE = 'unique_violation';
END;
$$ LANGUAGE plpgsql;
