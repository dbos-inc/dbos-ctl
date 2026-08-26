package migrations

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// DefaultRenameBatchSize matches the SDKs': terminal workflows and their steps
// move this many keys per transaction.
const DefaultRenameBatchSize = 10_000

// renameTimeout bounds the whole rename. A long history moves in many
// transactions, so this is generous; it is finite so that a rename blocked by
// the application it is renaming reports instead of hanging.
const renameTimeout = 30 * time.Minute

// ApplicationRowCounts reports what a rename durably moved, by kind.
//
// Counts and errors are not exclusive: the terminal workflows and steps move in
// their own transactions, so a rename that fails partway reports how far it
// committed, which is what a re-run resumes from. Nothing from the first
// transaction is counted until that transaction commits — it either all moved
// or none of it did, and reporting rows a rollback took back would be worse
// than reporting nothing.
type ApplicationRowCounts struct {
	Workflows int64 `json:"workflows"`
	Steps     int64 `json:"steps"`
	Queues    int64 `json:"queues"`
	Schedules int64 `json:"schedules"`
	Versions  int64 `json:"versions"`
}

// RenameInput describes a rename. At least one of OldName and
// AdoptUnclaimedRows must be set; callers check that, so that the CLI can
// report it as a usage error.
type RenameInput struct {
	OldName            string // Previous owner. Empty moves only the unclaimed rows.
	NewName            string // The application that ends up owning the rows.
	Schema             string // Schema holding the system tables. Empty means DefaultSchema.
	BatchSize          int    // Keys re-owned per transaction. Zero means DefaultRenameBatchSize.
	AdoptUnclaimedRows bool   // Also take rows no application owns (application_name IS NULL).
}

// sourcePredicate renders the WHERE clause matching the rows a rename moves,
// appending any bound arguments. Unclaimed rows are never implied: they move
// only when asked for.
func sourcePredicate(in RenameInput, args *[]any) string {
	var clauses []string
	if in.OldName != "" {
		*args = append(*args, in.OldName)
		clauses = append(clauses, fmt.Sprintf("application_name = $%d", len(*args)))
	}
	if in.AdoptUnclaimedRows {
		clauses = append(clauses, "application_name IS NULL")
	}
	return "(" + strings.Join(clauses, " OR ") + ")"
}

// rowPredicate renders the WHERE clause matching the rows one table's rename
// moves. Both tables pass sourcePredicate today, so the seam buys nothing yet;
// it is here because operation_outputs is known to need its own.
//
// Migration 104 added operation_outputs.application_name with DEFAULT NULL and
// no backfill, so every step row written before a database reached 104 is NULL
// no matter who owns its workflow. Matching that column alone leaves all of
// them behind: a rename over a long-lived database reports "steps": 0 and
// strands the entire history, while the reset path never notices because it
// deletes through the foreign key cascade instead.
//
// Every SDK has that gap, and closing it here alone would mean dbosctl and
// `dbos rename-application` disagreeing about the same database, so it wants
// designing once and porting rather than inventing in the CLI. Whatever comes
// out of that lands as a second rowPredicate.
//
// Whatever it is, it has to keep the invariant renameInBatches advances on:
// a row this clause moves must stop matching it, or the watermark never moves.
type rowPredicate func(in RenameInput, args *[]any) string

// RenameApplication gives NewName ownership of the rows OldName holds, of the
// unclaimed rows, or of both.
//
// The application being renamed must be stopped first. Nothing here locks it
// out, and a running one keeps dequeuing under its old name — racing the rows
// out from under this.
func RenameApplication(ctx context.Context, databaseURL string, in RenameInput, progress io.Writer) (ApplicationRowCounts, error) {
	var counts ApplicationRowCounts

	if in.Schema == "" {
		in.Schema = DefaultSchema
	}
	if err := ValidateSchemaName(in.Schema); err != nil {
		return counts, err
	}
	if in.NewName == "" {
		return counts, fmt.Errorf("no new application name")
	}
	if in.OldName == "" && !in.AdoptUnclaimedRows {
		return counts, fmt.Errorf("nothing to re-own")
	}
	// A row moved to the name it already has goes on matching the predicate, so
	// the batch watermark would never advance. It is also a no-op rename; to
	// adopt unclaimed rows into an application, name it with --to alone.
	if in.OldName != "" && in.OldName == in.NewName {
		return counts, fmt.Errorf("the old and new application names are the same (%q); to adopt unclaimed rows into it, pass --to without --from", in.NewName)
	}
	if in.BatchSize <= 0 {
		in.BatchSize = DefaultRenameBatchSize
	}

	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return counts, fmt.Errorf("failed to connect to the system database: %w", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	execCtx, cancel := context.WithTimeout(ctx, renameTimeout)
	defer cancel()

	// Checked before anything moves. The tables below are compiled in, so a
	// migration this build has never seen could carry application_name on one
	// they do not name, and the rename would report success over rows still
	// owned by the old name.
	if err := checkNotAheadOfBinary(execCtx, conn, in.Schema); err != nil {
		return counts, err
	}

	// The tables below are UPDATEd by name, so the columns have to be there.
	// Migrations 100-104 add application_name one table at a time, and although
	// no SDK release ships a part of that range -- all five land together in one
	// commit in Go, Python, and TypeScript alike -- the runner applies migrations
	// one at a time and commits each with its own version bump. A migrate that is
	// interrupted or fails inside the range leaves a schema that genuinely has
	// some of these columns and not others.
	//
	// That is a migration to finish, not a shape to accommodate, so it is
	// refused rather than worked around. Renaming the tables that do carry the
	// column would succeed at something nobody asked for, and the operator would
	// have to infer from a zero that their schema is half-migrated.
	//
	// Empty does accommodate it, and the difference is deliberate: emptying an
	// old schema's rows is useful work, where renaming across one is a rename
	// waiting to be redone after the migration finishes.
	if err := checkOwnershipColumns(execCtx, conn, in.Schema); err != nil {
		return counts, err
	}

	// Queues, schedules, versions, and the in-flight workflows move together.
	// A half-owned application is the one state worse than either end: it
	// dequeues work whose application_version row it can no longer see.
	tx, err := conn.Begin(execCtx)
	if err != nil {
		return counts, fmt.Errorf("failed to begin the rename transaction: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(execCtx))

	move := func(table string) (int64, error) {
		args := []any{in.NewName}
		predicate := sourcePredicate(in, &args)
		q := fmt.Sprintf("UPDATE %s SET application_name = $1 WHERE %s",
			pgx.Identifier{in.Schema, table}.Sanitize(), predicate)
		tag, err := tx.Exec(execCtx, q, args...)
		if err != nil {
			return 0, fmt.Errorf("failed to re-own %s rows: %w", table, err)
		}
		return tag.RowsAffected(), nil
	}

	// Held aside rather than accumulated into counts: until the commit below
	// these describe a transaction that may still roll back, and returning them
	// alongside an error would report rows that never moved.
	var queues, schedules, versions int64
	if queues, err = move("queues"); err != nil {
		return ApplicationRowCounts{}, err
	}
	if schedules, err = move("workflow_schedules"); err != nil {
		return ApplicationRowCounts{}, err
	}
	if versions, err = move("application_versions"); err != nil {
		return ApplicationRowCounts{}, err
	}

	// In-flight workflows are bounded by how much the application is running,
	// not by how long it has run, so they fit in the transaction above.
	args := []any{in.NewName}
	predicate := sourcePredicate(in, &args)
	inFlightQuery := fmt.Sprintf(
		"UPDATE %s SET application_name = $1 WHERE %s AND status IN ('PENDING', 'ENQUEUED', 'DELAYED')",
		pgx.Identifier{in.Schema, "workflow_status"}.Sanitize(), predicate)
	tag, err := tx.Exec(execCtx, inFlightQuery, args...)
	if err != nil {
		return ApplicationRowCounts{}, fmt.Errorf("failed to re-own in-flight workflow rows: %w", err)
	}
	inFlight := tag.RowsAffected()

	if err := tx.Commit(execCtx); err != nil {
		return ApplicationRowCounts{}, fmt.Errorf("failed to commit the rename: %w", err)
	}
	// Committed, so these are now facts rather than intentions.
	counts.Queues, counts.Schedules, counts.Versions = queues, schedules, versions
	counts.Workflows = inFlight
	logf(progress, "Re-owned %d queue(s), %d schedule(s), %d version(s), %d in-flight workflow(s)",
		counts.Queues, counts.Schedules, counts.Versions, inFlight)

	// Terminal workflows and their steps are unbounded — a year of history is
	// however many rows it is — so they move in their own transactions.
	// Each batch commits on its own, so what these moved before failing is real
	// and worth reporting: it is where a re-run picks up.
	terminal, batchErr := renameInBatches(execCtx, conn, "workflow_status", in, sourcePredicate, progress)
	counts.Workflows += terminal
	if batchErr != nil {
		return counts, batchErr
	}

	steps, batchErr := renameInBatches(execCtx, conn, "operation_outputs", in, sourcePredicate, progress)
	counts.Steps = steps
	if batchErr != nil {
		return counts, batchErr
	}
	return counts, nil
}

// checkOwnershipColumns refuses a schema that does not carry application_name on
// every table a rename re-owns.
//
// The two ways to fall short read very differently to whoever hits them, so they
// are reported separately: a schema with none of the columns is simply older
// than the feature, while a schema with some of them is a migration that stopped
// half way and wants finishing.
func checkOwnershipColumns(ctx context.Context, conn *pgx.Conn, schema string) error {
	owned, err := applicationNameTables(ctx, conn, schema)
	if err != nil {
		return err
	}
	return ownershipColumnError(schema, owned)
}

// ownershipColumnError reports what is wrong with a schema's application_name
// coverage, or nil if nothing is. Split from the query above so the three
// outcomes can be tested without a database.
func ownershipColumnError(schema string, owned map[string]struct{}) error {
	// applicationOwnedTables' order, so the message reads the same way every
	// time rather than a map's.
	var missing []string
	for _, table := range applicationOwnedTables {
		if _, ok := owned[table]; !ok {
			missing = append(missing, table)
		}
	}
	switch len(missing) {
	case 0:
		return nil
	case len(applicationOwnedTables):
		return fmt.Errorf("schema %s has no application_name columns: it predates migration %d, so it records no application ownership to re-own",
			schema, SharedMigrationBase)
	default:
		return fmt.Errorf("schema %s is missing application_name on %s: it is part way through the migrations that add it, so run `dbosctl sysdb migrate` before renaming",
			schema, strings.Join(missing, ", "))
	}
}

// renameInBatches re-owns a table's rows in half-open workflow_uuid ranges.
//
// Ranges rather than LIMIT: a LIMIT would repage the rows it has already moved
// — they still match the predicate on the next pass only until they are moved,
// and the scan to skip them grows with every batch — while an IN list of keys
// plans as a whole-table hash join. Bounding each batch by a key means the work
// already done is behind the watermark and never looked at again, and an
// interrupted run resumes where it stopped rather than starting over.
//
// That only holds if each batch carries the previous watermark forward as its
// lower bound, which is what lowerBound below is for. Dropping it would still
// be *correct* — a moved row stops matching the predicate — but nothing indexes
// application_name, so both statements would scan from the start of the table
// every pass and filter the whole moved prefix back out. That is the quadratic
// shape this is written to avoid: at the default batch size a ten-million-row
// history would visit billions of rows and hit renameTimeout. With the bound,
// both statements are key ranges over the primary key and each row is visited
// once across the whole rename.
func renameInBatches(ctx context.Context, conn *pgx.Conn, table string, in RenameInput, match rowPredicate, progress io.Writer) (int64, error) {
	qualified := pgx.Identifier{in.Schema, table}.Sanitize()
	var moved int64

	// Everything below this key has already been moved, so each batch starts
	// where the last one stopped. Empty means "from the beginning of the
	// table"; a workflow_uuid is never empty, so there is no ambiguity.
	var lowerBound string

	for {
		// Find the key that bounds this batch: the batch-size-th distinct
		// workflow_uuid still matching, at or after the previous watermark.
		// DISTINCT so one workflow's steps are never split across two batches.
		boundArgs := []any{}
		boundPredicate := match(in, &boundArgs)
		boundQuery := fmt.Sprintf("SELECT DISTINCT workflow_uuid FROM %s WHERE %s", qualified, boundPredicate)
		if lowerBound != "" {
			boundArgs = append(boundArgs, lowerBound)
			boundQuery += fmt.Sprintf(" AND workflow_uuid >= $%d", len(boundArgs))
		}
		boundArgs = append(boundArgs, in.BatchSize)
		boundQuery += fmt.Sprintf(" ORDER BY workflow_uuid OFFSET $%d LIMIT 1", len(boundArgs))

		var watermark string
		hasWatermark := true
		if err := conn.QueryRow(ctx, boundQuery, boundArgs...).Scan(&watermark); err != nil {
			if err != pgx.ErrNoRows {
				logf(progress, "Re-owned %d %s row(s) before failing", moved, table)
				return moved, fmt.Errorf("failed to bound a %s rename batch: %w", table, err)
			}
			// Fewer than a full batch remained, so this pass takes the rest.
			hasWatermark = false
		}

		args := []any{in.NewName}
		predicate := match(in, &args)
		query := fmt.Sprintf("UPDATE %s SET application_name = $1 WHERE %s", qualified, predicate)
		if lowerBound != "" {
			args = append(args, lowerBound)
			query += fmt.Sprintf(" AND workflow_uuid >= $%d", len(args))
		}
		if hasWatermark {
			args = append(args, watermark)
			query += fmt.Sprintf(" AND workflow_uuid < $%d", len(args))
		}

		tag, err := conn.Exec(ctx, query, args...)
		if err != nil {
			logf(progress, "Re-owned %d %s row(s) before failing", moved, table)
			return moved, fmt.Errorf("failed to re-own %s rows: %w", table, err)
		}
		moved += tag.RowsAffected()

		if !hasWatermark {
			logf(progress, "Re-owned %d %s row(s)", moved, table)
			return moved, nil
		}
		// A batch that moved nothing while a watermark still exists would spin:
		// nothing in the bounded range matches, and the bound never advances.
		if tag.RowsAffected() == 0 {
			logf(progress, "Re-owned %d %s row(s) before failing", moved, table)
			return moved, fmt.Errorf("a %s rename batch moved no rows while more remained; refusing to loop", table)
		}
		lowerBound = watermark
	}
}
