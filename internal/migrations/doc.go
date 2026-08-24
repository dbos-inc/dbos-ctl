// Package migrations creates and upgrades the DBOS system database schema on
// PostgreSQL (and CockroachDB, which the migration set special-cases in a few
// places).
//
// # This is where the Postgres migrations live
//
// This package is the master copy. A new migration is written here first, and
// the SDKs follow; nothing re-vendors it from dbos-transact-golang, and no
// tooling regenerates it. That is what the migrate command needs in order to
// mean anything: an operator provisioning a database should not have to know
// which SDK the application that will use it is written in, still less install
// that SDK's toolchain.
//
// The set started as a copy of dbos-transact-golang v1.2.0, where equivalent
// code lives in the unexported package dbos/internal/sysdb, and where the
// history below migration 100 was written. The latest migration is 108.
//
// # Adding a migration
//
// Three edits, all in this package: add sql/<version>_<description>.sql, add
// the //go:embed var for it in migrations.go, and add it to the list
// BuildMigrations returns. The tests fail if you do fewer than three — an
// orphaned file and a placeholder count that does not match the arguments both
// report there rather than against a customer's database.
//
// Write every placeholder as an explicit-index %[n]s, never a bare %s, so a
// value named many times is passed once: the schema is %[1]s throughout a
// file that takes only the schema, and %[2]s in one that takes the
// CONCURRENTLY keyword first. Mixing the two forms is the trap the tests
// guard — after an explicit index, fmt numbers any bare verb that follows
// from that index onward rather than from where it left off.
//
// Set Online on a migration whose SQL contains CREATE or DROP INDEX
// CONCURRENTLY: those cannot run inside a transaction, so the runner applies
// them outside one and bumps the version separately, which means such a
// migration must be safe to re-execute.
//
// # LISTEN/NOTIFY
//
// BuildMigrations takes two independent switches. isCockroach picks the
// dialect. listenNotify decides whether the triggers that fire pg_notify are
// installed, and it is off in two unrelated situations: CockroachDB, which has
// no LISTEN/NOTIFY and is detected, and a PostgreSQL deployment that cannot use
// it — a connection pooler in transaction mode being the usual reason — which
// cannot be detected and so is a flag the operator passes. Live migration
// detects the dialect and combines the two; print mode connects to nothing, so
// both are the caller's to supply.
//
// The dialect reaches further than the triggers do: no ALTER FUNCTION ... SET
// search_path anywhere (migrations 20, 38, 105), a different statement for
// migration 28, no DROP TRIGGER (43, 44), and no CONCURRENTLY. Measured against
// CockroachDB v24.1 through v26.2, the LISTEN/NOTIFY and ALTER FUNCTION gaps
// are permanent, while DROP TRIGGER and DO blocks arrived between v24.3 and
// v25.2 — which is also the oldest stream the whole set applies to.
//
// A migration that creates a notification trigger is gated on listenNotify. A
// migration that drops one is not: every such statement is an IF EXISTS no-op
// where the trigger was never created, and gating the drops would leave a hole.
// A process migrating without LISTEN/NOTIFY would skip them while advancing the
// version past them, and no later process would retry — the triggers would
// survive forever on that database. The same reasoning governs the Java SDK's
// copy of this set, which is where it was worked out.
//
// Whether a migration renders empty never changes which versions exist, so a
// database reports the same version number whichever way it was migrated.
//
// Versions from SharedMigrationBase on are defined identically by every DBOS
// SDK. Adding one here is a cross-SDK change: the same migration, with the same
// number and the same effect, has to land in Python, TypeScript, Java, and Go,
// or an application will find a database it does not recognize. Numbers below
// SharedMigrationBase are the Go SDK's own history and are frozen — the gap
// between the two ranges emits nothing.
//
// # What is not here
//
// The SQLite migration set, which the SDKs keep for embedded use. dbosctl
// migrates servers; a SQLite database is created by the application process
// that opens it.
//
// A retry wrapper around the runner. The SDKs retry because they migrate during
// application startup, where a transient failure should not take down a deploy.
// A CLI invocation reports the error and the operator runs it again.
package migrations
