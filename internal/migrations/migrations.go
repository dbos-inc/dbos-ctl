package migrations

import (
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5"
)

//go:embed sql/1_initial_dbos_schema.sql
var migration1SQL string

//go:embed sql/1_initial_dbos_schema_listen_notify.sql
var migration1ListenNotifySQL string

//go:embed sql/2_add_queue_partition_key.sql
var migration2SQL string

//go:embed sql/3_add_workflow_status_index.sql
var migration3SQL string

//go:embed sql/4_add_forked_from.sql
var migration4SQL string

//go:embed sql/5_add_step_timestamps.sql
var migration5SQL string

//go:embed sql/6_add_workflow_events_history.sql
var migration6SQL string

//go:embed sql/7_add_owner_xid.sql
var migration7SQL string

//go:embed sql/8_add_parent_workflow_id.sql
var migration8SQL string

//go:embed sql/9_add_workflow_schedules.sql
var migration9SQL string

//go:embed sql/10_add_notifications_pkey.sql
var migration10SQL string

//go:embed sql/10_check_notifications_pkey_cockroach.sql
var migration10CheckCockroachSQL string

//go:embed sql/10_add_notifications_pkey_cockroach.sql
var migration10AddCockroachSQL string

//go:embed sql/11_add_serialization_columns.sql
var migration11SQL string

//go:embed sql/12_add_notifications_consumed.sql
var migration12SQL string

//go:embed sql/13_add_application_versions.sql
var migration13SQL string

//go:embed sql/14_add_pgsql_client_functions.sql
var migration14SQL string

//go:embed sql/15_add_workflow_schedule_columns.sql
var migration15SQL string

//go:embed sql/16_add_delay_until.sql
var migration16SQL string

//go:embed sql/17_add_workflow_schedule_queue_name.sql
var migration17SQL string

//go:embed sql/18_add_was_forked_from.sql
var migration18SQL string

//go:embed sql/19_add_operation_outputs_completed_at_index.sql
var migration19SQL string

//go:embed sql/20_set_function_search_path.sql
var migration20SQL string

//go:embed sql/20_set_function_search_path_listen_notify.sql
var migration20ListenNotifySQL string

//go:embed sql/21_create_queues_table.sql
var migration21SQL string

//go:embed sql/22_drop_forked_from_index.sql
var migration22SQL string

//go:embed sql/23_create_partial_forked_from_index.sql
var migration23SQL string

//go:embed sql/24_drop_parent_workflow_id_index.sql
var migration24SQL string

//go:embed sql/25_create_partial_parent_workflow_id_index.sql
var migration25SQL string

//go:embed sql/26_drop_executor_id_index.sql
var migration26SQL string

//go:embed sql/27_create_partial_dedup_id_index.sql
var migration27SQL string

//go:embed sql/28_drop_dedup_id_constraint.sql
var migration28SQL string

//go:embed sql/28_drop_dedup_id_constraint_cockroach.sql
var migration28CockroachSQL string

//go:embed sql/29_create_pending_index.sql
var migration29SQL string

//go:embed sql/30_create_failed_index.sql
var migration30SQL string

//go:embed sql/31_drop_status_index.sql
var migration31SQL string

//go:embed sql/32_create_in_flight_index.sql
var migration32SQL string

//go:embed sql/33_add_rate_limited.sql
var migration33SQL string

//go:embed sql/34_create_rate_limited_index.sql
var migration34SQL string

//go:embed sql/35_drop_queue_status_started_index.sql
var migration35SQL string

//go:embed sql/36_add_completed_at.sql
var migration36SQL string

//go:embed sql/37_create_started_at_index.sql
var migration37SQL string

//go:embed sql/38_update_enqueue_workflow.sql
var migration38SQL string

//go:embed sql/38_set_enqueue_workflow_search_path.sql
var migration38SearchPathSQL string

//go:embed sql/39_create_streams_trigger.sql
var migration39SQL string

//go:embed sql/40_add_attributes.sql
var migration40SQL string

//go:embed sql/41_add_schedule_name.sql
var migration41SQL string

//go:embed sql/42_add_debounce_columns.sql
var migration42SQL string

//go:embed sql/43_drop_streams_trigger.sql
var migration43SQL string

//go:embed sql/44_drop_workflow_events_trigger.sql
var migration44SQL string

//go:embed sql/45_create_partition_dequeue_index.sql
var migration45SQL string

//go:embed sql/46_create_partition_dequeue_index_v2.sql
var migration46SQL string

//go:embed sql/47_drop_partition_dequeue_index.sql
var migration47SQL string

//go:embed sql/100_add_workflow_status_application_name.sql
var migration100SQL string

//go:embed sql/101_add_queues_application_name.sql
var migration101SQL string

//go:embed sql/102_add_workflow_schedules_application_name.sql
var migration102SQL string

//go:embed sql/103_add_application_versions_application_name.sql
var migration103SQL string

//go:embed sql/104_add_operation_outputs_application_name.sql
var migration104SQL string

//go:embed sql/105_update_enqueue_workflow.sql
var migration105SQL string

//go:embed sql/105_set_enqueue_workflow_search_path.sql
var migration105SearchPathSQL string

//go:embed sql/106_create_application_versions_owner_index.sql
var migration106SQL string

//go:embed sql/107_create_application_versions_unclaimed_index.sql
var migration107SQL string

//go:embed sql/108_add_queue_partition_limits.sql
var migration108SQL string

type MigrationFile struct {
	Version int64
	SQL     string
	Online  bool
}

const SharedMigrationBase = 100

// MigrationTable is the one-row table in the DBOS schema that records the
// highest migration version applied.
const MigrationTable = "dbos_migrations"

// returns the CONCURRENTLY keyword for online index DDL.
func concurrentlyKw(isCockroach bool) string {
	if isCockroach {
		return ""
	}
	return "CONCURRENTLY"
}

// BuildMigrations renders the full list of migrations against the target schema.
//
// Two independent switches shape the result. isCockroach picks the dialect:
// CockroachDB takes different SQL for a few catalog operations and rejects
// CONCURRENTLY. listenNotify decides whether the triggers that fire pg_notify
// are installed at all — false for CockroachDB, which has no LISTEN/NOTIFY, and
// false by request for a PostgreSQL deployment that cannot use it, a connection
// pooler in transaction mode being the usual reason.
//
// A migration that renders empty is still returned with its version: the runner
// advances past it, so version numbers mean the same thing on every deployment
// regardless of which switches were set.
func BuildMigrations(schema string, isCockroach, listenNotify bool) []MigrationFile {
	sanitizedSchema := pgx.Identifier{schema}.Sanitize()

	migration1SQLProcessed := fmt.Sprintf(migration1SQL,
		sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema,
		sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema,
		sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema)
	if listenNotify {
		migration1ListenNotifySQLProcessed := fmt.Sprintf(migration1ListenNotifySQL,
			sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema)
		migration1SQLProcessed = migration1SQLProcessed + "\n" + migration1ListenNotifySQLProcessed
	}

	c := concurrentlyKw(isCockroach)

	// Migration 20 is a Postgres-only function-hardening pass; on CockroachDB
	// it is a no-op (the version row still advances). Its trailing half pins the
	// trigger functions from migration 1's LISTEN/NOTIFY block, so it is
	// appended only where that block ran — the functions do not otherwise exist.
	migration20SQLProcessed := ""
	if !isCockroach {
		migration20SQLProcessed = fmt.Sprintf(migration20SQL, sanitizedSchema, sanitizedSchema)
		if listenNotify {
			migration20SQLProcessed = migration20SQLProcessed + "\n" + fmt.Sprintf(migration20ListenNotifySQL, sanitizedSchema, sanitizedSchema)
		}
	}

	// Migration 28 drops the legacy uq_workflow_status_queue_name_dedup_id
	// constraint. CockroachDB exposes it as an index (DROP INDEX ... CASCADE);
	// Postgres exposes it as a table constraint (ALTER TABLE DROP CONSTRAINT).
	// This is a fast catalog op, so CONCURRENTLY is not used in either path.
	migration28File := migration28SQL
	if isCockroach {
		migration28File = migration28CockroachSQL
	}
	migration28SQLProcessed := fmt.Sprintf(migration28File, sanitizedSchema)

	// Migration 38 replaces enqueue_workflow with a signature adding
	// authenticated_user, authenticated_roles, and delay_until_epoch_ms. The
	// DROP/CREATE base runs everywhere; the trailing search_path hardening is
	// Postgres-only (CockroachDB rejects ALTER FUNCTION ... SET).
	migration38SQLProcessed := fmt.Sprintf(migration38SQL, sanitizedSchema, sanitizedSchema, sanitizedSchema)
	if !isCockroach {
		migration38SQLProcessed = migration38SQLProcessed + "\n" + fmt.Sprintf(migration38SearchPathSQL, sanitizedSchema)
	}

	// Migration 39 installs the streams notification trigger, so it is gated on
	// LISTEN/NOTIFY support exactly like the migration 1 triggers. Without it
	// the migration is a no-op (the version row still advances).
	migration39SQLProcessed := ""
	if listenNotify {
		migration39SQLProcessed = fmt.Sprintf(migration39SQL,
			sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema)
	}

	// Migrations 43 and 44 drop the streams and workflow_events triggers
	// installed by migrations 39 and 1.
	//
	// Skipped on CockroachDB, which cannot always parse the statement: v24.1,
	// the oldest release DBOS supports, has no DROP TRIGGER at all, and v24.3
	// answers "DROP TRIGGER is only implemented in the declarative schema
	// changer". Nothing is lost by skipping — CockroachDB never had
	// LISTEN/NOTIFY, so the triggers these drop were never created there.
	//
	// Deliberately NOT gated on listenNotify, which would look like the same
	// condition and is not. On PostgreSQL the statements parse whatever the flag
	// says, and gating them there leaves a hole: a database migrated with the
	// triggers, later migrated by a process passing --no-listen-notify, would
	// skip the drops while advancing the version past them. No later process
	// would retry, so the triggers would survive forever, still notifying inside
	// every write transaction — the cost these migrations exist to remove.
	migration43SQLProcessed, migration44SQLProcessed := "", ""
	if !isCockroach {
		migration43SQLProcessed = fmt.Sprintf(migration43SQL, sanitizedSchema, sanitizedSchema)
		migration44SQLProcessed = fmt.Sprintf(migration44SQL, sanitizedSchema, sanitizedSchema)
	}

	// Migration 105 replaces enqueue_workflow with a signature adding a
	// trailing application_name. Like migration 38, the DROP/CREATE base runs
	// everywhere and the search_path hardening is Postgres-only.
	migration105SQLProcessed := fmt.Sprintf(migration105SQL, sanitizedSchema, sanitizedSchema, sanitizedSchema)
	if !isCockroach {
		migration105SQLProcessed = migration105SQLProcessed + "\n" + fmt.Sprintf(migration105SearchPathSQL, sanitizedSchema)
	}

	return []MigrationFile{
		{Version: 1, SQL: migration1SQLProcessed},
		{Version: 2, SQL: fmt.Sprintf(migration2SQL, sanitizedSchema)},
		{Version: 3, SQL: fmt.Sprintf(migration3SQL, sanitizedSchema)},
		{Version: 4, SQL: fmt.Sprintf(migration4SQL, sanitizedSchema, sanitizedSchema)},
		{Version: 5, SQL: fmt.Sprintf(migration5SQL, sanitizedSchema)},
		{Version: 6, SQL: fmt.Sprintf(migration6SQL, sanitizedSchema, sanitizedSchema, sanitizedSchema)},
		{Version: 7, SQL: fmt.Sprintf(migration7SQL, sanitizedSchema)},
		{Version: 8, SQL: fmt.Sprintf(migration8SQL, sanitizedSchema, sanitizedSchema)},
		{Version: 9, SQL: fmt.Sprintf(migration9SQL, sanitizedSchema)},
		{Version: 10, SQL: fmt.Sprintf(migration10SQL, schema, sanitizedSchema)},
		{Version: 11, SQL: fmt.Sprintf(migration11SQL, sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema)},
		{Version: 12, SQL: fmt.Sprintf(migration12SQL, sanitizedSchema, sanitizedSchema)},
		{Version: 13, SQL: fmt.Sprintf(migration13SQL, sanitizedSchema)},
		{Version: 14, SQL: fmt.Sprintf(migration14SQL, sanitizedSchema, sanitizedSchema, sanitizedSchema, sanitizedSchema)},
		{Version: 15, SQL: fmt.Sprintf(migration15SQL, sanitizedSchema, sanitizedSchema, sanitizedSchema)},
		{Version: 16, SQL: fmt.Sprintf(migration16SQL, sanitizedSchema, sanitizedSchema)},
		{Version: 17, SQL: fmt.Sprintf(migration17SQL, sanitizedSchema)},
		{Version: 18, SQL: fmt.Sprintf(migration18SQL, sanitizedSchema)},
		{Version: 19, SQL: fmt.Sprintf(migration19SQL, sanitizedSchema)},
		{Version: 20, SQL: migration20SQLProcessed},
		{Version: 21, SQL: fmt.Sprintf(migration21SQL, sanitizedSchema)},
		{Version: 22, SQL: fmt.Sprintf(migration22SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 23, SQL: fmt.Sprintf(migration23SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 24, SQL: fmt.Sprintf(migration24SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 25, SQL: fmt.Sprintf(migration25SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 26, SQL: fmt.Sprintf(migration26SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 27, SQL: fmt.Sprintf(migration27SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 28, SQL: migration28SQLProcessed},
		{Version: 29, SQL: fmt.Sprintf(migration29SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 30, SQL: fmt.Sprintf(migration30SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 31, SQL: fmt.Sprintf(migration31SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 32, SQL: fmt.Sprintf(migration32SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 33, SQL: fmt.Sprintf(migration33SQL, sanitizedSchema)},
		{Version: 34, SQL: fmt.Sprintf(migration34SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 35, SQL: fmt.Sprintf(migration35SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 36, SQL: fmt.Sprintf(migration36SQL, sanitizedSchema, sanitizedSchema)},
		{Version: 37, SQL: fmt.Sprintf(migration37SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 38, SQL: migration38SQLProcessed},
		{Version: 39, SQL: migration39SQLProcessed},
		{Version: 40, SQL: fmt.Sprintf(migration40SQL, sanitizedSchema, sanitizedSchema)},
		{Version: 41, SQL: fmt.Sprintf(migration41SQL, sanitizedSchema, sanitizedSchema)},
		{Version: 42, SQL: fmt.Sprintf(migration42SQL, sanitizedSchema, sanitizedSchema)},
		{Version: 43, SQL: migration43SQLProcessed},
		{Version: 44, SQL: migration44SQLProcessed},
		{Version: 45, SQL: fmt.Sprintf(migration45SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 46, SQL: fmt.Sprintf(migration46SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 47, SQL: fmt.Sprintf(migration47SQL, c, sanitizedSchema), Online: !isCockroach},
		// Versions from SharedMigrationBase on are defined identically by
		// every DBOS SDK; new migrations must be added to all of them.
		{Version: 100, SQL: fmt.Sprintf(migration100SQL, sanitizedSchema)},
		{Version: 101, SQL: fmt.Sprintf(migration101SQL, sanitizedSchema)},
		{Version: 102, SQL: fmt.Sprintf(migration102SQL, sanitizedSchema)},
		{Version: 103, SQL: fmt.Sprintf(migration103SQL, sanitizedSchema)},
		{Version: 104, SQL: fmt.Sprintf(migration104SQL, sanitizedSchema)},
		{Version: 105, SQL: migration105SQLProcessed},
		{Version: 106, SQL: fmt.Sprintf(migration106SQL, sanitizedSchema)},
		{Version: 107, SQL: fmt.Sprintf(migration107SQL, c, sanitizedSchema), Online: !isCockroach},
		{Version: 108, SQL: fmt.Sprintf(migration108SQL, sanitizedSchema)},
	}
}

// ShouldMigrate reports whether any migration work remains for the schema.
// Returns true if the schema is missing, the dbos_migrations table is missing,
// or the recorded version is behind the latest.
