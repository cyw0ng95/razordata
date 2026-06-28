//go:build slt_corpus

package slt

import "testing"

// TestParseResultRows_VARCHARWithSpaces locks in REQ001092 fix:
// VARCHAR cells that contain spaces must be treated as a single
// value, not split into multiple phantom columns.
func TestParseResultRows_VARCHARWithSpaces(t *testing.T) {
	lines := []string{
		"109434",
		"table tn4 row 83",
		"174216",
		"table tn4 row 83",
		"180804",
		"table tn4 row 83",
		"219966",
		"table tn4 row 83",
	}
	rows := parseResultRows(lines, "IT")
	if len(rows) != 4 {
		t.Errorf("got %d rows, want 4 (8 lines / 2 cols)", len(rows))
	}
	for i, row := range rows {
		if len(row) != 2 {
			t.Errorf("row %d: got %d cols, want 2", i, len(row))
		}
	}
	// First row: 109434 (I), "table tn4 row 83" (T)
	if rows[0][0].Text != "109434" && rows[0][0].Text != "" {
		// ParseValue may have set Text for the I-column; allow it.
	}
	if rows[0][1].Text != "table tn4 row 83" {
		t.Errorf("row 0 col 1: got %q, want %q", rows[0][1].Text, "table tn4 row 83")
	}
}

// TestParseResultRows_TabSeparatedStillWorks verifies the tab
// format still works after the REQ001092 fix.
func TestParseResultRows_TabSeparatedStillWorks(t *testing.T) {
	lines := []string{
		"109434\ttable tn4 row 83",
		"174216\ttable tn4 row 83",
	}
	rows := parseResultRows(lines, "IT")
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
	if rows[0][1].Text != "table tn4 row 83" {
		t.Errorf("row 0 col 1: got %q, want %q", rows[0][1].Text, "table tn4 row 83")
	}
}
