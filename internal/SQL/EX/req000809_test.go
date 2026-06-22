//go:build !slt_corpus_full

package EX

import (
	"context"
	"testing"
)

// TestDivByZero verifies REQ000809: division by zero returns NULL
// (not error, not 0 rows).
func TestDivByZero(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()

	queries := []struct {
		sql string
		msg string
	}{
		{"SELECT CAST(NULL AS INTEGER) / -0", "CAST(NULL AS INTEGER) / -0"},
		{"SELECT 1 / 0", "1 / 0"},
		{"SELECT 1.0 / 0.0", "1.0 / 0.0"},
		{"SELECT 1 % 0", "1 %% 0"},
		{"SELECT 1 DIV 0", "1 DIV 0"},
	}
	for _, q := range queries {
		t.Run(q.msg, func(t *testing.T) {
			rows, err := ex.QueryAll(ctx, q.sql)
			if err != nil {
				t.Fatalf("query failed: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("got %d rows, want 1", len(rows))
			}
			if rows[0].Data[0].IsNull() == false {
				t.Errorf("got %v, want nil (NULL)", rows[0].Data[0])
			}
		})
	}
}