package EX

import (
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// REQ001218 + REQ001219 — OR-to-IN conversion and N3 OR selectivity.
// These tests cover:
//   1. extractInListValues now accepts *PS.InExpr (parser output)
//   2. extractOrChainEquality detects same-column OR-chains of
//      col=literal (either side), synthesizes IN-list values
//   3. joinPredSel T_OR computes 1 - ∏(1 - 1/ndv) for same-column chains
//   4. joinPredSel T_AND falls back conservatively
//   5. End-to-end: select4-style OR-chain produces tiny result set

// TestExtractInListValues_InExpr verifies the parser-output InExpr
// form is now accepted (previously the function only matched
// BinaryExpr{T_IN}, making point-lookup for IN-lists dead code).
func TestExtractInListValues_InExpr(t *testing.T) {
	pred := &PS.InExpr{
		Expr: &PS.Ident{Name: "x"},
		List: []PS.Expr{
			&PS.NumberLiteral{Val: 1},
			&PS.NumberLiteral{Val: 2},
			&PS.NumberLiteral{Val: 3},
		},
	}
	col, vals, ok := CO.ExtractInListValues(pred)
	if !ok {
		t.Fatal("expected ok=true for InExpr (regression: previously only BinaryExpr matched)")
	}
	if col != "x" {
		t.Fatalf("expected col=x, got %q", col)
	}
	if len(vals) != 3 {
		t.Fatalf("expected 3 values, got %d", len(vals))
	}
	for i, v := range []int64{1, 2, 3} {
		if vals[i] != v {
			t.Fatalf("vals[%d] = %v, want %d", i, vals[i], v)
		}
	}
}

// TestExtractOrChainEquality_ColEqLits tests the canonical form:
// col = lit1 OR col = lit2 OR col = lit3.
func TestExtractOrChainEquality_ColEqLits(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op: LX.T_OR,
		Left: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "e8"},
			Right: &PS.NumberLiteral{Val: 180},
		},
		Right: &PS.BinaryExpr{
			Op: LX.T_OR,
			Left: &PS.BinaryExpr{
				Op:    LX.T_EQ,
				Left:  &PS.Ident{Name: "e8"},
				Right: &PS.NumberLiteral{Val: 333},
			},
			Right: &PS.BinaryExpr{
				Op:    LX.T_EQ,
				Left:  &PS.Ident{Name: "e8"},
				Right: &PS.NumberLiteral{Val: 38},
			},
		},
	}
	col, vals, ok := CO.ExtractOrChainEquality(pred, flattenOr)
	if !ok {
		t.Fatal("expected ok=true for same-column OR chain")
	}
	if col != "e8" {
		t.Fatalf("expected col=e8, got %q", col)
	}
	wantVals := []int64{180, 333, 38}
	if len(vals) != len(wantVals) {
		t.Fatalf("expected %d values, got %d", len(wantVals), len(vals))
	}
	for i, v := range wantVals {
		if vals[i] != v {
			t.Fatalf("vals[%d] = %v, want %d", i, vals[i], v)
		}
	}
}

// TestExtractOrChainEquality_LitEqCol tests the mixed form:
// lit = col (literal on the left side, where OR-chain leaves can have
// either side as the column ref).
func TestExtractOrChainEquality_LitEqCol(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op: LX.T_OR,
		Left: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.NumberLiteral{Val: 180},
			Right: &PS.Ident{Name: "e8"},
		},
		Right: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.NumberLiteral{Val: 333},
			Right: &PS.Ident{Name: "e8"},
		},
	}
	col, vals, ok := CO.ExtractOrChainEquality(pred, flattenOr)
	if !ok {
		t.Fatal("expected ok=true for lit=col OR chain")
	}
	if col != "e8" {
		t.Fatalf("expected col=e8, got %q", col)
	}
	if len(vals) != 2 {
		t.Fatalf("expected 2 values, got %d", len(vals))
	}
}

// TestExtractOrChainEquality_DifferentCols returns false (multi-column
// OR chains cannot be converted to a single IN-list).
func TestExtractOrChainEquality_DifferentCols(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op: LX.T_OR,
		Left: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.NumberLiteral{Val: 1},
		},
		Right: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "b"},
			Right: &PS.NumberLiteral{Val: 2},
		},
	}
	_, _, ok := CO.ExtractOrChainEquality(pred, flattenOr)
	if ok {
		t.Fatal("expected ok=false for cross-column OR chain")
	}
}

// TestExtractOrChainEquality_SingleEq returns false (single equality
// is handled by extractSingleEquality, not OR-chain).
func TestExtractOrChainEquality_SingleEq(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	}
	_, _, ok := CO.ExtractOrChainEquality(pred, flattenOr)
	if ok {
		t.Fatal("expected ok=false for single equality")
	}
}

// TestExtractOrChainEquality_NonLiteral returns false (must be literal
// values for point-lookup to work).
func TestExtractOrChainEquality_NonLiteral(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op: LX.T_OR,
		Left: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.NumberLiteral{Val: 1},
		},
		Right: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.Ident{Name: "b"}, // not a literal
		},
	}
	_, _, ok := CO.ExtractOrChainEquality(pred, flattenOr)
	if ok {
		t.Fatal("expected ok=false for OR chain with non-literal leaf")
	}
}

// TestJoinPredSel_ORChain_SameColumn: selectivity = 1 - (1 - 1/ndv)^k
// where k = number of OR equalities on the same column. For ndv=100,
// k=4: sel = 1 - 0.99^4 ≈ 0.0394.
func TestJoinPredSel_ORChain_SameColumn(t *testing.T) {
	p := NewPlanner()
	p.SetStatsCatalog(newMockStatsCatalog())
	p.statsCatalog.(*mockStatsCatalog).setStats("t8", "e", ls.ColumnStats{
		DistinctCount: 100,
		RowCount:      100,
	})
	pred := mkORChain("e", []int64{180, 333, 38, 349})
	sel := p.joinPredSel(pred, 100)
	want := 1.0 - pow(0.99, 4)
	if !approx(sel, want, 1e-6) {
		t.Fatalf("expected ~%v (1 - 0.99^4), got %v", want, sel)
	}
}

// TestJoinPredSel_ORChain_Monotonic: longer chains → higher selectivity.
// For fixed ndv=1000, k=2/4/8/16 must satisfy sel(k2) < sel(k4) < sel(k8) < sel(k16).
func TestJoinPredSel_ORChain_Monotonic(t *testing.T) {
	p := NewPlanner()
	p.SetStatsCatalog(newMockStatsCatalog())
	p.statsCatalog.(*mockStatsCatalog).setStats("t", "x", ls.ColumnStats{
		DistinctCount: 1000,
		RowCount:      1000,
	})
	cases := []int{2, 4, 8, 16}
	prev := 0.0
	for _, k := range cases {
		vals := make([]int64, k)
		for i := range vals {
			vals[i] = int64(i + 1)
		}
		sel := p.joinPredSel(mkORChain("x", vals), 1000)
		if sel <= prev {
			t.Fatalf("selectivity did not increase: k=%d sel=%v prev=%v", k, sel, prev)
		}
		prev = sel
	}
}

// TestJoinPredSel_ORChain_NoStats falls back to rowCount-based estimate.
func TestJoinPredSel_ORChain_NoStats(t *testing.T) {
	p := NewPlanner()
	pred := mkORChain("e", []int64{1, 2, 3, 4})
	sel := p.joinPredSel(pred, 1000)
	if sel <= 0 || sel > 1 {
		t.Fatalf("expected 0 < sel <= 1, got %v", sel)
	}
	// For ndv proxy=1000 (rowCount), 4 equalities:
	// sel = 1 - (1 - 1/1000)^4 ≈ 0.003996
	want := 1.0 - pow(0.999, 4)
	if !approx(sel, want, 1e-6) {
		t.Fatalf("expected ~%v, got %v", want, sel)
	}
}

// TestJoinPredSel_ORChain_MultiColumn: e.g. (a=1 OR a=2 OR b=3).
// Each column's selectivity is computed independently then multiplied.
// ndv(a)=1000, ndv(b)=500, rowCount=10000: sa = 1-(1-1/1000)^2 ≈ 0.001999,
// sb = 1/500 = 0.002. combined = sa * sb ≈ 3.998e-6.
// Floor at 1/rowCount = 0.0001. Result ≈ 0.0001 (clamped to floor).
func TestJoinPredSel_ORChain_MultiColumn(t *testing.T) {
	p := NewPlanner()
	p.SetStatsCatalog(newMockStatsCatalog())
	p.statsCatalog.(*mockStatsCatalog).setStats("t", "a", ls.ColumnStats{DistinctCount: 1000, RowCount: 10000})
	p.statsCatalog.(*mockStatsCatalog).setStats("t", "b", ls.ColumnStats{DistinctCount: 500, RowCount: 10000})
	pred := &PS.BinaryExpr{
		Op: LX.T_OR,
		Left: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.NumberLiteral{Val: 1},
		},
		Right: &PS.BinaryExpr{
			Op: LX.T_OR,
			Left: &PS.BinaryExpr{
				Op:    LX.T_EQ,
				Left:  &PS.Ident{Name: "a"},
				Right: &PS.NumberLiteral{Val: 2},
			},
			Right: &PS.BinaryExpr{
				Op:    LX.T_EQ,
				Left:  &PS.Ident{Name: "b"},
				Right: &PS.NumberLiteral{Val: 3},
			},
		},
	}
	sel := p.joinPredSel(pred, 10000)
	sa := 1.0 - pow(1.0-1.0/1000, 2) // ≈ 0.001999
	sb := 1.0 / 500                   // 0.002
	want := sa * sb                  // ≈ 3.998e-6
	// Floor = 1/rowCount = 0.0001, so result is clamped up.
	floor := 1.0 / 10000
	if want < floor {
		want = floor
	}
	if !approx(sel, want, 1e-6) {
		t.Fatalf("expected ~%v (sa=%v * sb=%v, clamped to floor=%v), got %v", want, sa, sb, floor, sel)
	}
}

// TestJoinPredSel_AND_Fallback: T_AND rarely reaches here (split upstream),
// but the branch should not panic and return a sensible value.
func TestJoinPredSel_AND_Fallback(t *testing.T) {
	p := NewPlanner()
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.NumberLiteral{Val: 1},
		Right: &PS.NumberLiteral{Val: 2},
	}
	sel := p.joinPredSel(pred, 100)
	if sel <= 0 || sel > 1 {
		t.Fatalf("expected 0 < sel <= 1, got %v", sel)
	}
}

// TestTryApplyPointLookup_ORChain verifies that the SeqScan point-lookup
// is set up when the predicate is a same-column OR-chain of equalities.
// Uses the in-memory DT.Tables path (no Store). REQ001218.
func TestTryApplyPointLookup_ORChain(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	// 10-row table with column "e" holding values 100..109.
	rows := make([]DT.Row, 10)
	for i := 0; i < 10; i++ {
		rows[i] = DT.Row{
			Cols: []string{"e"},
			Data: []DT.Value{DT.NewIntValue(int64(100 + i))},
		}
	}
	DT.RegisterTable("t", rows)
	// Build a SeqScan via the public executor path.
	ex := NewExecutor()
	ctx := context.Background()
	q := "SELECT e FROM t WHERE e=100 OR e=105 OR e=109"
	rowsOut, err := ex.QueryAll(ctx, q)
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(rowsOut) != 3 {
		t.Fatalf("expected 3 rows, got %d (point-lookup not applied?)", len(rowsOut))
	}
}

// TestQueryResult_ORChain_MatchesINList: equivalent OR-chain and
// IN-list queries must return the same rows. REQ001218.
func TestQueryResult_ORChain_MatchesINList(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	rows := make([]DT.Row, 100)
	for i := 0; i < 100; i++ {
		rows[i] = DT.Row{
			Cols: []string{"e"},
			Data: []DT.Value{DT.NewIntValue(int64(1000 + i))},
		}
	}
	DT.RegisterTable("t", rows)
	ex := NewExecutor()
	ctx := context.Background()
	orRes, err := ex.QueryAll(ctx,
		"SELECT e FROM t WHERE e=1010 OR e=1020 OR e=1030 OR e=1040 ORDER BY e")
	if err != nil {
		t.Fatalf("OR-chain query: %v", err)
	}
	inRes, err := ex.QueryAll(ctx,
		"SELECT e FROM t WHERE e IN (1010, 1020, 1030, 1040) ORDER BY e")
	if err != nil {
		t.Fatalf("IN-list query: %v", err)
	}
	if len(orRes) != len(inRes) {
		t.Fatalf("row count mismatch: OR=%d IN=%d", len(orRes), len(inRes))
	}
	if len(orRes) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(orRes))
	}
	want := []int64{1010, 1020, 1030, 1040}
	for i, r := range orRes {
		v := r.Data[0].I64
		if v != want[i] {
			t.Fatalf("row %d: got %d, want %d", i, v, want[i])
		}
	}
}

// mkORChain builds `col=v1 OR col=v2 OR ... OR col=vk`.
func mkORChain(col string, vals []int64) PS.Expr {
	if len(vals) == 0 {
		return nil
	}
	if len(vals) == 1 {
		return &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: col},
			Right: &PS.NumberLiteral{Val: vals[0]},
		}
	}
	return &PS.BinaryExpr{
		Op: LX.T_OR,
		Left: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: col},
			Right: &PS.NumberLiteral{Val: vals[0]},
		},
		Right: mkORChain(col, vals[1:]),
	}
}

// pow computes base^exp via repeated multiplication (no math.Pow needed
// for the small integer exponents used in tests).
func pow(base float64, exp int) float64 {
	r := 1.0
	for i := 0; i < exp; i++ {
		r *= base
	}
	return r
}

func approx(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}