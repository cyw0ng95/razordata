//go:build !slt_corpus

package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestJoinElimination_PreservesONReferencedTable verifies REQ001155:
// a join whose right table's column is referenced only in the ON clause
// must not be eliminated by the join-elimination pass.
//
// Repro: `SELECT t1.a FROM t1 JOIN t2 ON t1.id = t2.t1id`.
// `collectReferencedTables` (planner.go:1678) only walks SELECT/WHERE/
// ORDER BY/GROUP BY/HAVING columns. `t2.t1id` lives in the ON clause,
// so `refTables["t2"]` is false and the s.Joins filter would drop the
// t2 join — producing wrong cardinality.
//
// After the fix, the join is preserved and the result row count matches
// the seed data (2 t1 rows, 1 t2 row with t1id=1 -> 1 matching row).
func TestJoinElimination_PreservesONReferencedTable(t *testing.T) {
	ResetForTest(t)

	// Register t1 with 2 rows: id=1 and id=2.
	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"id", "a"}, Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(10)}},
		{Cols: []string{"id", "a"}, Data: []DT.Value{DT.NewIntValue(2), DT.NewIntValue(20)}},
	})
	// t2 has one row with t1id=1 (matches t1.id=1 only).
	DT.RegisterTable("t2", []DT.Row{
		{Cols: []string{"t1id", "b"}, Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(100)}},
	})

	ex := NewExecutor()
	defer UnregisterAll()
	ctx := context.Background()

	// Join key is t1.id = t2.t1id. The ON clause is the only place
	// t2 is referenced, so without the fix this query returns 2 rows
	// (full t1 scan, no join). With the fix it returns 1 row.
	rows, err := ex.QueryAll(ctx, `SELECT t1.a FROM t1 JOIN t2 ON t1.id = t2.t1id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row (inner join t1.id=1 matches t2.t1id=1), got %d", len(rows))
	}
	if rows[0].Data[0].I64 != 10 {
		t.Fatalf("want a=10, got %#v", rows[0].Data[0])
	}
}

// TestJoinElimination_PreservesONReferencedTable_Alias covers the
// aliased-table case (REQ000835/836): t2 joined with alias `x` and ON
// clause references `x.col`. The right-alias is the table reference in
// the ON clause, so the elimination pass must also consult the alias.
func TestJoinElimination_PreservesONReferencedTable_Alias(t *testing.T) {
	ResetForTest(t)

	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}},
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(2)}},
	})
	DT.RegisterTable("t2", []DT.Row{
		{Cols: []string{"ref", "v"}, Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(999)}},
	})

	ex := NewExecutor()
	defer UnregisterAll()
	ctx := context.Background()

	rows, err := ex.QueryAll(ctx, `SELECT t1.id FROM t1 JOIN t2 x ON t1.id = x.ref`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row (aliased inner join), got %d", len(rows))
	}
}

// TestJoinElimination_PreservesConstantON verifies REQ002007: a join
// whose ON clause is a constant expression (e.g. 1=1) must NOT be
// eliminated, even when the right table is unreferenced in SELECT/
// WHERE/ORDER/GROUP/HAVING. The constant's truth value is part of the
// query semantics — TRUE makes an INNER JOIN a cross product.
//
// This supersedes the former REQ001076 behavior, which dropped any join
// whose ON clause referenced neither joined table. That optimization was
// unsound: it treated a constant TRUE ON as a no-op, silently returning
// only the left table's rows instead of the cartesian product, and
// returned left rows for a constant FALSE ON instead of zero rows.
func TestJoinElimination_PreservesConstantON(t *testing.T) {
	ResetForTest(t)

	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}},
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(2)}},
	})
	// t2 is never referenced in SELECT/WHERE/etc, but has 2 rows so the
	// cross product (4) is distinguishable from the buggy drop (2).
	DT.RegisterTable("t2", []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(99)}},
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(100)}},
	})

	ex := NewExecutor()
	defer UnregisterAll()
	ctx := context.Background()

	// ON 1=1 is constant TRUE → INNER JOIN is a cross product: 2×2 = 4.
	rows, err := ex.QueryAll(ctx, `SELECT t1.id FROM t1 JOIN t2 ON 1=1`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("want 4 rows (constant-TRUE ON → cross product), got %d", len(rows))
	}
}
