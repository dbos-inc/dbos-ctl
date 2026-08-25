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

// ApplicationRowCounts reports what a rename moved, by kind.
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

	if counts.Queues, err = move("queues"); err != nil {
		return counts, err
	}
	if counts.Schedules, err = move("workflow_schedules"); err != nil {
		return counts, err
	}
	if counts.Versions, err = move("application_versions"); err != nil {
		return counts, err
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
		return counts, fmt.Errorf("failed to re-own in-flight workflow rows: %w", err)
	}
	inFlight := tag.RowsAffected()

	if err := tx.Commit(execCtx); err != nil {
		return counts, fmt.Errorf("failed to commit the rename: %w", err)
	}
	logf(progress, "Re-owned %d queue(s), %d schedule(s), %d version(s), %d in-flight workflow(s)",
		counts.Queues, counts.Schedules, counts.Versions, inFlight)

	// Terminal workflows and their steps are unbounded — a year of history is
	// however many rows it is — so they move in their own transactions.
	terminal, err := renameInBatches(execCtx, conn, "workflow_status", in, progress)
	if err != nil {
		return counts, err
	}
	counts.Workflows = inFlight + terminal

	if counts.Steps, err = renameInBatches(execCtx, conn, "operation_outputs", in, progress); err != nil {
		return counts, err
	}
	return counts, nil
}

// renameInBatches re-owns a table's rows in half-open workflow_uuid ranges.
//
// Ranges rather than LIMIT: a LIMIT would repage the rows it has already moved
// — they still match the predicate on the next pass only until they are moved,
// and the scan to skip them grows with every batch — while an IN list of keys
// plans as a whole-table hash join. Bounding each batch by a key means the work
// already done is behind the watermark and never looked at again, and an
// interrupted run resumes where it stopped rather than starting over.
func renameInBatches(ctx context.Context, conn *pgx.Conn, table string, in RenameInput, progress io.Writer) (int64, error) {
	qualified := pgx.Identifier{in.Schema, table}.Sanitize()
	var moved int64

	for {
		// Find the key that bounds this batch: the batch-size-th distinct
		// workflow_uuid still matching. DISTINCT so one workflow's steps are
		// never split across two batches.
		boundArgs := []any{}
		boundPredicate := sourcePredicate(in, &boundArgs)
		boundArgs = append(boundArgs, in.BatchSize)
		boundQuery := fmt.Sprintf(
			"SELECT DISTINCT workflow_uuid FROM %s WHERE %s ORDER BY workflow_uuid OFFSET $%d LIMIT 1",
			qualified, boundPredicate, len(boundArgs))

		var watermark string
		hasWatermark := true
		if err := conn.QueryRow(ctx, boundQuery, boundArgs...).Scan(&watermark); err != nil {
			if err != pgx.ErrNoRows {
				return moved, fmt.Errorf("failed to bound a %s rename batch: %w", table, err)
			}
			// Fewer than a full batch remained, so this pass takes the rest.
			hasWatermark = false
		}

		args := []any{in.NewName}
		predicate := sourcePredicate(in, &args)
		query := fmt.Sprintf("UPDATE %s SET application_name = $1 WHERE %s", qualified, predicate)
		if hasWatermark {
			args = append(args, watermark)
			query += fmt.Sprintf(" AND workflow_uuid < $%d", len(args))
		}

		tag, err := conn.Exec(ctx, query, args...)
		if err != nil {
			return moved, fmt.Errorf("failed to re-own %s rows: %w", table, err)
		}
		moved += tag.RowsAffected()

		if !hasWatermark {
			logf(progress, "Re-owned %d %s row(s)", moved, table)
			return moved, nil
		}
		// A batch that moved nothing while a watermark still exists would spin:
		// nothing below the bound matches, and the bound never advances.
		if tag.RowsAffected() == 0 {
			return moved, fmt.Errorf("a %s rename batch moved no rows while more remained; refusing to loop", table)
		}
	}
}
