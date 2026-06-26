package EX

import (
	"context"
	"math/rand/v2"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// makeTPCHLikeRows creates N rows simulating TPC-H lineitem schema:
// l_orderkey, l_linenumber, l_quantity, l_extendedprice, l_discount, l_tax
func makeTPCHLikeRows(n int, seed int64) []Row {
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed+1)))
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:  []string{"l_orderkey", "l_linenumber", "l_quantity", "l_extendedprice", "l_discount", "l_tax"},
			Types: []int{int(LX.T_INT_KW), int(LX.T_INT_KW), int(LX.T_FLOAT_KW), int(LX.T_FLOAT_KW), int(LX.T_FLOAT_KW), int(LX.T_FLOAT_KW)},
			Data: []Value{NewIntValue(int64(rng.IntN(1000000))), NewIntValue(int64(rng.IntN(7) + 1)), NewFloatValue(float64(rng.IntN(50) + 1)), NewFloatValue(float64(rng.IntN(100000)) / 100.), NewFloatValue(float64(rng.IntN(10)) / 100.), NewFloatValue(float64(rng.IntN(8)) / 100.)},
		}
	}
	return rows
}

// BenchmarkTPCH_Q1 simulates TPC-H Query 1: SUM(l_extendedprice * (1 - l_discount))
// on rows where l_shipdate <= some date. In our simplified version,
// we compute SUM on a filtered column.
func BenchmarkTPCH_Q1(b *testing.B) {
	const n = 10000
	rows := makeTPCHLikeRows(n, 42)
	schema := []string{"l_orderkey", "l_linenumber", "l_quantity", "l_extendedprice", "l_discount", "l_tax"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW, LX.T_FLOAT_KW, LX.T_FLOAT_KW, LX.T_FLOAT_KW, LX.T_FLOAT_KW}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src := &rowSourceForTest{rows: rows}
		scan := NewVectorizedSeqScan(src, schema, types)
		// Apply filter: l_quantity > 25 (simulating l_shipdate filter)
		filter := NewVectorizedFilter(scan, &PS.BinaryExpr{
			Left:  &PS.Ident{Name: "l_quantity"},
			Op:    int(LX.T_GT),
			Right: &PS.FloatLiteral{Val: 25.0},
		})
		// Aggregate: SUM(l_extendedprice)
		sum := NewVectorizedSum(filter, 3)

		for {
			batch, _ := sum.NextBatch(context.Background())
			if batch == nil {
				break
			}
			batch.Put()
		}
		sum.Close()
		filter.Close()
		scan.Close()
	}
}

// BenchmarkTPCH_Q6 simulates TPC-H Query 6: SUM(l_extendedprice * l_discount)
// on rows where l_shipdate in range and l_quantity < 24.
func BenchmarkTPCH_Q6(b *testing.B) {
	const n = 10000
	rows := makeTPCHLikeRows(n, 43)
	schema := []string{"l_orderkey", "l_linenumber", "l_quantity", "l_extendedprice", "l_discount", "l_tax"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW, LX.T_FLOAT_KW, LX.T_FLOAT_KW, LX.T_FLOAT_KW, LX.T_FLOAT_KW}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src := &rowSourceForTest{rows: rows}
		scan := NewVectorizedSeqScan(src, schema, types)
		// Apply filter: l_quantity < 24
		filter := NewVectorizedFilter(scan, &PS.BinaryExpr{
			Left:  &PS.Ident{Name: "l_quantity"},
			Op:    int(LX.T_LT),
			Right: &PS.FloatLiteral{Val: 24.0},
		})
		// Aggregate: SUM(l_extendedprice)
		sum := NewVectorizedSum(filter, 3)

		for {
			batch, _ := sum.NextBatch(context.Background())
			if batch == nil {
				break
			}
			batch.Put()
		}
		sum.Close()
		filter.Close()
		scan.Close()
	}
}

// BenchmarkTPCH_Q1_Sequential is the row-at-a-time baseline for comparison.
func BenchmarkTPCH_Q1_Sequential(b *testing.B) {
	const n = 10000
	rows := makeTPCHLikeRows(n, 42)
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "l_quantity"},
		Op:    int(LX.T_GT),
		Right: &PS.FloatLiteral{Val: 25.0},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sum float64
		for j := 0; j < n; j++ {
			val, err := EvalValue(pred, &rows[j], nil)
			if err != nil {
				continue
			}
			if val.Kind == KindBool && val.Bo {
				sum += rows[j].Data[3].ToAny().(float64)
			}
		}
	}
}
