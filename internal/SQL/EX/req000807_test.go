//go:build !slt_corpus_full

package EX

import (
	"context"
	"testing"
)

// TestNullIf verifies REQ000807: NULLIF returns NULL when equal,
// otherwise returns the first argument.
func TestNullIf(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()

	// Insert 3 rows
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 20)",
		"INSERT INTO t VALUES (3, 30)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}

	// Test basic NULLIF behavior (standard SQL semantics)
	// NULLIF(a, b) returns NULL if a = b, otherwise returns a
	// Note: a = NULL is unknown (not true), so NULLIF(a, NULL) returns a
	tests := []struct {
		sql  string
		want any
	}{
		{"SELECT NULLIF(5, 5)", nil},
		{"SELECT NULLIF(5, 3)", int64(5)},
		{"SELECT NULLIF(-3, 1000)", int64(-3)},
		{"SELECT NULLIF(NULL, 5)", nil},
		{"SELECT NULLIF(5, NULL)", int64(5)}, // standard SQL: 5 != NULL (unknown), returns 5
	}

	for _, tc := range tests {
		t.Run(tc.sql, func(t *testing.T) {
			rows, err := ex.QueryAll(ctx, tc.sql)
			if err != nil {
				t.Fatalf("query failed: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("got %d rows, want 1", len(rows))
			}
			got := rows[0].Data[0]
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
