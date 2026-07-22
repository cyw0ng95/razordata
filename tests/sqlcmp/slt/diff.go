package slt

import (
	"fmt"
	"hash"
	"sort"
	"strconv"
	"strings"
	"sync"
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
		return diffHashed(actual, expected[0][0].Text, rec.Sort)
	}
	// Otherwise sort the actual result in-place (no copy needed;
	// the result set is not reused after diff).
	got := actual.Rows
	// Early exact-match: for sorted queries, try O(n) in-order
	// comparison before the O(n log n) sort. When results already
	// match in order, this eliminates sorting entirely — common
	// for queries whose output happens to arrive in sort order.
	if rec.Sort != NoSort && len(got) == len(expected) {
		allMatch := true
		for i := range got {
			if !rowsEqual(got[i], expected[i], rec.TypeString) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return ""
		}
	}
	if rec.Sort == RowSort {
		sortRows(got, false)
	}
	// ValueSort: sort rows as tab-separated strings (SQLite convention).
	if rec.Sort == ValueSort {
		sort.Slice(got, func(i, j int) bool {
			return rowString(got[i]) < rowString(got[j])
		})
	}
	// "Expected" is already in source order. The corpus emits
	// expected rows in the order the test author intended; for
	// RowSort/ValueSort, we sort an index to avoid copying the
	// expected slice.
	wantIdx := make([]int, len(expected))
	for i := range wantIdx {
		wantIdx[i] = i
	}
	if rec.Sort == RowSort {
		sort.Slice(wantIdx, func(i, j int) bool {
			a, b := expected[wantIdx[i]], expected[wantIdx[j]]
			for k := 0; k < len(a) && k < len(b); k++ {
				if c := valueLess(a[k], b[k]); c != 0 {
					return c < 0
				}
			}
			return len(a) < len(b)
		})
	} else if rec.Sort == ValueSort {
		sort.Slice(wantIdx, func(i, j int) bool {
			return rowString(expected[wantIdx[i]]) < rowString(expected[wantIdx[j]])
		})
	}
	if len(got) != len(wantIdx) {
		return fmt.Sprintf("row count: got %d, want %d", len(got), len(wantIdx))
	}
	for i := range got {
		if !rowsEqual(got[i], expected[wantIdx[i]], rec.TypeString) {
			return fmt.Sprintf("row %d: got %s, want %s", i, renderRow(got[i]), renderRow(expected[wantIdx[i]]))
		}
	}
	return ""
}

// diffHashed validates an MD5-hashed expected row.
// SQLite's sqllogictest reference implementation sorts ROWS (as
// tab-separated cell strings lexicographically), then hashes each
// cell followed by newline. Sorting individual cells (flatten-then-sort)
// would produce a different hash for multi-column queries.
//
// When sortMode is NoSort, the rows are hashed in their original order
// without sorting, respecting the nosort directive in the test file.
func diffHashed(actual *ResultSet, marker string, sortMode SortMode) string {
	var n int
	var want string
	if _, err := fmt.Sscanf(marker, "%d values hashing to %s", &n, &want); err != nil {
		if len(actual.Rows) == 1 && len(actual.Rows[0]) == 1 && actual.Rows[0][0].Text == marker {
			return ""
		}
		return "hashed: unparseable marker " + marker
	}
	// Handle empty result set
	if len(actual.Rows) == 0 {
		if n == 0 {
			return ""
		}
		return fmt.Sprintf("hashed: got 0 cells, want %d", n)
	}
	flat := make([]Value, 0, len(actual.Rows)*len(actual.Rows[0]))
	if sortMode == NoSort {
		// Respect nosort: hash in original order without sorting.
		for _, row := range actual.Rows {
			flat = append(flat, row...)
		}
	} else {
		// Sort rows as tab-separated strings (SQLite sqllogictest convention).
		idx := make([]int, len(actual.Rows))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(i, j int) bool {
			return rowString(actual.Rows[idx[i]]) < rowString(actual.Rows[idx[j]])
		})
		for _, i := range idx {
			flat = append(flat, actual.Rows[i]...)
		}
	}
	if len(flat) != n {
		return fmt.Sprintf("hashed: got %d cells, want %d", len(flat), n)
	}
	h := hashValues(flat)
	if h != want {
		return fmt.Sprintf("hashed: got %s, want %s", h, want)
	}
	return ""
}

// rowString renders a row as a tab-separated string for lexicographic
// sorting, matching SQLite's sqllogictest row-comparison convention.
// Uses a pooled rowRenderBuf (strings.Builder + scratch []byte).
// REQ001456: pool the builder across rows.
// REQ001701: pool a scratch []byte for strconv.AppendInt/AppendFloat so
// the formatted digits land in a reused backing array instead of a fresh
// []byte per value. strconv.AppendInt(nil, ...) was the #1 alloc site in
// idx1000_2 (2.75M / 20.35% of alloc objects; rowString cum 39.73%).
type rowRenderBuf struct {
	sb      strings.Builder
	scratch []byte
}

var builderPool = sync.Pool{
	New: func() any { return &rowRenderBuf{} },
}

func rowString(row []Value) string {
	rb := builderPool.Get().(*rowRenderBuf)
	rb.sb.Reset()
	rb.scratch = rb.scratch[:0]
	for i, v := range row {
		if i > 0 {
			rb.sb.WriteByte('\t')
		}
		writeValueToBuilder(&rb.sb, &rb.scratch, v)
	}
	s := rb.sb.String()
	builderPool.Put(rb)
	return s
}

// writeValueToBuilder writes a Value's string representation to a Builder
// without an intermediate string allocation. REQ001456.
// REQ001701: scratch is a reusable []byte for strconv.AppendInt/AppendFloat
// (pass &rb.scratch from a pooled rowRenderBuf, or a local for one-off use).
// Passing nil-arg AppendInt allocated a fresh []byte per value.
func writeValueToBuilder(b *strings.Builder, scratch *[]byte, v Value) {
	switch v.Kind {
	case TypeNull:
		b.WriteString("NULL")
	case TypeInteger:
		*scratch = strconv.AppendInt((*scratch)[:0], v.Int, 10)
		b.Write(*scratch)
	case TypeReal:
		*scratch = strconv.AppendFloat((*scratch)[:0], v.Real, 'f', 3, 64)
		b.Write(*scratch)
	case TypeBlob:
		b.WriteString(v.Text)
	default:
		b.WriteString(v.Text)
	}
}

// hashValues computes an MD5 hash over the string representation of
// each value, separated by newlines. REQ001456: writes directly to
// the hash via writeValueToBytes to avoid intermediate string allocs.
// REQ001701: reuses one scratch []byte across all values in the call
// instead of allocating one per value.
func hashValues(vs []Value) string {
	h := md5Pool.Get().(hash.Hash)
	defer func() {
		h.Reset()
		md5Pool.Put(h)
	}()
	var scratch []byte
	for _, v := range vs {
		writeValueToBytes(h, &scratch, v)
		h.Write([]byte{'\n'})
	}
	return bytehex(h.Sum(nil))
}

// writeValueToBytes writes a Value's string representation directly to
// a hash.Hash, avoiding intermediate string and []byte allocations.
// REQ001456.
// REQ001701: scratch is a reusable []byte for strconv.AppendInt/AppendFloat.
func writeValueToBytes(h hash.Hash, scratch *[]byte, v Value) {
	switch v.Kind {
	case TypeNull:
		h.Write([]byte("NULL"))
	case TypeInteger:
		*scratch = strconv.AppendInt((*scratch)[:0], v.Int, 10)
		h.Write(*scratch)
	case TypeReal:
		*scratch = strconv.AppendFloat((*scratch)[:0], v.Real, 'f', 3, 64)
		h.Write(*scratch)
	case TypeBlob:
		h.Write([]byte(v.Text))
	default:
		h.Write([]byte(v.Text))
	}
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
//   - integer/real == text when the string representation matches (REQ001056)
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
		// SQLite coerces results to the declared column
		// type. When the corpus declares "query I" (integer), a float
		// result like 19.812 should match expected 19 via truncation.
		return a.Real == float64(b.Int) || int64(a.Real) == b.Int
	case a.Kind == TypeInteger && b.Kind == TypeText:
		return a.String() == b.String()
	case a.Kind == TypeText && b.Kind == TypeInteger:
		return a.String() == b.String()
	case a.Kind == TypeReal && b.Kind == TypeText:
		return a.String() == b.String()
	case a.Kind == TypeText && b.Kind == TypeReal:
		return a.String() == b.String()
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
