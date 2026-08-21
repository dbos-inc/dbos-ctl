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
// history below migration 100 was written. The latest migration is 107.
//
// # Adding a migration
//
// Three edits, all in this package: add sql/<version>_<description>.sql, add
// the //go:embed var for it in migrations.go, and add it to the list
// BuildMigrations returns. The tests fail if you do fewer than three — an
// orphaned file and a placeholder count that does not match the arguments both
// report there rather than against a customer's database.
//
// Set Online on a migration whose SQL contains CREATE or DROP INDEX
// CONCURRENTLY: those cannot run inside a transaction, so the runner applies
// them outside one and bumps the version separately, which means such a
// migration must be safe to re-execute.
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
