package EX

import (
	"context"
	"fmt"
	"testing"
)

// TestREQ001113_GroupBushyTransitiveDep verifies that groupBushyJoins
// correctly detects transitive dependencies via shared equi-join tables.
// Before the fix, a3=b9 AND a1=d9 would split t9,t3,t1 into separate
// groups because the function only checked direct pairKey matches.
func TestREQ001113_GroupBushyTransitiveDep(t *testing.T) {
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
			t.Fatalf("CREATE TABLE: %v", err)
		}
	}

	// Insert data where a3=b9 and a1=d9 produce correct join results.
	ex.Exec(ctx, "INSERT INTO t9 VALUES(1,10,688,100,1,'r1')")
	ex.Exec(ctx, "INSERT INTO t9 VALUES(2,20,688,200,2,'r2')")
	ex.Exec(ctx, "INSERT INTO t3 VALUES(10,1,2,3,4,'r3')")
	ex.Exec(ctx, "INSERT INTO t3 VALUES(20,5,6,7,8,'r4')")
	ex.Exec(ctx, "INSERT INTO t1 VALUES(100,1,2,3,4,'r5')")
	ex.Exec(ctx, "INSERT INTO t1 VALUES(200,5,6,7,8,'r6')")
	ex.Exec(ctx, "INSERT INTO t6 VALUES(1,2,3,885,4,'r7')")
	ex.Exec(ctx, "INSERT INTO t6 VALUES(5,6,7,924,8,'r8')")
	ex.Exec(ctx, "INSERT INTO t2 VALUES(1,2,3,488,4,'r9')")
	ex.Exec(ctx, "INSERT INTO t2 VALUES(5,6,7,488,8,'r10')")

	// Query with two cross-table equi-join predicates (a3=b9, a1=d9)
	// and single-table predicates. Different table orderings must
	// produce the same result.
	queries := []string{
		`SELECT b2, d6, b9*398, c1, e3*353+b9 FROM t9, t3, t6, t2, t1 WHERE d6 in (885,924) AND a3=b9 AND c9=688 AND 488=d2 AND a1=d9`,
		`SELECT b2, d6, b9*398, c1, e3*353+b9 FROM t1, t6, t2, t3, t9 WHERE d6 in (885,924) AND a3=b9 AND c9=688 AND 488=d2 AND a1=d9`,
		`SELECT b2, d6, b9*398, c1, e3*353+b9 FROM t3, t9, t1, t6, t2 WHERE d6 in (885,924) AND a3=b9 AND c9=688 AND 488=d2 AND a1=d9`,
	}

	var firstCount int
	for i, q := range queries {
		rows, err := ex.QueryAll(ctx, q)
		if err != nil {
			t.Fatalf("query %d failed: %v", i, err)
		}
		if i == 0 {
			firstCount = len(rows)
			if firstCount == 0 {
				t.Fatalf("query 0 returned 0 rows — bug")
			}
			t.Logf("query 0: %d rows", firstCount)
		} else {
			if len(rows) != firstCount {
				t.Errorf("query %d: got %d rows, want %d (order-dependent)", i, len(rows), firstCount)
			}
		}
	}
}

// TestREQ001113_CrossGroupEquiJoin verifies that equi-join predicates
// spanning multiple bushy groups are correctly applied during the merge
// phase, not consumed within a single group.
func TestREQ001113_CrossGroupEquiJoin(t *testing.T) {
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
			t.Fatalf("CREATE TABLE: %v", err)
		}
	}

	// 5-table query with two equi-join predicates spanning different
	// table pairs: a3=b9 (t3↔t9) and a1=d9 (t1↔t9).
	// Use unique values to avoid accidental cross-matches.
	for i := 0; i < 5; i++ {
		// t9: a9=1000+i, b9=2000+i, c9=688, d9=3000+i, e9=i
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t9 VALUES(%d,%d,688,%d,%d,'r9%d')", 1000+i, 2000+i, 3000+i, i, i))
		// t3: a3=2000+i (matches t9.b9), b3=i, ...
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t3 VALUES(%d,%d,%d,%d,%d,'r3%d')", 2000+i, i, i*2, i*3, i*4, i))
		// t1: a1=3000+i (matches t9.d9), b1=i, ...
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t1 VALUES(%d,%d,%d,%d,%d,'r1%d')", 3000+i, i, i*2, i*3, i*4, i))
		// t6: d6=885 for row 0 only
		if i == 0 {
			ex.Exec(ctx, fmt.Sprintf("INSERT INTO t6 VALUES(%d,%d,%d,%d,%d,'r6%d')", i, i*10, i*20, 885, i*30, i))
		}
		// t2: d2=488 for row 0 only
		if i == 0 {
			ex.Exec(ctx, fmt.Sprintf("INSERT INTO t2 VALUES(%d,%d,%d,%d,%d,'r2%d')", i, i*10, i*20, 488, i*30, i))
		}
	}

	query := `SELECT count(*) FROM t9, t3, t6, t2, t1 WHERE a3=b9 AND a1=d9 AND c9=688 AND d6=885 AND d2=488`
	rows, err := ex.QueryAll(ctx, query)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (count), got %d", len(rows))
	}
	// 5 t9 rows with c9=688, each matching exactly 1 t3 (a3=b9 unique)
	// and 1 t1 (a1=d9 unique). But d6=885 and d2=488 each match only 1 row.
	// So 5 × 1 × 1 × 1 × 1 = 5 rows.
	val := rows[0].Data[0]
	t.Logf("count = %v", val)
	if fmt.Sprintf("%v", val) != "5" {
		t.Errorf("expected count=5, got %v", val)
	}
}
