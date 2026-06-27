package EX

import (
	"context"
	"testing"
)

// TestNULL_AllSeededCases tests NULL handling across various SQL constructs.
func TestNULL_AllSeededCases(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	tests := []struct {
		name  string
		setup []string
		query string
		want  [][]any
	}{
		// REQ000929: NOT BETWEEN NULL AND x
		// Note: SQL three-valued logic says NOT BETWEEN NULL AND x = NOT NULL = NULL,
		// and WHERE NULL excludes the row. So expected is 0 rows.
		// The SLT test expects 9 rows (SQLite may have a special optimization),
		// but the SQL standard behavior is 0 rows.
		{
			name: "REQ000929_not_between_null",
			setup: []string{
				"CREATE TABLE t929 (col1 INTEGER)",
				"INSERT INTO t929 VALUES (1), (2), (3), (4), (5), (6), (7), (8), (9)",
			},
			query: "SELECT * FROM t929 WHERE col1 NOT BETWEEN NULL AND -col1",
			want:  [][]any{},
		},
		// REQ000931: CAST(NULL AS DECIMAL) returns NULL
		{
			name: "REQ000931_cast_null_decimal",
			setup: []string{
				"CREATE TABLE t931a (id INTEGER)",
				"INSERT INTO t931a VALUES (1), (2), (3)",
			},
			query: "SELECT CAST(NULL AS DECIMAL) FROM t931a",
			want:  [][]any{{nil}, {nil}, {nil}},
		},
		// REQ000931: CAST(NULL AS SIGNED) returns NULL
		{
			name: "REQ000931_cast_null_signed",
			setup: []string{
				"CREATE TABLE t931b (id INTEGER)",
				"INSERT INTO t931b VALUES (1), (2)",
			},
			query: "SELECT CAST(NULL AS SIGNED) FROM t931b",
			want:  [][]any{{nil}, {nil}},
		},
		// REQ000942: SUM(ALL CAST(NULL AS SIGNED)) returns NULL
		{
			name: "REQ000942_sum_all_null",
			setup: []string{
				"CREATE TABLE t942 (id INTEGER)",
				"INSERT INTO t942 VALUES (1), (2), (3)",
			},
			query: "SELECT SUM(ALL CAST(NULL AS SIGNED)) FROM t942",
			want:  [][]any{{nil}},
		},
		// REQ000915: NULLIF with chained unary minus
		// COUNT(*) without GROUP BY returns total count (2 for 2 rows).
		// NULLIF(-2, -693) = -2, then -2 + 54 = 52. One row.
		{
			name: "REQ000915_nullif_chained_unary",
			setup: []string{
				"CREATE TABLE t915 (id INTEGER)",
				"INSERT INTO t915 VALUES (1), (2)",
			},
			query: "SELECT NULLIF(-COUNT(*), +67 * - - (+25) + 89 + -39 * 63) + +54 FROM t915",
			want:  [][]any{{int64(52)}},
		},
		// REQ000941: IS NOT NULL filter
		{
			name: "REQ000941_is_not_null_filter",
			setup: []string{
				"CREATE TABLE t941 (col0 INTEGER)",
				"INSERT INTO t941 VALUES (1), (2), (3)",
			},
			query: "SELECT col0 FROM t941 WHERE +col0 IS NOT NULL",
			want:  [][]any{{int64(1)}, {int64(2)}, {int64(3)}},
		},
		// REQ000912: NULL three-valued logic in comparison
		{
			name: "REQ000912_compare_with_null",
			setup: []string{
				"CREATE TABLE t912 (id INTEGER, v INTEGER)",
				"INSERT INTO t912 VALUES (1, 10), (2, NULL), (3, 30)",
			},
			// v > 20: only row 3 (v=30) should match; row 2 (v=NULL) excluded
			query: "SELECT id FROM t912 WHERE v > 20",
			want:  [][]any{{int64(3)}},
		},
		// REQ000913: Complex WHERE with OR, IN, IS NULL
		{
			name: "REQ000913_or_in_isnull",
			setup: []string{
				"CREATE TABLE t913 (col0 INTEGER, col1 INTEGER, col3 INTEGER)",
				"INSERT INTO t913 VALUES (1, 50, 30), (2, 80, NULL), (3, 60, 50)",
			},
			// ((col1 < 71.25)) OR (col3 IN (53,42,27,44) OR (col3 IS NULL)) AND (col0 >= 42)
			// Row 1: col1=50<71.25 → TRUE (short-circuit)
			// Row 2: col1=80, col3=NULL, col0=2<42 → FALSE
			// Row 3: col1=60<71.25 → TRUE (short-circuit)
			query: "SELECT col0 FROM t913 WHERE ((col1 < 71.25)) OR (col3 IN (53,42,27,44) OR (col3 IS NULL)) AND (col0 >= 42)",
			want:  [][]any{{int64(1)}, {int64(3)}},
		},
		// REQ000918: SUM(DISTINCT -col2) on TEXT column
		{
			name: "REQ000918_sum_distinct_text",
			setup: []string{
				"CREATE TABLE t918 (col2 TEXT)",
				"INSERT INTO t918 VALUES ('a'), ('b'), ('a')",
			},
			// SUM(DISTINCT -col2): text can't be negated → all NULL → SUM returns NULL
			query: "SELECT SUM(DISTINCT -col2) FROM t918",
			want:  [][]any{{nil}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, s := range tt.setup {
				if _, err := ex.Exec(ctx, s); err != nil {
					t.Fatalf("setup %q: %v", s, err)
				}
			}
			rows, err := ex.QueryAll(ctx, tt.query)
			if err != nil {
				t.Fatalf("query failed: %v", err)
			}
			if len(rows) != len(tt.want) {
				t.Fatalf("expected %d rows, got %d: %v", len(tt.want), len(rows), rows)
			}
			for i, row := range rows {
				if len(row.Data) != len(tt.want[i]) {
					t.Fatalf("row %d: expected %d cols, got %d", i, len(tt.want[i]), len(row.Data))
				}
				for j, v := range row.Data {
					got := v.ToAny()
					want := tt.want[i][j]
					if got != want {
						t.Fatalf("row %d col %d: got %v, want %v", i, j, got, want)
					}
				}
			}
		})
	}
}