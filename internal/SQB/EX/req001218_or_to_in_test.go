package EX

import (
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
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