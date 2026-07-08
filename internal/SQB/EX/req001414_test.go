package EX

import (
	"context"
	"fmt"
	"testing"
)

// TestREQ001414_BushyGroup_SingleTablePredLeak verifies that single-table
// predicates (e.g. `a9 in (...)`, `d6 in (...)`) do NOT leak into the
// merge-phase NLJ's ON clause and silently drop valid rows.
//
// Before REQ001414, localConjuncts was built from crossTableConjuncts (ALL
// WHERE conjuncts) instead of crossTablePredicates (only cross-table).
// Single-table predicates like `a9 in (...)` would land in the merge-phase
// NLJ's ON clause. The NLJ ON evaluator only inspects the inner row —
// when the inner row's table doesn't own the referenced column, the
// column lookup returns nil, the predicate evaluates to false, and the
// entire (left, right) pair is dropped. This produced table-order-dependent
// row loss (14/21 in select4 L39784).
func TestREQ001414_BushyGroup_SingleTablePredLeak(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	for i := 1; i <= 9; i++ {
		ddl := fmt.Sprintf(
			"CREATE TABLE t%d(a%d INTEGER, b%d INTEGER, c%d INTEGER, d%d INTEGER, e%d INTEGER, x%d VARCHAR(30))",
			i, i, i, i, i, i, i,
		)
		if _, err := ex.Exec(ctx, ddl); err != nil {
			t.Fatalf("CREATE TABLE t%d: %v", i, err)
		}
	}

	// Insert 3 rows per table. Join keys:
	//   b4 = d6   → t4.b4 = t6.d6
	//   e8 = c9   → t8.e8 = t9.c9
	//   a1 = d8   → t1.a1 = t8.d8
	// Single-table predicates (a9 in, d6 in, c5=625, e7=168, a3 in)
	// must apply to their owning tables via predicate pushdown,
	// NOT to the merge-phase NLJ's ON clause.
	for i := 0; i < 3; i++ {
		// t4: b4 = 200+i (will match t6.d6)
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t4 VALUES(%d,%d,%d,%d,%d,'r4%d')", 100+i, 200+i, 300+i, 400+i, 500+i, i))
		// t6: d6 = 200+i (matches t4.b4)
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t6 VALUES(%d,%d,%d,%d,%d,'r6%d')", 600+i, 200+i, 800+i, 200+i, 900+i, i))
		// t8: e8 = 1200+i (matches t9.c9), d8 = 1300+i (matches t1.a1)
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t8 VALUES(%d,%d,%d,%d,%d,'r8%d')", 1000+i, 1100+i, 1200+i, 1300+i, 1200+i, i))
		// t9: c9 = 1200+i (matches t8.e8), a9 = 1400+i
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t9 VALUES(%d,%d,%d,%d,%d,'r9%d')", 1400+i, 1500+i, 1200+i, 1600+i, 1700+i, i))
		// t1: a1 = 1300+i (matches t8.d8)
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t1 VALUES(%d,%d,%d,%d,%d,'r1%d')", 1300+i, 1800+i, 1900+i, 2000+i, 2100+i, i))
		// t3: a3 = 2200+i
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t3 VALUES(%d,%d,%d,%d,%d,'r3%d')", 2200+i, 2300+i, 2400+i, 2500+i, 2600+i, i))
		// t5: c5 = 625
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t5 VALUES(%d,%d,%d,%d,%d,'r5%d')", 2700+i, 2800+i, 625, 2900+i, 3000+i, i))
		// t7: e7 = 168
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t7 VALUES(%d,%d,%d,%d,%d,'r7%d')", 3100+i, 3200+i, 3300+i, 3400+i, 168, i))
	}

	// Verify basic join keys work before testing the full 8-table query.
	if rows, err := ex.QueryAll(ctx, "SELECT count(*) FROM t4, t6 WHERE b4=d6"); err != nil || len(rows) == 0 {
		t.Fatalf("b4=d6 setup check failed: %v", err)
	} else {
		t.Logf("b4=d6: count=%v", rows[0].Data[0])
	}

	// Query structure mirrors select4 L39784: 8-table join with 3 cross-table
	// equi-join predicates + 5 single-table predicates. Different table
	// orderings must produce the SAME row count.
	//
	// Key: single-table predicates use narrow value sets (1-2 values) so
	// that row loss from single-table predicate leakage into the merge-phase
	// NLJ's ON clause is observable. If `a9 in (1400)` is evaluated against
	// a non-t9 row (wrong table), the lookup returns nil → false → row dropped.
	queries := []string{
		// Ordering 1: t3,t4,t9,t1,t8,t6,t5,t7
		`SELECT count(*) FROM t3, t4, t9, t1, t8, t6, t5, t7
		 WHERE a9 in (1400)
		   AND e8=c9
		   AND d6 in (200)
		   AND 625=c5
		   AND 168=e7
		   AND a3 in (2200)
		   AND a1=d8
		   AND b4=d6`,
		// Ordering 2: t3,t7,t4,t9,t5,t1,t8,t6
		`SELECT count(*) FROM t3, t7, t4, t9, t5, t1, t8, t6
		 WHERE a9 in (1400)
		   AND e8=c9
		   AND d6 in (200)
		   AND 625=c5
		   AND 168=e7
		   AND a3 in (2200)
		   AND a1=d8
		   AND b4=d6`,
		// Ordering 3: t4,t6,t8,t9,t1,t3,t5,t7
		`SELECT count(*) FROM t4, t6, t8, t9, t1, t3, t5, t7
		 WHERE a9 in (1400)
		   AND e8=c9
		   AND d6 in (200)
		   AND 625=c5
		   AND 168=e7
		   AND a3 in (2200)
		   AND a1=d8
		   AND b4=d6`,
	}

	var firstCount int
	for i, q := range queries {
		rows, err := ex.QueryAll(ctx, q)
		if err != nil {
			t.Fatalf("query %d failed: %v\nSQL: %s", i, err, q)
		}
		if len(rows) != 1 {
			t.Fatalf("query %d: expected 1 count row, got %d", i, len(rows))
		}
		val := fmt.Sprintf("%v", rows[0].Data[0])
		if i == 0 {
			firstCount = parseCount(val)
			if firstCount == 0 {
				t.Fatalf("query 0 returned 0 — predicate pushdown or join broken")
			}
			t.Logf("query 0 count: %d", firstCount)
		} else {
			got := parseCount(val)
			if got != firstCount {
				t.Errorf("query %d: got %d, want %d (table-order-dependent row loss)", i, got, firstCount)
			}
		}
	}
}

func parseCount(s string) int {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
