package EV

import (
	"fmt"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// BenchmarkSerializeCorrelatedValues_Hit measures the cost of the
// hot path AFTER the correlated column index cache has warmed up.
// REQ001292: a single int comparison + byte slice append into the
// pooled buffer should be the bulk of the per-call cost.
func BenchmarkSerializeCorrelatedValues_Hit(b *testing.B) {
	row := &Row{
		Cols: []string{"id", "c", "d", "e", "f"},
		Data: []Value{DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3), DT.NewIntValue(4), DT.NewIntValue(5)},
	}
	idxs := []int{1, 2}

	b.ResetTimer()
	for range b.N {
		_ = serializeCorrelatedValues(row, idxs)
	}
}

// BenchmarkSerializeCorrelatedValues_WideCols measures the cost
// when the correlated key contains three columns (mostly text)
// — the realistic shape for the EXPLAIN example in REQ001292
// (`WHERE x.c>t1.c AND x.d<t1.d`).
func BenchmarkSerializeCorrelatedValues_WideCols(b *testing.B) {
	row := &Row{
		Cols: []string{"a", "b", "c", "d", "e", "f", "g"},
		Data: []Value{
			DT.NewIntValue(1),
			DT.NewTextValue("alpha"),
			DT.NewIntValue(42),
			DT.NewTextValue("beta"),
			DT.NewIntValue(3),
			DT.NewIntValue(7),
			DT.NewTextValue("gamma"),
		},
	}
	idxs := []int{2, 3, 6}

	b.ResetTimer()
	for range b.N {
		_ = serializeCorrelatedValues(row, idxs)
	}
}

// BenchmarkSerializeCorrelatedValues_ManyRows simulates running
// the encoder back-to-back over 1K outer rows, mimicking the hot
// loop of a correlated scalar subquery. Used to confirm the
// sync.Pool reuse amortises the bytes.Buffer allocation cost.
// REQ001292.
func BenchmarkSerializeCorrelatedValues_ManyRows(b *testing.B) {
	rows := make([]*Row, 0, 1000)
	for i := range 1000 {
		rows = append(rows, &Row{
			Cols: []string{"id", "c", "d"},
			Data: []Value{DT.NewIntValue(int64(i)), DT.NewIntValue(int64(i) * 10), DT.NewIntValue(int64(i) * 100)},
		})
	}
	idxs := []int{1, 2}

	b.ResetTimer()
	for range b.N {
		for _, r := range rows {
			_ = serializeCorrelatedValues(r, idxs)
		}
	}
}

// _ = fmt reserved for future workload generators.
var _ = fmt.Sprintf
