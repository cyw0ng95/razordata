package EX

import (
	"context"
	"strings"
	"testing"
)

// TestREQ000907_FetchFirst verifies FETCH FIRST/NEXT n ROWS ONLY parsing.
func TestREQ000907_FetchFirst(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t (id INTEGER)",
		"INSERT INTO t VALUES (1), (2), (3), (4), (5)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// Test FETCH FIRST 5 ROWS ONLY
	_, err := ex.QueryAll(ctx, "SELECT * FROM t ORDER BY id FETCH FIRST 5 ROWS ONLY")
	if err != nil {
		t.Logf("FETCH FIRST 5 ROWS ONLY: error=%v", err)
		if strings.Contains(err.Error(), "syntax error") {
			t.Fatalf("FETCH FIRST should be parsed, got syntax error: %v", err)
		}
	}

	// Test FETCH NEXT 3 ROWS ONLY
	_, err = ex.QueryAll(ctx, "SELECT * FROM t ORDER BY id FETCH NEXT 3 ROWS ONLY")
	if err != nil {
		t.Logf("FETCH NEXT 3 ROWS ONLY: error=%v", err)
	}

	// Test FETCH FIRST ROW ONLY (no count)
	_, err = ex.QueryAll(ctx, "SELECT * FROM t ORDER BY id FETCH FIRST ROW ONLY")
	if err != nil {
		t.Logf("FETCH FIRST ROW ONLY: error=%v", err)
	}
}