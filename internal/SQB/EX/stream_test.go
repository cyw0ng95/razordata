package EX

import (
	"context"
	"testing"
)

// BenchmarkSelect1_Count_NoGoroutine runs SELECT count(*) FROM t1
// verifying the pipeline path is used. REQ002230.
func BenchmarkSelect1_Count_NoGoroutine(b *testing.B) {
	ResetForTest(b)
	ex, _ := newEngineExecutor(b)

	ctx := context.Background()
	mustExec(b, ex, ctx, "CREATE TABLE bm_count (a INT, b TEXT)")
	mustExec(b, ex, ctx, "INSERT INTO bm_count VALUES (1, 'x'), (2, 'y'), (3, 'z')")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := ex.QueryAll(ctx, "SELECT count(*) FROM bm_count")
		if err != nil {
			b.Fatalf("query: %v", err)
		}
		if len(rows) != 1 {
			b.Fatalf("expected 1 row, got %d", len(rows))
		}
		if rows[0].Data[0].I64 != 3 {
			b.Fatalf("expected count=3, got %d", rows[0].Data[0].I64)
		}
	}
}

// BenchmarkSelect1_Order_NoGoroutine runs SELECT * FROM t1 ORDER BY a
// verifying the pipeline path is used. REQ002230.
func BenchmarkSelect1_Order_NoGoroutine(b *testing.B) {
	ResetForTest(b)
	ex, _ := newEngineExecutor(b)

	ctx := context.Background()
	mustExec(b, ex, ctx, "CREATE TABLE bm_order (a INT, b TEXT)")
	mustExec(b, ex, ctx, "INSERT INTO bm_order VALUES (3, 'z'), (1, 'x'), (2, 'y')")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := ex.QueryAll(ctx, "SELECT * FROM bm_order ORDER BY a")
		if err != nil {
			b.Fatalf("query: %v", err)
		}
		if len(rows) != 3 {
			b.Fatalf("expected 3 rows, got %d", len(rows))
		}
		if rows[0].Data[0].I64 != 1 {
			b.Fatalf("expected first row a=1, got %d", rows[0].Data[0].I64)
		}
	}
}