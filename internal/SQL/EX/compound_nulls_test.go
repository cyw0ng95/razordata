package EX

import (
	"context"
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
