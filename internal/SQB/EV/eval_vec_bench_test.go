package EV

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
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
