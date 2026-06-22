//go:build !slt_corpus_full

package EX

import (
	"context"
	"testing"
)

// TestAggregate_AllKeyword verifies REQ000805: ALL keyword
// in aggregate functions is accepted as a no-op.
func TestAggregate_AllKeyword(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 20)",
		"INSERT INTO t VALUES (3, 30)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}

	tests := []struct {
		sql  string
		name string
		want any
	}{
		{"SELECT MIN(ALL v) FROM t", "MIN(ALL)", int64(10)},
		{"SELECT MAX(ALL v) FROM t", "MAX(ALL)", int64(30)},
		{"SELECT COUNT(ALL v) FROM t", "COUNT(ALL)", int64(3)},
		{"SELECT SUM(ALL v) FROM t", "SUM(ALL)", int64(60)},
		{"SELECT AVG(ALL v) FROM t", "AVG(ALL)", float64(20)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := ex.QueryAll(ctx, tc.sql)
			if err != nil {
				t.Fatalf("%s: %v", tc.sql, err)
			}
			if len(rows) != 1 {
				t.Fatalf("%s: got %d rows, want 1", tc.sql, len(rows))
			}
		got := rows[0].Data[0]
		if got.ToAny() != tc.want {
				t.Errorf("%s = %v, want %v", tc.sql, got, tc.want)
			}
		})
	}
}
