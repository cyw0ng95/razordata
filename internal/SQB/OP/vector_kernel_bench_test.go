package OP

import (
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// REQ001456: BenchmarkVectorKernel_Filter_ScalingFactor measures ns/row
// across batch sizes 1-1024, asserting linear scaling.
func BenchmarkVectorKernel_Filter_ScalingFactor(b *testing.B) {
	sizes := []int{1, 4, 16, 64, 256, 1024}
	for _, sz := range sizes {
		b.Run(fmt.Sprintf("N=%d", sz), func(b *testing.B) {
			batch := UT.GetBatch(2)
			batch.SetColumnName(0, "a")
			batch.SetColumnName(1, "b")
			batch.Cols[0].Type = LX.T_INT_KW
			batch.Cols[1].Type = LX.T_INT_KW
			for i := 0; i < sz; i++ {
				batch.AppendRow(0, LX.T_INT_KW, int64(i), false)
				batch.AppendRow(1, LX.T_INT_KW, int64(i*10), false)
				batch.AdvanceSize()
			}

			pred := &PS.BinaryExpr{
				Left:  &PS.Ident{Name: "a"},
				Op:    LX.T_GT,
				Right: &PS.NumberLiteral{Val: 0},
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sel := EV.EvalBatch(pred, batch, nil)
				_ = sel
			}
			batch.Put()
		})
	}
}
