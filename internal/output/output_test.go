package output

import (
	"bytes"
	"strings"
	"testing"
)

type row struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func testCols() []Column[row] {
	return []Column[row]{
		{Header: "NAME", Value: func(r row) string { return r.Name }},
		{Header: "STATUS", Value: func(r row) string { return r.Status }},
	}
}

func TestParseFormat(t *testing.T) {
	for _, s := range []string{"table", "json"} {
		if _, err := ParseFormat(s); err != nil {
			t.Errorf("ParseFormat(%q) = %v, want nil", s, err)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error(`ParseFormat("yaml") = nil, want error`)
	}
}

func TestListTable(t *testing.T) {
	rows := []row{{"app1", "RUNNING"}, {"app2", "STOPPED"}}
	var buf bytes.Buffer
	if err := List(&buf, FormatTable, rows, testCols()); err != nil {
		t.Fatal(err)
	}
	// Columns aligned with two-space padding; the last column is not padded.
	want := "NAME  STATUS\napp1  RUNNING\napp2  STOPPED\n"
	if got := buf.String(); got != want {
		t.Errorf("table mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestListTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := List(&buf, FormatTable, []row{}, testCols()); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "NAME  STATUS\n"; got != want {
		t.Errorf("empty table mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestListTableNoColumns(t *testing.T) {
	var buf bytes.Buffer
	if err := List(&buf, FormatTable, []row{{"a", "b"}}, nil); err == nil {
		t.Error("table with no columns = nil error, want error")
	}
}

func TestListJSON(t *testing.T) {
	rows := []row{{"app1", "RUNNING"}}
	var buf bytes.Buffer
	if err := List(&buf, FormatJSON, rows, testCols()); err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"name\": \"app1\",\n    \"status\": \"RUNNING\"\n  }\n]\n"
	if got := buf.String(); got != want {
		t.Errorf("json mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestListJSONEmptyIsArray(t *testing.T) {
	var buf bytes.Buffer
	// A nil slice must render as "[]", not "null".
	if err := List(&buf, FormatJSON, []row(nil), testCols()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("empty json = %q, want %q", got, "[]")
	}
}

func TestListUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := List(&buf, Format("xml"), []row{}, testCols()); err == nil {
		t.Error("unknown format = nil error, want error")
	}
}

func testFields() []Field[row] {
	return []Field[row]{
		{Label: "name", Value: func(r row) string { return r.Name }},
		{Label: "status", Value: func(r row) string { return r.Status }},
	}
}

func TestDetailTable(t *testing.T) {
	var buf bytes.Buffer
	if err := Detail(&buf, FormatTable, row{"app1", "RUNNING"}, testFields()); err != nil {
		t.Fatal(err)
	}
	want := "name    app1\nstatus  RUNNING\n"
	if got := buf.String(); got != want {
		t.Errorf("detail table mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestDetailTableOmitsEmpty(t *testing.T) {
	var buf bytes.Buffer
	// An empty projected value (an absent/nil API field) is left out entirely.
	if err := Detail(&buf, FormatTable, row{"app1", ""}, testFields()); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "name  app1\n"; got != want {
		t.Errorf("detail omit-empty mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestDetailJSON(t *testing.T) {
	var buf bytes.Buffer
	// json is the raw object; the field projection (and omit-empty) do not apply.
	if err := Detail(&buf, FormatJSON, row{"app1", ""}, testFields()); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"name\": \"app1\",\n  \"status\": \"\"\n}\n"
	if got := buf.String(); got != want {
		t.Errorf("detail json mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestDetailNoFields(t *testing.T) {
	var buf bytes.Buffer
	if err := Detail(&buf, FormatTable, row{"a", "b"}, nil); err == nil {
		t.Error("detail with no fields = nil error, want error")
	}
}
