//go:build !slt_corpus_full

package EX

import (
	"context"
	"fmt"
	"testing"
)

// REQ001433: bushy merge must inspect cross-group predicates
// (crossTablePredicates) for equi-join keys between the new group
// and the already-joined side. Previously the merge phase only
// looked at the group's intra-group predicates (gr.preds),
// missing bridge predicates such as a3=c9 between two unmerged
// groups, which made every bushy merge with multi-table groups
// fall back to NestedLoopJoin CrossJoin.
//
// This test exercises a 4-table comma join whose inner predicate
// a3=c9 bridges two groups that would otherwise have been
// cross-joined. The result row counts and values must match
// independently of which join operator the planner chose.
func TestBushyMerge_BridgePredicate_UsesHashJoin(t *testing.T) {
	ResetForTest(t)
	ctx := context.Background()
	ex := NewExecutor()
	ex.RegisterTableWithPK("t3", []string{"a3", "b3", "c3", "d3", "e3", "x3"}, "")
	ex.RegisterTableWithPK("t9", []string{"a9", "b9", "c9", "d9", "e9", "x9"}, "")
	ex.RegisterTableWithPK("t1", []string{"a1", "b1", "c1", "d1", "e1", "x1"}, "")
	ex.RegisterTableWithPK("t7", []string{"a7", "b7", "c7", "d7", "e7", "x7"}, "")
	mustExec := func(sql string) {
		if _, err := ex.Exec(ctx, sql); err != nil {
			t.Fatalf("exec %q failed: %v", sql, err)
		}
	}
	mustExec("INSERT INTO t3 VALUES (1, 10, 1, 11, 12, 'row3a')")
	mustExec("INSERT INTO t3 VALUES (2, 20, 2, 21, 22, 'row3b')")
	mustExec("INSERT INTO t9 VALUES (1, 100, 1, 110, 120, 'row9a')")
	mustExec("INSERT INTO t9 VALUES (2, 200, 2, 210, 220, 'row9b')")
	mustExec("INSERT INTO t1 VALUES (1, 1000, 1, 1010, 1020, 'row1a')")
	mustExec("INSERT INTO t7 VALUES (1, 7000, 1, 7010, 7020, 'row7a')")
	// a3 = c9 bridges two comma-joined groups. Old behaviour:
	// bushy merge fell back to CrossJoin and the post-join
	// filter caught the rows (correct, slow). New behaviour:
	// merge extracts the bridge predicate and emits HashJoin
	// for that pair (correct, fast).
	rows, err := ex.QueryAll(ctx,
		"SELECT t3.b3, t9.b9 FROM t3, t9, t1, t7 WHERE a3=c9 AND a1=1 AND a7=1")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	gotSet := map[[2]int64]bool{}
	for _, r := range rows {
		gotSet[[2]int64{r.Data[0].I64, r.Data[1].I64}] = true
	}
	for _, want := range [][2]int64{{10, 100}, {20, 200}} {
		if !gotSet[want] {
			t.Errorf("missing row %v, got %v", want, gotSet)
		}
	}
}

// REQ001433: bridge predicates referring to a yet-to-be-merged
// group must NOT be hoisted into an earlier group's HashJoin.
// Otherwise the earlier group's results would be over-restricted
// and the later table would have no rows to join.
func TestBushyMerge_BridgeToLaterGroup_NotConsumed(t *testing.T) {
	ResetForTest(t)
	ctx := context.Background()
	ex := NewExecutor()
	ex.RegisterTableWithPK("t1", []string{"a1", "b1"}, "")
	ex.RegisterTableWithPK("t2", []string{"a2", "b2"}, "")
	ex.RegisterTableWithPK("t3", []string{"a3", "b3"}, "")
	mustExec := func(sql string) {
		if _, err := ex.Exec(ctx, sql); err != nil {
			t.Fatalf("exec %q failed: %v", sql, err)
		}
	}
	mustExec("INSERT INTO t1 VALUES (1, 11)")
	mustExec("INSERT INTO t2 VALUES (1, 22)")
	mustExec("INSERT INTO t3 VALUES (1, 33)")
	mustExec("INSERT INTO t3 VALUES (2, 44)")
	// t1.a1 = t3.a3 references t3, which is in a later group;
	// the merge phase must leave this predicate untouched for
	// later consumption, not hoist it into the t1×t2 join.
	rows, err := ex.QueryAll(ctx,
		"SELECT t1.b1, t2.b2, t3.b3 FROM t1, t2, t3 WHERE a2=1 AND a1=a3")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].I64 != 11 {
		t.Errorf("t1.b1: got %d, want 11", rows[0].Data[0].I64)
	}
	if rows[0].Data[1].I64 != 22 {
		t.Errorf("t2.b2: got %d, want 22", rows[0].Data[1].I64)
	}
	if rows[0].Data[2].I64 != 33 {
		t.Errorf("t3.b3: got %d, want 33", rows[0].Data[2].I64)
	}
}

// BenchmarkBushyMerge_CrossFallback_vs_HashJoin verifies the planner
// produces a result for the bridge-predicate query and times the
// plan+execute path. The metric is end-to-end wall time because the
// optimisation replaces NestedLoopJoin CrossJoin with HashJoin in the
// bushy merge phase, which is visible only when the inner loop is
// actually executed.
func BenchmarkBushyMerge_CrossFallback_vs_HashJoin(b *testing.B) {
	ResetForTest(&testing.T{})
	ex := NewExecutor()
	ex.RegisterTableWithPK("t3", []string{"a3", "b3", "c3", "d3", "e3", "x3"}, "")
	ex.RegisterTableWithPK("t9", []string{"a9", "b9", "c9", "d9", "e9", "x9"}, "")
	ex.RegisterTableWithPK("t1", []string{"a1", "b1", "c1", "d1", "e1", "x1"}, "")
	ex.RegisterTableWithPK("t7", []string{"a7", "b7", "c7", "d7", "e7", "x7"}, "")
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		// t3.a3 = i, t9.c9 = i for the same i ⇒ equi-join
		// produces 50 rows, then t1.a1=10 / t7.a7=10 filter
		// each to one row, so the final result is 50 × 1 × 1.
		ins := fmt.Sprintf("INSERT INTO t3 VALUES (%d, %d, %d, %d, %d, 'x')", i, i*10, i, i, i)
		ex.Exec(ctx, ins)
		ins = fmt.Sprintf("INSERT INTO t9 VALUES (%d, %d, %d, %d, %d, 'x')", i, i*100, i, i, i)
		ex.Exec(ctx, ins)
		ins = fmt.Sprintf("INSERT INTO t1 VALUES (%d, %d, %d, %d, %d, 'x')", i, i*1000, i, i, i)
		ex.Exec(ctx, ins)
		ins = fmt.Sprintf("INSERT INTO t7 VALUES (%d, %d, %d, %d, %d, 'x')", i, i*7000, i, i, i)
		ex.Exec(ctx, ins)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := ex.QueryAll(ctx,
			"SELECT t3.b3 FROM t3, t9, t1, t7 WHERE a3=c9 AND a1=10 AND a7=10")
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != 50 {
			b.Fatalf("expected 50 rows, got %d", len(rows))
		}
	}
}
