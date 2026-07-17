package EV

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// BenchmarkEvalBatchExpr_Abs_vs_Row compares batch ABS evaluation
// against row-at-a-time ABS evaluation. REQ001463.
func BenchmarkEvalBatchExpr_Abs_vs_Row(b *testing.B) {
	n := 1024
	batch := UT.GetBatch(1)
	for i := 0; i < n; i++ {
		v := int64(i - 512)
		batch.AppendRow(0, LX.T_INT_KW, v, false)
		batch.AdvanceSize()
	}
	batch.Cols[0].Name = "x"
	batch.SetColMap(map[string]int{"x": 0})

	call := &PS.FunctionCall{Name: "abs", Args: []PS.Expr{&PS.Ident{Name: "x"}}}
	benchmarkExpr(b, "batch_abs", func() {
		_ = EvalBatchExpr(call, batch, nil)
	})
	benchmarkExpr(b, "row_abs", func() {
		for i := 0; i < n; i++ {
			row := batchToRow(batch, i)
			_, _ = EvalAbs([]PS.Expr{&PS.Ident{Name: "x"}}, row, nil)
		}
	})
}

// BenchmarkEvalBatchExpr_Length_vs_Row compares batch LENGTH evaluation
// against row-at-a-time LENGTH evaluation.
func BenchmarkEvalBatchExpr_Length_vs_Row(b *testing.B) {
	n := 1024
	batch := UT.GetBatch(1)
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_TEXT, "hello world", false)
		batch.AdvanceSize()
	}
	batch.Cols[0].Name = "z"
	batch.SetColMap(map[string]int{"z": 0})

	call := &PS.FunctionCall{Name: "length", Args: []PS.Expr{&PS.Ident{Name: "z"}}}
	benchmarkExpr(b, "batch_length", func() {
		_ = EvalBatchExpr(call, batch, nil)
	})
	benchmarkExpr(b, "row_length", func() {
		for i := 0; i < n; i++ {
			row := batchToRow(batch, i)
			v, _ := evalLength([]PS.Expr{&PS.Ident{Name: "z"}}, row, nil)
			_ = v
		}
	})
}

// BenchmarkEvalBatchExpr_Upper_vs_Row compares batch UPPER evaluation
// against row-at-a-time UPPER evaluation.
func BenchmarkEvalBatchExpr_Upper_vs_Row(b *testing.B) {
	n := 1024
	batch := UT.GetBatch(1)
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_TEXT, "hello world", false)
		batch.AdvanceSize()
	}
	batch.Cols[0].Name = "z"
	batch.SetColMap(map[string]int{"z": 0})

	call := &PS.FunctionCall{Name: "upper", Args: []PS.Expr{&PS.Ident{Name: "z"}}}
	benchmarkExpr(b, "batch_upper", func() {
		_ = EvalBatchExpr(call, batch, nil)
	})
	benchmarkExpr(b, "row_upper", func() {
		for i := 0; i < n; i++ {
			row := batchToRow(batch, i)
			v, _ := evalUpper([]PS.Expr{&PS.Ident{Name: "z"}}, row, nil)
			_ = v
		}
	})
}

func benchmarkExpr(b *testing.B, name string, fn func()) {
	b.Run(name, func(b *testing.B) {
		b.ResetTimer()
		for range b.N {
			fn()
		}
	})
}

// BenchmarkEvalBatchExpr_Subquery_NonCorrelated verifies REQ001460's
// caching win: a non-correlated scalar subquery should be evaluated
// once per query (via global cache) and broadcast as a constant
// column, avoiding the per-row batchToRow fallback. This benchmark
// cannot exercise the actual subquery execution (it requires a real
// planner and engine), so it focuses on the projection-path dispatch
// when the global cache is already populated. The benchmark
// populates the cache once, then measures the broadcast path.
//
// Without this optimisation the same code path would call batchToRow
// per row and re-enter evalScalarSubquery, paying Row allocation
// (1KB+) for every batch row.
func BenchmarkEvalBatchExpr_Subquery_NonCorrelated_CachedHit(b *testing.B) {
	n := 1024
	batch := UT.GetBatch(1)
	batch.Cols[0].Name = "x"
	batch.SetColMap(map[string]int{"x": 0})
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_INT_KW, int64(i), false)
		batch.AdvanceSize()
	}

	subq := &PS.SubqueryExpr{Subquery: &PS.Select{
		From: "t",
		Cols: []PS.Expr{&PS.NumberLiteral{Val: 42}},
	}}
	// Pre-populate the global cache so the broadcast path runs.
	key := cachedSubqueryKey(subq)
	globalSubqueryCache.Store(key, DT.NewIntValue(42))

	benchmarkExpr(b, "broadcast_hit", func() {
		col := EvalBatchExpr(subq, batch, nil)
		// Touch the result so the compiler cannot elide the call.
		if col.Data.Ints[0] != 42 {
			b.Fatalf("unexpected value: %d", col.Data.Ints[0])
		}
	})
	benchmarkExpr(b, "row_fallback_baseline", func() {
		for i := 0; i < n; i++ {
			row := batchToRow(batch, i)
			_ = row
		}
	})
}
