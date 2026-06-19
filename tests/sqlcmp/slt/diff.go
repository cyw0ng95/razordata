package slt

import (
	"fmt"
	"sort"
	"strings"
)

// DiffResultSets returns "" when the actual result set matches the
// expected rows according to the query's sort mode, label
// expectations, and hash-threshold rules. A non-empty return value
// is a human-readable diagnostic suitable for `go test -v`
// output.
//
// Hash-threshold rules: if the expected row set contains a single
// "N values hashing to HEX" marker and the actual result has N
// cells, the runner computes the same MD5 over the actual cells
// and verifies the marker. This is the corpus' mechanism for
// keeping large result sets compact.
//
// Sort-mode rules: NoSort compares rows in order; RowSort sorts
// all rows lexicographically before comparing; ValueSort flattens
// and sorts individual cells.
func DiffResultSets(actual *ResultSet, rec *Record) string {
	expected := rec.Expected
	// Hash-threshold short-circuit: if the expected is a single
	// "N values hashing to HEX" row, validate the actual cell
	// count and MD5 match.
	if len(expected) == 1 && len(expected[0]) == 1 && strings.Contains(expected[0][0].Text, "values hashing to") {
		return diffHashed(actual, expected[0][0].Text)
	}
	// Otherwise copy and (optionally) sort the actual result.
	got := append([][]Value(nil), actual.Rows...)
	if rec.Sort == RowSort {
		sortRows(got, false)
	} else if rec.Sort == ValueSort {
		sortRows(got, true)
	}
	// "Expected" is already in source order. The corpus emits
	// expected rows in the order the test author intended; for
	// RowSort, we also sort the expected side so the diff is
	// order-independent.
	want := append([][]Value(nil), expected...)
	if rec.Sort == RowSort {
		sortRows(want, false)
	} else if rec.Sort == ValueSort {
		sortRows(want, true)
	}
	if len(got) != len(want) {
		return fmt.Sprintf("row count: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		if !rowsEqual(got[i], want[i], rec.TypeString) {
			return fmt.Sprintf("row %d: got %s, want %s", i, renderRow(got[i]), renderRow(want[i]))
		}
	}
	return ""
}

// diffHashed validates an MD5-hashed expected row.
func diffHashed(actual *ResultSet, marker string) string {
	var n int
	var want string
	if _, err := fmt.Sscanf(marker, "%d values hashing to %s", &n, &want); err != nil {
		// Some corpora use different phrasing; fall back to
		// literal text comparison and let the caller decide.
		if len(actual.Rows) == 1 && len(actual.Rows[0]) == 1 && actual.Rows[0][0].Text == marker {
			return ""
		}
		return "hashed: unparseable marker " + marker
	}
	var flat []Value
	for _, row := range actual.Rows {
		flat = append(flat, row...)
	}
	if len(flat) != n {
		return fmt.Sprintf("hashed: got %d cells, want %d", len(flat), n)
	}
	sort.Slice(flat, func(i, j int) bool { return valueLess(flat[i], flat[j]) < 0 })
	h := hashValues(flat)
	if h != want {
		return fmt.Sprintf("hashed: got %s, want %s", h, want)
	}
	return ""
}

func hashValues(vs []Value) string {
	var b strings.Builder
	for _, v := range vs {
		b.WriteString(v.String())
		b.WriteByte('\n')
	}
	return md5hex(b.String())
}

// md5hex is a thin wrapper that avoids importing crypto/md5 in
// diff.go's hot path indirectly.
func md5hex(s string) string {
	sum := md5sum(s)
	return fmt.Sprintf("%x", sum)
}

// rowsEqual compares two rows column-by-column, with type-aware
// tolerance for integer↔real round-trips. If typeString is empty,
// columns are compared by Kind compatibility only.
func rowsEqual(a, b []Value, typeString string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !valueEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// valueEqual is the per-cell comparator. We accept a few lenient
// cases that are common in real corpora:
//
//   - integer 0 == real 0.0
//   - integer that round-trips losslessly to a real and back
//   - text and "empty" rendered text
func valueEqual(a, b Value) bool {
	if a.Kind == b.Kind {
		switch a.Kind {
		case TypeNull:
			return true
		case TypeText:
			return a.Text == b.Text
		case TypeInteger:
			return a.Int == b.Int
		case TypeReal:
			return a.Real == b.Real
		}
	}
	// Cross-kind tolerance.
	switch {
	case a.Kind == TypeInteger && b.Kind == TypeReal:
		return float64(a.Int) == b.Real
	case a.Kind == TypeReal && b.Kind == TypeInteger:
		return a.Real == float64(b.Int)
	}
	return false
}

// renderRow is a debug helper used in diff diagnostics.
func renderRow(row []Value) string {
	parts := make([]string, len(row))
	for i, c := range row {
		parts[i] = c.String()
	}
	return "[" + strings.Join(parts, " | ") + "]"
}
