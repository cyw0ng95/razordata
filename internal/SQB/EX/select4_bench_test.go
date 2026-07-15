package EX

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// setupSelect4TablesSLT creates tables with the exact SLT select4 schema
// (aN,bN,cN,dN,eN,xN) and random data matching the real distribution.
func setupSelect4TablesSLT(b *testing.B) {
	b.Helper()
	UnregisterAll()
	rng := rand.New(rand.NewPCG(42, 42))
	rowCounts := []int{128, 113, 129, 111, 110, 92, 110, 109, 98}
	DT.TablesMu.Lock()
	for i := 1; i <= 9; i++ {
		si := fmt.Sprintf("%d", i)
		tbl := fmt.Sprintf("t%d", i)
		cols := []string{"a" + si, "b" + si, "c" + si, "d" + si, "e" + si, "x" + si}
		DT.Schemas[tbl] = cols
		DT.Tables[tbl] = make([]DT.Row, 0, rowCounts[i-1])
		for j := 0; j < rowCounts[i-1]; j++ {
			DT.Tables[tbl] = append(DT.Tables[tbl], DT.Row{
				Cols: cols,
				Data: []DT.Value{
					NewIntValue(rng.Int64N(1000)),
					NewIntValue(rng.Int64N(1000)),
					NewIntValue(rng.Int64N(1000)),
					NewIntValue(rng.Int64N(1000)),
					NewIntValue(rng.Int64N(1000)),
					NewTextValue(fmt.Sprintf("x%d", rng.Int64N(500))),
				},
			})
		}
	}
	DT.TablesMu.Unlock()
}

func BenchmarkSelect4_SLTRandom(b *testing.B) {
	setupSelect4TablesSLT(b)
	ctx := context.Background()
	sql := `SELECT b9*320, b8, x5, e7, c3+758, b1
FROM t1, t3, t5, t9, t7, t8
WHERE a3 in (971,341,380,30,566,865,478)
  AND 241=a1
  AND e8=a9
  AND c5 in (668,348,799,437,820,697,613)
  AND e7 in (782,460,27,826)
  AND e9 in (858,146,788)`

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		ex := NewExecutor()
		_, err := ex.QueryAll(ctx, sql)
		if err != nil {
			b.Fatal(err)
		}
		b.Logf("iteration %d: %v", i, time.Since(start))
	}
}

func BenchmarkSelect4_PlanSLT(b *testing.B) {
	setupSelect4TablesSLT(b)
	sql := `SELECT b9*320, b8, x5, e7, c3+758, b1
FROM t1, t3, t5, t9, t7, t8
WHERE a3 in (971,341,380,30,566,865,478)
  AND 241=a1
  AND e8=a9
  AND c5 in (668,348,799,437,820,697,613)
  AND e7 in (782,460,27,826)
  AND e9 in (858,146,788)`

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p := NewPlanner()
		_, err := p.ParseAndPlan(sql)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSelect4_ExecSLT(b *testing.B) {
	setupSelect4TablesSLT(b)
	ctx := context.Background()
	sql := `SELECT b9*320, b8, x5, e7, c3+758, b1
FROM t1, t3, t5, t9, t7, t8
WHERE a3 in (971,341,380,30,566,865,478)
  AND 241=a1
  AND e8=a9
  AND c5 in (668,348,799,437,820,697,613)
  AND e7 in (782,460,27,826)
  AND e9 in (858,146,788)`

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ex := NewExecutor()
		_, err := ex.QueryAll(ctx, sql)
		if err != nil {
			b.Fatal(err)
		}
	}
}
