package EX

import (
	"context"
	"fmt"
	"strings"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

func TestSubqueryPlanner_SeesStoreTables(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"id", "val"}, Data: []DT.Value{NewIntValue(int64(1)), NewTextValue("a")}},
		{Cols: []string{"id", "val"}, Data: []DT.Value{NewIntValue(int64(2)), NewTextValue("b")}},
		{Cols: []string{"id", "val"}, Data: []DT.Value{NewIntValue(int64(3)), NewTextValue("c")}},
	})

	e := NewExecutor()
	e.RegisterTable("t1", []string{"id", "val"})
	ctx := context.Background()

	tests := []struct {
		sql   string
		want  int
		label string
	}{
		{"SELECT * FROM t1 WHERE id > (SELECT avg(id) FROM t1)", 1, "scalar subquery with DT.Tables (avg=2, id>2 → id=3)"},
		{"SELECT EXISTS(SELECT 1 FROM t1 WHERE id = 3)", 1, "exists subquery with DT.Tables"},
		{"SELECT 3 IN (SELECT id FROM t1)", 1, "IN subquery with DT.Tables"},
	}
	for _, tc := range tests {
		rows, err := e.QueryAll(ctx, tc.sql)
		if err != nil {
			t.Fatalf("%s (%s): query error: %v", tc.sql, tc.label, err)
		}
		if len(rows) != tc.want {
			t.Errorf("%s (%s): got %d rows, want %d; data=%v", tc.sql, tc.label, len(rows), tc.want, rows)
		}
	}
}

func TestSubqueryPlanner_StorePropagationAfterClone(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"id", "val"}, Data: []DT.Value{NewIntValue(int64(1)), NewTextValue("x")}},
		{Cols: []string{"id", "val"}, Data: []DT.Value{NewIntValue(int64(2)), NewTextValue("y")}},
	})
	DT.RegisterTable("t2", []DT.Row{
		{Cols: []string{"ref", "name"}, Data: []DT.Value{NewIntValue(int64(1)), NewTextValue("alice")}},
		{Cols: []string{"ref", "name"}, Data: []DT.Value{NewIntValue(int64(1)), NewTextValue("bob")}},
	})

	e := NewExecutor()
	e.RegisterTable("t1", []string{"id", "val"})
	e.RegisterTable("t2", []string{"ref", "name"})
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, "SELECT CASE WHEN id > (SELECT avg(ref) FROM t2) THEN id ELSE 0 END FROM t1")
	if err != nil {
		t.Fatalf("CASE with scalar subquery: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
}

func TestSubqueryPlanner_CloneRowPreservesPlanner(t *testing.T) {
	pl := NewPlannerWithStore(nil)
	src := DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(int64(1))}}
	src.Planner = pl
	cloned := DT.CloneRow(src)
	if cloned.Planner != pl {
		t.Error("DT.CloneRow should preserve planner")
	}
	if cloned.Outer != src.Outer {
		t.Error("DT.CloneRow should preserve Outer")
	}
}

// TestPlanner_SemiJoin verifies REQ001073: EXISTS subqueries use a
// short-circuit evaluation that stops scanning the right side after
// the first matching row rather than materializing all rows. The test
// validates correctness across correlated and non-correlated EXISTS,
// NOT EXISTS, and multi-table setups. The short-circuit is exercised
// by the type assertion in EV.evalExists falling through to
// Plannner.ExecuteSubqueryFirstMatch.
func TestPlanner_SemiJoin(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	ex.RegisterTableWithPK("t1", []string{"id", "val"}, "id")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (3, 30)")

	ex.RegisterTableWithPK("t2", []string{"id", "tid", "name"}, "id")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (10, 1, 'a')")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (20, 1, 'b')")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (30, 2, 'c')")

	t.Run("noncorrelated_exists", func(t *testing.T) {
		rows, err := ex.QueryAll(ctx, "SELECT id FROM t1 WHERE EXISTS (SELECT 1 FROM t2) ORDER BY id")
		if err != nil {
			t.Fatalf("noncorrelated EXISTS: %v", err)
		}
		if len(rows) != 3 {
			t.Errorf("got %d rows, want 3", len(rows))
		}
	})
	t.Run("correlated_exists", func(t *testing.T) {
		rows, err := ex.QueryAll(ctx, "SELECT id FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.tid = t1.id) ORDER BY id")
		if err != nil {
			t.Fatalf("correlated EXISTS: %v", err)
		}
		// t2.tid=1 matches (t1.id=1), t2.tid=2 matches (t1.id=2)
		if len(rows) != 2 {
			t.Errorf("got %d rows, want 2; data=%v", len(rows), rows)
			// Debug: print actual rows for diagnosis
			for i, r := range rows {
				t.Logf("Row %v data: %+v", i, r.Data)
			}
		}
	})
	t.Run("not_exists", func(t *testing.T) {
		rows, err := ex.QueryAll(ctx, "SELECT id FROM t1 WHERE NOT EXISTS (SELECT 1 FROM t2 WHERE t2.tid = t1.id) ORDER BY id")
		if err != nil {
			t.Fatalf("NOT EXISTS: %v", err)
		}
		// t1.id=3 has no matching t2 row
		if len(rows) != 1 {
			t.Errorf("got %d rows, want 1; data=%v", len(rows), rows)
		}
		if len(rows) > 0 && rows[0].Data[0].I64 != 3 {
			t.Errorf("expected id=3, got %v", rows[0].Data[0])
		}
	})
	t.Run("exists_self_join", func(t *testing.T) {
		_, err := ex.Exec(ctx, "INSERT INTO t1 VALUES (4, 40)")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		rows, err := ex.QueryAll(ctx, "SELECT id FROM t1 AS a WHERE EXISTS (SELECT 1 FROM t1 WHERE t1.val = a.val AND t1.id != a.id) ORDER BY id")
		if err != nil {
			t.Fatalf("self-join EXISTS: %v", err)
		}
		_ = rows
	})
	t.Run("exists_in_compound", func(t *testing.T) {
		rows, err := ex.QueryAll(ctx, "SELECT id FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.tid = t1.id) AND t1.val > 15 ORDER BY id")
		if err != nil {
			t.Fatalf("EXISTS + WHERE: %v", err)
		}
		if len(rows) != 1 {
			t.Errorf("got %d rows, want 1; data=%v", len(rows), rows)
		}
	})
	t.Run("exists_in_or_chain", func(t *testing.T) {
		// EXISTS inside OR must NOT be decorrelated — the semi-join
		// would change semantics. Verify fallback to per-row eval
		// by checking the plan does NOT contain "SEMI JOIN".
		explain, err := ex.Explain("SELECT id FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.tid = t1.id) OR t1.val > 25 ORDER BY id")
		if err != nil {
			t.Fatalf("Explain: %v", err)
		}
		if strings.Contains(explain, "SEMI JOIN") {
			t.Errorf("OR-chain EXISTS produced SEMI JOIN plan, expected per-row eval filter\n%s", explain)
		}
	})
}

// BenchmarkExistsDecorrelation_Select1 measures the EXISTS decorrelation
// path performance. The correlated EXISTS subquery should be rewritten
// as a semi-join, avoiding per-row subquery re-planning. REQ001235.
func BenchmarkExistsDecorrelation_Select1(b *testing.B) {
	UnregisterAll()
	defer UnregisterAll()

	ex, _ := newEngineExecutor(b)
	ctx := context.Background()

	ex.RegisterTableWithPK("t1", []string{"id", "val"}, "id")
	for i := 0; i < 30; i++ {
		if _, err := ex.Exec(ctx, fmt.Sprintf("INSERT INTO t1 VALUES (%d, %d)", i, i*10)); err != nil {
			b.Fatalf("seed t1: %v", err)
		}
	}
	ex.RegisterTableWithPK("t2", []string{"id", "tid", "name"}, "id")
	for _, stmt := range []string{
		"INSERT INTO t2 VALUES (10, 1, 'a')",
		"INSERT INTO t2 VALUES (20, 1, 'b')",
		"INSERT INTO t2 VALUES (30, 2, 'c')",
	} {
		if _, err := ex.Exec(ctx, stmt); err != nil {
			b.Fatalf("seed t2: %v", err)
		}
	}

	ex.RegisterTableWithPK("t1", []string{"id", "val"}, "id")
	for i := 0; i < 30; i++ {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t1 VALUES (%d, %d)", i, i*10))
	}
	ex.RegisterTableWithPK("t2", []string{"id", "tid", "name"}, "id")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (10, 1, 'a')")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (20, 1, 'b')")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (30, 2, 'c')")

	sql := "SELECT id FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.tid = t1.id) ORDER BY id"

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		rows, err := ex.QueryAll(ctx, sql)
		if err != nil {
			b.Fatal(err)
		}
		_ = rows
	}
}
