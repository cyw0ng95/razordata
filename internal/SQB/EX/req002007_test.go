//go:build !slt_corpus

package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestREQ002007_ConstantON_InnerJoin covers the four canonical constant
// ON clauses on an INNER JOIN. SQLite semantics:
//
//	ON 1=1            → TRUE  → cross product (left.rows × right.rows)
//	ON 1=0            → FALSE → zero rows
//	ON NULL IS NULL   → TRUE  → cross product
//	ON NOT NULL IS NULL → FALSE → zero rows
//
// Before the fix, the join-elimination pass dropped these joins entirely
// (constant ON references no tables), returning only the left table's
// rows for every case.
func TestREQ002007_ConstantON_InnerJoin(t *testing.T) {
	ResetForTest(t)
	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"id", "a"}, Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(10)}},
		{Cols: []string{"id", "a"}, Data: []DT.Value{DT.NewIntValue(2), DT.NewIntValue(20)}},
	})
	DT.RegisterTable("t2", []DT.Row{
		{Cols: []string{"c", "d"}, Data: []DT.Value{DT.NewIntValue(5), DT.NewIntValue(60)}},
		{Cols: []string{"c", "d"}, Data: []DT.Value{DT.NewIntValue(7), DT.NewIntValue(80)}},
	})
	ex := NewExecutor()
	defer UnregisterAll()
	ctx := context.Background()

	cases := []struct {
		name string
		sql  string
		want int
	}{
		{"ON 1=1 cross", "SELECT t1.id FROM t1 JOIN t2 ON 1=1", 4},
		{"ON 1=0 empty", "SELECT t1.id FROM t1 JOIN t2 ON 1=0", 0},
		{"ON NULL IS NULL cross", "SELECT t1.id FROM t1 JOIN t2 ON NULL IS NULL", 4},
		{"ON NOT NULL IS NULL empty", "SELECT t1.id FROM t1 JOIN t2 ON NOT NULL IS NULL", 0},
	}
	for _, c := range cases {
		rows, err := ex.QueryAll(ctx, c.sql)
		if err != nil {
			t.Errorf("%s: query err: %v", c.name, err)
			continue
		}
		if len(rows) != c.want {
			t.Errorf("%s: want %d rows, got %d", c.name, c.want, len(rows))
		}
	}
}

// TestREQ002007_ConstantON_LeftJoin verifies outer-join semantics for
// constant ON clauses. The generic NLJ path handles these (the
// planConstantOnJoin short-circuit only applies to INNER joins):
//
//	LEFT JOIN ON TRUE  → cross product (every left row matches every right)
//	LEFT JOIN ON FALSE → left rows with NULL-padded right columns
func TestREQ002007_ConstantON_LeftJoin(t *testing.T) {
	ResetForTest(t)
	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"a"}, Data: []DT.Value{DT.NewIntValue(1)}},
		{Cols: []string{"a"}, Data: []DT.Value{DT.NewIntValue(2)}},
	})
	DT.RegisterTable("t2", []DT.Row{
		{Cols: []string{"b"}, Data: []DT.Value{DT.NewIntValue(10)}},
		{Cols: []string{"b"}, Data: []DT.Value{DT.NewIntValue(20)}},
	})
	ex := NewExecutor()
	defer UnregisterAll()
	ctx := context.Background()

	// LEFT JOIN ON 1=1 → 2×2 = 4 rows.
	rows, err := ex.QueryAll(ctx, "SELECT t1.a FROM t1 LEFT JOIN t2 ON 1=1")
	if err != nil {
		t.Fatalf("ON 1=1 query: %v", err)
	}
	if len(rows) != 4 {
		t.Errorf("LEFT JOIN ON 1=1: want 4 rows, got %d", len(rows))
	}

	// LEFT JOIN ON 1=0 → 2 left rows, t2.b NULL-padded.
	rows, err = ex.QueryAll(ctx, "SELECT t1.a, t2.b FROM t1 LEFT JOIN t2 ON 1=0")
	if err != nil {
		t.Fatalf("ON 1=0 query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("LEFT JOIN ON 1=0: want 2 rows (left with nulls), got %d", len(rows))
	}
	for i, r := range rows {
		// First column is t1.a (1 then 2); second column t2.b is NULL.
		if r.Data[0].I64 != int64(i+1) {
			t.Errorf("row %d: want t1.a=%d, got %v", i, i+1, r.Data[0])
		}
		if r.Data[1].Kind != DT.KindNull {
			t.Errorf("row %d: want t2.b NULL, got %v", i, r.Data[1])
		}
	}
}

// TestREQ002007_ConstantON_WithWhere verifies a constant ON clause
// composes correctly with a WHERE filter. WHERE applies after the join,
// so the cross product is filtered down.
func TestREQ002007_ConstantON_WithWhere(t *testing.T) {
	ResetForTest(t)
	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"a"}, Data: []DT.Value{DT.NewIntValue(1)}},
		{Cols: []string{"a"}, Data: []DT.Value{DT.NewIntValue(2)}},
	})
	DT.RegisterTable("t2", []DT.Row{
		{Cols: []string{"b"}, Data: []DT.Value{DT.NewIntValue(10)}},
		{Cols: []string{"b"}, Data: []DT.Value{DT.NewIntValue(20)}},
	})
	ex := NewExecutor()
	defer UnregisterAll()
	ctx := context.Background()

	// JOIN ON 1=1 → 4-row cross product; WHERE t1.a=1 filters to 2 rows.
	rows, err := ex.QueryAll(ctx, "SELECT t1.a, t2.b FROM t1 JOIN t2 ON 1=1 WHERE t1.a = 1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (cross filtered by WHERE), got %d", len(rows))
	}
}

// TestREQ002007_ConstantON_MultiJoin verifies a constant ON clause in
// the middle of a join chain. t1⋈t2 (equi) ⋈ t3 (constant TRUE) → the
// final join is a cross product against t3.
func TestREQ002007_ConstantON_MultiJoin(t *testing.T) {
	ResetForTest(t)
	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"a"}, Data: []DT.Value{DT.NewIntValue(1)}},
	})
	DT.RegisterTable("t2", []DT.Row{
		{Cols: []string{"b"}, Data: []DT.Value{DT.NewIntValue(10)}},
	})
	DT.RegisterTable("t3", []DT.Row{
		{Cols: []string{"c"}, Data: []DT.Value{DT.NewIntValue(100)}},
		{Cols: []string{"c"}, Data: []DT.Value{DT.NewIntValue(200)}},
	})
	ex := NewExecutor()
	defer UnregisterAll()
	ctx := context.Background()

	// t1 JOIN t2 ON a=b → 1 row; JOIN t3 ON 1=1 → ×2 = 2 rows.
	rows, err := ex.QueryAll(ctx, "SELECT t1.a FROM t1 JOIN t2 ON a=b JOIN t3 ON 1=1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (equi-join then cross), got %d", len(rows))
	}
}

// TestREQ002007_ConstantON_StarProject verifies SELECT * returns all
// columns from all joined tables for a constant-TRUE ON (cross product),
// not just the left table's columns.
func TestREQ002007_ConstantON_StarProject(t *testing.T) {
	ResetForTest(t)
	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"a"}, Data: []DT.Value{DT.NewIntValue(1)}},
	})
	DT.RegisterTable("t2", []DT.Row{
		{Cols: []string{"b"}, Data: []DT.Value{DT.NewIntValue(2)}},
		{Cols: []string{"b"}, Data: []DT.Value{DT.NewIntValue(3)}},
	})
	ex := NewExecutor()
	defer UnregisterAll()
	ctx := context.Background()

	rows, err := ex.QueryAll(ctx, "SELECT * FROM t1 JOIN t2 ON 1=1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (1×2 cross), got %d", len(rows))
	}
	for i, r := range rows {
		// Each row should carry both t1.a and t2.b columns.
		if len(r.Data) < 2 {
			t.Errorf("row %d: want >=2 columns (t1.a + t2.b), got %d", i, len(r.Data))
		}
	}
}
