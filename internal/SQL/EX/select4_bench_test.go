package EX

import (
	"context"
	"fmt"
	"testing"
)

// setupSelect4TablesN is a variant of setupSelect4Tables that
// creates 9 tables (t1-t9) with n rows each and 5 int columns
// [a, b, c, d, e]. Values: a%1000, b%900, c%800, d%700, e%600.
// REQ000844: used for select4 slow-case benchmarks with smaller
// data sizes (15-30 rows instead of 100).
func setupSelect4TablesN(b *testing.B, n int) {
	b.Helper()
	UnregisterAll()
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("t%d", i)
		if i == 6 {
			RegisterTableSchema("tn2", []string{"a", "b", "c", "d", "e"})
		}
		RegisterTableSchema(name, []string{"a", "b", "c", "d", "e"})
	}
	tablesMu.Lock()
	for i := 1; i <= 9; i++ {
		names := []string{fmt.Sprintf("t%d", i)}
		if i == 6 {
			names = append(names, "tn2")
		}
		for _, name := range names {
			for j := 0; j < n; j++ {
				v := int64(j)
				tables[name] = append(tables[name], Row{
					Cols: []string{"a", "b", "c", "d", "e"},
					Data: []Value{
						NewIntValue(v % 1000),
						NewIntValue(v % 900),
						NewIntValue(v % 800),
						NewIntValue(v % 700),
						NewIntValue(v % 600),
					},
				})
			}
		}
	}
	tablesMu.Unlock()
}

// BenchmarkSelect4_Join277 replicates the worst-case 8-table join
// from select4 profile (L39756, 5431ms). Pattern: 1 equi-join
// (e8=c9) + 7 IN-list filters. Uses 30 rows per table.
// REQ000844: targeted optimization benchmark.
func BenchmarkSelect4_Join277(b *testing.B) {
	setupSelect4TablesN(b, 30)
	ex := NewExecutor()
	ctx := context.Background()
	q := `SELECT e5+c5, d1, c8, e9+108, a7, a3+149+a5, e4+358
  FROM t5, t3, t9, t6, t8, t4, t7, t1
 WHERE e8=c9
   AND d6 IN (277,256,469,924,846,729,901,168)
   AND a9 IN (28,11,739,102,413,389,130,982)
   AND a3 IN (515,190,306,513,894,997,398,196)
   AND c7 IN (488,333,519,863,269,31,574,663)
   AND b4 IN (660,634,708,938,748,607,892,167)
   AND c5 IN (489,250,927,855,697,130,856,421)
   AND a1 IN (358,451,479,382,330,281,953,732)`
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSelect4_Join255 replicates the 2nd worst-case 8-table join
// (L38819, 2108ms). Pattern: 1 equi-join (a3=d1) + 7 IN-list filters.
// Uses 30 rows per table.
func BenchmarkSelect4_Join255(b *testing.B) {
	setupSelect4TablesN(b, 30)
	ex := NewExecutor()
	ctx := context.Background()
	q := `SELECT e8, b3, a5*693, e2, e6*447, b1+778
  FROM t4, t2, t7, t6, t3, t1, t8, t5
 WHERE a3=d1
   AND c5 IN (489,250,927,855,697,130,856,421)
   AND d6 IN (590,73,34,942,970,786,21,35,369,826)
   AND b1 IN (783,259,280,581,896,754,730)
   AND e2 IN (659,428,707,812,556,16,114,125,699)
   AND e7 IN (456,460,978,502,560,941,168,290)
   AND b8 IN (966,603,934,211,205,259,103,813)
   AND a4 IN (469,579,982,337,541,606,589,451)`
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSelect4_4TableJoin is a 4-table cross product with
// IN-list filters (no equi-join). Smaller data (30 rows/table)
// makes iteration fast while still testing the NLJ hot path.
func BenchmarkSelect4_4TableJoin(b *testing.B) {
	setupSelect4TablesN(b, 30)
	ex := NewExecutor()
	ctx := context.Background()
	q := `SELECT a1, b2, c3, d4
  FROM t1, t2, t3, t4
 WHERE a1 IN (101, 103, 105, 107, 109)
   AND b2 IN (201, 203, 205)
   AND c3 > 100
   AND d4 < 500`
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSelect4_UnionAll8 replicates the 8-branch UNION ALL
// chain pattern from select4 compound queries. REQ000838: this
// exercises the streaming fast path at full depth.
// Uses 15 rows per table (smaller since UNION ALL chains
// materialize all intermediate results).
func BenchmarkSelect4_UnionAll8(b *testing.B) {
	setupSelect4TablesN(b, 15)
	ex := NewExecutor()
	ctx := context.Background()
	q := `SELECT a FROM t1
UNION ALL SELECT a FROM t2
UNION ALL SELECT a FROM t3
UNION ALL SELECT a FROM t4
UNION ALL SELECT a FROM t5
UNION ALL SELECT a FROM t6
UNION ALL SELECT a FROM t7
UNION ALL SELECT a FROM t8`
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSelect4_ScaleJoin277 measures join277 at 3 data sizes
// (10, 30, 100 rows) to understand scaling behavior.
func BenchmarkSelect4_ScaleJoin277(b *testing.B) {
	sizes := []int{10, 30, 100}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			setupSelect4TablesN(b, n)
			ex := NewExecutor()
			ctx := context.Background()
			q := `SELECT e5+c5, d1, c8, e9+108, a7, a3+149+a5, e4+358
  FROM t5, t3, t9, t6, t8, t4, t7, t1
 WHERE e8=c9
   AND d6 IN (277,256,469,924,846,729,901,168)
   AND a9 IN (28,11,739,102,413,389,130,982)
   AND a3 IN (515,190,306,513,894,997,398,196)
   AND c7 IN (488,333,519,863,269,31,574,663)
   AND b4 IN (660,634,708,938,748,607,892,167)
   AND c5 IN (489,250,927,855,697,130,856,421)
   AND a1 IN (358,451,479,382,330,281,953,732)`
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := ex.QueryAll(ctx, q)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSelect4_Compound8 is an 8-branch compound with
// EXCEPT/UNION mix (no UNION ALL — stresses materialized path).
// Uses 15 rows per table.
func BenchmarkSelect4_Compound8(b *testing.B) {
	setupSelect4TablesN(b, 15)
	ex := NewExecutor()
	ctx := context.Background()
	q := `SELECT a FROM t1 WHERE a IN (1,2,3)
EXCEPT
 SELECT b FROM t2 WHERE b IN (10,20)
UNION
 SELECT c FROM t3 WHERE c IN (100,200,300)
EXCEPT
 SELECT d FROM t4 WHERE d IN (50)
UNION
 SELECT e FROM t5 WHERE e IN (400,500)
UNION ALL
 SELECT a FROM t6 WHERE a IN (1)
UNION ALL
 SELECT b FROM t7 WHERE b IN (10)
UNION
 SELECT c FROM t8 WHERE c IN (100)`
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ex.QueryAll(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}