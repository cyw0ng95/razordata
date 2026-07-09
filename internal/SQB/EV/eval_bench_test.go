package EV

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// BenchmarkIdentLookup measures Ident column reference evaluation via
// the runtime Lookup path (SlotIdx = -1). Baseline for REQ001202.
func BenchmarkIdentLookup(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: []Value{
			DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5),
		},
	}
	expr := &PS.Ident{Name: "c", SlotIdx: -1}

	b.ResetTimer()
	for range b.N {
		v, err := EvalValue(expr, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if v.Kind != KindInt || v.I64 != 3 {
			b.Fatalf("got %v, want 3", v)
		}
	}
}

// BenchmarkIdentSlotIdx measures Ident column reference evaluation via
// the pre-resolved SlotIdx fast path (REQ001202).
func BenchmarkIdentSlotIdx(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: []Value{
			DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5),
		},
	}
	expr := &PS.Ident{Name: "c", SlotIdx: 2}

	b.ResetTimer()
	for range b.N {
		v, err := EvalValue(expr, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if v.Kind != KindInt || v.I64 != 3 {
			b.Fatalf("got %v, want 3", v)
		}
	}
}

// BenchmarkIdentSlotIdx_Nth evaluates the Nth column (last column "e" at idx 4).
func BenchmarkIdentSlotIdx_Last(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: []Value{
			DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5),
		},
	}
	expr := &PS.Ident{Name: "e", SlotIdx: 4}

	b.ResetTimer()
	for range b.N {
		v, err := EvalValue(expr, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if v.Kind != KindInt || v.I64 != 5 {
			b.Fatalf("got %v, want 5", v)
		}
	}
}

// BenchmarkIdentLookup_Last evaluates the last column via runtime Lookup.
// Shows linear scan cost when the target is at the end.
func BenchmarkIdentLookup_Last(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: []Value{
			DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5),
		},
	}
	expr := &PS.Ident{Name: "e", SlotIdx: -1}

	b.ResetTimer()
	for range b.N {
		v, err := EvalValue(expr, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if v.Kind != KindInt || v.I64 != 5 {
			b.Fatalf("got %v, want 5", v)
		}
	}
}

// BenchmarkIdentColIndex measures Ident column reference evaluation via
// the ColIndex map fallback (REQ001283) — path between SlotIdx miss
// and linear-scan Lookup. Simulates a Project output row with ColIndex.
func BenchmarkIdentColIndex(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: []Value{
			DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5),
		},
		ColIndex: map[string]int{"c": 2},
	}
	expr := &PS.Ident{Name: "c", SlotIdx: -1}
	b.ResetTimer()
	for range b.N {
		v, err := EvalValue(expr, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if v.Kind != KindInt || v.I64 != 3 {
			b.Fatalf("got %v, want 3", v)
		}
	}
}

func BenchmarkIdentColIndex_Last(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: []Value{
			DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5),
		},
		ColIndex: map[string]int{"e": 4},
	}
	expr := &PS.Ident{Name: "e", SlotIdx: -1}
	b.ResetTimer()
	for range b.N {
		v, err := EvalValue(expr, row, nil)
		if err != nil {
			b.Fatal(err)
		}
		if v.Kind != KindInt || v.I64 != 5 {
			b.Fatalf("got %v, want 5", v)
		}
	}
}

// BenchmarkEvalScalarSubquery_CorrelatedCache measures correlated
// subquery cache hit vs miss performance. REQ001292.
func BenchmarkEvalScalarSubquery_CorrelatedCache(b *testing.B) {
	row := &Row{
		Cols: []string{"id", "name"},
		Data: []Value{DT.NewIntValue(1), DT.NewTextValue("alice")},
	}
	expr := &PS.SubqueryExpr{Subquery: &PS.Select{
		Cols: []PS.Expr{&PS.NumberLiteral{Val: 1}},
		From: "t",
		Where: &PS.BinaryExpr{
			Left: &PS.QualifiedName{
				Table: "t",
				Name:  "id",
			},
			Op:    LX.T_EQ,
			Right: &PS.QualifiedName{Table: "outer", Name: "id"},
		},
		FromAlias: "t",
	}}

	b.Run("cache_hit", func(b *testing.B) {
		for i := 0; i < 5; i++ {
			_, _ = evalScalarSubquery(expr, row, nil)
		}
		b.ResetTimer()
		for range b.N {
			_, _ = evalScalarSubquery(expr, row, nil)
		}
	})

	b.Run("serialize_only", func(b *testing.B) {
		cols := cachedCorrelatedCols(expr)
		idxs := resolveColIndices(row, cols)
		b.ResetTimer()
		for range b.N {
			_ = serializeCorrelatedValuesWithIndices(row, idxs)
		}
	})
}
