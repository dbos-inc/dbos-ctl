// Package output renders command results as an aligned table or raw JSON,
// selected by the -o/--output flag. Array responses go through List; the
// single-object key/value detail view lands with the app-reads milestone.
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
)

// ParseFormat validates an -o/--output value.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatTable, FormatJSON:
		return Format(s), nil
	default:
		return "", fmt.Errorf("unknown output format %q (want: table, json)", s)
	}
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
		return fmt.Errorf("unknown output format %q", format)
	}
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
