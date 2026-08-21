// Package migrations creates and upgrades the DBOS system database schema on
// PostgreSQL (and CockroachDB, which the migration set special-cases in a few
// places).
//
// # Provenance
//
// This is a deliberate copy of the migration set and runner from
// dbos-transact-golang, not an import of it. Copied from
// github.com/dbos-inc/dbos-transact-golang v1.2.0, where the same code lives in
// the unexported package dbos/internal/sysdb. The latest migration is 107.
//
// Copying rather than importing is what lets dbosctl migrate a system database
// without linking a Go SDK: dbosctl is a cross-language tool, its release train
// is its own, and the Go SDK's entry point to the runner is a client
// constructor that also creates pools and starts background work. The cost is
// drift, which is what the re-vendoring target and the fidelity tests below are
// for.
//
// # What was left out
//
// The SQLite migration set, which the SDK keeps alongside this one. dbosctl
// migrates servers, not embedded files; SQLite databases are created by the
// application process that opens them.
//
// The upstream runner's retry wrapper is also absent: the SDK retries because
// it migrates during application startup, where a transient failure should not
// take down a deploy. A CLI invocation reports the error and the operator runs
// it again.
//
// # Re-vendoring
//
// `make migrations TRANSACT_DIR=/path/to/dbos-transact-golang` copies the SQL
// files verbatim. The Go code is not generated: after re-vendoring the SQL,
// diff migrations.go and runner.go against the upstream sysdb package by hand
// and update the version named above. The only intended difference in
// migrations.go is the embed path prefix (sql/ here, migrations/ upstream).
//
// # Migration numbering
//
// Versions are not contiguous. Numbers below SharedMigrationBase are the Go
// SDK's own history; numbers from SharedMigrationBase on are defined
// identically by every DBOS SDK, and a database migrated by any of them — or by
// dbosctl — is readable by all of them. The gap between the two ranges emits
// nothing.
package migrations
