package EX

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// REQ001656: Reproduce the exact slt_good_124 failure pattern where
// the cross join produces too few rows or wrong values.
func TestCrossJoin_ShortCircuitRows(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()
	// Exact data from slt_good_124.test
	for _, s := range []string{
		"CREATE TABLE tab0(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"CREATE TABLE tab1(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"CREATE TABLE tab2(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"INSERT INTO tab0 VALUES(89,91,82)",
		"INSERT INTO tab0 VALUES(35,97,1)",
		"INSERT INTO tab0 VALUES(24,86,33)",
		"INSERT INTO tab1 VALUES(64,10,57)",
		"INSERT INTO tab1 VALUES(3,26,54)",
		"INSERT INTO tab1 VALUES(80,13,96)",
		"INSERT INTO tab2 VALUES(7,31,27)",
		"INSERT INTO tab2 VALUES(79,17,38)",
		"INSERT INTO tab2 VALUES(78,59,26)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// L838: SELECT 76 * - cor0.col2 col2 FROM tab0, tab1 AS cor0
	// Expected: 9 values hashing to 7c7275be2a652135d8807f37aa5bda8f
	rows, err := e.QueryAll(ctx, "SELECT 76 * - cor0.col2 col2 FROM tab0, tab1 AS cor0")
	if err != nil {
		t.Fatalf("L838 query: %v", err)
	}
	// Compute hash the same way as SLT runner (rowsort).
	vals := make([]string, 0, len(rows))
	for _, r := range rows {
		if len(r.Data) > 0 {
			vals = append(vals, strconv.FormatInt(r.Data[0].ToAny().(int64), 10))
		}
	}
	sort.Strings(vals)
	h := md5.New()
	for _, v := range vals {
		h.Write([]byte(v))
		h.Write([]byte{'\n'})
	}
	gotHash := hex.EncodeToString(h.Sum(nil))
	t.Logf("L838: %d rows, hash=%s (want 7c7275be2a652135d8807f37aa5bda8f)", len(rows), gotHash)
	t.Logf("  values: %v", vals)
	if gotHash != "7c7275be2a652135d8807f37aa5bda8f" {
		t.Errorf("L838: hash mismatch: got %s, want 7c7275be2a652135d8807f37aa5bda8f", gotHash)
	}

	// Cell count tests
	queries := []struct {
		sql      string
		wantRows int
		wantCols int
	}{
		{"SELECT ALL * FROM tab1, tab1 cor0, tab2, tab0 AS cor1", 81, 12},
		{"SELECT ALL * FROM tab0, tab0 cor0, tab1 cor1, tab2, tab2 AS cor2", 243, 15},
		{"SELECT DISTINCT cor0.col2 + + tab0.col1 AS col2 FROM tab0, tab0 AS cor0", 9, 1},
		{"SELECT DISTINCT * FROM tab1, tab0 cor0, tab0, tab0 cor1", 81, 12},
	}
	for _, q := range queries {
		rows, err := e.QueryAll(ctx, q.sql)
		if err != nil {
			t.Errorf("query %q: %v", q.sql, err)
			continue
		}
		cols := 0
		if len(rows) > 0 {
			cols = len(rows[0].Data)
		}
		t.Logf("QUERY: %s\n  rows=%d cols=%d cells=%d (want rows=%d cols=%d cell=%d)",
			q.sql, len(rows), cols, len(rows)*cols, q.wantRows, q.wantCols, q.wantRows*q.wantCols)
		if len(rows) != q.wantRows {
			t.Errorf("query %q: expected %d rows, got %d", q.sql, q.wantRows, len(rows))
		}
		if cols != q.wantCols {
			t.Errorf("query %q: expected %d cols, got %d", q.sql, q.wantCols, cols)
		}
	}

	// L372: SELECT DISTINCT cor0.col2 + - cor0.col0 FROM tab2, tab0 AS cor0
	// Expected: 3 distinct values: -7, -34, 9
	rows, err = e.QueryAll(ctx, "SELECT DISTINCT cor0.col2 + - cor0.col0 FROM tab2, tab0 AS cor0")
	if err != nil {
		t.Fatalf("L372 query: %v", err)
	}
	t.Logf("L372 rows=%d", len(rows))
	wantL372 := map[int64]bool{-7: true, -34: true, 9: true}
	for _, r := range rows {
		if len(r.Data) == 0 {
			continue
		}
		v := r.Data[0].ToAny().(int64)
		t.Logf("  L372 value: %d", v)
		if !wantL372[v] {
			t.Errorf("L372: unexpected value %d (want one of -7, -34, 9)", v)
		}
	}
	if len(rows) != 3 {
		t.Errorf("L372: expected 3 distinct values, got %d", len(rows))
	}

	// L491: SELECT tab1.col1 FROM tab0, tab1 AS cor0 CROSS JOIN tab1
	// This is a 3-way cross join: tab0(3) × cor0(3) × tab1(3) = 27 rows.
	// REQ001656: before the fix, this returned too few rows because
	// the unaliased tab1 reference couldn't be resolved in the
	// pre-built join schema (bare "col1" collided with cor0's
	// "col1", so tab1.col1 resolved to the wrong column).
	rows, err = e.QueryAll(ctx, "SELECT tab1.col1 FROM tab0, tab1 AS cor0 CROSS JOIN tab1")
	if err != nil {
		t.Fatalf("L491 query: %v", err)
	}
	t.Logf("L491: %d rows", len(rows))
	if len(rows) != 27 {
		t.Errorf("L491: expected 27 rows (3×3×3 cross join), got %d", len(rows))
	}
}

// rowsColCount returns the number of columns in the first row, or 0.
func rowsColCount(rows []DT.Row) int {
	if len(rows) == 0 {
		return 0
	}
	return len(rows[0].Data)
}

var _ = rowsColCount
var _ = fmt.Sprintf
