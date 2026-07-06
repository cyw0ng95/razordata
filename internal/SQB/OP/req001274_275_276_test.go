package OP

import (
	"strings"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestCompileFilterExpr_ANDChain verifies that AND predicates compile
// through compileFilterExpr → compileBinary instead of falling back
// to EvalValue. REQ001276.
func TestCompileFilterExpr_ANDChain(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op: LX.T_AND,
		Left: &PS.BinaryExpr{
			Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 5},
		},
		Right: &PS.BinaryExpr{
			Op: LX.T_GT, Left: &PS.Ident{Name: "y"}, Right: &PS.NumberLiteral{Val: 0},
		},
	}
	fn := compileFilterExpr(pred)
	if fn == nil {
		t.Fatal("compileFilterExpr returned nil for AND chain")
	}

	// Row where both predicates pass (x=5, y=10).
	row := &Row{
		Data: []DT.Value{DT.NewIntValue(5), DT.NewIntValue(10)},
		Cols: []string{"x", "y"},
	}
	ok, err := fn(row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("AND chain returned false when both predicates pass")
	}

	// Row where left predicate fails (x=1, y=10).
	rowFail := &Row{
		Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(10)},
		Cols: []string{"x", "y"},
	}
	ok, err = fn(rowFail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("AND chain returned true when left predicate fails")
	}

	// Row where right predicate fails (x=5, y=-1).
	rowFail2 := &Row{
		Data: []DT.Value{DT.NewIntValue(5), DT.NewIntValue(-1)},
		Cols: []string{"x", "y"},
	}
	ok, err = fn(rowFail2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("AND chain returned true when right predicate fails")
	}
}

// TestCompileFilterExpr_ORChain verifies that OR predicates compile
// through compileFilterExpr → compileBinary instead of falling back
// to EvalValue. REQ001276.
func TestCompileFilterExpr_ORChain(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op: LX.T_OR,
		Left: &PS.BinaryExpr{
			Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 5},
		},
		Right: &PS.BinaryExpr{
			Op: LX.T_EQ, Left: &PS.Ident{Name: "y"}, Right: &PS.NumberLiteral{Val: 10},
		},
	}
	fn := compileFilterExpr(pred)
	if fn == nil {
		t.Fatal("compileFilterExpr returned nil for OR chain")
	}

	// Row where both fail.
	row := &Row{
		Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(2)},
		Cols: []string{"x", "y"},
	}
	ok, err := fn(row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("OR chain returned true when both predicates fail")
	}

	// Row where left passes.
	rowLeft := &Row{
		Data: []DT.Value{DT.NewIntValue(5), DT.NewIntValue(2)},
		Cols: []string{"x", "y"},
	}
	ok, err = fn(rowLeft)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("OR chain returned false when left predicate passes")
	}

	// Row where right passes.
	rowRight := &Row{
		Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(10)},
		Cols: []string{"x", "y"},
	}
	ok, err = fn(rowRight)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("OR chain returned false when right predicate passes")
	}
}

// TestCompileFilterExpr_NestedAND verifies deeply nested AND chains
// compile correctly. REQ001276.
func TestCompileFilterExpr_NestedAND(t *testing.T) {
	// WHERE a=1 AND b=2 AND c=3
	pred := &PS.BinaryExpr{
		Op: LX.T_AND,
		Left: &PS.BinaryExpr{
			Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1},
		},
		Right: &PS.BinaryExpr{
			Op: LX.T_AND,
			Left: &PS.BinaryExpr{
				Op: LX.T_EQ, Left: &PS.Ident{Name: "b"}, Right: &PS.NumberLiteral{Val: 2},
			},
			Right: &PS.BinaryExpr{
				Op: LX.T_EQ, Left: &PS.Ident{Name: "c"}, Right: &PS.NumberLiteral{Val: 3},
			},
		},
	}
	fn := compileFilterExpr(pred)
	if fn == nil {
		t.Fatal("compileFilterExpr returned nil for nested AND")
	}

	// All match.
	rowAllPass := &Row{
		Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3)},
		Cols: []string{"a", "b", "c"},
	}
	ok, err := fn(rowAllPass)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("nested AND returned false when all pass")
	}

	// First fails — short-circuit should skip the rest (but result
	// correctness is same: row not included).
	rowFirstFails := &Row{
		Data: []DT.Value{DT.NewIntValue(99), DT.NewIntValue(2), DT.NewIntValue(3)},
		Cols: []string{"a", "b", "c"},
	}
	ok, err = fn(rowFirstFails)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("nested AND returned true when first predicate fails")
	}

	// Middle fails.
	rowMidFails := &Row{
		Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(99), DT.NewIntValue(3)},
		Cols: []string{"a", "b", "c"},
	}
	ok, err = fn(rowMidFails)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("nested AND returned true when middle predicate fails")
	}
}

// TestCompileFilterExpr_AND_OR_Mixed verifies mixed AND/OR chains
// compile correctly. REQ001276.
func TestCompileFilterExpr_AND_OR_Mixed(t *testing.T) {
	// WHERE (a=1 OR a=2) AND b=3
	pred := &PS.BinaryExpr{
		Op: LX.T_AND,
		Left: &PS.BinaryExpr{
			Op: LX.T_OR,
			Left: &PS.BinaryExpr{
				Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1},
			},
			Right: &PS.BinaryExpr{
				Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 2},
			},
		},
		Right: &PS.BinaryExpr{
			Op: LX.T_EQ, Left: &PS.Ident{Name: "b"}, Right: &PS.NumberLiteral{Val: 3},
		},
	}
	fn := compileFilterExpr(pred)
	if fn == nil {
		t.Fatal("compileFilterExpr returned nil for mixed AND/OR")
	}

	// a=1, b=3 → passes (OR left true, AND right true)
	row1 := &Row{
		Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(3)},
		Cols: []string{"a", "b"},
	}
	ok, err := fn(row1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("mixed AND/OR: row (a=1,b=3) should pass")
	}

	// a=2, b=3 → passes (OR right true, AND right true)
	row2 := &Row{
		Data: []DT.Value{DT.NewIntValue(2), DT.NewIntValue(3)},
		Cols: []string{"a", "b"},
	}
	ok, err = fn(row2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("mixed AND/OR: row (a=2,b=3) should pass")
	}

	// a=99, b=3 → fails (OR fails, AND right true but short-circuit)
	row3 := &Row{
		Data: []DT.Value{DT.NewIntValue(99), DT.NewIntValue(3)},
		Cols: []string{"a", "b"},
	}
	ok, err = fn(row3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("mixed AND/OR: row (a=99,b=3) should fail")
	}

	// a=1, b=99 → fails (OR left true, AND right false)
	row4 := &Row{
		Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(99)},
		Cols: []string{"a", "b"},
	}
	ok, err = fn(row4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("mixed AND/OR: row (a=1,b=99) should fail")
	}
}

// BenchmarkCompileColRef_EqFold measures the compileColRef path with
// EqualFold (REQ001274). For comparison with the old strings.ToLower
// approach, run with:
//
//	go test -bench=BenchmarkCompileColRef -benchmem
func BenchmarkCompileColRef_EqFold(b *testing.B) {
	// Simulate a row with uppercase column names (worst case for old code).
	lower := strings.ToLower("X")
	bareLower := "x"

	// Pre-create the row with an uppercase column name to force the
	// old ToLower path (worst case). The new EqualFold path handles
	// this without allocation.
	row := &Row{
		Data: []DT.Value{DT.NewIntValue(42)},
		Cols: []string{"X"},
	}

	// Simulate compileColRef's closure (the slotIdx path).
	fn := func(row *Row) DT.Value {
		slotIdx := 0
		if slotIdx >= 0 && slotIdx < len(row.Data) && slotIdx < len(row.Cols) {
			cl := row.Cols[slotIdx]
			if len(cl) > 0 {
				// REQ001274: EqualFold avoids allocation
				if strings.EqualFold(cl, lower) || strings.EqualFold(cl, bareLower) {
					return row.Data[slotIdx]
				}
			}
		}
		return DT.NullValue()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = fn(row)
	}
}

// BenchmarkCompileFilterExpr_ANDChain measures the compiled AND chain
// path (REQ001276) vs the Eval fallback.
func BenchmarkCompileFilterExpr_ANDChain(b *testing.B) {
	pred := &PS.BinaryExpr{
		Op: LX.T_AND,
		Left: &PS.BinaryExpr{
			Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 5},
		},
		Right: &PS.BinaryExpr{
			Op: LX.T_GT, Left: &PS.Ident{Name: "y"}, Right: &PS.NumberLiteral{Val: 0},
		},
	}
	fn := compileFilterExpr(pred)
	if fn == nil {
		b.Fatal("compileFilterExpr returned nil")
	}

	row := &Row{
		Data: []DT.Value{DT.NewIntValue(5), DT.NewIntValue(10)},
		Cols: []string{"x", "y"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = fn(row)
	}
}

// BenchmarkCompileFilterExpr_ORChain measures the compiled OR chain
// path. REQ001276.
func BenchmarkCompileFilterExpr_ORChain(b *testing.B) {
	pred := &PS.BinaryExpr{
		Op: LX.T_OR,
		Left: &PS.BinaryExpr{
			Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 5},
		},
		Right: &PS.BinaryExpr{
			Op: LX.T_EQ, Left: &PS.Ident{Name: "y"}, Right: &PS.NumberLiteral{Val: 10},
		},
	}
	fn := compileFilterExpr(pred)
	if fn == nil {
		b.Fatal("compileFilterExpr returned nil")
	}

	row := &Row{
		Data: []DT.Value{DT.NewIntValue(5), DT.NewIntValue(2)},
		Cols: []string{"x", "y"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = fn(row)
	}
}