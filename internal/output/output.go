// Package output renders command results as an aligned table or raw JSON,
// selected by the -o/--output flag. Array responses go through List; a single
// object goes through Detail as an aligned label/value block.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Format is an -o/--output value.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	// FormatIDs prints one identifier per line, for piping into commands that
	// read IDs from stdin. Only commands with a natural ID honor it (see WriteIDs);
	// others reject it.
	FormatIDs Format = "ids"
)

// ParseFormat validates an -o/--output value.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatTable, FormatJSON, FormatIDs:
		return Format(s), nil
	default:
		return "", fmt.Errorf("unknown output format %q (want: table, json, ids)", s)
	}
}

// WriteIDs prints one identifier per line — the FormatIDs rendering, so
// `... -o ids | dbosctl workflow cancel -` pipelines work.
func WriteIDs(w io.Writer, ids []string) error {
	for _, id := range ids {
		if _, err := fmt.Fprintln(w, id); err != nil {
			return err
		}
	}
	return nil
}

// Column projects one table column from a row of type T.
type Column[T any] struct {
	Header string
	Value  func(T) string
}

// List renders rows in the given format: an aligned table built from cols, or
// the raw rows as indented JSON. json is deliberately the raw API shape — the
// column projection is never applied to it — so cols is ignored for JSON.
func List[T any](w io.Writer, format Format, rows []T, cols []Column[T]) error {
	// A nil slice would marshal as JSON "null"; normalize so an empty result is
	// the empty array "[]".
	if rows == nil {
		rows = []T{}
	}
	switch format {
	case FormatJSON:
		return writeJSON(w, rows)
	case FormatTable:
		return writeTable(w, rows, cols)
	default:
		return fmt.Errorf("output format %q is not supported by this command (try table or json)", format)
	}
}

// JSON renders a single value as indented JSON — the raw API shape for a
// single-object response, as List does for arrays.
func JSON(w io.Writer, v any) error {
	return writeJSON(w, v)
}

// Field projects one labeled value from a detail object of type T — Detail's
// single-object analog of Column.
type Field[T any] struct {
	Label string
	Value func(T) string
}

// Detail renders a single object: an aligned "label  value" block in table
// format, or the raw object as indented JSON. As with List, json is deliberately
// the raw API shape and ignores the field projection. A field whose projected
// value is empty is omitted from the table (nil/absent API fields don't clutter
// the view); json still shows it.
func Detail[T any](w io.Writer, format Format, v T, fields []Field[T]) error {
	switch format {
	case FormatJSON:
		return writeJSON(w, v)
	case FormatTable:
		return writeDetail(w, v, fields)
	default:
		return fmt.Errorf("output format %q is not supported by this command (try table or json)", format)
	}
}

func writeDetail[T any](w io.Writer, v T, fields []Field[T]) error {
	if len(fields) == 0 {
		return fmt.Errorf("detail output requires at least one field")
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, f := range fields {
		val := f.Value(v)
		if val == "" {
			continue // omit empty/absent fields from the detail view
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", f.Label, val); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func writeTable[T any](w io.Writer, rows []T, cols []Column[T]) error {
	if len(cols) == 0 {
		return fmt.Errorf("table output requires at least one column")
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = c.Header
	}
	if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
		return err
	}

	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = c.Value(row)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}
