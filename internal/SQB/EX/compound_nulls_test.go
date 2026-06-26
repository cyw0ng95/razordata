package EX

import (
	"context"
	"fmt"
	"testing"
)

// TestUnionNulls verifies REQ000814: UNION and UNION ALL
// handle NULL values correctly in deduplication.
func TestUnionNulls(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v"})

	ctx := context.Background()
	// Insert rows with NULL values.
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, NULL), (2, 10), (3, NULL)"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// First, test basic SELECT with NULL to verify NULLs are returned correctly.
	rowsSelect, err := ex.QueryAll(ctx, "SELECT v FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("SELECT v: %v", err)
	}
	t.Logf("SELECT v: %d rows", len(rowsSelect))
	for i, r := range rowsSelect {
		t.Logf("  row %d: Data=%v", i, r.Data)
	}

	// First, test basic UNION without NULLs to verify the operator works.
	rowsBasic, err := ex.QueryAll(ctx, "SELECT id FROM t UNION SELECT id FROM t")
	if err != nil {
		t.Fatalf("UNION (basic): %v", err)
	}
	t.Logf("UNION (basic): %d rows", len(rowsBasic))

	// UNION ALL should preserve all rows including NULLs.
	rowsAll, err := ex.QueryAll(ctx, "SELECT v FROM t UNION ALL SELECT v FROM t")
	if err != nil {
		t.Fatalf("UNION ALL: %v", err)
	}
	t.Logf("UNION ALL: %d rows", len(rowsAll))
	if len(rowsAll) != 6 {
		t.Errorf("UNION ALL: expected 6 rows, got %d", len(rowsAll))
	}

	// UNION should deduplicate, treating NULL = NULL as equal.
	rows, err := ex.QueryAll(ctx, "SELECT v FROM t UNION SELECT v FROM t")
	if err != nil {
		t.Fatalf("UNION: %v", err)
	}
	t.Logf("UNION: %d rows", len(rows))
	// Expected: NULL, 10 (two distinct values)
	if len(rows) != 2 {
		t.Errorf("UNION: expected 2 rows, got %d", len(rows))
	}
}

// REQ000838: multi-branch UNION ALL chains (e.g., 8 SELECT branches
// concatenated) must produce the right number of rows and preserve
// order. The streaming fast path avoids materializing the full
// result at every level of the binary tree, but the externally
// observable behavior must be identical to the materialized path.
//
// This regression test exercises a 6-branch UNION ALL where each
// branch contributes 1 row from a different table — historically
// the materialized path allocated an intermediate []Row for each
// pair, which dominated the SLT select4 compound-query timings.
func TestCompoundUnionAll_MultiBranchStreaming(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	for i := 1; i <= 6; i++ {
		ex.RegisterTable(
			fmt.Sprintf("t%d", i),
			[]string{"id", "v"},
		)
	}
	ctx := context.Background()
	for i := 1; i <= 6; i++ {
		_, err := ex.Exec(ctx, fmt.Sprintf("INSERT INTO t%d VALUES (1, %d)", i, i*10))
		if err != nil {
			t.Fatalf("insert t%d: %v", i, err)
		}
	}
	q := "SELECT v FROM t1 UNION ALL SELECT v FROM t2 UNION ALL SELECT v FROM t3 " +
		"UNION ALL SELECT v FROM t4 UNION ALL SELECT v FROM t5 UNION ALL SELECT v FROM t6"
	rows, err := ex.QueryAll(ctx, q)
	if err != nil {
		t.Fatalf("6-branch UNION ALL: %v", err)
	}
	if len(rows) != 6 {
		t.Errorf("expected 6 rows, got %d", len(rows))
	}
	want := []int64{10, 20, 30, 40, 50, 60}
	for i, r := range rows {
		if got := r.Data[0].I64; got != want[i] {
			t.Errorf("row %d: got v=%d, want %d", i, got, want[i])
		}
	}
}
